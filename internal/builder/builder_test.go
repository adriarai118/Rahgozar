package builder

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"rahgozar/internal/model"
	"rahgozar/internal/parser"
)

var links = []string{
	"vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?type=tcp&security=reality&pbk=jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0&sid=12ab&sni=www.speedtest.net&fp=chrome&flow=xtls-rprx-vision#Reality",
	"vless://11111111-2222-3333-4444-555555555555@a.example.com:443?type=ws&security=tls&path=%2Fws%3Fed%3D2048&host=a.example.com&sni=a.example.com#ws",
	"vless://11111111-2222-3333-4444-555555555555@a.example.com:443?type=grpc&security=tls&serviceName=gun&sni=a.example.com#grpc",
	"vless://11111111-2222-3333-4444-555555555555@a.example.com:443?type=httpupgrade&security=tls&path=%2Fhu&host=a.example.com#hu",
	"vmess://eyJ2IjoiMiIsInBzIjoidGVzdCIsImFkZCI6ImEuZXhhbXBsZS5jb20iLCJwb3J0IjoiNDQzIiwiaWQiOiIxMTExMTExMS0yMjIyLTMzMzMtNDQ0NC01NTU1NTU1NTU1NTUiLCJhaWQiOiIwIiwic2N5IjoiYXV0byIsIm5ldCI6IndzIiwidHlwZSI6Im5vbmUiLCJob3N0IjoiYS5leGFtcGxlLmNvbSIsInBhdGgiOiIvd3MiLCJ0bHMiOiJ0bHMifQ==",
	"trojan://pass@t.example.com:443?sni=t.example.com#tr",
	"ss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpwdw@5.6.7.8:8388#ss",
	"hysteria2://pw@h.example.com:443?sni=h.example.com&obfs=salamander&obfs-password=x#hy2",
	"hy2://pw@h.example.com:20000-30000?insecure=1#hop",
	"hysteria://h.example.com:443?auth=x&peer=h.example.com&upmbps=50&downmbps=100#hy1",
	"tuic://11111111-2222-3333-4444-555555555555:pw@t.example.com:443?congestion_control=bbr&alpn=h3&sni=t.example.com#tuic",
	"wireguard://yAnz5TF%2BlXXJte14tji3zlMNq%2BhpluSHdNzVbP7O9FA%3D@162.159.192.1:2408?publickey=bmq7Ny0g6SmrcVPF%2F6nCYJmZkWhn6r8xgHfRPYGBHUo%3D&address=172.16.0.2/32&reserved=1,2,3#warp",
	"socks://user:pass@127.0.0.1:1080#s",
	"anytls://pw@a.example.com:443?sni=a.example.com#any",
	"ssh://root:pw@s.example.com:22#ssh",
	// xray + mihomo helpers
	"vless://11111111-2222-3333-4444-555555555555@x.example.com:443?type=xhttp&security=tls&path=%2Fx&mode=auto&sni=x.example.com#xhttp",
	"ssr://OS45LjkuOTo4OTg5OmF1dGhfYWVzMTI4X21kNTphZXMtMjU2LWNmYjp0bHMxLjJfdGlja2V0X2F1dGg6Y0hjLz9yZW1hcmtzPVUxTlM",
}

const ovpn = `client
dev tun
proto udp
remote vpn.example.com 1194
remote vpn2.example.com 443 tcp
cipher AES-256-CBC
data-ciphers AES-256-GCM:AES-128-GCM
auth SHA256
auth-user-pass
remote-cert-tls server
key-direction 1
comp-lzo
<ca>
-----BEGIN CERTIFICATE-----
MIIBszCCAVmgAwIBAgIUEXAMPLE
-----END CERTIFICATE-----
</ca>
<tls-auth>
-----BEGIN OpenVPN Static key V1-----
00112233445566778899aabbccddeeff
-----END OpenVPN Static key V1-----
</tls-auth>`

func profiles(t *testing.T) []*model.Profile {
	var ps []*model.Profile
	for _, l := range links {
		p, err := parser.ParseLink(l)
		if err != nil {
			t.Fatalf("parse %s: %v", l[:20], err)
		}
		ps = append(ps, p)
	}
	o, err := parser.ParseOVPN(ovpn, "ovpn")
	if err != nil {
		t.Fatal(err)
	}
	o.Username, o.Password = "u", "p"
	ps = append(ps, o)
	return ps
}

func singbox() string {
	if b := os.Getenv("SINGBOX_BIN"); b != "" {
		return b
	}
	return "/home/claude/bin/sing-box"
}

func check(t *testing.T, name string, cfg M) {
	bin := singbox()
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no sing-box binary")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "c.json")
	b, _ := json.MarshalIndent(cfg, "", " ")
	os.WriteFile(f, b, 0o644)
	out, err := exec.Command(bin, "check", "-c", f).CombinedOutput()
	if err != nil {
		t.Errorf("%s: sing-box check failed: %v\n%s\n%s", name, err, out, b)
	}
}

func setupDirs(t *testing.T) (string, string, map[string]bool) {
	rules := t.TempDir()
	learned := t.TempDir()
	has := map[string]bool{}
	src := "/home/claude/ref/irrules"
	for tag, file := range RuleSetFiles {
		if b, err := os.ReadFile(filepath.Join(src, file)); err == nil {
			os.WriteFile(filepath.Join(rules, file), b, 0o644)
			has[tag] = true
		}
	}
	for _, file := range LearnedFiles {
		os.WriteFile(filepath.Join(learned, file), EmptyRuleSet(), 0o644)
	}
	return rules, learned, has
}

