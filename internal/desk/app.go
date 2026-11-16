package desk

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rahgozar/internal/builder"
	"rahgozar/internal/core"
	"rahgozar/internal/engine"
	"rahgozar/internal/geo"
	"rahgozar/internal/model"
	"rahgozar/internal/parser"
	"rahgozar/internal/smart"
	"rahgozar/internal/store"
	"rahgozar/internal/sys"
)

// Platform is what the host (Wails on Windows, the Android app) provides.
type Platform interface {
	OS() string
	Emit(name string, data any)
	ClipboardGet() (string, error)
	ClipboardSet(text string) error
	// PickFiles shows a file chooser; pattern like "*.ovpn;*.conf".
	PickFiles(title, pattern string, multiple bool) ([]string, error)
	OpenURL(url string)
	OpenPath(path string) error
	Minimise()
	ToggleMaximise()
	Focus()
	Quit()
	// Relaunch starts a new instance (elevated when admin) and quits this one.
	Relaunch(admin bool) error
	// InstallUpdate installs a downloaded app update and restarts.
	InstallUpdate(c *core.Core, u *core.AppUpdate, progress func(float64)) error
	// TunAllowed reports whether TUN can start without more privileges.
	TunAllowed() bool
}

// Options configure NewApp. Empty fields get desktop defaults.
type Options struct {
	DataDir     string
	AppDir      string
	EnginesDir  string
	ExeName     string
	OwnPackage  string
	Platform    Platform
	Runner      core.Runner
	Builtin     []*engine.Status // engines compiled into the app (Android)
	NetworkName func() string
}

// App is bound to the frontend (window.go.desk.App).
type App struct {
	plat    Platform
	store   *store.Store
	eng     *engine.Manager
	core    *core.Core
	dataDir string
	appDir  string
	update  *core.AppUpdate
	api     *extAPI
	emitFn  func(string, any)
	geo     *geo.Resolver
	builtin []*engine.Status
	ready   bool
}

// NewApp wires everything together.
func NewApp(o Options) *App {
	if o.DataDir == "" {
		core.CleanupOldBinary()
		o.DataDir = store.DataDir()
	}
	exe, _ := os.Executable()
	if o.AppDir == "" {
		o.AppDir = filepath.Dir(exe)
	}
	if o.ExeName == "" {
		o.ExeName = filepath.Base(exe)
	}
	st := store.Open(o.DataDir)
	if o.EnginesDir == "" {
		o.EnginesDir = filepath.Join(o.AppDir, "engines")
		if err := os.MkdirAll(o.EnginesDir, 0o755); err != nil || !writable(o.EnginesDir) {
			o.EnginesDir = filepath.Join(o.DataDir, "engines")
		}
	}
	eng := engine.NewManager(o.EnginesDir)
	eng.SeedRules(filepath.Join(o.AppDir, "rules"))
	c := core.New(st, eng, o.DataDir)
	c.ExeName = o.ExeName
	c.OwnPackage = o.OwnPackage
	if o.Runner != nil {
		c.Runner = o.Runner
	}
	if o.NetworkName != nil {
		sys.NetworkNameProvider = o.NetworkName
	}
	a := &App{plat: o.Platform, store: st, eng: eng, core: c, dataDir: o.DataDir, appDir: o.AppDir, builtin: o.Builtin}
	if a.plat != nil {
		c.TunAllowed = a.plat.TunAllowed
	}
	a.geo = geo.NewResolver(filepath.Join(o.DataDir, "geo-cache.json"))
	a.geo.OnNew = func() {
		if a.emitFn != nil {
			a.emitFn("profiles", nil)
		}
	}
	eng.Mirror = func() string { return st.Settings().GitHubMirror }
	eng.Client = func() *http.Client {
		s := st.Settings()
		return c.HTTPClient(s.UpdateViaProxy, 15*time.Minute)
	}
	return a
}

func writable(dir string) bool {
	f := filepath.Join(dir, ".w")
	if err := os.WriteFile(f, []byte("1"), 0o644); err != nil {
		return false
	}
	os.Remove(f)
	return true
}

func (a *App) emit(name string, data any) {
	if a.emitFn != nil {
		a.emitFn(name, data)
	}
}

