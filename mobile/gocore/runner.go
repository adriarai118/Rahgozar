package gocore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/libbox"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	sjson "github.com/sagernet/sing/common/json"

	xcore "github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"

	"rahgozar/internal/core"
	"rahgozar/internal/engine"
	"rahgozar/internal/model"
)

func setVersion(v string) {
	if v != "" {
		core.Version = v
	}
}

func builtinEngines() []*engine.Status {
	sb := C.Version
	return []*engine.Status{
		{Spec: engine.Spec{ID: "sing-box", Name: "sing-box", Repo: "SagerNet/sing-box", Kind: "binary", Required: true,
			Desc:      "هسته اصلی داخل برنامه: TUN، مسیریابی هوشمند، DNS و اکثر پروتکل‌ها",
			Protocols: []string{"VMess", "VLESS", "Reality", "Trojan", "Shadowsocks", "Hysteria", "Hysteria2", "TUIC", "WireGuard", "OpenVPN", "AnyTLS", "SOCKS5", "HTTP(S)", "SSH", "TUN"}},
			Installed: true, Version: sb, Latest: sb, Builtin: true},
		{Spec: engine.Spec{ID: "xray", Name: "Xray-core", Repo: "XTLS/Xray-core", Kind: "binary",
			Desc:      "داخل برنامه: XHTTP، mKCP، VLESS Encryption",
			Protocols: []string{"XHTTP", "VLESS", "VMess", "Reality", "Trojan", "mKCP"}},
			Installed: true, Version: xcore.Version(), Latest: xcore.Version(), Builtin: true},
	}
}

// androidRunner runs engines inside the app process.
type androidRunner struct{ dataDir string }

func (r *androidRunner) Available(eng string) error {
	switch eng {
	case model.EngineSingBox, model.EngineXray:
		return nil
	case model.EngineMihomo:
		return errors.New("موتور Mihomo (برای ShadowsocksR و AmneziaWG) در نسخه اندروید نیست")
	}
	return fmt.Errorf("موتور %s روی اندروید وجود ندارد", eng)
}

func (r *androidRunner) OwnsPath(string) bool { return false }
func (r *androidRunner) ExeNames() []string   { return nil }

func (r *androidRunner) Start(j core.Job, onLine func(name, line string)) (core.Process, error) {
	switch j.Engine {
	case model.EngineXray:
		return startXray(j, onLine)
	case model.EngineSingBox:
		if j.Main {
			return startMain(j, onLine)
		}
		return startPlainBox(j, onLine)
	}
	return nil, r.Available(j.Engine)
}

// ---------------- in-process process ----------------

type inproc struct {
	label string
	done  chan struct{}
	once  sync.Once
	err   error
	stop  func()
	mu    sync.Mutex
}

func newInproc(label string) *inproc { return &inproc{label: label, done: make(chan struct{})} }

func (p *inproc) PID() int              { return 0 }
func (p *inproc) Label() string         { return p.label }
func (p *inproc) Done() <-chan struct{} { return p.done }
func (p *inproc) Alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (p *inproc) ExitError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return fmt.Errorf("%s stopped: %w", p.label, p.err)
	}
	return fmt.Errorf("%s stopped", p.label)
}
func (p *inproc) finish(err error) {
	p.once.Do(func() {
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		close(p.done)
	})
}
func (p *inproc) Stop() {
	if !p.Alive() {
		return
	}
	if p.stop != nil {
		p.stop()
	}
	p.finish(nil)
}

// ---------------- xray ----------------

func startXray(j core.Job, onLine func(name, line string)) (core.Process, error) {
	cfg, err := xcore.LoadConfig("json", bytes.NewReader(j.Config))
	if err != nil {
		return nil, err
	}
	inst, err := xcore.New(cfg)
	if err != nil {
		return nil, err
	}
	if err := inst.Start(); err != nil {
		return nil, err
	}
	p := newInproc(j.Name)
	p.stop = func() { inst.Close() }
	if onLine != nil {
		onLine(j.Name, "Xray "+xcore.Version()+" started (in-process)")
	}
	return p, nil
}

