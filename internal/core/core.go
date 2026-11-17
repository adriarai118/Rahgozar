// Package core is the connection controller: it starts/stops engines,
// switches TUN/proxy mode, watches health, fails over, and feeds the learner.
package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"rahgozar/internal/builder"
	"rahgozar/internal/engine"
	"rahgozar/internal/model"
	"rahgozar/internal/smart"
	"rahgozar/internal/store"
	"rahgozar/internal/sys"
)

// Version of the application.
var Version = "1.0.0"

// Status is the live connection state shown in the UI.
type Status struct {
	State       string `json:"state"` // idle | connecting | connected | disconnecting | error
	ProfileID   string `json:"profileId"`
	ProfileName string `json:"profileName"`
	Protocol    string `json:"protocol"`
	Mode        string `json:"mode"`
	Engine      string `json:"engine"`
	Since       int64  `json:"since"`
	Error       string `json:"error"`
	Message     string `json:"message"`
	NetID       string `json:"netId"`
	NetLabel    string `json:"netLabel"`
	Admin       bool   `json:"admin"`
	Up          int64  `json:"up"`
	Down        int64  `json:"down"`
	UpSpeed     int64  `json:"upSpeed"`
	DownSpeed   int64  `json:"downSpeed"`
	Conns       int    `json:"conns"`
	Latency     int    `json:"latency"`
	Failovers   int    `json:"failovers"`
	AutoChosen  bool   `json:"autoChosen"`
	Port        int    `json:"port"`
}

// Core is the controller.
type Core struct {
	Store  *store.Store
	Eng    *engine.Manager
	Runner Runner
	// TunAllowed reports whether TUN mode can be started (admin on Windows).
	TunAllowed func() bool
	// Platform-specific additions to the generated config (Android: own package, per-app lists).
	OS         string
	OwnPackage string
	Learner    *smart.Learner
	Domains    *smart.Domains
	DataDir    string
	Emit       func(name string, data any)
	ExeName    string

	opMu    sync.Mutex
	mu      sync.Mutex
	status  Status
	procs   []Process
	rasName string
	clash   string
	secret  string
	cancel  context.CancelFunc
	logs    *logRing
	current *model.Profile
	exclude map[string]time.Time
	prober  *smart.Prober
}

var (
	errBadHost   = errors.New("آدرس سایت نامعتبر است")
	errBadAction = errors.New("عملیات نامعتبر است")
)

// New creates the controller.
func New(st *store.Store, eng *engine.Manager, dataDir string) *Core {
	c := &Core{Store: st, Eng: eng, DataDir: dataDir, Learner: smart.NewLearner(), logs: newLogRing(3000), exclude: map[string]time.Time{}}
	c.Runner = ExecRunner{Eng: eng}
	c.TunAllowed = sys.IsAdmin
	c.OS = goos
	st.ReadJSON("learning.json", c.Learner)
	c.Domains = smart.NewDomains(filepath.Join(dataDir, "learned"))
	c.status = Status{State: "idle", Admin: c.TunAllowed()}
	c.Emit = func(string, any) {}
	return c
}

// Init performs crash recovery and starts background services.
func (c *Core) Init() {
	builder.HelperAvailable = func(e string) bool { return c.Runner != nil && c.Runner.Available(e) == nil }
	go sys.NetworkID()
	set := c.Store.Settings()
	// never leave the user without internet after a crash
	if sys.ClearSystemProxyIf("127.0.0.1:" + strconv.Itoa(set.MixedPort)) {
		c.Log("app", "system proxy from previous session cleared")
	}
	c.killStale()
	c.Domains.OnLearn = func(d *smart.DomainInfo) {
		c.Emit("domain", d)
		c.Log("learn", fmt.Sprintf("%s → %s (%s)", d.Domain, d.Verdict, d.Reason))
	}
	c.WriteUserRules()
	prober := &smart.Prober{
		DirectDNS: firstOr(directDNSHost(set.DirectDNS), "8.8.8.8"),
		ProxyURL: func() string {
			if c.Status().State != "connected" {
				return ""
			}
			return "http://127.0.0.1:" + strconv.Itoa(c.Store.Settings().MixedPort)
		},
	}
	c.prober = prober
	c.Domains.Start(prober.Probe, c.userRuleMatch, 3)
	go c.saveLoop()
	go c.subscriptionLoop()
}

