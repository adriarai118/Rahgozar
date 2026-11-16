package main

import (
	"os"
	"strconv"
	"time"

	"rahgozar/internal/sys"

	"rahgozar/frontend"
	"rahgozar/internal/desk"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

func main() {
	// After an in-app update or "run as administrator" the old instance is
	// still shutting down; wait for it, otherwise the single-instance lock
	// hands control to the dying process and no window appears.
	for i, a := range os.Args[1:] {
		if a == "--wait-pid" && i+2 < len(os.Args) {
			if pid, err := strconv.Atoi(os.Args[i+2]); err == nil {
				sys.WaitPID(pid, 20*time.Second)
			}
		}
	}
	app := desk.NewApp(os.Args[1:])
	onStart, onStop := desk.Hooks(app)
	start := options.Normal
	for _, a := range os.Args[1:] {
		if a == "--minimized" {
			start = options.Minimised
		}
	}
	err := wails.Run(&options.App{
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
