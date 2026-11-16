package smart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRank(t *testing.T) {
	l := NewLearner()
	for i := 0; i < 10; i++ {
		l.Record("good", "vless+reality", "net1", "Home", true, 120)
		l.Record("slow", "vmess+ws+tls", "net1", "Home", true, 900)
		l.Record("bad", "trojan", "net1", "Home", i%4 == 0, 200)
	}
	r := l.Rank([]Candidate{{"bad", "trojan"}, {"slow", "vmess+ws+tls"}, {"good", "vless+reality"}, {"new", "vless+reality"}}, "net1")
	if r[0].ID != "good" {
		t.Fatalf("best=%s %+v", r[0].ID, r)
	}
	pos := map[string]int{}
	for i, x := range r {
		pos[x.ID] = i
	}
	if pos["bad"] < pos["slow"] {
		t.Errorf("bad ranked above slow: %+v", r)
	}
	// unknown server in a healthy family should beat a failing one
	if pos["new"] > pos["bad"] {
		t.Errorf("new below bad: %+v", r)
	}
	fh := l.FamilyHealth("net1")
	if fh[0].Family != "vless+reality" || fh[len(fh)-1].State == "good" {
		t.Errorf("family health %+v", fh)
	}
	// other network has no knowledge -> exploration
	r2 := l.Rank([]Candidate{{"good", "vless+reality"}, {"bad", "trojan"}}, "net2")
	if len(r2) != 2 {
		t.Fatal()
	}
}

func TestDomains(t *testing.T) {
	dir := t.TempDir()
	d := NewDomains(dir)
	d.Start(func(dom string) (string, string) {
		if strings.Contains(dom, "youtube") {
			return VerdictProxy, "blocked"
		}
		return VerdictDirect, "ok"
	}, nil, 2)
	d.Observe("www.youtube.com")
	d.Observe("i.ytimg.youtube.com")
	d.Observe("api.example.org")
	d.Observe("digikala.ir")
	d.Observe("1.2.3.4")
	time.Sleep(2500 * time.Millisecond)
	b, _ := os.ReadFile(filepath.Join(dir, "learned-proxy.json"))
	if !strings.Contains(string(b), "youtube.com") {
		t.Fatalf("proxy file: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "learned-direct.json"))
	if !strings.Contains(string(b), "example.org") || strings.Contains(string(b), "digikala") {
		t.Fatalf("direct file: %s", b)
	}
	if BaseDomain("a.b.example.co.uk") != "example.co.uk" || BaseDomain("x.github.io") != "x.github.io" {
		t.Fatal("base domain")
	}
	d.Set("example.org", VerdictProxy)
	b, _ = os.ReadFile(filepath.Join(dir, "learned-proxy.json"))
	if !strings.Contains(string(b), "example.org") {
		t.Fatalf("user override: %s", b)
	}
}

func TestResolve(t *testing.T) {
	if os.Getenv("NET") == "" {
		t.Skip()
	}
}
