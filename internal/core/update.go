package core

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"rahgozar/internal/engine"
)

// AppUpdate describes an available application update.
type AppUpdate struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	URL       string `json:"url"`
	Notes     string `json:"notes"`
}

func semverLess(a, b string) bool {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(strings.SplitN(pa[i], "-", 2)[0])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(strings.SplitN(pb[i], "-", 2)[0])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

var appAsset = regexp.MustCompile(`(?i)rahgozar.*` + runtime.GOOS + `.*` + runtime.GOARCH + `.*\.(zip|exe)$`)

// CheckAppUpdate looks for a newer release in the configured repo.
func (c *Core) CheckAppUpdate() (*AppUpdate, error) {
	set := c.Store.Settings()
	if set.AppRepo == "" {
		return nil, errors.New("مخزن آپدیت تنظیم نشده")
	}
	rel, err := engine.LatestRelease(c.HTTPClient(set.UpdateViaProxy, 30*time.Second), set.AppRepo)
	if err != nil {
		return nil, err
	}
	u := &AppUpdate{Current: Version, Latest: strings.TrimPrefix(rel.TagName, "v")}
	u.Available = semverLess(Version, u.Latest)
	for _, a := range rel.Assets {
		if appAsset.MatchString(a.Name) {
			u.URL = a.URL
		}
	}
	if u.URL == "" {
		u.URL = fmt.Sprintf("https://github.com/%s/releases/download/%s/Rahgozar-%s-%s.zip", set.AppRepo, rel.TagName, runtime.GOOS, runtime.GOARCH)
	}
	return u, nil
}

// ApplyAppUpdate downloads the new build and swaps the running executable.
// The caller restarts the app afterwards.
func (c *Core) ApplyAppUpdate(u *AppUpdate, progress func(float64)) error {
	set := c.Store.Settings()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	tmp := filepath.Join(dir, ".update")
	os.RemoveAll(tmp)
	os.MkdirAll(tmp, 0o755)
	defer os.RemoveAll(tmp)
	urlStr := u.URL
	if m := strings.TrimSpace(set.GitHubMirror); m != "" && strings.Contains(urlStr, "github.com") {
		if !strings.HasSuffix(m, "/") {
			m += "/"
		}
		urlStr = m + urlStr
	}
	resp, err := c.HTTPClient(set.UpdateViaProxy, 15*time.Minute).Get(urlStr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	dl := filepath.Join(tmp, filepath.Base(u.URL))
	f, err := os.Create(dl)
	if err != nil {
		return err
	}
	var n int64
	buf := make([]byte, 64*1024)
	last := time.Now()
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			f.Write(buf[:k])
			n += int64(k)
			if resp.ContentLength > 0 && time.Since(last) > 200*time.Millisecond {
				last = time.Now()
				progress(float64(n) / float64(resp.ContentLength))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	f.Close()
	newExe := dl
	if strings.HasSuffix(strings.ToLower(dl), ".zip") {
		zr, err := zip.OpenReader(dl)
		if err != nil {
			return err
		}
		found := false
		for _, zf := range zr.File {
			name := filepath.Base(zf.Name)
			if strings.EqualFold(filepath.Ext(name), filepath.Ext(exe)) && strings.Contains(strings.ToLower(name), "rahgozar") {
				rc, _ := zf.Open()
				out, _ := os.Create(filepath.Join(tmp, "new.bin"))
				io.Copy(out, rc)
				out.Close()
				rc.Close()
				newExe = filepath.Join(tmp, "new.bin")
				found = true
				break
			}
		}
		if !found {
			zr.Close()
			return errors.New("فایل برنامه داخل بسته آپدیت پیدا نشد")
		}
		// browser extension and bundled rule-sets travel with the app
		for _, sub := range []string{"extension", "rules"} {
			if err := extractSubdir(zr, sub, filepath.Join(dir, sub)); err != nil {
				zr.Close()
				return fmt.Errorf("%s: %w", sub, err)
			}
		}
		zr.Close()
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := copyFileTo(newExe, exe); err != nil {
		os.Rename(old, exe)
		return err
	}
	os.Chmod(exe, 0o755)
	return nil
}

// extractSubdir copies every file under "<sub>/" in the archive (at any
// depth, e.g. "Rahgozar/extension/...") into dst, replacing old files.
func extractSubdir(zr *zip.ReadCloser, sub, dst string) error {
	var files []*zip.File
	prefix := ""
	for _, zf := range zr.File {
		n := strings.ReplaceAll(zf.Name, "\\", "/")
		i := strings.Index("/"+n, "/"+sub+"/")
		if i < 0 || zf.FileInfo().IsDir() {
			continue
		}
		if prefix == "" {
			prefix = n[:i+len(sub)+1]
		}
		if strings.HasPrefix(n, prefix) {
			files = append(files, zf)
		}
	}
	if len(files) == 0 {
		return nil
	}
	stage := dst + ".new"
	os.RemoveAll(stage)
	for _, zf := range files {
		rel := strings.TrimPrefix(strings.ReplaceAll(zf.Name, "\\", "/"), prefix)
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		out := filepath.Join(stage, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(out), 0o755)
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		f, err := os.Create(out)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(f, rc)
		f.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	old := dst + ".old"
	os.RemoveAll(old)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			// folder in use (e.g. a browser has it open): overwrite file by file
			defer os.RemoveAll(stage)
			return filepath.Walk(stage, func(p string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(stage, p)
				os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755)
				return copyFileTo(p, filepath.Join(dst, rel))
			})
		}
	}
	if err := os.Rename(stage, dst); err != nil {
		os.Rename(old, dst)
		return err
	}
	os.RemoveAll(old)
	return nil
}

// CleanupOldBinary removes the previous executable after an update.
func CleanupOldBinary() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}

func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

var _ = http.StatusOK
