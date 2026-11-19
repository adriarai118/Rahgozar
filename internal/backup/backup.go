// Package backup writes and reads Rahgozar backups and reads backups and
// exports of other apps (WG Tunnel, zip archives of configs).
package backup

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"rahgozar/internal/model"
)

var (
	// ErrNeedPassword: the file is encrypted and no password was given.
	ErrNeedPassword = errors.New("NEED_PASSWORD")
	// ErrPassword: wrong password (or corrupted file).
	ErrPassword = errors.New("رمز اشتباه است یا فایل خراب شده")
	// ErrUnknown: not a backup format we know.
	ErrUnknown = errors.New("قالب این فایل پشتیبان شناخته نشد")
)

// Manifest describes a Rahgozar backup.
type Manifest struct {
	App      string    `json:"app"`
	Format   int       `json:"format"`
	Version  string    `json:"version"`
	Platform string    `json:"platform"`
	Created  time.Time `json:"created"`
	Profiles int       `json:"profiles"`
	Subs     int       `json:"subs"`
	Settings bool      `json:"settings"`
	Learning bool      `json:"learning"`
}

// Data is the content of a Rahgozar backup.
type Data struct {
	Manifest Manifest
	Profiles []*model.Profile
	Subs     []*model.Subscription
	Settings *model.Settings
	Learning json.RawMessage // smart.Learner
	Domains  json.RawMessage // learned site list
}

const encMagic = "RGBAK1\n"

// Write serialises a backup; with a password the whole archive is
// encrypted (AES-256-GCM, PBKDF2-SHA256).
func Write(d *Data, password string) ([]byte, error) {
	d.Manifest.App, d.Manifest.Format = "rahgozar", 1
	d.Manifest.Profiles, d.Manifest.Subs = len(d.Profiles), len(d.Subs)
	d.Manifest.Settings = d.Settings != nil
	d.Manifest.Learning = len(d.Learning) > 0
	if d.Manifest.Created.IsZero() {
		d.Manifest.Created = time.Now()
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name string, v any) error {
		if v == nil {
			return nil
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		e := json.NewEncoder(w)
		e.SetIndent("", " ")
		return e.Encode(v)
	}
	if err := put("manifest.json", d.Manifest); err != nil {
		return nil, err
	}
	put("profiles.json", d.Profiles)
	put("subscriptions.json", d.Subs)
	if d.Settings != nil {
		put("settings.json", d.Settings)
	}
	if len(d.Learning) > 0 {
		put("learning.json", d.Learning)
	}
	if len(d.Domains) > 0 {
		put("domains.json", d.Domains)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if password == "" {
		return buf.Bytes(), nil
	}
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	rand.Read(salt)
	rand.Read(nonce)
	gcm, err := rgCipher(password, salt)
	if err != nil {
		return nil, err
	}
	out := append([]byte(encMagic), salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, buf.Bytes(), []byte(encMagic)), nil
}

func rgCipher(password string, salt []byte) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, 200_000, 32)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

// Kind identifies a backup or export file.
type Kind string

const (
	KindRahgozar Kind = "rahgozar"
	KindWGTunnel Kind = "wgtunnel"
	KindZip      Kind = "zip" // archive of configs (WireGuard, WireSock, WG Tunnel export, …)
	KindOther    Kind = ""
)

// Info is what Detect learns without a password.
type Info struct {
	Kind      Kind      `json:"kind"`
	Encrypted bool      `json:"encrypted"`
	Manifest  *Manifest `json:"manifest,omitempty"`
}

// Detect looks at a file's bytes (and name) to decide its kind.
func Detect(b []byte, name string) Info {
	if bytes.HasPrefix(b, []byte(encMagic)) {
		return Info{Kind: KindRahgozar, Encrypted: true}
	}
	ext := strings.ToLower(path.Ext(name))
	if isExclave(b) {
		return Info{Kind: KindExclave}
	}
	if bytes.HasPrefix(b, []byte("SQLite format 3\x00")) {
		return Info{Kind: KindWGTunnel}
	}
	if ext == ".aes" {
		return Info{Kind: KindWGTunnel, Encrypted: true}
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return Info{}
	}
	if isV2rayNG(zr) {
		return Info{Kind: KindV2rayNG}
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	if f := files["manifest.json"]; f != nil {
		raw, _ := readZipFile(f)
		var m Manifest
		if json.Unmarshal(raw, &m) == nil && m.App == "rahgozar" {
			return Info{Kind: KindRahgozar, Manifest: &m}
		}
		if files["database.sqlite3"] != nil {
			return Info{Kind: KindWGTunnel}
		}
		if files["database.sqlite3.aes"] != nil {
			return Info{Kind: KindWGTunnel, Encrypted: true}
		}
	}
	return Info{Kind: KindZip}
}

// Read decodes a Rahgozar backup.
func Read(b []byte, password string) (*Data, error) {
	if bytes.HasPrefix(b, []byte(encMagic)) {
		if password == "" {
			return nil, ErrNeedPassword
		}
		if len(b) < len(encMagic)+16+12+16 {
			return nil, ErrPassword
		}
		salt := b[len(encMagic) : len(encMagic)+16]
		nonce := b[len(encMagic)+16 : len(encMagic)+28]
		gcm, err := rgCipher(password, salt)
		if err != nil {
			return nil, err
		}
		plain, err := gcm.Open(nil, nonce, b[len(encMagic)+28:], []byte(encMagic))
		if err != nil {
			return nil, ErrPassword
		}
		b = plain
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, ErrUnknown
	}
	d := &Data{}
	get := func(name string, v any) error {
		for _, f := range zr.File {
			if f.Name == name {
				raw, err := readZipFile(f)
				if err != nil {
					return err
				}
				return json.Unmarshal(raw, v)
			}
		}
		return nil
	}
	if err := get("manifest.json", &d.Manifest); err != nil || d.Manifest.App != "rahgozar" {
		return nil, ErrUnknown
	}
	if err := get("profiles.json", &d.Profiles); err != nil {
		return nil, err
	}
	if err := get("subscriptions.json", &d.Subs); err != nil {
		return nil, err
	}
	var st model.Settings
	found := false
	for _, f := range zr.File {
		if f.Name == "settings.json" {
			found = true
		}
	}
	if found {
		if err := get("settings.json", &st); err != nil {
			return nil, err
		}
		d.Settings = &st
	}
	get("learning.json", &d.Learning)
	get("domains.json", &d.Domains)
	return d, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > 256<<20 {
		return nil, errors.New("فایل داخل آرشیو بیش از حد بزرگ است")
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 256<<20))
}

// Item is a config text found in another app's file.
type Item struct {
	Name string
	Text string
}

// ZipConfigs returns the config files inside a zip archive.
func ZipConfigs(b []byte) ([]Item, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, ErrUnknown
	}
	var out []Item
	for _, f := range zr.File {
		base := path.Base(f.Name)
		if f.FileInfo().IsDir() || strings.HasPrefix(f.Name, "__MACOSX/") || strings.HasPrefix(base, ".") {
			continue
		}
		switch strings.ToLower(path.Ext(base)) {
		case ".conf", ".ovpn", ".json", ".yaml", ".yml", ".txt":
		default:
			continue
		}
		raw, err := readZipFile(f)
		if err != nil {
			continue
		}
		out = append(out, Item{Name: strings.TrimSuffix(base, path.Ext(base)), Text: string(raw)})
	}
	if len(out) == 0 {
		return nil, errors.New("فایل کانفیگی داخل این zip پیدا نشد")
	}
	return out, nil
}

