package desk

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"rahgozar/internal/core"
	"rahgozar/internal/model"
	"rahgozar/internal/smart"
	"rahgozar/internal/sys"
)

// Local API used by the Rahgozar browser extension. It listens on 127.0.0.1
// only, rejects requests coming from web pages (Origin check + Host check
// against DNS rebinding) and requires a token obtained through pairing,
// which the user approves inside the app.

type pairReq struct {
	ID      string
	Name    string
	Created time.Time
	State   string // pending | approved | denied
}

type extAPI struct {
	app   *App
	mu    sync.Mutex
	pairs map[string]*pairReq
	srv   *http.Server
	port  int
	emit  func(string, any)
}

func newToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *App) startAPI(emit func(string, any)) {
	if a.api != nil {
		a.api.stop()
	}
	set := a.store.Settings()
	if !set.ExtAPI {
		return
	}
	port := set.ExtPort
	if port <= 0 {
		port = 47123
	}
	x := &extAPI{app: a, pairs: map[string]*pairReq{}, port: port, emit: emit}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", x.wrap(false, x.ping))
	mux.HandleFunc("/api/pair/request", x.wrap(false, x.pairRequest))
	mux.HandleFunc("/api/pair/poll", x.wrap(false, x.pairPoll))
	mux.HandleFunc("/api/status", x.wrap(true, x.status))
	mux.HandleFunc("/api/connect", x.wrap(true, x.connect))
	mux.HandleFunc("/api/disconnect", x.wrap(true, x.disconnect))
	mux.HandleFunc("/api/toggle", x.wrap(true, x.toggle))
	mux.HandleFunc("/api/servers", x.wrap(true, x.servers))
	mux.HandleFunc("/api/select", x.wrap(true, x.selectServer))
	mux.HandleFunc("/api/mode", x.wrap(true, x.mode))
	mux.HandleFunc("/api/routing", x.wrap(true, x.routing))
	mux.HandleFunc("/api/site", x.wrap(true, x.site))
	mux.HandleFunc("/api/site/probe", x.wrap(true, x.probe))
	mux.HandleFunc("/api/sites", x.wrap(true, x.sites))
	mux.HandleFunc("/api/test", x.wrap(true, x.test))
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		a.core.Log("api", "extension API: "+err.Error())
		return
	}
	x.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	a.api = x
	go x.srv.Serve(ln)
	a.core.Log("api", "extension API on 127.0.0.1:"+strconv.Itoa(port))
}

func (x *extAPI) stop() {
	if x.srv != nil {
		x.srv.Close()
	}
}

type apiErr struct {
	code int
	msg  string
}

func (e apiErr) Error() string { return e.msg }

