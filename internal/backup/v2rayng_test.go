package backup

import (
	"os"
	"testing"
)

// Real v2rayNG backup when V2RAYNG_BACKUP points to one (private data, not shipped).
func TestV2rayNGRealBackup(t *testing.T) {
	f := os.Getenv("V2RAYNG_BACKUP")
	if f == "" {
		t.Skip("V2RAYNG_BACKUP not set")
	}
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if k := Detect(b, f).Kind; k != KindV2rayNG {
		t.Fatalf("detect: %q", k)
	}
	d, err := V2rayNG(b)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]int{}
	for _, p := range d.Profiles {
		types[p.Profile.Type+"/"+p.Profile.Network+"/"+p.Profile.Security]++
	}
	subs := 0
	for _, g := range d.Groups {
		if g.Sub {
			subs++
		}
	}
	t.Logf("groups %d (subs %d), profiles %d: %v, skipped %v", len(d.Groups), subs, len(d.Profiles), types, d.Skipped)
}
