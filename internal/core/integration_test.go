package core

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"rahgozar/internal/engine"
	"rahgozar/internal/model"
	"rahgozar/internal/parser"
	"rahgozar/internal/store"
)

const binDir = "/home/claude/bin"

func copyBin(t *testing.T, engDir, id, name string) {
	os.MkdirAll(filepath.Join(engDir, id), 0o755)
	b, err := os.ReadFile(filepath.Join(binDir, name))
	if err != nil {
		t.Skip("missing " + name)
	}
	os.WriteFile(filepath.Join(engDir, id, name), b, 0o755)
}

func startServer(t *testing.T, bin string, args ...string) *exec.Cmd {
	c := exec.Command(bin, args...)
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Process.Kill(); c.Wait() })
	return c
}

func waitListen(t *testing.T, port int) {
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("port %d not listening", port)
}

func TestEndToEnd(t *testing.T) {
	if _, err := os.Stat(filepath.Join(binDir, "sing-box")); err != nil {
		t.Skip("no engines")
	}
	// target web server
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	target := ln.Addr().(*net.TCPAddr).Port
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello-rahgozar") }))

	work := t.TempDir()
	uuid := "11111111-2222-3333-4444-555555555555"
	// xray server: VLESS + XHTTP
	xport := FreePort()
	xcfg := fmt.Sprintf(`{"log":{"loglevel":"warning"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"clients":[{"id":"%s"}],"decryption":"none"},"streamSettings":{"network":"xhttp","xhttpSettings":{"path":"/x"}}}],"outbounds":[{"protocol":"freedom"}]}`, xport, uuid)
	os.WriteFile(filepath.Join(work, "xs.json"), []byte(xcfg), 0o644)
	startServer(t, filepath.Join(binDir, "xray"), "run", "-c", filepath.Join(work, "xs.json"))
	// sing-box server: shadowsocks + trojan(no tls) + vless ws
	sport, vport := FreePort(), FreePort()
	scfg := fmt.Sprintf(`{"log":{"level":"warn"},"inbounds":[
	 {"type":"shadowsocks","listen":"127.0.0.1","listen_port":%d,"method":"chacha20-ietf-poly1305","password":"pw"},
	 {"type":"vless","listen":"127.0.0.1","listen_port":%d,"users":[{"uuid":"%s"}],"transport":{"type":"ws","path":"/ws"}}
	],"outbounds":[{"type":"direct"}]}`, sport, vport, uuid)
	os.WriteFile(filepath.Join(work, "ss.json"), []byte(scfg), 0o644)
	startServer(t, filepath.Join(binDir, "sing-box"), "run", "-c", filepath.Join(work, "ss.json"))
	waitListen(t, xport)
	waitListen(t, sport)
	waitListen(t, vport)

	data := filepath.Join(work, "data")
	st := store.Open(data)
	set := model.DefaultSettings()
	set.SetSystemProxy = false
	set.BypassLAN = false
	set.RoutingMode = "global"
	set.AutoFailover = false
	set.MixedPort = FreePort()
	set.DirectDNS = "local"
	st.SetSettings(set)
	engDir := filepath.Join(work, "engines")
	copyBin(t, engDir, "sing-box", "sing-box")
	copyBin(t, engDir, "xray", "xray")
	eng := engine.NewManager(engDir)
	c := New(st, eng, data)
	c.ExeName = "rahgozar.test"
	c.Emit = func(n string, d any) {
		if n == "log" {
			for _, l := range d.([]LogLine) {
				t.Logf("[%s] %s", l.Src, l.Text)
			}
		}
	}
	links := []string{
		fmt.Sprintf("vless://%s@127.0.0.1:%d?type=xhttp&path=%%2Fx&security=none#xhttp-xray", uuid, xport),
		fmt.Sprintf("ss://%s@127.0.0.1:%d#ss-singbox", "Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpwdw", sport),
		fmt.Sprintf("vless://%s@127.0.0.1:%d?type=ws&path=%%2Fws&security=none#vless-ws", uuid, vport),
	}
	for _, l := range links {
		p, err := parser.ParseLink(l)
		if err != nil {
			t.Fatal(err)
		}
		st.UpsertProfile(p)
	}
	proxy, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(set.MixedPort))
	cl := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}, Timeout: 10 * time.Second}
	for _, p := range st.Profiles() {
		s := st.Settings()
		s.SelectedID = p.ID
		st.SetSettings(s)
		if err := c.Connect(); err != nil {
			t.Fatalf("%s: connect: %v", p.Name, err)
		}
		if c.Status().State != "connected" {
			t.Fatalf("%s: state %s", p.Name, c.Status().State)
		}
		resp, err := cl.Get("http://127.0.0.1:" + strconv.Itoa(target) + "/")
		if err != nil {
			t.Fatalf("%s: get via tunnel: %v", p.Name, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(b), "hello-rahgozar") {
			t.Fatalf("%s: body %q", p.Name, b)
		}
		time.Sleep(1200 * time.Millisecond)
		if s := c.Status(); s.Down == 0 {
			t.Errorf("%s: no traffic counted", p.Name)
		}
		t.Logf("%s OK via %s", p.Name, c.Status().Engine)
		c.Disconnect()
		if c.Status().State != "idle" {
			t.Fatal("not idle")
		}
	}
	// latency test path (no internet here: expect graceful failures, not hangs)
	s := st.Settings()
	s.TestTimeoutMs = 1500
	st.SetSettings(s)
	var ids []string
	for _, p := range st.Profiles() {
		ids = append(ids, p.ID)
	}
	start := time.Now()
	res := c.TestProfiles(ids)
	if len(res) != len(ids) {
		t.Fatalf("test results %d", len(res))
	}
	if time.Since(start) > 25*time.Second {
		t.Fatalf("test too slow %v", time.Since(start))
	}
	t.Logf("test results: %+v", res)
}