func TestBuildAll(t *testing.T) {
	ps := profiles(t)
	rules, learned, has := setupDirs(t)
	byID := map[string]*model.Profile{}
	for _, p := range ps {
		byID[p.ID] = p
	}
	port := 20000
	for _, mode := range []string{"proxy", "tun"} {
		for _, rm := range []string{"smart", "global", "rules", "direct"} {
			for _, p := range ps {
				st := model.DefaultSettings()
				st.RoutingMode = rm
				st.BlockAds = true
				st.FakeIP = mode == "tun"
				st.FragmentEnabled = true
				st.ProxyDomains = []string{"youtube.com"}
				st.DirectDomains = []string{"aparat.com"}
				st.BlockDomains = []string{"ads.example.com"}
				in := &Input{
					Settings: st, Profile: p, Mode: mode, RulesDir: rules, LearnedDir: learned,
					ClashAddr: "127.0.0.1:9090", ClashSecret: "s", CacheFile: filepath.Join(t.TempDir(), "cache.db"),
					PortAlloc: func() int { port++; return port }, ExeName: "Rahgozar.exe",
					EngineExes: []string{"xray.exe", "mihomo.exe"}, HasRuleFiles: has,
					Resolve: func(id string) *model.Profile { return byID[id] },
				}
				res, err := Build(in)
				if err != nil {
					t.Fatalf("%s %s %s: %v", mode, rm, p.Name, err)
				}
				if mode == "tun" && strings.Contains(os.Getenv("SKIP_TUN"), "1") {
					continue
				}
				check(t, mode+"/"+rm+"/"+p.Name, res.SingBox)
				for _, h := range res.Helpers {
					if len(h.Config) == 0 {
						t.Errorf("empty helper config")
					}
				}
			}
			if rm != "smart" {
				continue
			}
		}
	}
	// chain
	chain := &model.Profile{ID: "chain1", Name: "chain", Type: model.TChain, Chain: []string{ps[0].ID, ps[5].ID, ps[11].ID}}
	byID[chain.ID] = chain
	res, err := Build(&Input{Settings: model.DefaultSettings(), Profile: chain, Mode: "proxy", RulesDir: rules, LearnedDir: learned,
		PortAlloc: func() int { port++; return port }, HasRuleFiles: has, Resolve: func(id string) *model.Profile { return byID[id] }})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "chain", res.SingBox)
	// test config
	tc, skipped := BuildTest(&TestInput{Profiles: ps, ClashAddr: "127.0.0.1:9191", Settings: model.DefaultSettings()})
	if len(skipped) != 2 {
		t.Errorf("skipped %d", len(skipped))
	}
	check(t, "test", tc)
}

func TestXrayConfigs(t *testing.T) {
	bin := "/home/claude/bin/xray"
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no xray")
	}
	ls := []string{
		"vless://11111111-2222-3333-4444-555555555555@x.example.com:443?type=xhttp&security=reality&pbk=jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0&sid=ab&sni=www.speedtest.net&mode=stream-one&path=%2Fx#a",
		"vless://11111111-2222-3333-4444-555555555555@x.example.com:443?type=xhttp&security=tls&path=%2Fx&host=x.example.com&extra=%7B%22xPaddingBytes%22%3A%22100-1000%22%7D#b",
		"vless://11111111-2222-3333-4444-555555555555@x.example.com:443?type=kcp&headerType=wechat-video&seed=abc#c",
		"vless://11111111-2222-3333-4444-555555555555@x.example.com:80?type=tcp&headerType=http&host=a.com&path=%2F#d",
		"vmess://eyJ2IjoiMiIsInBzIjoidGVzdCIsImFkZCI6ImEuZXhhbXBsZS5jb20iLCJwb3J0IjoiNDQzIiwiaWQiOiIxMTExMTExMS0yMjIyLTMzMzMtNDQ0NC01NTU1NTU1NTU1NTUiLCJhaWQiOiIwIiwic2N5IjoiYXV0byIsIm5ldCI6IndzIiwidHlwZSI6Im5vbmUiLCJob3N0IjoiYS5leGFtcGxlLmNvbSIsInBhdGgiOiIvd3MiLCJ0bHMiOiJ0bHMifQ==",
		"trojan://pass@t.example.com:443?sni=t.example.com&type=grpc&serviceName=g#tr",
		"ss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpwdw@5.6.7.8:8388#ss",
		"vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?type=tcp&security=reality&pbk=jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0&sid=12ab&sni=www.speedtest.net&fp=chrome&flow=xtls-rprx-vision#R",
	}
	for _, l := range ls {
		p, err := parser.ParseLink(l)
		if err != nil {
			t.Fatal(err)
		}
		b, err := XrayConfig(p, 30001, "warn")
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(t.TempDir(), "x.json")
		os.WriteFile(f, b, 0o644)
		out, err := exec.Command(bin, "run", "-test", "-c", f).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "Configuration OK") {
			t.Errorf("%s: %v\n%s\n%s", p.Name, err, out, b)
		}
	}
}
