package main

import (
	"context"
	"embed"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"rahgozar/internal/sys"

	"rahgozar/frontend"
	"rahgozar/internal/desk"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// The browser extension ships inside the exe and is written next to it on
// start, so the folder exists even after exe-only updates.
//
//go:embed all:extension
var extensionFS embed.FS

func main() {
	// After an in-app update or "run as administrator" the old instance is
	// still shutting down; wait for it, otherwise the single-instance lock
	// hands control to the dying process and no window appears.
	// Its WebView2 processes must be gone too: Wails shows the window only
	// after WebView2 has loaded the page, and WebView2 stalls while the old
	// instance's browser processes still hold the shared profile folder.
	retried := false
	for i, a := range os.Args[1:] {
		if a == "--retried" {
			retried = true
		}
		if a == "--wait-pid" && i+2 < len(os.Args) {
			if pid, err := strconv.Atoi(os.Args[i+2]); err == nil {
				deadline := time.Now().Add(25 * time.Second)
				kids := sys.DescendantPIDs(pid)
				sys.WaitPID(pid, time.Until(deadline))
				for _, k := range kids {
					if left := time.Until(deadline); left > 0 {
						sys.WaitPID(k, left)
					}
				}
				for _, k := range kids { // still alive: they belong to a dead instance
					sys.KillPID(k)
				}
				time.Sleep(400 * time.Millisecond)
			}
		}
	}
	desk.EnsureExtension(extensionFS, "extension")
	app := desk.NewApp(os.Args[1:])
	onStart, onStop := desk.Hooks(app)
	start := options.Normal
	for _, a := range os.Args[1:] {
		if a == "--minimized" {
			start = options.Minimised
		}
	}
	// Watchdog: if the page never finishes loading (window stays invisible),
	// start a fresh instance once instead of leaving a ghost process.
	var domReady atomic.Bool
	go func() {
		time.Sleep(30 * time.Second)
		if domReady.Load() || retried {
			return
		}
		if exe, err := os.Executable(); err == nil {
			if sys.StartDetached(exe, "--wait-pid", strconv.Itoa(os.Getpid()), "--retried") == nil {
				os.Exit(0)
			}
		}
	}()
	err := wails.Run(&options.App{
		OnDomReady:       func(ctx context.Context) { domReady.Store(true) },
		Title:            "Rahgozar",
		Width:            1180,
		Height:           760,
		MinWidth:         940,
		MinHeight:        620,
		Frameless:        true,
		WindowStartState: start,
		BackgroundColour: &options.RGBA{R: 13, G: 24, B: 38, A: 255},
		AssetServer:      &assetserver.Options{Assets: frontend.Assets},
		OnStartup:        onStart,
		OnShutdown:       onStop,
		Bind:             []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "rahgozar-vpn-7f3c",
			OnSecondInstanceLaunch: desk.OnSecondInstance(app),
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
			Theme:                windows.Dark,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