func directDNSHost(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "local" || strings.Contains(s, "://") {
		return ""
	}
	return s
}

func (c *Core) userRuleMatch(base string) bool {
	st := c.Store.Settings()
	for _, list := range [][]string{st.ProxyDomains, st.DirectDomains, st.BlockDomains} {
		for _, d := range list {
			d = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "*.")
			if d != "" && (base == d || strings.HasSuffix(base, "."+d) || strings.HasSuffix(d, "."+base)) {
				return true
			}
		}
	}
	return false
}

func (c *Core) saveLoop() {
	t := time.NewTicker(30 * time.Second)
	for range t.C {
		c.SaveLearning()
	}
}

// SaveLearning persists learned stats if changed.
func (c *Core) SaveLearning() {
	snap, dirty := c.Learner.Snapshot()
	if dirty {
		c.Store.SaveJSON("learning.json", snap)
	}
}

// Status returns a copy of the live status.
func (c *Core) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Core) setStatus(f func(s *Status)) {
	c.mu.Lock()
	f(&c.status)
	s := c.status
	c.mu.Unlock()
	c.Emit("status", s)
}

func (c *Core) runtimeDir() string {
	d := filepath.Join(c.DataDir, "runtime")
	os.MkdirAll(d, 0o755)
	return d
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// FreePort returns an available local TCP port.
func FreePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 20000 + int(time.Now().UnixNano()%10000)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// ---------------- connect / disconnect ----------------

// Connect starts the VPN with the selected (or smartly chosen) profile.
func (c *Core) Connect() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if s := c.Status(); s.State == "connected" || s.State == "connecting" {
		return nil
	}
	return c.connectLocked("")
}

func (c *Core) connectLocked(forceID string) error {
	set := c.Store.Settings()
	netID, netLabel := sys.NetworkID()
	c.setStatus(func(s *Status) {
		*s = Status{State: "connecting", Mode: set.Mode, Admin: c.TunAllowed(), NetID: netID, NetLabel: netLabel, Message: "در حال آماده‌سازی…", Failovers: s.Failovers, Port: set.MixedPort}
	})
	fail := func(err error) error {
		c.stopAll()
		c.setStatus(func(s *Status) { s.State = "error"; s.Error = err.Error(); s.Message = "" })
		c.Log("app", "connect failed: "+err.Error())
		return err
	}
	var p *model.Profile
	auto := false
	id := forceID
	if id == "" {
		id = set.SelectedID
	}
	if id == "" || id == "auto" {
		c.setStatus(func(s *Status) { s.Message = "انتخاب هوشمند بهترین سرور…" })
		best, err := c.ChooseBest(netID, nil)
		if err != nil {
			return fail(err)
		}
		p = best
		auto = true
	} else {
		p = c.Store.Profile(id)
		if p == nil {
			return fail(errors.New("سرور انتخاب‌شده پیدا نشد"))
		}
	}
	if p.IsRAS() {
		return c.connectRAS(p, netID, fail)
	}
	if set.Mode == "tun" && !c.TunAllowed() {
		return fail(errors.New("NEED_ADMIN"))
	}
	if err := c.startEngines(p, set); err != nil {
		return fail(err)
	}
	if set.Mode == "proxy" && set.SetSystemProxy {
		if err := sys.SetSystemProxy("127.0.0.1:"+strconv.Itoa(set.MixedPort), ""); err != nil {
			c.Log("app", "system proxy: "+err.Error())
		}
	}
	c.mu.Lock()
	c.current = p
	c.mu.Unlock()
	c.setStatus(func(s *Status) {
		s.State = "connected"
		s.ProfileID, s.ProfileName, s.Protocol = p.ID, p.Name, p.Family()
		s.Engine = builder.EngineLabel(builder.EngineFor(p))
		s.Since = time.Now().Unix()
		s.Message = ""
		s.Error = ""
		s.AutoChosen = auto
	})
	c.Log("app", fmt.Sprintf("connected: %s [%s, %s mode]", p.Name, p.Family(), set.Mode))
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	go c.watchProcs(ctx)
	go c.trafficLoop(ctx)
	go c.healthLoop(ctx, p)
	return nil
}

