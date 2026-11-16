package engine

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"rahgozar/internal/sys"
)

// Status is the UI view of a component.
type Status struct {
	Spec
	Installed bool    `json:"installed"`
	Version   string  `json:"version"`
	Latest    string  `json:"latest"`
	HasUpdate bool    `json:"hasUpdate"`
	Busy      bool    `json:"busy"`
	Progress  float64 `json:"progress"`
	Message   string  `json:"message"`
	Error     string  `json:"error"`
	HasBackup bool    `json:"hasBackup"`
	Path      string  `json:"path"`
	CheckedAt int64   `json:"checkedAt"`
}

// Manager installs and updates components under Dir.
type Manager struct {
	Dir      string
	Client   func() *http.Client
	Mirror   func() string
	OnChange func(*Status)

	mu    sync.Mutex
	specs []*Spec
	state map[string]*Status
}

type manifestEntry struct {
	Version string `json:"version"`
	ModTime int64  `json:"modTime"`
	Latest  string `json:"latest"`
	Checked int64  `json:"checked"`
}

// NewManager creates a manager.
func NewManager(dir string) *Manager {
	m := &Manager{Dir: dir, specs: Specs(), state: map[string]*Status{}}
	os.MkdirAll(dir, 0o755)
	m.Client = func() *http.Client { return &http.Client{Timeout: 10 * time.Minute} }
	m.Mirror = func() string { return "" }
	for _, s := range m.specs {
		m.state[s.ID] = &Status{Spec: *s}
	}
	m.loadManifest()
	return m
}

func (m *Manager) manifestPath() string { return filepath.Join(m.Dir, "manifest.json") }

func (m *Manager) loadManifest() {
	var mf map[string]manifestEntry
	b, err := os.ReadFile(m.manifestPath())
	if err == nil && json.Unmarshal(b, &mf) == nil {
		for id, e := range mf {
			if st, ok := m.state[id]; ok {
				st.Version, st.Latest, st.CheckedAt = e.Version, e.Latest, e.Checked
			}
		}
	}
}

func (m *Manager) saveManifest() {
	mf := map[string]manifestEntry{}
	for id, st := range m.state {
		mt := int64(0)
		if fi, err := os.Stat(m.exePath(id)); err == nil {
			mt = fi.ModTime().Unix()
		}
		mf[id] = manifestEntry{Version: st.Version, ModTime: mt, Latest: st.Latest, Checked: st.CheckedAt}
	}
	b, _ := json.MarshalIndent(mf, "", " ")
	os.WriteFile(m.manifestPath(), b, 0o644)
}

func (m *Manager) spec(id string) *Spec {
	for _, s := range m.specs {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// InstallDir returns the folder of a component.
func (m *Manager) InstallDir(id string) string { return filepath.Join(m.Dir, id) }

func (m *Manager) exePath(id string) string {
	s := m.spec(id)
	if s == nil || s.Kind != "binary" {
		return ""
	}
	return filepath.Join(m.InstallDir(id), s.exe)
}

// ExePath returns the executable path if installed.
func (m *Manager) ExePath(id string) (string, error) {
	p := m.exePath(id)
	if p == "" {
		return "", fmt.Errorf("unknown engine %s", id)
	}
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("موتور %s نصب نیست؛ از بخش «موتورها» آن را نصب کنید", id)
	}
	return p, nil
}

// ExeNames returns the file names of all engine executables.
func (m *Manager) ExeNames() []string {
	var out []string
	for _, s := range m.specs {
		if s.exe != "" {
			out = append(out, s.exe)
		}
	}
	return out
}

// RulesDir is where rule-set files live.
func (m *Manager) RulesDir() string { return m.InstallDir("rules-ir") }

func (m *Manager) emit(st *Status) {
	if m.OnChange != nil {
		cp := *st
		m.OnChange(&cp)
	}
}

// List refreshes local state and returns all statuses.
func (m *Manager) List() []*Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Status
	for _, s := range m.specs {
		st := m.state[s.ID]
		m.refreshLocal(s, st)
		cp := *st
		out = append(out, &cp)
	}
	return out
}