// Start initialises background services once the host UI exists.
func Start(a *App) {
	if a.ready {
		return
	}
	a.ready = true
	a.emitFn = a.plat.Emit
	a.core.Emit = a.emitFn
	a.eng.OnChange = func(s *engine.Status) { a.emit("engine", s) }
	a.core.Init()
	a.startAPI(a.emitFn)
	if a.core.Runner.Available(model.EngineSingBox) != nil {
		// first run: fetch the required engines in the background
		go func() {
			time.Sleep(2 * time.Second)
			a.emit("toast", map[string]string{"kind": "info", "text": "دریافت خودکار موتورهای لازم از GitHub…"})
			a.UpdateAllEngines()
		}()
	} else if a.builtin != nil {
		if s, err := a.eng.ExePath("rules-ir"); err != nil || s == "" {
			_ = s
		}
	}
	set := a.store.Settings()
	if set.ConnectOnStart {
		go func() {
			time.Sleep(1500 * time.Millisecond)
			a.core.Connect()
		}()
	}
	if set.CheckUpdates {
		go func() {
			time.Sleep(8 * time.Second)
			if u, err := a.core.CheckAppUpdate(); err == nil && u.Available {
				a.update = u
				a.emit("appUpdate", u)
			}
			for _, e := range a.eng.List() {
				if e.Installed && (a.builtin == nil || e.Kind == "rules") {
					a.eng.CheckLatest(e.ID)
				}
			}
		}()
	}
}

// Stop disconnects and saves state (host shutdown).
func Stop(a *App) {
	a.core.Disconnect()
	a.core.SaveLearning()
}

// ---------------- state ----------------

// AppState is everything the UI needs on load.
type AppState struct {
	Version   string                `json:"version"`
	Settings  model.Settings        `json:"settings"`
	Profiles  []*ProfileView        `json:"profiles"`
	Subs      []*model.Subscription `json:"subs"`
	Status    core.Status           `json:"status"`
	Admin     bool                  `json:"admin"`
	DataDir   string                `json:"dataDir"`
	EnginesOK bool                  `json:"enginesOk"`
	OS        string                `json:"os"`
}

// ProfileView adds computed fields for the list.
type ProfileView struct {
	*model.Profile
	EngineID  string  `json:"engineId"`
	Family    string  `json:"family"`
	Link      string  `json:"link"`
	Success   float64 `json:"success"`
	Latency   float64 `json:"latency"`
	Samples   int     `json:"samples"`
	LastOK    int64   `json:"lastOk"`
	LastFail  int64   `json:"lastFail"`
	Country   string  `json:"country"`
	CountryBy string  `json:"countryBy"`
}

func (a *App) views() []*ProfileView {
	netID, _ := sys.NetworkIDFast()
	var out []*ProfileView
	for _, p := range a.store.Profiles() {
		v := &ProfileView{Profile: p, EngineID: builder.EngineFor(p), Family: p.Family()}
		v.Country, v.CountryBy = a.geo.Country(p.Name, p.Server)
		if s := a.core.Learner.ServerStat(p.ID, netID); s != nil {
			v.Success, v.Latency, v.Samples, v.LastOK, v.LastFail = s.Success, s.Latency, s.N, s.LastOK, s.LastFail
		}
		out = append(out, v)
	}
	return out
}

// GetState returns the full UI state.
func (a *App) GetState() AppState {
	sbOK := a.core.Runner.Available(model.EngineSingBox) == nil
	return AppState{
		Version: core.Version, Settings: a.store.Settings(), Profiles: a.views(), Subs: a.store.Subscriptions(),
		Status: a.core.Status(), Admin: a.core.TunAllowed(), DataDir: a.dataDir, EnginesOK: sbOK, OS: a.plat.OS(),
	}
}

// GetProfiles returns the profile list.
func (a *App) GetProfiles() []*ProfileView { return a.views() }

// GetStatus returns the connection status.
func (a *App) GetStatus() core.Status { return a.core.Status() }