func (c *Core) connectRAS(p *model.Profile, netID string, fail func(error) error) error {
	name := "Rahgozar " + strings.ToUpper(p.Type)
	c.setStatus(func(s *Status) {
		s.Message = "اتصال " + strings.ToUpper(p.Type) + " ویندوز…"
		s.Engine = "Windows VPN"
		s.Mode = "system"
	})
	err := sys.RASConnect(sys.RASConfig{Name: name, Type: p.Type, Server: p.Server, Port: p.Port, Username: p.Username, Password: p.Password, PSK: p.PSK})
	if err != nil {
		c.Learner.Record(p.ID, p.Family(), netID, "", false, 0)
		return fail(err)
	}
	c.Learner.Record(p.ID, p.Family(), netID, "", true, 0)
	c.mu.Lock()
	c.rasName = name
	c.current = p
	c.mu.Unlock()
	c.setStatus(func(s *Status) {
		s.State = "connected"
		s.ProfileID, s.ProfileName, s.Protocol = p.ID, p.Name, p.Type
		s.Since = time.Now().Unix()
		s.Message = ""
	})
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !sys.RASConnected(name) {
					c.Log("app", "Windows VPN dropped")
					go c.Disconnect()
					return
				}
			}
		}
	}()
	return nil
}

// startEngines builds configs and launches helper engines + sing-box.
func (c *Core) startEngines(p *model.Profile, set model.Settings) error {
	if err := c.Runner.Available(model.EngineSingBox); err != nil {
		return err
	}
	c.clash = "127.0.0.1:" + strconv.Itoa(FreePort())
	c.secret = randomHex(12)
	rt := c.runtimeDir()
	in := &builder.Input{
		Settings:     set,
		Profile:      p,
		Resolve:      c.Store.Profile,
		Mode:         set.Mode,
		RulesDir:     c.Eng.RulesDir(),
		LearnedDir:   filepath.Join(c.DataDir, "learned"),
		CacheFile:    filepath.Join(rt, "cache.db"),
		ClashAddr:    c.clash,
		ClashSecret:  c.secret,
		PortAlloc:    FreePort,
		ExeName:      c.ExeName,
		EngineExes:   c.Runner.ExeNames(),
		OS:           c.OS,
		OwnPackage:   c.OwnPackage,
		HasRuleFiles: c.Eng.HasRuleFiles(builder.RuleSetFiles),
	}
	if !portFree(set.MixedPort, set.AllowLAN) {
		return fmt.Errorf("پورت %d توسط برنامه دیگری اشغال است؛ در تنظیمات پورت را عوض کنید", set.MixedPort)
	}
	c.Domains.WriteRuleFiles()
	c.WriteUserRules()
	res, err := builder.Build(in)
	if err != nil {
		return err
	}
	for _, h := range res.Helpers {
		if err := c.Runner.Available(h.Engine); err != nil {
			return err
		}
		cfgPath := filepath.Join(rt, h.FileName)
		if err := os.WriteFile(cfgPath, h.Config, 0o600); err != nil {
			return err
		}
		c.setStatus(func(s *Status) { s.Message = "اجرای " + builder.EngineLabel(h.Engine) + "…" })
		proc, err := c.Runner.Start(Job{Name: h.Engine, Engine: h.Engine, ConfigPath: cfgPath, Config: h.Config, RuntimeDir: rt}, c.Log)
		if err != nil {
			return err
		}
		c.addProc(proc)
		if err := waitPort(proc, h.Port, 8*time.Second); err != nil {
			return err
		}
	}
	b, _ := json.MarshalIndent(res.SingBox, "", "  ")
	cfgPath := filepath.Join(rt, "sing-box.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		return err
	}
	c.setStatus(func(s *Status) { s.Message = "اجرای sing-box…" })
	proc, err := c.Runner.Start(Job{Name: "sing-box", Engine: model.EngineSingBox, ConfigPath: cfgPath, Config: b, RuntimeDir: rt, Main: true}, c.Log)
	if err != nil {
		return err
	}
	c.addProc(proc)
	if err := c.waitClash(proc, 15*time.Second); err != nil {
		return err
	}
	c.savePIDs()
	return nil
}

