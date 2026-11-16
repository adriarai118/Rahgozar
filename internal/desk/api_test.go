package desk

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"rahgozar/internal/core"
)

func TestExtensionAPI(t *testing.T) {
	a := NewApp(nil)
	s := a.store.Settings()
	s.ExtPort = core.FreePort()
	s.ExtToken = ""
	s.ProxyDomains, s.DirectDomains, s.BlockDomains = nil, nil, nil
	a.store.SetSettings(s)
	events := make(chan string, 20)
	StartHeadless(a, func(n string, d any) {
		if n == "pairRequest" {
			events <- d.(map[string]string)["id"]
		}
	})
	base := "http://127.0.0.1:" + itoa(s.ExtPort)
	do := func(method, path, origin, token string, body any) (int, map[string]any) {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, base+path, rd)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if token != "" {
			req.Header.Set("X-Rahgozar-Token", token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	if c, m := do("GET", "/api/ping", "", "", nil); c != 200 || m["app"] != "rahgozar" {
		t.Fatalf("ping %d %v", c, m)
	}
	if c, _ := do("GET", "/api/ping", "https://evil.example", "", nil); c != 403 {
		t.Fatalf("web origin allowed: %d", c)
	}
	if c, _ := do("GET", "/api/status", "chrome-extension://abc", "", nil); c != 401 {
		t.Fatalf("unpaired status: %d", c)
	}
	// pairing
	_, m := do("POST", "/api/pair/request", "chrome-extension://abc", "", map[string]string{"name": "Chrome"})
	id, _ := m["id"].(string)
	select {
	case got := <-events:
		if got != id {
			t.Fatal("pair id mismatch")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no pair event")
	}
	if _, m := do("GET", "/api/pair/poll?id="+id, "chrome-extension://abc", "", nil); m["state"] != "pending" {
		t.Fatalf("%v", m)
	}
	if err := a.ApprovePair(id, true); err != nil {
		t.Fatal(err)
	}
	_, m = do("GET", "/api/pair/poll?id="+id, "chrome-extension://abc", "", nil)
	tok, _ := m["token"].(string)
	if m["state"] != "approved" || len(tok) < 20 {
		t.Fatalf("%v", m)
	}
	if c, m := do("GET", "/api/status", "chrome-extension://abc", tok, nil); c != 200 || m["state"] != "idle" {
		t.Fatalf("status %d %v", c, m)
	}
	// per-site rules
	c, m := do("POST", "/api/site", "chrome-extension://abc", tok, map[string]string{"host": "https://www.aparat.com/v/x", "action": "direct"})
	if c != 200 || m["route"] != "direct" || m["rule"] != "direct" {
		t.Fatalf("site direct %d %v", c, m)
	}
	_, m = do("GET", "/api/site?host=cdn.aparat.com", "chrome-extension://abc", tok, nil)
	if m["route"] != "direct" || m["ruleOn"] != "aparat.com" {
		t.Fatalf("subdomain %v", m)
	}
	do("POST", "/api/site", "chrome-extension://abc", tok, map[string]string{"host": "youtube.com", "action": "proxy"})
	do("POST", "/api/site", "chrome-extension://abc", tok, map[string]string{"host": "ads.tracker.com", "action": "block", "scope": "host"})
	b, _ := os.ReadFile(filepath.Join(a.dataDir, "learned", "user-direct.json"))
	if !strings.Contains(string(b), "aparat.com") {
		t.Fatalf("user-direct %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(a.dataDir, "learned", "user-block.json"))
	if !strings.Contains(string(b), "ads.tracker.com") {
		t.Fatalf("user-block %s", b)
	}
	// switching aparat to auto removes it everywhere
	_, m = do("POST", "/api/site", "chrome-extension://abc", tok, map[string]string{"host": "aparat.com", "action": "auto"})
	if m["rule"] != "auto" {
		t.Fatalf("auto %v", m)
	}
	if len(a.store.Settings().DirectDomains) != 0 || len(a.store.Settings().ProxyDomains) != 1 {
		t.Fatalf("lists %+v", a.store.Settings())
	}
	// revoke
	a.RevokeExtensions()
	if c, _ := do("GET", "/api/status", "chrome-extension://abc", tok, nil); c != 401 {
		t.Fatal("revoked token still works")
	}
	// GET on a POST endpoint
	if c, _ := do("GET", "/api/mode", "", a.store.Settings().ExtToken, nil); c != 401 {
		t.Fatalf("mode %d", c)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