func (m *Manager) refreshLocal(s *Spec, st *Status) {
	_, err := os.Stat(m.InstallDir(s.ID) + ".bak")
	st.HasBackup = err == nil
	if s.Kind == "rules" {
		n := 0
		for _, f := range s.files {
			if _, err := os.Stat(filepath.Join(m.InstallDir(s.ID), f)); err == nil {
				n++
			}
		}
		st.Installed = n > 0
		if b, err := os.ReadFile(filepath.Join(m.InstallDir(s.ID), "version.txt")); err == nil {
			st.Version = strings.TrimSpace(string(b))
		}
		st.Path = m.InstallDir(s.ID)
	} else {
		p := m.exePath(s.ID)
		fi, err := os.Stat(p)
		st.Installed = err == nil
		st.Path = p
		if st.Installed && (st.Version == "" || st.Version == "?") {
			st.Version = m.detectVersion(s, p)
		}
		if !st.Installed {
			st.Version = ""
		}
		_ = fi
	}
	st.HasUpdate = st.Installed && st.Latest != "" && normVer(st.Latest) != normVer(st.Version)
}

func normVer(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	return v
}

func (m *Manager) detectVersion(s *Spec, exe string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c := sys.Command(exe, s.versionArgs...)
	out, _ := runCtx(ctx, c)
	if mm := s.versionRe.FindStringSubmatch(out); len(mm) > 1 {
		return mm[1]
	}
	return "?"
}

// HasRuleFiles reports which rule-set tags have files present.
func (m *Manager) HasRuleFiles(files map[string]string) map[string]bool {
	out := map[string]bool{}
	for tag, f := range files {
		if _, err := os.Stat(filepath.Join(m.RulesDir(), f)); err == nil {
			out[tag] = true
		}
	}
	return out
}

// ---------------- GitHub ----------------

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// LatestRelease asks GitHub for the latest release of repo. When the API is
// unreachable/rate limited it falls back to the releases/latest redirect.
func LatestRelease(client *http.Client, repo string) (*ghRelease, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Rahgozar")
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			var r ghRelease
			if err := json.NewDecoder(resp.Body).Decode(&r); err == nil && r.TagName != "" {
				return &r, nil
			}
		}
	}
	// fallback: follow redirect manually
	nc := *client
	nc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req2, _ := http.NewRequest("HEAD", "https://github.com/"+repo+"/releases/latest", nil)
	req2.Header.Set("User-Agent", "Rahgozar")
	resp2, err2 := nc.Do(req2)
	if err2 != nil {
		if err != nil {
			return nil, err
		}
		return nil, err2
	}
	resp2.Body.Close()
	loc := resp2.Header.Get("Location")
	if i := strings.LastIndex(loc, "/tag/"); i >= 0 {
		return &ghRelease{TagName: loc[i+5:]}, nil
	}
	return nil, fmt.Errorf("github: cannot determine latest release of %s", repo)
}

func (m *Manager) mirror(u string) string {
	mr := strings.TrimSpace(m.Mirror())
	if mr == "" || !strings.Contains(u, "github") {
		return u
	}
	if !strings.HasSuffix(mr, "/") {
		mr += "/"
	}
	return mr + u
}

// guessAsset builds an asset name from a tag when the API is unavailable.
func guessAsset(s *Spec, tag string) string {
	goos, arch := osArch()
	v := strings.TrimPrefix(tag, "v")
	switch s.ID {
	case "sing-box":
		return fmt.Sprintf("sing-box-%s-%s-%s.zip", v, goos, arch)
	case "xray":
		xa := map[string]string{"amd64": "64", "386": "32", "arm64": "arm64-v8a"}[arch]
		xo := map[string]string{"windows": "windows", "linux": "linux", "darwin": "macos"}[goos]
		return fmt.Sprintf("Xray-%s-%s.zip", xo, xa)
	case "mihomo":
		ext := "zip"
		if goos != "windows" {
			ext = "gz"
		}
		return fmt.Sprintf("mihomo-%s-%s-compatible-%s.%s", goos, arch, tag, ext)
	}
	return ""
}

// CheckLatest queries GitHub for the newest version of one component.
func (m *Manager) CheckLatest(id string) (*Status, error) {
	s := m.spec(id)
	if s == nil {
		return nil, fmt.Errorf("unknown %s", id)
	}
	rel, err := LatestRelease(m.Client(), s.Repo)
	m.mu.Lock()
	st := m.state[id]
	if err != nil {
		st.Error = err.Error()
	} else {
		st.Error = ""
		st.Latest = strings.TrimPrefix(rel.TagName, "v")
		if s.Kind == "rules" {
			st.Latest = rel.TagName
		}
		st.CheckedAt = time.Now().Unix()
	}
	m.refreshLocal(s, st)
	m.saveManifest()
	cp := *st
	m.mu.Unlock()
	m.emit(&cp)
	return &cp, err
}