func portFree(port int, lan bool) bool {
	host := "127.0.0.1"
	if lan {
		host = "0.0.0.0"
	}
	l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func waitPort(proc Process, port int, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !proc.Alive() {
			return proc.ExitError()
		}
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("%s did not start in time", proc.Label())
}

func (c *Core) clashGet(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+c.clash+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := localClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct {
			Message string `json:"message"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, e.Message)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

var localClient = &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}

func (c *Core) waitClash(proc Process, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !proc.Alive() {
			return proc.ExitError()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := c.clashGet(ctx, "/version", nil)
		cancel()
		if err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("sing-box did not become ready")
}

func (c *Core) addProc(p Process) {
	c.mu.Lock()
	c.procs = append(c.procs, p)
	c.mu.Unlock()
}

func (c *Core) stopAll() {
	c.mu.Lock()
	procs := c.procs
	c.procs = nil
	cancel := c.cancel
	c.cancel = nil
	ras := c.rasName
	c.rasName = ""
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	set := c.Store.Settings()
	sys.ClearSystemProxyIf("127.0.0.1:" + strconv.Itoa(set.MixedPort))
	for i := len(procs) - 1; i >= 0; i-- {
		procs[i].Stop()
	}
	if ras != "" {
		sys.RASDisconnect(ras)
	}
	os.Remove(filepath.Join(c.DataDir, "runtime", "pids.json"))
}

// Disconnect stops everything.
func (c *Core) Disconnect() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	return c.disconnectLocked()
}

func (c *Core) disconnectLocked() error {
	st := c.Status()
	if st.State == "idle" {
		return nil
	}
	c.setStatus(func(s *Status) { s.State = "disconnecting" })
	c.recordUsage(st)
	c.stopAll()
	c.mu.Lock()
	c.current = nil
	c.mu.Unlock()
	c.setStatus(func(s *Status) {
		*s = Status{State: "idle", Admin: sys.IsAdmin(), NetID: s.NetID, NetLabel: s.NetLabel, Mode: c.Store.Settings().Mode, Up: s.Up, Down: s.Down}
	})
	c.Log("app", "disconnected")
	c.SaveLearning()
	return nil
}

func (c *Core) recordUsage(st Status) {
	if st.ProfileID == "" || st.Since == 0 {
		return
	}
	c.Learner.RecordUsage(st.ProfileID, st.NetID, float64(time.Now().Unix()-st.Since), 0)
}

// Reconnect restarts with current settings (used after settings change).
func (c *Core) Reconnect() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	st := c.Status()
	if st.State != "connected" && st.State != "error" {
		return nil
	}
	set := c.Store.Settings()
	force := ""
	if set.SelectedID == "auto" || set.SelectedID == "" {
		force = st.ProfileID // keep the auto-chosen server
	}
	c.disconnectLocked()
	return c.connectLocked(force)
}

type pidFile struct {
	PIDs []int    `json:"pids"`
	Exes []string `json:"exes"`
}

func (c *Core) savePIDs() {
	c.mu.Lock()
	var pf pidFile
	for _, p := range c.procs {
		pf.PIDs = append(pf.PIDs, p.PID())
		pf.Exes = append(pf.Exes, p.Label())
	}
	c.mu.Unlock()
	b, _ := json.Marshal(pf)
	os.WriteFile(filepath.Join(c.runtimeDir(), "pids.json"), b, 0o644)
}

// killStale terminates engine processes left by a crashed previous run.
func (c *Core) killStale() {
	b, err := os.ReadFile(filepath.Join(c.runtimeDir(), "pids.json"))
	if err != nil {
		return
	}
	var pf pidFile
	if json.Unmarshal(b, &pf) != nil {
		return
	}
	for _, pid := range pf.PIDs {
		path := strings.ToLower(sys.ProcessExePath(pid))
		if c.Runner.OwnsPath(path) {
			sys.KillPID(pid)
			c.Log("app", fmt.Sprintf("killed stale engine pid %d", pid))
		}
	}
	os.Remove(filepath.Join(c.runtimeDir(), "pids.json"))
}

// ---------------- loops ----------------

func (c *Core) watchProcs(ctx context.Context) {
	c.mu.Lock()
	procs := append([]Process(nil), c.procs...)
	c.mu.Unlock()
	cases := make(chan Process, len(procs))
	for _, p := range procs {
		go func(p Process) {
			select {
			case <-p.Done():
				cases <- p
			case <-ctx.Done():
			}
		}(p)
	}
	select {
	case <-ctx.Done():
		return
	case p := <-cases:
		err := p.ExitError()
		c.Log("app", err.Error())
		set := c.Store.Settings()
		if set.AutoFailover {
			go c.failover("engine crashed: " + err.Error())
			return
		}
		go func() {
			c.opMu.Lock()
			defer c.opMu.Unlock()
			c.stopAll()
			c.setStatus(func(s *Status) { s.State = "error"; s.Error = err.Error() })
		}()
	}
}

type connSnapshot struct {
	DownloadTotal int64 `json:"downloadTotal"`
	UploadTotal   int64 `json:"uploadTotal"`
	Connections   []struct {
		Metadata struct {
			Host          string `json:"host"`
			DestinationIP string `json:"destinationIP"`
			ProcessPath   string `json:"processPath"`
		} `json:"metadata"`
		Chains   []string `json:"chains"`
		Rule     string   `json:"rule"`
		Upload   int64    `json:"upload"`
		Download int64    `json:"download"`
	} `json:"connections"`
}

// ConnInfo is a connection row for the UI.
type ConnInfo struct {
	Host     string `json:"host"`
	Outbound string `json:"outbound"`
	Process  string `json:"process"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
}

func (c *Core) trafficLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var lastUp, lastDown int64
	first := true
	peak := 0.0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var snap connSnapshot
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := c.clashGet(cctx, "/connections", &snap)
		cancel()
		if err != nil {
			continue
		}
		up, down := snap.UploadTotal-lastUp, snap.DownloadTotal-lastDown
		if first {
			up, down, first = 0, 0, false
		}
		lastUp, lastDown = snap.UploadTotal, snap.DownloadTotal
		if float64(down) > peak {
			peak = float64(down)
		}
		set := c.Store.Settings()
		learn := set.LearnDomains && set.RoutingMode == "smart"
		var conns []ConnInfo
		for _, cn := range snap.Connections {
			host := cn.Metadata.Host
			if host == "" {
				host = cn.Metadata.DestinationIP
			}
			ob := ""
			if len(cn.Chains) > 0 {
				ob = cn.Chains[0]
			}
			if learn && cn.Metadata.Host != "" && !strings.Contains(cn.Rule, "process_name") {
				c.Domains.Observe(cn.Metadata.Host)
			}
			if len(conns) < 200 {
				conns = append(conns, ConnInfo{Host: host, Outbound: ob, Process: filepath.Base(cn.Metadata.ProcessPath), Up: cn.Upload, Down: cn.Download})
			}
		}
		c.setStatusQuiet(func(s *Status) {
			s.Up, s.Down = snap.UploadTotal, snap.DownloadTotal
			s.UpSpeed, s.DownSpeed = up, down
			s.Conns = len(snap.Connections)
		})
		c.Emit("traffic", map[string]any{"up": up, "down": down, "upTotal": snap.UploadTotal, "downTotal": snap.DownloadTotal, "conns": len(snap.Connections), "list": conns})
		st := c.Status()
		if peak > 0 && st.ProfileID != "" {
			c.Learner.RecordUsage(st.ProfileID, st.NetID, 0, peak)
		}
	}
}

