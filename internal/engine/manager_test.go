package engine

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func mkzip(t *testing.T, path string, files map[string]string) {
	f, _ := os.Create(path)
	z := zip.NewWriter(f)
	for n, c := range files {
		w, _ := z.Create(n)
		w.Write([]byte(c))
	}
	z.Close()
	f.Close()
}

func TestInstallArchive(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(filepath.Join(dir, "engines"))
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	z := filepath.Join(dir, "sb.zip")
	mkzip(t, z, map[string]string{"sing-box-1.14.4-linux-amd64/sing-box" + ext: "#!/bin/sh\necho 'sing-box version 1.14.4'\n", "sing-box-1.14.4-linux-amd64/LICENSE": "x"})
	if err := m.InstallArchive("sing-box", z); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ExePath("sing-box"); err != nil {
		t.Fatal(err)
	}
	z2 := filepath.Join(dir, "mh.zip")
	mkzip(t, z2, map[string]string{"mihomo-linux-amd64-compatible" + ext: "#!/bin/sh\necho 'Mihomo Meta v1.19.34'\n"})
	if err := m.InstallArchive("mihomo", z2); err != nil {
		t.Fatal(err)
	}
	// second install keeps a backup, rollback restores it
	if err := m.InstallArchive("sing-box", z); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback("sing-box"); err != nil {
		t.Fatal(err)
	}
	for _, s := range m.List() {
		if s.ID == "sing-box" && (!s.Installed || s.Version != "1.14.4") {
			t.Fatalf("%+v", s)
		}
		if s.ID == "mihomo" && s.Version != "v1.19.34" {
			t.Fatalf("mihomo %+v", s)
		}
	}
}
