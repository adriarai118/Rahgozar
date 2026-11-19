package builder

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"rahgozar/internal/model"
)

// Validate rejects profiles sing-box would refuse to start with. One such
// outbound fails a whole config, so tests and connections check first.
func Validate(p *model.Profile) error {
	if p.Security == "reality" {
		if !validRealityKey(p.PublicKey) {
			return errors.New("کلید عمومی Reality نامعتبر است")
		}
		if sid := strings.TrimSpace(p.ShortID); sid != "" {
			if len(sid) > 16 || len(sid)%2 != 0 {
				return errors.New("short id نامعتبر است")
			}
			if _, err := hex.DecodeString(sid); err != nil {
				return errors.New("short id نامعتبر است")
			}
		}
	}
	if p.Network == "quic" && p.Security != "tls" && (p.Type == model.TVMess || p.Type == model.TVLESS || p.Type == model.TTrojan) {
		return errors.New("ترنسپورت QUIC بدون TLS پشتیبانی نمی‌شود")
	}
	if p.Type == model.TShadowsocks {
		m := strings.ToLower(strings.TrimSpace(p.Method))
		if !ssMethods[m] {
			return errors.New("رمزنگاری " + p.Method + " در sing-box پشتیبانی نمی‌شود")
		}
		if m != "none" && p.Password == "" {
			return errors.New("رمز Shadowsocks خالی است")
		}
		if strings.HasPrefix(m, "2022-") {
			want := 32
			if m == "2022-blake3-aes-128-gcm" {
				want = 16
			}
			for _, k := range strings.Split(p.Password, ":") {
				b, err := base64.StdEncoding.DecodeString(k)
				if err != nil || len(b) != want {
					return errors.New("کلید Shadowsocks 2022 نامعتبر است")
				}
			}
		}
	}
	return nil
}

var ssMethods = map[string]bool{
	"none": true, "2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true, "2022-blake3-chacha20-poly1305": true,
	"aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true, "chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
	"aes-128-ctr": true, "aes-192-ctr": true, "aes-256-ctr": true, "aes-128-cfb": true, "aes-192-cfb": true, "aes-256-cfb": true,
	"rc4-md5": true, "chacha20-ietf": true, "xchacha20": true,
}

func validRealityKey(k string) bool {
	k = strings.TrimRight(strings.TrimSpace(k), "=")
	b, err := base64.RawURLEncoding.DecodeString(k)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(k)
	}
	return err == nil && len(b) == 32
}

// fixPercent escapes stray '%' so a path parses as a URL ("/a%2" -> "/a%252").
func fixPercent(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	isHex := func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && !(i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2])) {
			b.WriteString("%25")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// fixPluginOpts drops values sing-box can't parse ("mux=true", "mux=undefined").
func fixPluginOpts(opts string) string {
	if !strings.Contains(opts, "mux") {
		return opts
	}
	var out []string
	for _, kv := range strings.Split(opts, ";") {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.TrimSpace(k) == "mux" {
			v = strings.TrimSpace(v)
			if i := strings.LastIndexByte(v, '='); i >= 0 {
				v = v[i+1:] // "mux=mux=4"
			}
			if v == "true" {
				v = "1"
			}
			if _, err := strconv.Atoi(v); err != nil {
				continue
			}
			kv = "mux=" + v
		}
		out = append(out, kv)
	}
	return strings.Join(out, ";")
}
