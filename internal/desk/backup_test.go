package desk

import (
	"os"
	"path/filepath"
	"testing"

	"rahgozar/internal/backup"
	"rahgozar/internal/model"
)

type filePlat struct {
	HeadlessPlatform
	saved []byte
	pick  string
}

func (f *filePlat) SaveFile(name string, b []byte) (string, error) { f.saved = b; return name, nil }
func (f *filePlat) PickFiles(string, string, bool) ([]string, error) {
	return []string{f.pick}, nil
}

func newTestApp(t *testing.T, p *filePlat) *App {
	a := NewApp(Options{DataDir: t.TempDir(), Platform: p})
	return a
}

func TestBackupRestore(t *testing.T) {
	pa := &filePlat{}
	a := newTestApp(t, pa)
	sub := &model.Subscription{ID: "sub1", Name: "اشتراک", URL: "https://example.com/sub"}
	a.store.UpsertSub(sub)
	ps := []*model.Profile{
		{ID: "p1", Name: "آلمان", Type: model.TVLESS, Server: "1.2.3.4", Port: 443, UUID: "u1", Group: "manual", Raw: "vless://u1@1.2.3.4:443#a"},
		{ID: "p2", Name: "wg", Type: model.TWireGuard, Server: "5.6.7.8", Port: 51820, PrivateKey: "k", Group: "manual", AWG: map[string]string{"jc": "6"}},
		{ID: "p3", Name: "s1", Type: model.TTrojan, Server: "t.example.com", Port: 443, Password: "x", Group: "sub1", Raw: "trojan://x@t.example.com:443#s1"},
		{ID: "c1", Name: "زنجیره", Type: model.TChain, Chain: []string{"p2", "p1"}, Group: "manual"},
	}
	a.store.AddProfiles(ps)
	st := a.store.Settings()
	st.ProxyDomains = []string{"x.com"}
	st.RoutingMode = "global"
	st.SelectedID = "c1"
	st.ExtToken = "secret-token"
	a.store.SetSettings(st)

	if _, err := a.CreateBackup(BackupOptions{Settings: true, Learning: true, Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if len(pa.saved) == 0 {
		t.Fatal("nothing saved")
	}
	file := filepath.Join(t.TempDir(), "b.rgbak")
	os.WriteFile(file, pa.saved, 0o600)

	// another device that already has one of the servers and other settings
	pb := &filePlat{pick: file}
	b := newTestApp(t, pb)
	b.store.AddProfiles([]*model.Profile{{ID: "x9", Name: "local", Type: model.TVLESS, Server: "1.2.3.4", Port: 443, UUID: "u1", Group: "manual", Raw: "vless://u1@1.2.3.4:443#a"}})
	lst := b.store.Settings()
	lst.Mode = "proxy"
	lst.ProxyDomains = []string{"y.com"}
	b.store.SetSettings(lst)

	bf, err := b.OpenBackup()
	if err != nil || bf == nil || !bf.Encrypted || bf.Token == "" {
		t.Fatalf("open: %+v %v", bf, err)
	}
	if _, err := b.RestoreBackup(bf.Token, "", RestoreOptions{Servers: true}); err != backup.ErrNeedPassword {
		t.Fatalf("want need password, got %v", err)
	}
	if _, err := b.RestoreBackup(bf.Token, "bad", RestoreOptions{Servers: true}); err != backup.ErrPassword {
		t.Fatalf("want bad password, got %v", err)
	}
	r, err := b.RestoreBackup(bf.Token, "pw", RestoreOptions{Servers: true, Settings: true, Learning: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Added != 3 { // p1 already there
		t.Fatalf("added %d: %v", r.Added, r.Errors)
	}
	if len(b.store.Profiles()) != 4 {
		t.Fatalf("profiles %d", len(b.store.Profiles()))
	}
	if len(b.store.Subscriptions()) != 1 || b.store.Profile("p3") == nil || b.store.Profile("p3").Group != b.store.Subscriptions()[0].ID {
		t.Fatalf("subscription not linked")
	}
	if c := b.store.Profile("c1"); c == nil || len(c.Chain) != 2 || b.store.Profile("p2") == nil || b.store.Profile("p2").AWG["jc"] != "6" {
		t.Fatalf("chain/wg not restored")
	}
	got := b.store.Settings()
	if got.Mode != "proxy" || got.RoutingMode != "global" || got.ExtToken == "secret-token" || len(got.ProxyDomains) != 2 || got.SelectedID != "c1" {
		t.Fatalf("settings merge: mode=%s routing=%s token=%q proxy=%v sel=%s", got.Mode, got.RoutingMode, got.ExtToken, got.ProxyDomains, got.SelectedID)
	}
	// restoring again adds nothing
	bf, _ = b.OpenBackup()
	if r, _ := b.RestoreBackup(bf.Token, "pw", RestoreOptions{Servers: true}); r.Added != 0 {
		t.Fatalf("second restore added %d", r.Added)
	}
}

func TestImportWGTunnelBackupViaFiles(t *testing.T) {
	src, err := os.ReadFile("../backup/testdata/wgt.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "wgtunnel.sqlite3")
	os.WriteFile(f, src, 0o600)
	p := &filePlat{pick: f}
	a := newTestApp(t, p)
	r := a.ImportFiles()
	if r.Added != 300 {
		t.Fatalf("added %d errors %v", r.Added, r.Errors[:min(3, len(r.Errors))])
	}
	var wg *model.Profile
	for _, x := range a.store.Profiles() {
		wg = x
		break
	}
	if wg.Type != model.TWireGuard || wg.AWG["jc"] != "6" || wg.Name == "" {
		t.Fatalf("%+v", wg)
	}
}
