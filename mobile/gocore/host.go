// Package gocore is the Android bridge of Rahgozar (built with gomobile).
// The Kotlin app implements Host; the WebView UI talks to Go through Call.
package gocore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"rahgozar/internal/desk"
	"rahgozar/internal/engine"
)

// Host is implemented by the Android app.
type Host interface {
	// OpenTun builds the VpnService interface from JSON options and returns its fd.
	OpenTun(optionsJSON string) (int32, error)
	// Protect excludes a socket from the VPN.
	Protect(fd int32) bool
	// CloseTun stops the VpnService (called when the tunnel stops).
	CloseTun()
	// Emit delivers a UI event (name, JSON data) to the WebView.
	Emit(name string, data string)
	ClipboardGet() string
	ClipboardSet(text string)
	// PickFiles opens the system picker; the app answers with FilesPicked.
	PickFiles(mimeTypes string, multiple bool)
	OpenURL(url string)
	// SaveFile asks the user where to save a file (system picker) and copies it there.
	SaveFile(path, name string)
	// InstallAPK starts the package installer for a downloaded update.
	InstallAPK(path string) error
	// Interfaces returns the network interfaces as JSON.
	Interfaces() string
	// NetworkName returns the Wi-Fi SSID or network type ("" if unknown).
	NetworkName() string
	// StartNetworkMonitor asks the app to report the default network via
	// UpdateDefaultInterface until StopNetworkMonitor.
	StartNetworkMonitor()
	StopNetworkMonitor()
	Log(line string)
}

var (
	mu      sync.Mutex
	host    Host
	app     *desk.App
	appVal  reflect.Value
	plat    *androidPlatform
	pickCh  chan []string
	started bool
)

// Setup creates the Go side. dataDir: app files dir, cacheDir: cache dir,
// pkg: our package name, version: versionName.
func Setup(dataDir, cacheDir, pkg, version string, h Host) error {
	mu.Lock()
	defer mu.Unlock()
	if app != nil {
		host = h
		plat.host = h
		return nil
	}
	host = h
	if err := setupLibbox(dataDir, cacheDir); err != nil {
		h.Log("libbox setup: " + err.Error())
	}
	plat = &androidPlatform{host: h, cacheDir: cacheDir}
	runner := &androidRunner{dataDir: dataDir}
	appDir := filepath.Join(dataDir, "app")
	os.MkdirAll(appDir, 0o755)
	app = desk.NewApp(desk.Options{
		DataDir:     dataDir,
		AppDir:      appDir,
		EnginesDir:  filepath.Join(dataDir, "engines"),
		ExeName:     pkg,
		OwnPackage:  pkg,
		Platform:    plat,
		Runner:      runner,
		Builtin:     builtinEngines(),
		NetworkName: h.NetworkName,
	})
	appVal = reflect.ValueOf(app)
	setVersion(version)
	if _, err := os.Stat(filepath.Join(dataDir, "settings.json")); err != nil {
		// first run on Android: whole-device tunnel, no Windows-only features
		st := app.GetState().Settings
		st.Mode = "tun"
		st.SetSystemProxy = false
		st.ExtAPI = false
		app.SaveSettings(st)
	}
	return nil
}

// Start launches background services (call once the WebView is ready).
func Start() {
	mu.Lock()
	a := app
	s := started
	started = true
	mu.Unlock()
	if a != nil && !s {
		desk.Start(a)
	}
}

// Shutdown disconnects and saves state.
func Shutdown() {
	if app != nil {
		desk.Stop(app)
	}
}

type callResult struct {
	Result any    `json:"result"`
	Error  string `json:"error,omitempty"`
}

// Call invokes an App method by name with a JSON array of arguments and
// returns {"result":...,"error":...}. It may block (run off the UI thread).
func Call(method string, argsJSON string) string {
	if app == nil {
		return `{"error":"not initialised"}`
	}
	out := callResult{}
	func() {
		defer func() {
			if r := recover(); r != nil {
				out.Error = fmt.Sprint("panic: ", r)
			}
		}()
		m := appVal.MethodByName(method)
		if !m.IsValid() || method == "" || strings.ToLower(method[:1]) == method[:1] {
			out.Error = "unknown method " + method
			return
		}
		var raw []json.RawMessage
		if argsJSON != "" {
			if err := json.Unmarshal([]byte(argsJSON), &raw); err != nil {
				out.Error = "bad arguments: " + err.Error()
				return
			}
		}
		var in []reflect.Value
		for i := 0; i < m.Type().NumIn(); i++ {
			p := reflect.New(m.Type().In(i))
			if i < len(raw) {
				if err := json.Unmarshal(raw[i], p.Interface()); err != nil {
					out.Error = fmt.Sprintf("argument %d: %v", i, err)
					return
				}
			}
			in = append(in, p.Elem())
		}
		errType := reflect.TypeOf((*error)(nil)).Elem()
		for _, r := range m.Call(in) {
			if r.Type().Implements(errType) {
				if !r.IsNil() {
					out.Error = r.Interface().(error).Error()
				}
			} else {
				out.Result = r.Interface()
			}
		}
	}()
	b, err := json.Marshal(out)
	if err != nil {
		b, _ = json.Marshal(callResult{Error: err.Error()})
	}
	return string(b)
}

// FilesPicked answers a PickFiles request with a JSON array of local paths
// (the app copies content URIs to cache first). An empty array = cancelled.
func FilesPicked(pathsJSON string) {
	var paths []string
	json.Unmarshal([]byte(pathsJSON), &paths)
	mu.Lock()
	ch := pickCh
	pickCh = nil
	mu.Unlock()
	if ch != nil {
		ch <- paths
	}
}

// ImportText imports links shared to the app (share sheet / deep link).
func ImportText(text string) string { return Call("ImportText", mustJSON([]any{text})) }

// Toggle connects or disconnects (quick settings tile, notification).
func Toggle() {
	if app == nil {
		return
	}
	go func() {
		st := app.GetStatus()
		if st.State == "connected" || st.State == "connecting" {
			app.Disconnect()
		} else {
			app.Connect()
		}
	}()
}

// Disconnect stops the tunnel (e.g. VPN permission revoked).
func Disconnect() {
	if app != nil {
		go app.Disconnect()
	}
}

// StatusJSON returns the connection status (for notification / tile).
func StatusJSON() string {
	if app == nil {
		return "{}"
	}
	return mustJSON(app.GetStatus())
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

var _ = engine.Status{}
