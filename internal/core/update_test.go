package core

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractSubdir(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "u.zip")
	f, _ := os.Create(zp)
	z := zip.NewWriter(f)
	for name, body := range map[string]string{
		"Rahgozar.exe": "x", "extension/manifest.json": `{"version":"9.9.9"}`, "extension/flags/de.png": "png",
		"rules/geoip-ir.srs": "srs",
	} {
		w, _ := z.Create(name)
		w.Write([]byte(body))
	}
	z.Close()
	f.Close()
	dst := filepath.Join(dir, "app", "extension")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(dst, "stale.js"), []byte("old"), 0o644)
	zr, err := zip.OpenReader(zp)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if err := extractSubdir(zr, "extension", dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "manifest.json")); string(b) != `{"version":"9.9.9"}` {
		t.Fatalf("manifest %q", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "flags", "de.png")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.js")); err == nil {
		t.Fatal("stale file kept")
	}
	if err := extractSubdir(zr, "rules", filepath.Join(dir, "app", "rules")); err != nil {
		t.Fatal(err)
	}
}
