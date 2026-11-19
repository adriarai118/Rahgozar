package desk

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"rahgozar/internal/backup"
	"rahgozar/internal/builder"
	"rahgozar/internal/core"
	"rahgozar/internal/model"
	"rahgozar/internal/parser"
	"rahgozar/internal/smart"
)

// fileSaver platforms can save a file where the user chooses. The returned
// string is the saved path, "" when cancelled, or "pending" when the
// platform finishes asynchronously (Android shows its own result).
type fileSaver interface {
	SaveFile(defaultName string, data []byte) (string, error)
}

// BackupOptions chooses what goes into a backup.
type BackupOptions struct {
	Settings bool   `json:"settings"`
	Learning bool   `json:"learning"`
	Password string `json:"password"`
}

// CreateBackup writes a backup file of servers, subscriptions and,
// optionally, settings and learned data.
func (a *App) CreateBackup(o BackupOptions) (string, error) {
	d := &backup.Data{Profiles: a.store.Profiles(), Subs: a.store.Subscriptions()}
	d.Manifest.Version = core.Version
	d.Manifest.Platform = a.plat.OS()
	if o.Settings {
		st := a.store.Settings()
		st.ExtToken = "" // per-device browser pairing
		d.Settings = &st
	}
	if o.Learning {
		a.core.SaveLearning()
		if b, err := os.ReadFile(filepath.Join(a.dataDir, "learning.json")); err == nil {
			d.Learning = b
		}
		if a.core.Domains != nil {
			d.Domains, _ = json.Marshal(a.core.Domains.List())
		}
	}
	b, err := backup.Write(d, o.Password)
	if err != nil {
		return "", err
	}
	name := "Rahgozar-backup-" + time.Now().Format("2006-01-02-1504") + ".rgbak"
	if s, ok := a.plat.(fileSaver); ok {
		return s.SaveFile(name, b)
	}
	dir := filepath.Join(a.dataDir, "backups")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", err
	}
	a.plat.OpenPath(dir)
	return p, nil
}

// BackupFile describes a picked backup waiting for options or a password.
type BackupFile struct {
	Token     string           `json:"token,omitempty"`
	Name      string           `json:"name"`
	Kind      backup.Kind      `json:"kind"`
	Encrypted bool             `json:"encrypted"`
	Manifest  *backup.Manifest `json:"manifest,omitempty"`
	Imported  *ImportResult    `json:"imported,omitempty"` // already handled (no questions needed)
	Summary   *ForeignSummary  `json:"summary,omitempty"`  // other apps' backups (Exclave)
}

// ForeignSummary counts what another app's backup holds.
type ForeignSummary struct {
	Subs       int            `json:"subs"`       // subscription links
	Manual     int            `json:"manual"`     // servers outside subscriptions
	SubServers int            `json:"subServers"` // servers cached in subscriptions
	Skipped    map[string]int `json:"skipped"`
}

var pending = struct {
	sync.Mutex
	m map[string]pendingFile
}{m: map[string]pendingFile{}}

type pendingFile struct {
	name string
	b    []byte
	ex   *backup.ExclaveData
}

// OpenBackup lets the user pick a backup/export file. Files that need no
// answers (config zips, WG Tunnel without password, plain configs) are
// imported right away; Rahgozar backups and encrypted files come back with
// a token for RestoreBackup.
func (a *App) OpenBackup() (*BackupFile, error) {
	files, err := a.plat.PickFiles("انتخاب فایل پشتیبان", "*.rgbak;*.zip;*.sqlite3;*.aes;*.json;*.conf;*.txt;*.yaml", false)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		return nil, err
	}
	name := filepath.Base(files[0])
	info := backup.Detect(b, name)
	bf := &BackupFile{Name: name, Kind: info.Kind, Encrypted: info.Encrypted, Manifest: info.Manifest}
	if info.Kind == backup.KindExclave {
		ex, err := backup.Exclave(b)
		if err != nil {
			return nil, err
		}
		bf.Summary = exclaveSummary(ex)
		bf.Token = model.NewID()
		pending.Lock()
		pending.m = map[string]pendingFile{bf.Token: {name: name, ex: ex}}
		pending.Unlock()
		return bf, nil
	}
	if info.Kind == backup.KindRahgozar || info.Encrypted {
		bf.Token = model.NewID()
		pending.Lock()
		pending.m = map[string]pendingFile{bf.Token: {name: name, b: b}} // keep only the latest
		pending.Unlock()
		return bf, nil
	}
	r := a.importForeign(b, name, info.Kind, "")
	bf.Imported = &r
	return bf, nil
}

// RestoreOptions chooses what to take from a Rahgozar backup.
type RestoreOptions struct {
	Servers    bool `json:"servers"`
	Settings   bool `json:"settings"`
	Learning   bool `json:"learning"`
	Subs       bool `json:"subs"`       // other apps: add subscription links
	SubServers bool `json:"subServers"` // other apps: also the servers cached in them
}

