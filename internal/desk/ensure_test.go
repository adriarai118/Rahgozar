package desk

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestEnsureExtension(t *testing.T) {
	exe, _ := os.Executable()
	dst := filepath.Join(filepath.Dir(exe), "extension")
	os.RemoveAll(dst)
	defer os.RemoveAll(dst)
	f := fstest.MapFS{
		"extension/manifest.json": {Data: []byte(`{"version":"2.0.0"}`)},
		"extension/flags/de.png":  {Data: []byte("png")},
	}
	EnsureExtension(f, "extension")
	if b, _ := os.ReadFile(filepath.Join(dst, "manifest.json")); string(b) != `{"version":"2.0.0"}` {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "flags", "de.png")); err != nil {
		t.Fatal(err)
	}
	if a := (&App{appDir: filepath.Dir(exe)}).extensionVersion(); a != "2.0.0" {
		t.Fatal(a)
	}
}