// WGTunnel reads a WG Tunnel backup (zip with manifest + database, a bare
// .sqlite3, or a password-protected .aes) and returns its tunnels.
func WGTunnel(b []byte, name, password string) ([]Item, error) {
	db := b
	if zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b))); err == nil {
		db = nil
		for _, f := range zr.File {
			switch f.Name {
			case "database.sqlite3":
				db, err = readZipFile(f)
			case "database.sqlite3.aes":
				var enc []byte
				if enc, err = readZipFile(f); err == nil {
					db, err = wgtDecrypt(enc, password)
				}
			}
			if err != nil {
				return nil, err
			}
		}
		if db == nil {
			return nil, ErrUnknown
		}
	} else if !bytes.HasPrefix(b, []byte("SQLite format 3\x00")) {
		var err error
		if db, err = wgtDecrypt(b, password); err != nil {
			return nil, err
		}
	}
	sq, err := openSQLite(db)
	if err != nil {
		return nil, err
	}
	rows, err := sq.table("TunnelConfig")
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, r := range rows {
		n, _ := r["name"].(string)
		var text string
		for _, c := range []string{"quick_config", "amnezia_quick", "wg_quick"} {
			if s, _ := r[c].(string); strings.TrimSpace(s) != "" {
				text = s
				break
			}
		}
		if text != "" {
			out = append(out, Item{Name: n, Text: text})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("تونلی در این پشتیبان WG Tunnel پیدا نشد")
	}
	return out, nil
}

// wgtDecrypt: int32 iv length | iv | AES-128-GCM(ciphertext+tag), key =
// PBKDF2-HMAC-SHA1(password, salt=iv, 65536 rounds).
func wgtDecrypt(b []byte, password string) ([]byte, error) {
	if password == "" {
		return nil, ErrNeedPassword
	}
	if len(b) < 4+12+16 {
		return nil, ErrPassword
	}
	n := int(binary.BigEndian.Uint32(b))
	if n != 12 || len(b) < 4+n+16 {
		return nil, ErrPassword
	}
	iv := b[4 : 4+n]
	key, err := pbkdf2.Key(sha1.New, password, iv, 65536, 16)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, iv, b[4+n:], nil)
	if err != nil {
		return nil, ErrPassword
	}
	return plain, nil
}
