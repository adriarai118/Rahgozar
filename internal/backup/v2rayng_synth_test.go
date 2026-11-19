package backup

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"testing"
)

func mmkvFile(kv ...string) []byte {
	var body []byte
	put := func(b []byte) {
		body = binary.AppendUvarint(body, uint64(len(b)))
		body = append(body, b...)
	}
	body = binary.AppendUvarint(body, 0xffffff) // item size holder
	for i := 0; i+1 < len(kv); i += 2 {
		put([]byte(kv[i]))
		v := binary.AppendUvarint(nil, uint64(len(kv[i+1])))
		put(append(v, kv[i+1]...))
	}
	out := binary.LittleEndian.AppendUint32(nil, uint32(len(body)))
	return append(append(out, body...), make([]byte, 64)...) // MMKV files are page-padded
}

func TestV2rayNGSynthetic(t *testing.T) {
	xray := `{"remarks":"cdn","outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"a.example.com","port":443,"users":[{"id":"11111111-2222-3333-4444-555555555555","encryption":"none"}]}]},"streamSettings":{"network":"ws","security":"tls","tlsSettings":{"serverName":"a.example.com","fingerprint":"chrome"},"wsSettings":{"path":"/ws","headers":{"Host":"a.example.com"}}},"tag":"proxy"},{"protocol":"freedom","tag":"direct"}]}`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(n string, b []byte) { w, _ := zw.Create(n); w.Write(b) }
	put("MAIN", mmkvFile("SELECTED_SERVER", "p1"))
	put("SUB", mmkvFile("s1", `{"remarks":"اشتراک","url":"https://sub.example.com/x"}`, "__default_subscription__", `{"remarks":"Default","url":""}`))
	put("PROFILE_FULL_CONFIG", mmkvFile(
		"p1", `{"configType":"VLESS","subscriptionId":"s1","remarks":"R","server":"1.2.3.4","serverPort":"443","password":"11111111-2222-3333-4444-555555555555","flow":"xtls-rprx-vision","network":"tcp","security":"reality","sni":"www.speedtest.net","publicKey":"jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0","shortId":"ab"}`,
		"p2", `{"configType":"SHADOWSOCKS","subscriptionId":"__default_subscription__","remarks":"ss","server":"5.6.7.8","serverPort":"8388","method":"aes-256-gcm","password":"pw"}`,
		"p3", `{"configType":"CUSTOM","subscriptionId":"s1","remarks":"custom"}`,
		"p4", `{"configType":"HYSTERIA2","remarks":"hy","server":"h.example.com","serverPort":"443","password":"x","portHopping":"20000-30000","portHoppingInterval":"30","bandwidthUp":"50 Mbps"}`,
		"p5", `{"configType":"VMESS","remarks":"gone","server":"x","serverPort":"443","password":"u"}`,
		"p5", "", // deleted later in the log
	))
	put("SERVER_RAW", mmkvFile("p3", xray))
	zw.Close()
	b := buf.Bytes()
	if Detect(b, "v2rayNG.zip").Kind != KindV2rayNG {
		t.Fatal("detect")
	}
	d, err := V2rayNG(b)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*ExProfile{}
	for i := range d.Profiles {
		got[d.Profiles[i].Profile.Name] = &d.Profiles[i]
	}
	if len(d.Profiles) != 4 || got["gone"] != nil {
		t.Fatalf("profiles: %d", len(d.Profiles))
	}
	r := got["R"].Profile
	if r.Type != "vless" || r.Security != "reality" || r.Flow != "xtls-rprx-vision" || r.Port != 443 || r.Fingerprint != "chrome" {
		t.Fatalf("vless: %+v", r)
	}
	if c := got["custom"].Profile; c.Type != "vless" || c.Network != "ws" || c.Path != "/ws" || c.Host != "a.example.com" || c.SNI != "a.example.com" {
		t.Fatalf("custom: %+v", c)
	}
	if h := got["hy"].Profile; h.Ports != "20000-30000" || h.HopInterval != "30s" || h.UpMbps != 50 {
		t.Fatalf("hy2: %+v", h)
	}
	subs := 0
	for _, g := range d.Groups {
		if g.Sub {
			subs++
			if got["R"].Group != g.ID {
				t.Fatal("group link")
			}
		}
	}
	if subs != 1 {
		t.Fatalf("subs %d", subs)
	}
}