// RestoreBackup restores a picked backup (see OpenBackup). Servers and
// subscriptions are merged: nothing already here is removed or changed.
func (a *App) RestoreBackup(token, password string, o RestoreOptions) (ImportResult, error) {
	pending.Lock()
	f, ok := pending.m[token]
	pending.Unlock()
	if !ok {
		return ImportResult{}, errors.New("فایل پشتیبان دوباره انتخاب شود")
	}
	if f.ex != nil {
		r := a.restoreExclave(f.ex, o)
		a.forget(token)
		return r, nil
	}
	info := backup.Detect(f.b, f.name)
	if info.Kind != backup.KindRahgozar {
		r := a.importForeign(f.b, f.name, info.Kind, password)
		if len(r.Errors) == 1 && r.Errors[0] == backup.ErrNeedPassword.Error() {
			return r, backup.ErrNeedPassword
		}
		if len(r.Errors) == 1 && r.Errors[0] == backup.ErrPassword.Error() {
			return r, backup.ErrPassword
		}
		a.forget(token)
		return r, nil
	}
	d, err := backup.Read(f.b, password)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{}
	if o.Servers {
		res.Added = a.mergeServers(d)
	}
	if o.Settings && d.Settings != nil {
		if err := a.SaveSettings(mergeSettings(a.store.Settings(), *d.Settings, a.store)); err != nil {
			res.Errors = append(res.Errors, err.Error())
		}
	}
	if o.Learning {
		if len(d.Learning) > 0 {
			if err := a.core.Learner.MergeJSON(d.Learning); err == nil {
				a.core.SaveLearning()
			}
		}
		if len(d.Domains) > 0 && a.core.Domains != nil {
			var list []smart.DomainInfo
			if json.Unmarshal(d.Domains, &list) == nil {
				a.core.Domains.Import(list)
			}
		}
	}
	a.forget(token)
	a.emit("profiles", nil)
	a.emit("subs", nil)
	a.emit("settingsChanged", nil)
	return res, nil
}

func (a *App) forget(token string) {
	pending.Lock()
	delete(pending.m, token)
	pending.Unlock()
}

// mergeServers adds subscriptions and servers from a backup that aren't here
// yet; IDs are kept where free so chains and learned stats still match.
func (a *App) mergeServers(d *backup.Data) int {
	subByURL := map[string]string{}
	subIDs := map[string]bool{}
	for _, s := range a.store.Subscriptions() {
		subByURL[strings.TrimSpace(s.URL)] = s.ID
		subIDs[s.ID] = true
	}
	groupMap := map[string]string{} // backup sub id -> local sub id
	for _, s := range d.Subs {
		if s == nil {
			continue
		}
		if id, ok := subByURL[strings.TrimSpace(s.URL)]; ok {
			groupMap[s.ID] = id
			continue
		}
		cp := *s
		if subIDs[cp.ID] || cp.ID == "" {
			cp.ID = model.NewID()
		}
		groupMap[s.ID] = cp.ID
		subIDs[cp.ID] = true
		a.store.UpsertSub(&cp)
	}
	have := map[string]bool{}
	ids := map[string]bool{}
	for _, p := range a.store.Profiles() {
		have[profileKey(p)] = true
		ids[p.ID] = true
	}
	var add []*model.Profile
	for _, p := range d.Profiles {
		if p == nil || have[profileKey(p)] {
			continue
		}
		cp := *p
		if g, ok := groupMap[cp.Group]; ok {
			cp.Group = g
		}
		if ids[cp.ID] {
			cp.ID = model.NewID()
		}
		ids[cp.ID] = true
		have[profileKey(&cp)] = true
		add = append(add, &cp)
	}
	n, _ := a.store.AddProfiles(add)
	a.core.RecountSubs()
	return n
}

func profileKey(p *model.Profile) string {
	if p.Type == model.TChain {
		return "chain|" + p.Name + "|" + strings.Join(p.Chain, ",")
	}
	if p.Raw != "" {
		return "raw|" + p.Raw
	}
	return fmt.Sprintf("%s|%s|%d|%s|%s|%s", p.Type, strings.ToLower(p.Server), p.Port, p.UUID, p.Password, p.PrivateKey)
}

