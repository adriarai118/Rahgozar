package backup

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"rahgozar/internal/model"
)

func TestSQLiteReader(t *testing.T) {
	b, err := os.ReadFile("testdata/wgt.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	db, err := openSQLite(b)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.table("TunnelConfig")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 300 {
		t.Fatalf("rows: %d", len(rows))
	}
	for i, r := range rows {
		n := i + 1
		if r["id"].(int64) != int64(n) {
			t.Fatalf("row %d id %v", n, r["id"])
		}
		if !strings.HasPrefix(r["name"].(string), "tunnel-") || !strings.HasSuffix(r["name"].(string), "سرور") {
			t.Fatalf("row %d name %q", n, r["name"])
		}
		q := r["quick_config"].(string)
		if !strings.Contains(q, "Jmax = 50") {
			t.Fatalf("row %d config %q", n, q[:40])
		}
		if n%37 == 0 && len(q) < 9000+n {
			t.Fatalf("row %d overflow text truncated: %d", n, len(q))
		}
		if r["position"].(int64) != int64(-n*70000) {
			t.Fatalf("row %d position %v", n, r["position"])
		}
		if r["prefer_ipv6"].(float64) != 0.5 {
			t.Fatalf("row %d real %v", n, r["prefer_ipv6"])
		}
	}
}

func wgtEncrypt(data []byte, password string) []byte {
	iv := make([]byte, 12)
	rand.Read(iv)
	key, _ := pbkdf2.Key(sha1.New, password, iv, 65536, 16)
	blk, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(blk)
	out := binary.BigEndian.AppendUint32(nil, 12)
	out = append(out, iv...)
	return gcm.Seal(out, iv, data, nil)
}

func zipOf(files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range files {
		w, _ := zw.Create(n)
		w.Write(b)
	}
	zw.Close()
	return buf.Bytes()
}

func TestWGTunnel(t *testing.T) {
	db, _ := os.ReadFile("testdata/wgt.sqlite3")
	man := []byte(`{"platform":"android","schemaVersion":39,"appVersionName":"4.2.2","encrypted":false}`)
	cases := map[string]struct {
		b    []byte
		name string
		pw   string
	}{
		"zip":      {zipOf(map[string][]byte{"manifest.json": man, "database.sqlite3": db}), "wgtunnel-backup.zip", ""},
		"zip-aes":  {zipOf(map[string][]byte{"manifest.json": man, "database.sqlite3.aes": wgtEncrypt(db, "s3cret")}), "b.zip", "s3cret"},
		"bare":     {db, "backup.sqlite3", ""},
		"bare-aes": {wgtEncrypt(db, "رمز"), "backup.sqlite3.aes", "رمز"},
	}
	for name, c := range cases {
		info := Detect(c.b, c.name)
		if info.Kind != KindWGTunnel || info.Encrypted != (c.pw != "") {
			t.Fatalf("%s: detect %+v", name, info)
		}
		if c.pw != "" {
			if _, err := WGTunnel(c.b, c.name, ""); err != ErrNeedPassword {
				t.Fatalf("%s: want ErrNeedPassword, got %v", name, err)
			}
			if _, err := WGTunnel(c.b, c.name, "wrong"); err != ErrPassword {
				t.Fatalf("%s: want ErrPassword, got %v", name, err)
			}
		}
		items, err := WGTunnel(c.b, c.name, c.pw)
		if err != nil || len(items) != 300 {
			t.Fatalf("%s: %d items, %v", name, len(items), err)
		}
	}
}

func TestZipConfigs(t *testing.T) {
	z := zipOf(map[string][]byte{
		"wg_tunnels/home.conf":   []byte("[Interface]\nPrivateKey = x\n"),
		"wg_tunnels/office.conf": []byte("[Interface]\nPrivateKey = y\n"),
		"__MACOSX/._home.conf":   []byte("junk"),
		"readme.pdf":             []byte("%PDF"),
	})
	if Detect(z, "wg.zip").Kind != KindZip {
		t.Fatal("detect zip")
	}
	items, err := ZipConfigs(z)
	if err != nil || len(items) != 2 {
		t.Fatalf("%v %v", items, err)
	}
}

func TestRahgozarRoundTrip(t *testing.T) {
	st := model.DefaultSettings()
	st.ProxyDomains = []string{"x.com"}
	d := &Data{
		Profiles: []*model.Profile{{ID: "a", Name: "آلمان", Type: model.TVLESS, Server: "1.2.3.4", Port: 443, AWG: map[string]string{"jc": "6"}}},
		Subs:     []*model.Subscription{{ID: "s", Name: "sub", URL: "https://example.com/s"}},
		Settings: &st,
		Learning: json.RawMessage(`{"servers":{}}`),
		Domains:  json.RawMessage(`[]`),
	}
	d.Manifest.Version = "1.4.3"
	for _, pw := range []string{"", "pa55 word"} {
		b, err := Write(d, pw)
		if err != nil {
			t.Fatal(err)
		}
		info := Detect(b, "x.rgbak")
		if info.Kind != KindRahgozar || info.Encrypted != (pw != "") {
			t.Fatalf("detect %+v", info)
		}
		if pw != "" {
			if _, err := Read(b, ""); err != ErrNeedPassword {
				t.Fatalf("want ErrNeedPassword: %v", err)
			}
			if _, err := Read(b, "nope"); err != ErrPassword {
				t.Fatalf("want ErrPassword: %v", err)
			}
		} else if info.Manifest == nil || info.Manifest.Profiles != 1 {
			t.Fatalf("manifest %+v", info.Manifest)
		}
		got, err := Read(b, pw)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Profiles) != 1 || got.Profiles[0].Name != "آلمان" || got.Profiles[0].AWG["jc"] != "6" ||
			len(got.Subs) != 1 || got.Settings == nil || got.Settings.ProxyDomains[0] != "x.com" || string(got.Learning) == "" {
			t.Fatalf("round trip: %+v", got)
		}
	}
}