func (m *Manager) setBusy(id string, busy bool, prog float64, msg string, errS string) {
	m.mu.Lock()
	st := m.state[id]
	st.Busy, st.Progress, st.Message, st.Error = busy, prog, msg, errS
	cp := *st
	m.mu.Unlock()
	m.emit(&cp)
}

type progressReader struct {
	r       io.Reader
	n, size int64
	last    time.Time
	cb      func(float64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.size > 0 && time.Since(p.last) > 200*time.Millisecond {
		p.last = time.Now()
		p.cb(float64(p.n) / float64(p.size))
	}
	return n, err
}

func (m *Manager) download(url, dst string, cb func(float64)) error {
	urls := []string{m.mirror(url)}
	if urls[0] != url {
		urls = append(urls, url)
	}
	var lastErr error
	for _, u := range urls {
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "Rahgozar")
		resp, err := m.Client().Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
			continue
		}
		f, err := os.Create(dst)
		if err != nil {
			resp.Body.Close()
			return err
		}
		_, err = io.Copy(f, &progressReader{r: resp.Body, size: resp.ContentLength, cb: cb})
		resp.Body.Close()
		f.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// Update downloads and installs the latest release of a component. The
// caller must make sure the engine is not running.
func (m *Manager) Update(id string) error {
	s := m.spec(id)
	if s == nil {
		return fmt.Errorf("unknown %s", id)
	}
	m.setBusy(id, true, 0, "در حال بررسی نسخه…", "")
	err := m.update(s)
	if err != nil {
		m.setBusy(id, false, 0, "", err.Error())
		return err
	}
	m.mu.Lock()
	st := m.state[id]
	st.Version = ""
	m.refreshLocal(s, st)
	m.saveManifest()
	m.mu.Unlock()
	m.setBusy(id, false, 1, "نصب شد", "")
	return nil
}

func (m *Manager) update(s *Spec) error {
	rel, err := LatestRelease(m.Client(), s.Repo)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(m.Dir, ".dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if s.Kind == "rules" {
		stage := filepath.Join(tmp, "stage")
		os.MkdirAll(stage, 0o755)
		for i, f := range s.files {
			sources := []string{
				"https://raw.githubusercontent.com/" + s.Repo + "/rule-set/" + f,
				"https://cdn.jsdelivr.net/gh/" + s.Repo + "@rule-set/" + f,
			}
			var lastErr error
			ok := false
			for _, u := range sources {
				m.setBusy(s.ID, true, float64(i)/float64(len(s.files)), "دانلود "+f, "")
				if err := m.download(u, filepath.Join(stage, f), func(float64) {}); err == nil {
					ok = true
					break
				} else {
					lastErr = err
				}
			}
			if !ok {
				return fmt.Errorf("%s: %v", f, lastErr)
			}
		}
		os.WriteFile(filepath.Join(stage, "version.txt"), []byte(rel.TagName), 0o644)
		return m.swapIn(s.ID, stage)
	}
	var url string
	for _, re := range s.assetRe {
		for _, a := range rel.Assets {
			if re.MatchString(a.Name) {
				url = a.URL
				break
			}
		}
		if url != "" {
			break
		}
	}
	if url == "" {
		name := guessAsset(s, rel.TagName)
		if name == "" {
			return errors.New("asset not found")
		}
		url = "https://github.com/" + s.Repo + "/releases/download/" + rel.TagName + "/" + name
	}
	archive := filepath.Join(tmp, filepath.Base(url))
	m.setBusy(s.ID, true, 0, "دانلود "+rel.TagName, "")
	if err := m.download(url, archive, func(p float64) { m.setBusy(s.ID, true, p*0.9, "دانلود "+rel.TagName, "") }); err != nil {
		return err
	}
	m.setBusy(s.ID, true, 0.92, "استخراج…", "")
	return m.InstallArchive(s.ID, archive)
}

// InstallArchive installs a component from a local zip/tar.gz/exe file.
func (m *Manager) InstallArchive(id, archive string) error {
	s := m.spec(id)
	if s == nil {
		return fmt.Errorf("unknown %s", id)
	}
	stage, err := os.MkdirTemp(m.Dir, ".stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	lower := strings.ToLower(archive)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		err = extractZip(archive, stage)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		err = extractTarGz(archive, stage)
	case strings.HasSuffix(lower, ".gz"):
		err = extractGz(archive, filepath.Join(stage, s.exe))
	case strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".srs") || s.Kind == "binary":
		err = copyFile(archive, filepath.Join(stage, filepath.Base(archive)))
	default:
		err = errors.New("unsupported file type")
	}
	if err != nil {
		return err
	}
	if s.Kind == "binary" {
		// locate & normalise the executable name
		found := ""
		entries, _ := os.ReadDir(stage)
		for _, e := range entries {
			if e.Name() == s.exe {
				found = e.Name()
			}
		}
		if found == "" {
			for _, e := range entries {
				for re, to := range s.rename {
					if re.MatchString(e.Name()) && !e.IsDir() && (strings.HasSuffix(e.Name(), ".exe") || !strings.Contains(e.Name(), ".")) {
						os.Rename(filepath.Join(stage, e.Name()), filepath.Join(stage, to))
						found = to
					}
				}
				if found == "" && strings.EqualFold(strings.TrimSuffix(e.Name(), ".exe"), strings.TrimSuffix(s.exe, ".exe")) {
					os.Rename(filepath.Join(stage, e.Name()), filepath.Join(stage, s.exe))
					found = s.exe
				}
			}
		}
		if found == "" {
			return fmt.Errorf("فایل %s داخل بسته پیدا نشد", s.exe)
		}
		os.Chmod(filepath.Join(stage, s.exe), 0o755)
	}
	return m.swapIn(id, stage)
}