// SaveSettings stores settings and reconnects when needed.
func (a *App) SaveSettings(s model.Settings) error {
	old := a.store.Settings()
	if s.MixedPort <= 0 || s.MixedPort > 65535 {
		return errors.New("پورت نامعتبر است")
	}
	if err := a.store.SetSettings(s); err != nil {
		return err
	}
	if old.AutoStart != s.AutoStart {
		sys.SetAutoStart(s.AutoStart)
	}
	a.core.WriteUserRules()
	if (old.ExtAPI != s.ExtAPI || old.ExtPort != s.ExtPort) && a.emitFn != nil {
		a.startAPI(a.emitFn)
	}
	if needsRestart(old, s) && a.core.Status().State == "connected" {
		go a.core.Reconnect()
	}
	return nil
}

func needsRestart(a, b model.Settings) bool {
	type key struct {
		Mode, RoutingMode, RemoteDNS, DirectDNS, TunStack, LogLevel, FragmentLen   string
		Port, MTU                                                                  int
		LAN, BypassLAN, BypassIran, Ads, Fake, Strict, IPv6, Sniff, Frag, Mux, Sys bool
		Pr                                                                         string
	}
	k := func(s model.Settings) key {
		return key{s.Mode, s.RoutingMode, s.RemoteDNS, s.DirectDNS, s.TunStack, s.LogLevel, s.FragmentLen, s.MixedPort, s.TunMTU,
			s.AllowLAN, s.BypassLAN, s.BypassIran, s.BlockAds, s.FakeIP, s.StrictRoute, s.IPv6, s.Sniff, s.FragmentEnabled, s.MuxEnabled, s.SetSystemProxy,
			strings.Join(s.DirectProcesses, ",") + "|" + s.PerAppMode + "|" + strings.Join(s.PerAppList, ",")}
	}
	return k(a) != k(b)
}

// ---------------- connection ----------------

// Connect starts the tunnel.
func (a *App) Connect() error { return a.core.Connect() }

// Disconnect stops the tunnel.
func (a *App) Disconnect() error { return a.core.Disconnect() }

// Select chooses a profile ("auto" for smart) and reconnects if connected.
func (a *App) Select(id string) error {
	s := a.store.Settings()
	s.SelectedID = id
	if err := a.store.SetSettings(s); err != nil {
		return err
	}
	if st := a.core.Status(); st.State == "connected" && st.ProfileID != id {
		go func() {
			a.core.Disconnect()
			a.core.Connect()
		}()
	}
	return nil
}

// SetMode switches between proxy and tun.
func (a *App) SetMode(mode string) error {
	s := a.store.Settings()
	if s.Mode == mode {
		return nil
	}
	if mode == "tun" && !a.core.TunAllowed() {
		return errors.New("NEED_ADMIN")
	}
	s.Mode = mode
	return a.SaveSettings(s)
}

// SetRouting switches routing mode.
func (a *App) SetRouting(mode string) error {
	s := a.store.Settings()
	s.RoutingMode = mode
	return a.SaveSettings(s)
}

// RelaunchAdmin restarts elevated.
func (a *App) RelaunchAdmin() error {
	a.core.Disconnect()
	return a.plat.Relaunch(true)
}

// ---------------- profiles ----------------

// ImportResult summarises an import.
type ImportResult struct {
	Added  int      `json:"added"`
	Errors []string `json:"errors"`
}

func (a *App) importText(text, group string) ImportResult {
	ps, errs := parser.ParseText(text)
	for _, p := range ps {
		if p.Group == "" {
			p.Group = group
		}
	}
	n, err := a.store.AddProfiles(ps)
	r := ImportResult{Added: n}
	for _, e := range errs {
		r.Errors = append(r.Errors, e.Error())
	}
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
	}
	return r
}

// ImportText imports links / configs. Subscription URLs are added as subscriptions.
func (a *App) ImportText(text string) ImportResult {
	t := strings.TrimSpace(text)
	if (strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://")) && !strings.ContainsAny(t, "\n ") {
		if _, err := parser.ParseLink(t); err != nil { // not a proxy link -> subscription
			sub, err := a.AddSubscription("", t)
			if err != nil {
				return ImportResult{Errors: []string{err.Error()}}
			}
			return ImportResult{Added: sub.Count}
		}
	}
	return a.importText(text, "manual")
}