// ---------------- sing-box: tests and helpers (no TUN) ----------------

func boxContext() context.Context {
	return box.Context(context.Background(), include.InboundRegistry(), include.OutboundRegistry(), include.EndpointRegistry(),
		include.DNSTransportRegistry(), include.ServiceRegistry(), include.CertificateProviderRegistry())
}

func startPlainBox(j core.Job, onLine func(name, line string)) (core.Process, error) {
	// our app is outside the VPN; without a platform the interface monitor
	// would need netlink, which Android denies
	var m map[string]any
	if err := json.Unmarshal(j.Config, &m); err == nil {
		if rt, ok := m["route"].(map[string]any); ok {
			delete(rt, "auto_detect_interface")
		}
		j.Config, _ = json.Marshal(m)
	}
	ctx, cancel := context.WithCancel(boxContext())
	opts, err := sjson.UnmarshalExtendedContext[option.Options](ctx, j.Config)
	if err != nil {
		cancel()
		return nil, err
	}
	inst, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		cancel()
		return nil, err
	}
	if err := inst.Start(); err != nil {
		inst.Close()
		cancel()
		return nil, err
	}
	p := newInproc(j.Name)
	p.stop = func() { inst.Close(); cancel() }
	return p, nil
}

// ---------------- sing-box: main tunnel via libbox ----------------

var (
	serverMu sync.Mutex
	server   *libbox.CommandServer
)

func setupLibbox(dataDir, cacheDir string) error {
	work := filepath.Join(dataDir, "runtime")
	os.MkdirAll(work, 0o755)
	return libbox.Setup(&libbox.SetupOptions{
		BasePath:        dataDir,
		WorkingPath:     work,
		TempPath:        cacheDir,
		FixAndroidStack: true,
		LogMaxLines:     3000,
	})
}

func startMain(j core.Job, onLine func(name, line string)) (core.Process, error) {
	serverMu.Lock()
	defer serverMu.Unlock()
	// stream engine logs into the app log: write sing-box's log to a file and tail it
	logPath := filepath.Join(j.RuntimeDir, "sing-box.log")
	os.Remove(logPath)
	var m map[string]any
	if err := json.Unmarshal(j.Config, &m); err == nil {
		lg, _ := m["log"].(map[string]any)
		if lg == nil {
			lg = map[string]any{}
		}
		lg["output"] = logPath
		lg["timestamp"] = true
		m["log"] = lg
		j.Config, _ = json.Marshal(m)
	}
	if server == nil {
		s, err := libbox.NewCommandServer(&cmdHandler{}, &libboxPlatform{})
		if err != nil {
			return nil, err
		}
		server = s
	}
	p := newInproc(j.Name)
	if err := server.StartOrReloadService(string(j.Config), &libbox.OverrideOptions{}); err != nil {
		return nil, err
	}
	stopTail := tailFile(logPath, j.Name, onLine)
	p.stop = func() {
		serverMu.Lock()
		if server != nil {
			server.CloseService()
		}
		serverMu.Unlock()
		stopTail()
		if host != nil {
			host.CloseTun()
		}
	}
	return p, nil
}

func tailFile(path, name string, onLine func(name, line string)) func() {
	stop := make(chan struct{})
	if onLine == nil {
		return func() { close(stop) }
	}
	go func() {
		var f *os.File
		for i := 0; i < 50 && f == nil; i++ {
			f, _ = os.Open(path)
			if f == nil {
				select {
				case <-stop:
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		if f == nil {
			return
		}
		defer f.Close()
		rd := bufio.NewReader(f)
		var partial string
		for {
			line, err := rd.ReadString('\n')
			if len(line) > 0 {
				partial += line
				if strings.HasSuffix(partial, "\n") {
					onLine(name, strings.TrimRight(partial, "\r\n"))
					partial = ""
				}
			}
			if err == io.EOF {
				select {
				case <-stop:
					return
				case <-time.After(300 * time.Millisecond):
				}
				continue
			}
			if err != nil {
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}