// swapIn atomically replaces the install dir with stage, keeping a backup.
func (m *Manager) swapIn(id, stage string) error {
	dst := m.InstallDir(id)
	bak := dst + ".bak"
	newDir := dst + ".new"
	os.RemoveAll(newDir)
	if err := copyDir(stage, newDir); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		os.RemoveAll(bak)
		if err := os.Rename(dst, bak); err != nil {
			os.RemoveAll(newDir)
			return fmt.Errorf("موتور در حال اجراست یا قفل است: %v", err)
		}
	}
	if err := os.Rename(newDir, dst); err != nil {
		os.Rename(bak, dst)
		return err
	}
	m.mu.Lock()
	m.state[id].Version = ""
	m.mu.Unlock()
	return nil
}

// Rollback restores the previous version.
func (m *Manager) Rollback(id string) error {
	dst := m.InstallDir(id)
	bak := dst + ".bak"
	if _, err := os.Stat(bak); err != nil {
		return errors.New("نسخه قبلی موجود نیست")
	}
	tmp := dst + ".tmp"
	os.RemoveAll(tmp)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, tmp); err != nil {
			return err
		}
	}
	if err := os.Rename(bak, dst); err != nil {
		os.Rename(tmp, dst)
		return err
	}
	os.Rename(tmp, bak)
	m.mu.Lock()
	m.state[id].Version = ""
	m.mu.Unlock()
	m.List()
	m.saveManifest()
	return nil
}

// SeedRules copies bundled rule files when none are installed yet.
func (m *Manager) SeedRules(from string) {
	dir := m.RulesDir()
	if _, err := os.Stat(filepath.Join(dir, "geosite-ir.srs")); err == nil {
		return
	}
	if _, err := os.Stat(from); err != nil {
		return
	}
	copyDir(from, dir)
}

// ---------------- archive helpers ----------------

func flatName(name string) string {
	return filepath.Base(strings.ReplaceAll(name, "\\", "/"))
}

var skipFile = regexp.MustCompile(`(?i)(readme|license|\.md$|\.txt$)`)

func extractZip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := flatName(f.Name)
		if skipFile.MatchString(name) && !strings.HasSuffix(name, ".exe") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(filepath.Join(dst, name))
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := flatName(h.Name)
		if skipFile.MatchString(name) {
			continue
		}
		out, err := os.Create(filepath.Join(dst, name))
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		out.Close()
		if err != nil {
			return err
		}
	}
}

func extractGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, gz)
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	os.MkdirAll(dst, 0o755)
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
		if fi, err := os.Stat(filepath.Join(src, e.Name())); err == nil {
			os.Chmod(filepath.Join(dst, e.Name()), fi.Mode())
		}
	}
	return nil
}