// ImportClipboard imports from the clipboard.
func (a *App) ImportClipboard() ImportResult {
	t, err := a.plat.ClipboardGet()
	if err != nil || strings.TrimSpace(t) == "" {
		return ImportResult{Errors: []string{"کلیپ‌بورد خالی است"}}
	}
	return a.ImportText(t)
}

// ImportFiles lets the user pick .ovpn/.conf/.txt/.yaml/.json files.
func (a *App) ImportFiles() ImportResult {
	files, err := a.plat.PickFiles("وارد کردن کانفیگ", "*.ovpn;*.conf;*.txt;*.yaml;*.yml;*.json", true)
	if err != nil || len(files) == 0 {
		return ImportResult{}
	}
	total := ImportResult{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			total.Errors = append(total.Errors, err.Error())
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		text := string(b)
		var r ImportResult
		switch strings.ToLower(filepath.Ext(f)) {
		case ".ovpn":
			p, err := parser.ParseOVPN(text, name)
			if err != nil {
				r.Errors = []string{name + ": " + err.Error()}
			} else {
				p.Group = "manual"
				a.store.UpsertProfile(p)
				r.Added = 1
			}
		case ".conf":
			if p, err := parser.ParseWGConf(text, name); err == nil {
				p.Group = "manual"
				a.store.UpsertProfile(p)
				r.Added = 1
			} else {
				r = a.importText(text, "manual")
			}
		default:
			r = a.importText(text, "manual")
		}
		total.Added += r.Added
		total.Errors = append(total.Errors, r.Errors...)
	}
	return total
}

