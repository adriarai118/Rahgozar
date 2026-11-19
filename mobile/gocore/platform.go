package gocore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rahgozar/internal/core"
)

// androidPlatform implements desk.Platform on top of Host.
type androidPlatform struct {
	host     Host
	cacheDir string
}

func (p *androidPlatform) OS() string { return "android" }

func (p *androidPlatform) Emit(name string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	p.host.Emit(name, string(b))
}

func (p *androidPlatform) ClipboardGet() (string, error) { return p.host.ClipboardGet(), nil }
func (p *androidPlatform) ClipboardSet(t string) error   { p.host.ClipboardSet(t); return nil }

func (p *androidPlatform) PickFiles(title, pattern string, multiple bool) ([]string, error) {
	ch := make(chan []string, 1)
	mu.Lock()
	if pickCh != nil {
		mu.Unlock()
		return nil, errors.New("یک انتخاب فایل دیگر باز است")
	}
	pickCh = ch
	mu.Unlock()
	mime := "*/*"
	if strings.Contains(pattern, "*.png") {
		mime = "image/*"
	}
	p.host.PickFiles(mime, multiple)
	select {
	case paths := <-ch:
		return paths, nil
	case <-time.After(10 * time.Minute):
		mu.Lock()
		pickCh = nil
		mu.Unlock()
		return nil, nil
	}
}

func (p *androidPlatform) OpenURL(u string) { p.host.OpenURL(u) }
func (p *androidPlatform) OpenPath(string) error {
	return errors.New("روی اندروید در دسترس نیست")
}
func (p *androidPlatform) Minimise()           {}
func (p *androidPlatform) ToggleMaximise()     {}
func (p *androidPlatform) Focus()              {}
func (p *androidPlatform) Quit()               {}
func (p *androidPlatform) Relaunch(bool) error { return nil }
func (p *androidPlatform) TunAllowed() bool    { return true }

// InstallUpdate downloads the APK and hands it to the package installer.
func (p *androidPlatform) InstallUpdate(c *core.Core, u *core.AppUpdate, progress func(float64)) error {
	if u == nil || u.URL == "" {
		return errors.New("آپدیتی موجود نیست")
	}
	if !strings.HasSuffix(strings.ToLower(u.URL), ".apk") {
		return errors.New("فایل APK در این نسخه پیدا نشد")
	}
	set := c.Store.Settings()
	url := u.URL
	if m := strings.TrimSpace(set.GitHubMirror); m != "" && strings.Contains(url, "github.com") {
		if !strings.HasSuffix(m, "/") {
			m += "/"
		}
		url = m + url
	}
	resp, err := c.HTTPClient(set.UpdateViaProxy, 20*time.Minute).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	dir := filepath.Join(p.cacheDir, "updates")
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755)
	dst := filepath.Join(dir, "Rahgozar-"+u.Latest+".apk")
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	buf := make([]byte, 64*1024)
	var n int64
	last := time.Now()
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			if _, err := f.Write(buf[:k]); err != nil {
				f.Close()
				return err
			}
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
	progress(1)
	return p.host.InstallAPK(dst)
}
