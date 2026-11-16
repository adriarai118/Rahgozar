package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v2/pkg/assetserver"
	opts "github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"rahgozar/frontend"
	"rahgozar/internal/desk"
)

type rt struct{ js, ipc []byte }

func (r rt) RuntimeDesktopJS() []byte { return r.js }
func (r rt) DesktopIPC() []byte       { return r.ipc }
func (r rt) WebsocketIPC() []byte     { return nil }

type lg struct{}

func (lg) Debug(string, ...interface{})     {}
func (lg) Info(string, ...interface{})      {}
func (lg) Error(m string, a ...interface{}) { println("ERR", fmt.Sprintf(m, a...)) }

const shim = `window.chrome={webview:{postMessage:function(m){fetch('/ipc',{method:'POST',body:m}).then(r=>r.text()).then(t=>{if(t)window.wails.Callback(t)})}}};`

// TestHarness serves the real UI + real App over HTTP, emulating Wails IPC.
func TestHarness(t *testing.T) {
	if os.Getenv("HARNESS") == "" {
		t.Skip()
	}
	js, _ := os.ReadFile(os.Getenv("RTJS"))
	ipc, _ := os.ReadFile(os.Getenv("IPCJS"))
	app := desk.NewApp(desk.Options{})
	v := reflect.ValueOf(app)
	methods := map[string]int{}
	for i := 0; i < v.NumMethod(); i++ {
		methods[v.Type().Method(i).Name] = 0
	}
	bj, _ := json.Marshal(map[string]any{"desk": map[string]any{"App": methods}})
	srv, err := assetserver.NewAssetServer(string(bj), opts.Options{Assets: frontend.Assets}, false, lg{}, rt{js, append([]byte(shim), ipc...)})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ipc", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		msg := string(b)
		if !strings.HasPrefix(msg, "C") {
			return
		}
		var cm struct {
			Name       string            `json:"name"`
			Args       []json.RawMessage `json:"args"`
			CallbackID string            `json:"callbackID"`
		}
		json.Unmarshal(b[1:], &cm)
		name := cm.Name[strings.LastIndex(cm.Name, ".")+1:]
		m := v.MethodByName(name)
		out := map[string]any{"callbackid": cm.CallbackID}
		if !m.IsValid() {
			out["error"] = "no method " + name
		} else {
			var in []reflect.Value
			for i := 0; i < m.Type().NumIn(); i++ {
				p := reflect.New(m.Type().In(i))
				if i < len(cm.Args) {
					json.Unmarshal(cm.Args[i], p.Interface())
				}
				in = append(in, p.Elem())
			}
			func() {
				defer func() {
					if e := recover(); e != nil {
						out["error"] = fmt.Sprint("PANIC ", e)
					}
				}()
				res := m.Call(in)
				for _, r := range res {
					if r.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
						if !r.IsNil() {
							out["error"] = r.Interface().(error).Error()
						}
					} else {
						out["result"] = r.Interface()
					}
				}
			}()
		}
		jb, err := json.Marshal(out)
		if err != nil {
			println("MARSHAL ERR", name, err.Error())
		}
		fmt.Println("CALL", name, len(jb), out["error"])
		w.Write(jb)
	})
	mux.Handle("/", srv)
	go http.ListenAndServe("127.0.0.1:34115", mux)
	time.Sleep(150 * time.Second)
}

// TestExtHarness runs the real app core + extension API (auto-approving pairing).
func TestExtHarness(t *testing.T) {
	if os.Getenv("EXTHARNESS") == "" {
		t.Skip()
	}
	app := desk.NewApp(desk.Options{})
	var approve func(string)
	desk.StartHeadless(app, func(n string, d any) {
		if n == "pairRequest" {
			id := d.(map[string]string)["id"]
			go func() { time.Sleep(1500 * time.Millisecond); approve(id) }()
		}
	})
	approve = func(id string) { app.ApprovePair(id, true) }
	app.ImportText("vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?type=tcp&security=reality&pbk=abc&sid=12&sni=a.com#آلمان ریلیتی\ntrojan://p@b.com:443#ترکیه تروجان\nhy2://pw@h.example.com:443#فنلاند")
	fmt.Println("READY")
	time.Sleep(150 * time.Second)
}