// SaveProfile creates or updates a profile from the editor.
func (a *App) SaveProfile(p model.Profile) (*model.Profile, error) {
	if p.Type == "" {
		return nil, errors.New("نوع پروتکل را انتخاب کنید")
	}
	if p.Type != model.TChain && p.Type != model.TOpenVPN && strings.TrimSpace(p.Server) == "" {
		return nil, errors.New("آدرس سرور را وارد کنید")
	}
	if p.Type == model.TOpenVPN {
		o, err := parser.ParseOVPNOptions(p.OVPN)
		if err != nil || len(o.Remotes) == 0 {
			return nil, errors.New("فایل OpenVPN معتبر نیست (remote ندارد)")
		}
		p.Server, p.Port = o.Remotes[0].Host, o.Remotes[0].Port
	}
	if p.Group == "" {
		p.Group = "manual"
	}
	if p.ID == "" {
		p.ID = model.NewID()
		p.Created = time.Now().Unix()
	}
	if p.Name == "" {
		p.Name = strings.ToUpper(p.Type) + " " + p.Server
	}
	p.Raw = parser.ToLink(&p)
	if err := a.store.UpsertProfile(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// DeleteProfiles removes profiles.
func (a *App) DeleteProfiles(ids []string) error {
	a.core.Learner.Forget(ids)
	return a.store.DeleteProfiles(ids)
}

// DeleteDead removes servers whose latest test failed (ids come from the UI's
// test results); the selected/connected server is kept.
func (a *App) DeleteDead(ids []string) (int, error) {
	res := make([]core.TestResult, 0, len(ids))
	for _, id := range ids {
		res = append(res, core.TestResult{ID: id})
	}
	return a.core.RemoveDead(res, false)
}

// PruneProfiles tests the servers and removes the dead ones (blocking).
func (a *App) PruneProfiles(ids []string) (int, error) { return a.core.PruneDead(ids) }

// DuplicateProfile copies a profile.
func (a *App) DuplicateProfile(id string) error {
	p := a.store.Profile(id)
	if p == nil {
		return errors.New("not found")
	}
	cp := *p
	cp.ID = model.NewID()
	cp.Name = p.Name + " (کپی)"
	cp.Group = "manual"
	cp.Raw = ""
	return a.store.UpsertProfile(&cp)
}

// CopyLink copies a profile's share link to the clipboard.
func (a *App) CopyLink(id string) error {
	p := a.store.Profile(id)
	if p == nil {
		return errors.New("not found")
	}
	l := parser.ToLink(p)
	if p.Type == model.TOpenVPN {
		l = p.OVPN
	}
	return a.plat.ClipboardSet(l)
}

// TestProfiles runs latency tests (async; results via "test" events).
func (a *App) TestProfiles(ids []string) {
	go a.core.TestProfiles(ids)
}

// ReorderProfiles saves manual ordering.
func (a *App) ReorderProfiles(ids []string) error { return a.store.ReorderProfiles(ids) }

// ---------------- subscriptions ----------------

// AddSubscription adds and fetches a subscription.
func (a *App) AddSubscription(name, url string) (*model.Subscription, error) {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http") {
		return nil, errors.New("آدرس اشتراک باید با http شروع شود")
	}
	if name == "" {
		name = "اشتراک " + time.Now().Format("01/02 15:04")
	}
	sub := &model.Subscription{ID: model.NewID(), Name: name, URL: url, AutoUpdate: true, IntervalH: 12, ViaProxy: true}
	if err := a.store.UpsertSub(sub); err != nil {
		return nil, err
	}
	n, err := a.core.UpdateSubscription(sub.ID)
	sub.Count = n
	return sub, err
}

// SaveSubscription edits a subscription.
func (a *App) SaveSubscription(s model.Subscription) error { return a.store.UpsertSub(&s) }

// UpdateSubscription refreshes one subscription.
func (a *App) UpdateSubscription(id string) (int, error) { return a.core.UpdateSubscription(id) }

// UpdateAllSubscriptions refreshes all.
func (a *App) UpdateAllSubscriptions() []string {
	var errs []string
	for _, s := range a.store.Subscriptions() {
		if _, err := a.core.UpdateSubscription(s.ID); err != nil {
			errs = append(errs, s.Name+": "+err.Error())
		}
	}
	return errs
}

// DeleteSubscription removes a subscription and its servers.
func (a *App) DeleteSubscription(id string) error { return a.store.DeleteSub(id) }

// ---------------- engines & updates ----------------

// ListEngines returns component statuses.
func (a *App) ListEngines() []*engine.Status {
	all := a.eng.List()
	if a.builtin == nil {
		return all
	}
	out := append([]*engine.Status(nil), a.builtin...)
	for _, e := range all {
		if e.Kind == "rules" {
			out = append(out, e)
		}
	}
	return out
}

func (a *App) isBuiltin(id string) bool {
	for _, b := range a.builtin {
		if b.ID == id {
			return true
		}
	}
	return false
}

// CheckEngine checks the latest version of one engine.
func (a *App) CheckEngine(id string) (*engine.Status, error) {
	if a.isBuiltin(id) {
		for _, b := range a.builtin {
			if b.ID == id {
				return b, nil
			}
		}
	}
	return a.eng.CheckLatest(id)
}

func (a *App) withEngineStopped(id string, f func() error) error {
	st := a.core.Status()
	wasOn := st.State == "connected" || st.State == "connecting"
	used := id == "sing-box" || strings.EqualFold(st.Engine, id) || (id == "rules-ir")
	if wasOn && used && id != "rules-ir" {
		a.core.Disconnect()
	}
	err := f()
	if wasOn && used && id != "rules-ir" {
		go a.core.Connect()
	}
	return err
}

// UpdateEngine downloads the latest version (async, progress via "engine" events).
func (a *App) UpdateEngine(id string) {
	if a.isBuiltin(id) {
		a.emit("toast", map[string]string{"kind": "info", "text": "این موتور داخل برنامه است و با آپدیت خود برنامه به‌روز می‌شود"})
		return
	}
	go func() {
		err := a.withEngineStopped(id, func() error { return a.eng.Update(id) })
		if err != nil {
			a.emit("toast", map[string]string{"kind": "error", "text": id + ": " + err.Error()})
		} else {
			a.emit("toast", map[string]string{"kind": "ok", "text": id + " به‌روز شد"})
		}
		a.emit("engines", nil)
	}()
}

// InstallEngineFromFile installs an engine from a local archive.
func (a *App) InstallEngineFromFile(id string) error {
	if a.builtin != nil && id != "rules-ir" {
		return errors.New("این موتور داخل برنامه است و با آپدیت برنامه به‌روز می‌شود")
	}
	fs, err := a.plat.PickFiles("انتخاب فایل "+id, "*.zip;*.gz;*.tar.gz;*.exe;*.srs", false)
	if err != nil || len(fs) == 0 {
		return err
	}
	f := fs[0]
	return a.withEngineStopped(id, func() error { return a.eng.InstallArchive(id, f) })
}

// RollbackEngine restores the previous version.
func (a *App) RollbackEngine(id string) error {
	return a.withEngineStopped(id, func() error { return a.eng.Rollback(id) })
}

// UpdateAllEngines updates every installed (or required) component.
func (a *App) UpdateAllEngines() {
	go func() {
		for _, e := range a.ListEngines() {
			if e.Builtin {
				continue
			}
			if e.Installed && !e.HasUpdate && e.Latest != "" {
				continue
			}
			if !e.Installed && !e.Required {
				continue
			}
			a.withEngineStopped(e.ID, func() error { return a.eng.Update(e.ID) })
		}
		a.emit("engines", nil)
	}()
}

// CheckAppUpdate checks GitHub for a new Rahgozar build.
func (a *App) CheckAppUpdate() (*core.AppUpdate, error) {
	u, err := a.core.CheckAppUpdate()
	if err == nil {
		a.update = u
	}
	return u, err
}

// ApplyAppUpdate installs the update and restarts.
func (a *App) ApplyAppUpdate() error {
	if a.update == nil || !a.update.Available {
		return errors.New("آپدیتی موجود نیست")
	}
	return a.plat.InstallUpdate(a.core, a.update, func(p float64) { a.emit("appUpdateProgress", p) })
}

// ---------------- learning ----------------

// LearningView is the "smart" page data.
type LearningView struct {
	NetID    string               `json:"netId"`
	NetLabel string               `json:"netLabel"`
	Networks map[string]string    `json:"networks"`
	Families []smart.FamilyHealth `json:"families"`
	Ranking  []RankView           `json:"ranking"`
	Domains  []smart.DomainInfo   `json:"domains"`
	Direct   int                  `json:"direct"`
	Proxy    int                  `json:"proxy"`
	Unknown  int                  `json:"unknown"`
	Samples  int                  `json:"samples"`
}

// RankView is a ranked server.
type RankView struct {
	smart.Ranked
	Name    string `json:"name"`
	Family  string `json:"family"`
	Country string `json:"country"`
}

// GetLearning returns learned knowledge for the current network.
func (a *App) GetLearning() LearningView {
	netID, label := sys.NetworkID()
	v := LearningView{NetID: netID, NetLabel: label}
	snap, _ := a.core.Learner.Snapshot()
	v.Networks = snap.Networks
	v.Samples = snap.Total
	v.Families = a.core.Learner.FamilyHealth(netID)
	var cands []smart.Candidate
	names := map[string]*model.Profile{}
	for _, p := range a.store.Profiles() {
		if p.IsRAS() || p.Type == model.TChain {
			continue
		}
		cands = append(cands, smart.Candidate{ID: p.ID, Family: p.Family()})
		names[p.ID] = p
	}
	for i, r := range a.core.Learner.Rank(cands, netID) {
		if i >= 30 {
			break
		}
		p := names[r.ID]
		cc, _ := a.geo.Country(p.Name, p.Server)
		v.Ranking = append(v.Ranking, RankView{Ranked: r, Name: p.Name, Family: p.Family(), Country: cc})
	}
	v.Domains = a.core.Domains.List()
	if len(v.Domains) > 500 {
		v.Domains = v.Domains[:500]
	}
	v.Direct, v.Proxy, v.Unknown = a.core.Domains.Counts()
	return v
}

// SetDomain forces a verdict for a domain (direct | proxy | auto).
func (a *App) SetDomain(domain, verdict string) { a.core.Domains.Set(domain, verdict) }

// ResetLearning clears learned data (what: servers | domains | all).
func (a *App) ResetLearning(what string) {
	if what == "servers" || what == "all" {
		a.core.Learner.Reset()
		a.core.SaveLearning()
	}
	if what == "domains" || what == "all" {
		a.core.Domains.Reset()
	}
}

// ---------------- misc ----------------

// GetLogs returns buffered logs.
func (a *App) GetLogs() []core.LogLine { return a.core.Logs() }

// ClearLogs clears the log buffer.
func (a *App) ClearLogs() { a.core.ClearLogs() }

// CopyText copies text to clipboard.
func (a *App) CopyText(t string) error { return a.plat.ClipboardSet(t) }

// OpenDataDir opens the data folder.
func (a *App) OpenDataDir() error { return a.plat.OpenPath(a.dataDir) }

// extensionVersion reads the version of the extension folder next to the app.
func (a *App) extensionVersion() string {
	b, err := os.ReadFile(filepath.Join(a.appDir, "extension", "manifest.json"))
	if err != nil {
		return ""
	}
	var m struct {
		Version string `json:"version"`
	}
	json.Unmarshal(b, &m)
	return m.Version
}

// OpenExtensionDir opens the bundled browser extension folder.
func (a *App) OpenExtensionDir() error {
	d := filepath.Join(a.appDir, "extension")
	if _, err := os.Stat(d); err != nil {
		return errors.New("پوشه extension کنار برنامه پیدا نشد؛ آن را از صفحه Releases دانلود کنید")
	}
	return a.plat.OpenPath(d)
}

// OpenURL opens a link in the browser.
func (a *App) OpenURL(u string) { a.plat.OpenURL(u) }

// FixL2TP applies the L2TP-behind-NAT registry fix.
func (a *App) FixL2TP() error { return sys.FixL2TPBehindNAT() }

// WindowMinimise minimises the window.
func (a *App) WindowMinimise() { a.plat.Minimise() }

// WindowToggleMaximise toggles maximise.
func (a *App) WindowToggleMaximise() { a.plat.ToggleMaximise() }

// Quit exits the app (disconnecting first).
func (a *App) Quit() {
	a.core.Disconnect()
	a.plat.Quit()
}

// Focus brings the window to the front (second instance launched).
func Focus(a *App) { a.plat.Focus() }

// EnsureExtension writes the embedded browser extension to "<app dir>/extension"
// when the folder is missing or holds a different version.
func EnsureExtension(efs fs.FS, root string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dst := filepath.Join(filepath.Dir(exe), "extension")
	readVer := func(b []byte) string {
		var m struct {
			Version string `json:"version"`
		}
		json.Unmarshal(b, &m)
		return m.Version
	}
	emb, err := fs.ReadFile(efs, root+"/manifest.json")
	if err != nil {
		return
	}
	if disk, err := os.ReadFile(filepath.Join(dst, "manifest.json")); err == nil && readVer(disk) == readVer(emb) {
		return
	}
	fs.WalkDir(efs, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		b, err := fs.ReadFile(efs, p)
		if err != nil {
			return nil
		}
		out := filepath.Join(dst, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, b, 0o644)
		return nil
	})
}

