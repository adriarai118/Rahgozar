package builder

import (
	"testing"

	"rahgozar/internal/model"
)

func TestWireGuardEngine(t *testing.T) {
	old := HelperAvailable
	defer func() { HelperAvailable = old }()
	junk := map[string]string{"jc": "6", "jmin": "10", "jmax": "50"}
	awg := map[string]string{"jc": "4", "s1": "122", "h1": "1318795025-1318795156"}
	cases := []struct {
		mihomo bool
		engine string
		awg    map[string]string
		want   string
	}{
		{false, model.EngineSingBox, junk, model.EngineSingBox}, // Android, user forced sing-box
		{false, "", junk, model.EngineSingBox},                  // Android, auto: junk is optional
		{false, "", nil, model.EngineSingBox},
		{false, "", awg, model.EngineMihomo}, // real AmneziaWG server needs Mihomo
		{false, model.EngineSingBox, awg, model.EngineSingBox},
		{true, "", junk, model.EngineMihomo},
		{true, "", nil, model.EngineMihomo},
		{true, model.EngineSingBox, junk, model.EngineSingBox},
		{false, "", map[string]string{"jc": "4", "s1": "0", "h1": "1", "h2": "2"}, model.EngineSingBox},
	}
	for i, c := range cases {
		HelperAvailable = func(string) bool { return c.mihomo }
		p := &model.Profile{Type: model.TWireGuard, Engine: c.engine, AWG: c.awg}
		if got := EngineFor(p); got != c.want {
			t.Errorf("case %d: got %s want %s", i, got, c.want)
		}
	}
}

func TestValidateAndFixes(t *testing.T) {
	good := "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0"
	cases := []struct {
		p  model.Profile
		ok bool
	}{
		{model.Profile{Type: model.TVLESS, Security: "reality", PublicKey: good, ShortID: "12ab"}, true},
		{model.Profile{Type: model.TVLESS, Security: "reality", PublicKey: "abc"}, false},
		{model.Profile{Type: model.TVLESS, Security: "reality", PublicKey: good, ShortID: "r@"}, false},
		{model.Profile{Type: model.TVLESS, Security: "reality", PublicKey: good, ShortID: "123"}, false},
		{model.Profile{Type: model.TShadowsocks, Method: "chacha20", Password: "x"}, false},
		{model.Profile{Type: model.TShadowsocks, Method: "aes-256-gcm"}, false},
		{model.Profile{Type: model.TShadowsocks, Method: "AES-256-GCM", Password: "x"}, true},
		{model.Profile{Type: model.TShadowsocks, Method: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}, false},
		{model.Profile{Type: model.TShadowsocks, Method: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA=="}, true},
		{model.Profile{Type: model.TVMess, Network: "quic"}, false},
	}
	for i, c := range cases {
		if err := Validate(&c.p); (err == nil) != c.ok {
			t.Errorf("case %d: %v", i, err)
		}
	}
	if got := fixPercent("/a%2.b%20c%"); got != "/a%252.b%20c%25" {
		t.Error(got)
	}
	if got := fixPluginOpts("obfs=http;mux=true;host=a"); got != "obfs=http;mux=1;host=a" {
		t.Error(got)
	}
	if got := fixPluginOpts("tls;mux=undefined"); got != "tls" {
		t.Error(got)
	}
	if got := fixPluginOpts("mux=mux=4"); got != "mux=4" {
		t.Error(got)
	}
}