func (c *Core) setStatusQuiet(f func(s *Status)) {
	c.mu.Lock()
	f(&c.status)
	c.mu.Unlock()
}

// probeActive measures latency through the running "proxy" outbound.
func (c *Core) probeActive(ctx context.Context) (int, error) {
	set := c.Store.Settings()
	to := set.TestTimeoutMs
	if to <= 0 {
		to = 5000
	}
	var r struct {
		Delay int `json:"delay"`
	}
	q := url.Values{"url": {set.TestURL}, "timeout": {strconv.Itoa(to)}}
	err := c.clashGet(ctx, "/proxies/proxy/delay?"+q.Encode(), &r)
	if err != nil {
		return 0, err
	}
	return r.Delay, nil
}

func (c *Core) healthLoop(ctx context.Context, p *model.Profile) {
	// quick first check
	select {
	case <-ctx.Done():
		return
	case <-time.After(1500 * time.Millisecond):
	}
	fails := 0
	interval := 20 * time.Second
	for {
		cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		ms, err := c.probeActive(cctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		st := c.Status()
		c.Learner.Record(p.ID, p.Family(), st.NetID, st.NetLabel, err == nil, ms)
		if err == nil {
			fails = 0
			c.setStatus(func(s *Status) { s.Latency = ms; s.Message = "" })
			interval = 20 * time.Second
		} else {
			fails++
			c.Log("health", fmt.Sprintf("check %d failed: %v", fails, err))
			c.setStatus(func(s *Status) { s.Latency = -1; s.Message = "اتصال ضعیف است…" })
			interval = 6 * time.Second
			if fails >= 3 && c.Store.Settings().AutoFailover {
				go c.failover("سرور پاسخ نمی‌دهد")
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// failover switches to the next best server.
func (c *Core) failover(reason string) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	st := c.Status()
	if st.State != "connected" {
		return
	}
	set := c.Store.Settings()
	c.Log("app", "failover: "+reason)
	c.mu.Lock()
	c.exclude[st.ProfileID] = time.Now()
	c.mu.Unlock()
	c.Emit("toast", map[string]string{"kind": "warn", "text": "تعویض خودکار سرور: " + reason})
	c.recordUsage(st)
	c.stopAll()
	failovers := st.Failovers + 1
	c.setStatus(func(s *Status) {
		s.State = "connecting"
		s.Message = "جستجوی سرور سالم…"
		s.Failovers = failovers
	})
	best, err := c.ChooseBest(st.NetID, map[string]bool{st.ProfileID: true})
	if err != nil {
		// retry the same server once more
		c.Log("app", "no alternative: "+err.Error())
		if e2 := c.connectLocked(st.ProfileID); e2 != nil {
			return
		}
		return
	}
	_ = set
	if err := c.connectLocked(best.ID); err == nil {
		c.setStatus(func(s *Status) { s.Failovers = failovers; s.AutoChosen = true })
		c.Emit("toast", map[string]string{"kind": "ok", "text": "به " + best.Name + " منتقل شد"})
	}
}