// StartHeadless initialises the core and the extension API without a window
// (used by tests and the UI harness).
func StartHeadless(a *App, emit func(string, any)) {
	if a.plat == nil {
		a.plat = &HeadlessPlatform{EmitFn: emit}
		a.core.TunAllowed = a.plat.TunAllowed
	}
	Start(a)
}

// HeadlessPlatform is a no-UI platform for tests.
type HeadlessPlatform struct{ EmitFn func(string, any) }

func (h *HeadlessPlatform) OS() string { return "headless" }
func (h *HeadlessPlatform) Emit(n string, d any) {
	if h.EmitFn != nil {
		h.EmitFn(n, d)
	}
}
func (h *HeadlessPlatform) ClipboardGet() (string, error)                    { return "", nil }
func (h *HeadlessPlatform) ClipboardSet(string) error                        { return nil }
func (h *HeadlessPlatform) PickFiles(string, string, bool) ([]string, error) { return nil, nil }
func (h *HeadlessPlatform) OpenURL(string)                                   {}
func (h *HeadlessPlatform) OpenPath(string) error                            { return nil }
func (h *HeadlessPlatform) Minimise()                                        {}
func (h *HeadlessPlatform) ToggleMaximise()                                  {}
func (h *HeadlessPlatform) Focus()                                           {}
func (h *HeadlessPlatform) Quit()                                            {}
func (h *HeadlessPlatform) Relaunch(bool) error                              { return nil }
func (h *HeadlessPlatform) InstallUpdate(*core.Core, *core.AppUpdate, func(float64)) error {
	return nil
}
func (h *HeadlessPlatform) TunAllowed() bool { return true }
