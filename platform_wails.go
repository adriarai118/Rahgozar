package main

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"rahgozar/internal/core"
	"rahgozar/internal/sys"
)

// wailsPlatform implements desk.Platform for the Windows desktop app.
type wailsPlatform struct{ ctx context.Context }

func (p *wailsPlatform) OS() string { return runtime.GOOS }

func (p *wailsPlatform) Emit(name string, data any) {
	if p.ctx != nil {
		wr.EventsEmit(p.ctx, name, data)
	}
}

func (p *wailsPlatform) ClipboardGet() (string, error)  { return wr.ClipboardGetText(p.ctx) }
func (p *wailsPlatform) ClipboardSet(text string) error { return wr.ClipboardSetText(p.ctx, text) }

func (p *wailsPlatform) PickFiles(title, pattern string, multiple bool) ([]string, error) {
	filters := []wr.FileFilter{{DisplayName: "(" + strings.ReplaceAll(pattern, ";", ", ") + ")", Pattern: pattern}, {DisplayName: "All files", Pattern: "*.*"}}
	if multiple {
		return wr.OpenMultipleFilesDialog(p.ctx, wr.OpenDialogOptions{Title: title, Filters: filters})
	}
	f, err := wr.OpenFileDialog(p.ctx, wr.OpenDialogOptions{Title: title, Filters: filters})
	if err != nil || f == "" {
		return nil, err
	}
	return []string{f}, nil
}

func (p *wailsPlatform) OpenURL(url string)         { wr.BrowserOpenURL(p.ctx, url) }
func (p *wailsPlatform) OpenPath(path string) error { return sys.OpenPath(path) }
func (p *wailsPlatform) Minimise()                  { wr.WindowMinimise(p.ctx) }
func (p *wailsPlatform) ToggleMaximise()            { wr.WindowToggleMaximise(p.ctx) }
func (p *wailsPlatform) Quit()                      { wr.Quit(p.ctx) }
func (p *wailsPlatform) TunAllowed() bool           { return sys.IsAdmin() }

func (p *wailsPlatform) Focus() {
	if p.ctx == nil {
		return
	}
	wr.WindowUnminimise(p.ctx)
	wr.WindowShow(p.ctx)
	wr.WindowSetAlwaysOnTop(p.ctx, true)
	go func() {
		time.Sleep(300 * time.Millisecond)
		wr.WindowSetAlwaysOnTop(p.ctx, false)
	}()
}

// Relaunch starts a new instance that waits for this one, then quits.
func (p *wailsPlatform) Relaunch(admin bool) error {
	pid := strconv.Itoa(os.Getpid())
	var err error
	if admin {
		err = sys.RelaunchAsAdmin("--wait-pid", pid)
	} else {
		exe, _ := os.Executable()
		err = sys.StartDetached(exe, "--wait-pid", pid)
	}
	if err != nil {
		return err
	}
	forceExitSoon()
	wr.Quit(p.ctx)
	return nil
}

func (p *wailsPlatform) InstallUpdate(c *core.Core, u *core.AppUpdate, progress func(float64)) error {
	if err := c.ApplyAppUpdate(u, progress); err != nil {
		return err
	}
	c.Disconnect()
	return p.Relaunch(false)
}

// forceExitSoon guarantees the process ends after a relaunch even if the
// window shutdown hangs, so the new instance can take over.
func forceExitSoon() {
	go func() {
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}()
}
