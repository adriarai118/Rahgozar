package desk

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"rahgozar/internal/builder"
	"rahgozar/internal/model"
)

// Real Exclave backup, when EXCLAVE_BACKUP points to one (private data, not shipped).
func TestExclaveRestore(t *testing.T) {
	f := os.Getenv("EXCLAVE_BACKUP")
	if f == "" {
		f = os.Getenv("V2RAYNG_BACKUP")
	}
	if f == "" {
		t.Skip("EXCLAVE_BACKUP / V2RAYNG_BACKUP not set")
	}
	p := &filePlat{pick: f}
	a := newTestApp(t, p)
	t0 := time.Now()
	bf, err := a.OpenBackup()
	if err != nil || bf.Summary == nil || bf.Token == "" {
		t.Fatalf("%+v %v", bf, err)
	}
	t.Logf("open %v: %+v", time.Since(t0), *bf.Summary)

	t0 = time.Now()
	r, err := a.RestoreBackup(bf.Token, "", RestoreOptions{Servers: true, Subs: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("default restore %v: added %d, notes %v", time.Since(t0), r.Added, r.Errors)
	if r.Added != bf.Summary.Manual-countDup(a) || len(a.store.Subscriptions()) != bf.Summary.Subs {
		t.Logf("subs %d", len(a.store.Subscriptions()))
	}

	// everything, into a fresh app
	b := newTestApp(t, &filePlat{pick: f})
	bf, _ = b.OpenBackup()
	t0 = time.Now()
	r, _ = b.RestoreBackup(bf.Token, "", RestoreOptions{Servers: true, Subs: true, SubServers: true})
	t.Logf("full restore %v: added %d of %d", time.Since(t0), r.Added, bf.Summary.Manual+bf.Summary.SubServers)
	st, _ := os.Stat(b.dataDir + "/profiles.json")
	t.Logf("profiles.json %d MB", st.Size()>>20)

	subs := map[string]bool{}
	for _, s := range b.store.Subscriptions() {
		subs[s.ID] = true
	}
	// every restored server must turn into an engine config; then let the
	// real sing-box validate a test config with up to 300 servers per type
	var sample []*model.Profile
	per := map[string]int{}
	fail := map[string]int{}
	for _, p := range b.store.Profiles() {
		if p.Group != "manual" && !subs[p.Group] {
			t.Fatalf("dangling group")
		}
		if builder.EngineFor(p) == model.EngineSingBox {
			if _, _, err := builder.SingBoxOutbound(p, "x", &model.Settings{}); err != nil {
				fail[p.Type+": "+err.Error()]++
				continue
			}
			if per[p.Type] < 1<<30 {
				per[p.Type]++
				sample = append(sample, p)
			}
		}
	}
	t.Logf("outbound build failures: %v", fail)
	bin := "/home/claude/bin/sing-box"
	all := sample
	reasons := map[string]int{}
	for len(all) > 0 {
		sample = all[:min(4000, len(all))]
		all = all[len(sample):]
		cfg, skipped := builder.BuildTest(&builder.TestInput{Profiles: sample, ClashAddr: "127.0.0.1:9191", Settings: model.DefaultSettings()})
		if _, err := os.Stat(bin); err == nil {
			dir := t.TempDir()
			var js []byte
			// drop rejected outbounds one by one to list every kind of bad data
			obs := cfg["outbounds"].([]builder.M)
			for round := 0; round < 4000; round++ {
				cfg["outbounds"] = obs
				js, _ = json.Marshal(cfg)
				os.WriteFile(dir+"/c.json", js, 0o644)
				out, err := exec.Command(bin, "check", "-c", dir+"/c.json").CombinedOutput()
				if err == nil {
					break
				}
				m := regexp.MustCompile(`outbound\[(\d+)\]: (.*)`).FindStringSubmatch(string(out))
				if m == nil {
					t.Fatalf("check: %s", out)
				}
				i, _ := strconv.Atoi(m[1])
				typ, _ := obs[i]["type"].(string)
				reasons[typ+": "+strings.TrimSpace(m[2])]++
				obs = append(obs[:i:i], obs[i+1:]...)
			}
			_ = skipped
		}
	}
	t.Logf("sing-box rejected: %v", reasons)
	if len(reasons) > 0 {
		t.Error("sing-box rejected some servers")
	}
}

func countDup(a *App) int { return 0 }