func (x *extAPI) wrap(auth bool, h func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		allowedOrigin := origin == "" || strings.HasPrefix(origin, "chrome-extension://") ||
			strings.HasPrefix(origin, "moz-extension://") || strings.HasPrefix(origin, "extension://")
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		if !allowedOrigin || (host != "127.0.0.1" && host != "localhost") {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Rahgozar-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if auth {
			tok := x.app.store.Settings().ExtToken
			got := r.Header.Get("X-Rahgozar-Token")
			if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(got)) != 1 {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"unpaired"}`))
				return
			}
		}
		res, err := h(r)
		if err != nil {
			code := http.StatusBadRequest
			var ae apiErr
			if errors.As(err, &ae) {
				code = ae.code
			}
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if res == nil {
			res = map[string]bool{"ok": true}
		}
		json.NewEncoder(w).Encode(res)
	}
}

func readBody(r *http.Request, v any) error {
	if r.Method != http.MethodPost {
		return apiErr{http.StatusMethodNotAllowed, "POST required"}
	}
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16)).Decode(v)
}

func (x *extAPI) ping(r *http.Request) (any, error) {
	return map[string]any{"app": "rahgozar", "version": core.Version, "extVersion": x.app.extensionVersion()}, nil
}

// pairing: extension asks, user approves inside the app, extension polls.
func (x *extAPI) pairRequest(r *http.Request) (any, error) {
	var b struct {
		Name string `json:"name"`
	}
	if err := readBody(r, &b); err != nil {
		return nil, err
	}
	x.mu.Lock()
	for id, p := range x.pairs {
		if time.Since(p.Created) > 3*time.Minute {
			delete(x.pairs, id)
		}
	}
	if len(x.pairs) > 5 {
		x.mu.Unlock()
		return nil, apiErr{http.StatusTooManyRequests, "too many pairing requests"}
	}
	name := b.Name
	if len(name) > 60 {
		name = name[:60]
	}
	p := &pairReq{ID: newToken(8), Name: name, Created: time.Now(), State: "pending"}
	x.pairs[p.ID] = p
	x.mu.Unlock()
	x.emit("pairRequest", map[string]string{"id": p.ID, "name": p.Name})
	return map[string]string{"id": p.ID}, nil
}

func (x *extAPI) pairPoll(r *http.Request) (any, error) {
	id := r.URL.Query().Get("id")
	x.mu.Lock()
	p := x.pairs[id]
	x.mu.Unlock()
	if p == nil {
		return map[string]string{"state": "expired"}, nil
	}
	switch p.State {
	case "approved":
		x.mu.Lock()
		delete(x.pairs, id)
		x.mu.Unlock()
		return map[string]string{"state": "approved", "token": x.app.store.Settings().ExtToken}, nil
	case "denied":
		return map[string]string{"state": "denied"}, nil
	}
	return map[string]string{"state": "pending"}, nil
}

// ApprovePair is called from the app UI.
func (a *App) ApprovePair(id string, ok bool) error {
	if a.api == nil {
		return errors.New("API خاموش است")
	}
	a.api.mu.Lock()
	p := a.api.pairs[id]
	a.api.mu.Unlock()
	if p == nil {
		return errors.New("درخواست منقضی شده است")
	}
	if ok {
		s := a.store.Settings()
		if s.ExtToken == "" {
			s.ExtToken = newToken(24)
			if err := a.store.SetSettings(s); err != nil {
				return err
			}
		}
		p.State = "approved"
		a.core.Log("api", "browser extension paired: "+p.Name)
	} else {
		p.State = "denied"
	}
	return nil
}

// RevokeExtensions invalidates every paired extension.
func (a *App) RevokeExtensions() error {
	s := a.store.Settings()
	s.ExtToken = ""
	return a.store.SetSettings(s)
}

// extStatus is the compact status sent to the extension.
type extStatus struct {
	core.Status
	RoutingMode string `json:"routingMode"`
	SelectedID  string `json:"selectedId"`
	Version     string `json:"version"`
	ProxyPort   int    `json:"proxyPort"`
	EnginesOK   bool   `json:"enginesOk"`
	Servers     int    `json:"servers"`
	Country     string `json:"country"`
}

func (x *extAPI) statusValue() extStatus {
	a := x.app
	set := a.store.Settings()
	_, err := a.eng.ExePath("sing-box")
	st := a.core.Status()
	st.Mode = set.Mode
	if st.State == "connected" && a.core.Status().Mode == "system" {
		st.Mode = "system"
	}
	es := extStatus{Status: st, RoutingMode: set.RoutingMode, SelectedID: set.SelectedID, Version: core.Version,
		ProxyPort: set.MixedPort, EnginesOK: err == nil, Servers: len(a.store.Profiles())}
	if p := a.store.Profile(st.ProfileID); p != nil {
		es.Country, _ = a.geo.Country(p.Name, p.Server)
	}
	return es
}

func (x *extAPI) status(r *http.Request) (any, error) { return x.statusValue(), nil }

func (x *extAPI) connect(r *http.Request) (any, error) {
	if _, err := x.app.eng.ExePath("sing-box"); err != nil {
		return nil, err
	}
	if x.app.store.Settings().Mode == "tun" && !sys.IsAdmin() {
		return nil, errors.New("حالت TUN دسترسی مدیر می‌خواهد؛ برنامه رهگذر را با دسترسی مدیر باز کنید")
	}
	go x.app.core.Connect()
	time.Sleep(150 * time.Millisecond)
	return x.statusValue(), nil
}

func (x *extAPI) disconnect(r *http.Request) (any, error) {
	x.app.core.Disconnect()
	return x.statusValue(), nil
}

func (x *extAPI) toggle(r *http.Request) (any, error) {
	switch x.app.core.Status().State {
	case "connected", "connecting":
		return x.disconnect(r)
	}
	return x.connect(r)
}

type extServer struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Family  string  `json:"family"`
	Latency float64 `json:"latency"`
	Success float64 `json:"success"`
	Samples int     `json:"samples"`
	Group   string  `json:"group"`
	Country string  `json:"country"`
}

func (x *extAPI) servers(r *http.Request) (any, error) {
	a := x.app
	netID, _ := sys.NetworkIDFast()
	var cands []smart.Candidate
	byID := map[string]*model.Profile{}
	var ras []*model.Profile
	for _, p := range a.store.Profiles() {
		if p.IsRAS() || p.Type == model.TChain {
			ras = append(ras, p)
			continue
		}
		cands = append(cands, smart.Candidate{ID: p.ID, Family: p.Family()})
		byID[p.ID] = p
	}
	var out []extServer
	for _, rk := range a.core.Learner.Rank(cands, netID) {
		p := byID[rk.ID]
		s := extServer{ID: p.ID, Name: p.Name, Type: p.Type, Family: p.Family(), Group: p.Group}
		s.Country, _ = a.geo.Country(p.Name, p.Server)
		if rk.Samples > 0 {
			s.Latency, s.Success, s.Samples = rk.Latency, rk.Success, rk.Samples
		}
		out = append(out, s)
	}
	for _, p := range ras {
		cc, _ := a.geo.Country(p.Name, p.Server)
		out = append(out, extServer{ID: p.ID, Name: p.Name, Type: p.Type, Family: p.Family(), Group: p.Group, Country: cc})
	}
	return map[string]any{"selectedId": a.store.Settings().SelectedID, "servers": out}, nil
}

func (x *extAPI) selectServer(r *http.Request) (any, error) {
	var b struct {
		ID string `json:"id"`
	}
	if err := readBody(r, &b); err != nil {
		return nil, err
	}
	if b.ID != "auto" && x.app.store.Profile(b.ID) == nil {
		return nil, errors.New("سرور پیدا نشد")
	}
	if err := x.app.Select(b.ID); err != nil {
		return nil, err
	}
	x.emit("settingsChanged", nil)
	return x.statusValue(), nil
}

func (x *extAPI) mode(r *http.Request) (any, error) {
	var b struct {
		Mode string `json:"mode"`
	}
	if err := readBody(r, &b); err != nil {
		return nil, err
	}
	if b.Mode != "proxy" && b.Mode != "tun" {
		return nil, errors.New("bad mode")
	}
	if err := x.app.SetMode(b.Mode); err != nil {
		if err.Error() == "NEED_ADMIN" {
			return nil, errors.New("حالت TUN دسترسی مدیر می‌خواهد؛ برنامه رهگذر را با دسترسی مدیر باز کنید")
		}
		return nil, err
	}
	x.emit("settingsChanged", nil)
	return x.statusValue(), nil
}

func (x *extAPI) routing(r *http.Request) (any, error) {
	var b struct {
		Mode string `json:"mode"`
	}
	if err := readBody(r, &b); err != nil {
		return nil, err
	}
	switch b.Mode {
	case "smart", "rules", "global", "direct":
	default:
		return nil, errors.New("bad routing mode")
	}
	if err := x.app.SetRouting(b.Mode); err != nil {
		return nil, err
	}
	x.emit("settingsChanged", nil)
	return x.statusValue(), nil
}

func (x *extAPI) site(r *http.Request) (any, error) {
	if r.Method == http.MethodGet {
		return x.app.core.Site(r.URL.Query().Get("host")), nil
	}
	var b struct {
		Host   string `json:"host"`
		Action string `json:"action"`
		Scope  string `json:"scope"`
	}
	if err := readBody(r, &b); err != nil {
		return nil, err
	}
	si, err := x.app.core.SetSite(b.Host, b.Action, b.Scope)
	if err != nil {
		return nil, err
	}
	x.emit("settingsChanged", nil)
	return si, nil
}

func (x *extAPI) probe(r *http.Request) (any, error) {
	var b struct {
		Host string `json:"host"`
	}
	if err := readBody(r, &b); err != nil {
		return nil, err
	}
	return x.app.core.ProbeSite(b.Host), nil
}

func (x *extAPI) sites(r *http.Request) (any, error) {
	s := x.app.store.Settings()
	return map[string]any{"proxy": s.ProxyDomains, "direct": s.DirectDomains, "block": s.BlockDomains}, nil
}

func (x *extAPI) test(r *http.Request) (any, error) {
	var ids []string
	for _, p := range x.app.store.Profiles() {
		if !p.IsRAS() && p.Type != model.TChain {
			ids = append(ids, p.ID)
		}
	}
	go x.app.core.TestProfiles(ids)
	return map[string]int{"started": len(ids)}, nil
}
