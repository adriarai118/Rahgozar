package backup

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// Runs against a real Exclave backup when EXCLAVE_BACKUP points to one
// (not shipped: it holds private server credentials).
func TestExclaveRealBackup(t *testing.T) {
	f := os.Getenv("EXCLAVE_BACKUP")
	if f == "" {
		t.Skip("EXCLAVE_BACKUP not set")
	}
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if k := Detect(b, f).Kind; k != KindExclave {
		t.Fatalf("detect: %q", k)
	}
	d, err := Exclave(b)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]int{}
	for _, p := range d.Profiles {
		types[p.Profile.Type]++
		if p.Profile.Server == "" || p.Profile.Port <= 0 || p.Profile.Port > 65535 {
			if p.Profile.Ports == "" {
				t.Errorf("bad address: type %s port %d", p.Profile.Type, p.Profile.Port)
			}
		}
	}
	subs, urls := 0, 0
	for _, g := range d.Groups {
		if g.Sub {
			subs++
			if strings.HasPrefix(g.URL, "http") {
				urls++
			}
		}
	}
	var reasons []string
	bad := 0
	for r, n := range d.Skipped {
		reasons = append(reasons, r+": "+itoa(n))
		if strings.HasPrefix(r, "خطا") || r == "فایل خراب" {
			bad += n
		}
	}
	sort.Strings(reasons)
	t.Logf("groups %d (subs %d, with url %d), profiles %d, types %v", len(d.Groups), subs, urls, len(d.Profiles), types)
	t.Logf("skipped: %v", reasons)
	if bad > 0 {
		t.Errorf("%d profiles failed to decode", bad)
	}
}

func itoa(n int) string {
	s := ""
	if n == 0 {
		return "0"
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