// mergeSettings takes a backup's settings but keeps what belongs to this
// device: mode, system proxy, autostart, tray, per-app list, browser pairing.
func mergeSettings(local, b model.Settings, st interface{ Profile(string) *model.Profile }) model.Settings {
	out := b
	out.Mode = local.Mode
	out.SetSystemProxy = local.SetSystemProxy
	out.TunStack = local.TunStack
	out.AutoStart = local.AutoStart
	out.MinimizeToTray = local.MinimizeToTray
	out.ExtAPI, out.ExtPort, out.ExtToken = local.ExtAPI, local.ExtPort, local.ExtToken
	out.PerAppMode, out.PerAppList = local.PerAppMode, local.PerAppList
	out.DirectProcesses = local.DirectProcesses
	if out.SelectedID != "auto" && out.SelectedID != "" && st.Profile(out.SelectedID) == nil {
		out.SelectedID = local.SelectedID
	}
	out.ProxyDomains = union(local.ProxyDomains, b.ProxyDomains)
	out.DirectDomains = union(local.DirectDomains, b.DirectDomains)
	out.BlockDomains = union(local.BlockDomains, b.BlockDomains)
	if out.MixedPort <= 0 || out.MixedPort > 65535 {
		out.MixedPort = local.MixedPort
	}
	return out
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range [][]string{a, b} {
		for _, s := range l {
			if s = strings.TrimSpace(s); s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// importForeign imports another app's backup/export or a plain config file.
func (a *App) importForeign(b []byte, name string, kind backup.Kind, password string) ImportResult {
	var items []backup.Item
	var err error
	switch kind {
	case backup.KindWGTunnel:
		items, err = backup.WGTunnel(b, name, password)
	case backup.KindZip:
		items, err = backup.ZipConfigs(b)
	case backup.KindExclave:
		ex, err := backup.Exclave(b)
		if err != nil {
			return ImportResult{Errors: []string{err.Error()}}
		}
		return a.restoreExclave(ex, RestoreOptions{Servers: true, Subs: true})
	case backup.KindRahgozar:
		d, err := backup.Read(b, password)
		if err != nil {
			return ImportResult{Errors: []string{err.Error()}}
		}
		return ImportResult{Added: a.mergeServers(d)}
	default:
		items = []backup.Item{{Name: strings.TrimSuffix(name, filepath.Ext(name)), Text: string(b)}}
	}
	if err != nil {
		return ImportResult{Errors: []string{err.Error()}}
	}
	total := ImportResult{}
	for _, it := range items {
		r := a.importNamed(it.Text, it.Name)
		total.Added += r.Added
		total.Errors = append(total.Errors, r.Errors...)
	}
	return total
}

func exclaveSummary(ex *backup.ExclaveData) *ForeignSummary {
	subs := map[int64]bool{}
	s := &ForeignSummary{Skipped: ex.Skipped}
	for _, g := range ex.Groups {
		if g.Sub && g.URL != "" {
			subs[g.ID] = true
			s.Subs++
		}
	}
	for _, p := range ex.Profiles {
		if subs[p.Group] {
			s.SubServers++
		} else {
			s.Manual++
		}
	}
	return s
}

// restoreExclave adds an Exclave backup's subscriptions and servers.
func (a *App) restoreExclave(ex *backup.ExclaveData, o RestoreOptions) ImportResult {
	res := ImportResult{}
	byURL := map[string]string{}
	for _, s := range a.store.Subscriptions() {
		byURL[strings.TrimSpace(s.URL)] = s.ID
	}
	subOf := map[int64]string{} // exclave group -> local sub id ("" = not imported)
	isSub := map[int64]bool{}
	for _, g := range ex.Groups {
		if !g.Sub || g.URL == "" {
			continue
		}
		isSub[g.ID] = true
		if !o.Subs {
			continue
		}
		u := strings.TrimSpace(g.URL)
		if id, ok := byURL[u]; ok {
			subOf[g.ID] = id
			continue
		}
		name := g.Name
		if name == "" {
			name = "Exclave"
		}
		sub := &model.Subscription{Name: name, URL: u, UserAgent: g.UserAgent, AutoUpdate: true, IntervalH: 12, ViaProxy: true}
		a.store.UpsertSub(sub)
		byURL[u] = sub.ID
		subOf[g.ID] = sub.ID
	}
	have := map[string]bool{}
	for _, p := range a.store.Profiles() {
		have[profileKey(p)] = true
	}
	skipped := map[string]int{}
	for r, c := range ex.Skipped {
		skipped[r] = c
	}
	var add []*model.Profile
	for _, x := range ex.Profiles {
		p := x.Profile
		if isSub[x.Group] {
			if !o.SubServers || subOf[x.Group] == "" {
				continue
			}
			p.Group = subOf[x.Group]
		} else {
			if !o.Servers {
				continue
			}
			p.Group = "manual"
		}
		if err := builder.Validate(p); err != nil {
			skipped[err.Error()]++
			continue
		}
		p.ID = model.NewID()
		p.Raw = parser.ToLink(p)
		k := profileKey(p)
		if have[k] {
			continue
		}
		have[k] = true
		add = append(add, p)
	}
	n, err := a.store.AddProfiles(add)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
	}
	res.Added = n
	a.core.RecountSubs()
	for r, c := range skipped {
		res.Errors = append(res.Errors, fmt.Sprintf("%s: %d مورد رد شد", r, c))
	}
	sort.Strings(res.Errors)
	a.emit("profiles", nil)
	a.emit("subs", nil)
	return res
}
