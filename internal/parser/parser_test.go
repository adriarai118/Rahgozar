package parser

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLinks(t *testing.T) {
	vm := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"وی‌مس تست","add":"a.example.com","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"cdn.example.com","fp":"chrome"}`))
	cases := map[string]string{
		"vmess://" + vm: "vmess",
		"vless://11111111-2222-3333-4444-555555555555@1.2.3.4:443?type=tcp&security=reality&pbk=abc&sid=12ab&sni=www.speedtest.net&fp=chrome&flow=xtls-rprx-vision#Reality%20DE":                                                                    "vless",
		"vless://uuid@host.com:443?type=xhttp&security=tls&path=%2Fx&mode=auto&sni=host.com#xhttp":                                                                                                                                                  "vless",
		"trojan://pass@t.example.com:443?sni=t.example.com&type=grpc&serviceName=gun#tr":                                                                                                                                                            "trojan",
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw")) + "@5.6.7.8:8388#ss1":                                                                                                                                   "shadowsocks",
		"ss://2022-blake3-aes-128-gcm:YWJjZGVmZ2hpamtsbW5vcA==@5.6.7.8:8388#ss2022":                                                                                                                                                                 "shadowsocks",
		"ssr://" + base64.RawURLEncoding.EncodeToString([]byte("9.9.9.9:8989:auth_aes128_md5:aes-256-cfb:tls1.2_ticket_auth:"+base64.RawURLEncoding.EncodeToString([]byte("pw"))+"/?remarks="+base64.RawURLEncoding.EncodeToString([]byte("SSR")))): "ssr",
		"hysteria2://pw@h.example.com:443?sni=h.example.com&obfs=salamander&obfs-password=x#hy2":                                                                                                                                                    "hysteria2",
		"hy2://pw@h.example.com:20000-30000?insecure=1#hop":                                                                                                                                                                                         "hysteria2",
		"hysteria://h.example.com:443?auth=x&peer=h.example.com&upmbps=50&downmbps=100#hy1":                                                                                                                                                         "hysteria",
		"tuic://uuid:pw@t.example.com:443?congestion_control=bbr&alpn=h3&sni=t.example.com#tuic":                                                                                                                                                    "tuic",
		"wireguard://cHJpdmF0ZQ%3D%3D@162.159.192.1:2408?publickey=bmV3&address=172.16.0.2/32&reserved=1,2,3#warp":                                                                                                                                  "wireguard",
		"socks://user:pass@127.0.0.1:1080#s":    "socks",
		"https://user:pass@p.example.com:443":   "http",
		"l2tp://u:p@vpn.example.com?psk=key#l2": "l2tp",
		"sstp://u:p@vpn.example.com:443#s":      "sstp",
	}
	for link, typ := range cases {
		p, err := ParseLink(link)
		if err != nil {
			t.Errorf("%s: %v", link[:20], err)
			continue
		}
		if p.Type != typ {
			t.Errorf("%s: type %s want %s", link[:20], p.Type, typ)
		}
		if p.Server == "" || (p.Port == 0 && !p.IsRAS()) {
			t.Errorf("%s: bad server %q %d", link[:20], p.Server, p.Port)
		}
		// round trip
		if l := ToLink(p); l != "" {
			q, err := ParseLink(l)
			if err != nil || q.Server != p.Server || q.Type != p.Type {
				t.Errorf("roundtrip %s -> %s: %v", typ, l, err)
			}
		}
	}
	p, _ := ParseLink("hy2://pw@h.example.com:20000-30000?insecure=1#hop")
	if p.Ports != "20000-30000" || p.Port != 20000 {
		t.Errorf("hop ports %q %d", p.Ports, p.Port)
	}
	p, _ = ParseLink("vmess://" + vm)
	if p.Network != "ws" || p.Path != "/ws" || p.Security != "tls" || p.Name != "وی‌مس تست" {
		t.Errorf("vmess fields %+v", p)
	}
}

func TestSubscription(t *testing.T) {
	body := "vless://u@a.com:443?security=tls#a\ntrojan://p@b.com:443#b\n"
	ps, errs := ParseText(base64.StdEncoding.EncodeToString([]byte(body)))
	if len(ps) != 2 || len(errs) != 0 {
		t.Fatalf("got %d %v", len(ps), errs)
	}
}

func TestWGConf(t *testing.T) {
	conf := `[Interface]
PrivateKey = aaaa
Address = 10.0.0.2/32, fd00::2/128
MTU = 1280
[Peer]
PublicKey = bbbb
Endpoint = 1.2.3.4:51820
AllowedIPs = 0.0.0.0/0`
	ps, errs := ParseText(conf)
	if len(ps) != 1 || len(errs) > 0 || len(ps[0].LocalAddress) != 2 {
		t.Fatalf("%v %v", ps, errs)
	}
}

func TestOVPN(t *testing.T) {
	conf := `client
dev tun
proto tcp
remote vpn.example.com 443
cipher AES-256-GCM
auth SHA256
auth-user-pass
remote-cert-tls server
<ca>
-----BEGIN CERTIFICATE-----
MIIB
-----END CERTIFICATE-----
</ca>
<tls-crypt>
-----BEGIN OpenVPN Static key V1-----
abcd
-----END OpenVPN Static key V1-----
</tls-crypt>`
	ps, errs := ParseText(conf)
	if len(ps) != 1 || len(errs) > 0 {
		t.Fatalf("%v %v", ps, errs)
	}
	o, _ := ParseOVPNOptions(ps[0].OVPN)
	if o.Remotes[0].Proto != "tcp" || o.Remotes[0].Port != 443 || !strings.Contains(o.CA, "BEGIN") || o.TLSCrypt == "" || !o.AuthUserPass {
		t.Fatalf("%+v", o)
	}
}

func TestClash(t *testing.T) {
	y := `proxies:
  - {name: a, type: vmess, server: a.com, port: 443, uuid: u, alterId: 0, cipher: auto, tls: true, network: ws, ws-opts: {path: /p, headers: {Host: a.com}}}
  - {name: b, type: ssr, server: b.com, port: 80, cipher: aes-256-cfb, password: p, protocol: origin, obfs: plain}
  - {name: c, type: hysteria2, server: c.com, port: 443, password: p}
`
	ps, errs := ParseText(y)
	if len(ps) != 3 || len(errs) > 0 {
		t.Fatalf("%d %v", len(ps), errs)
	}
	if ps[0].Path != "/p" || ps[1].Type != "ssr" {
		t.Fatalf("%+v", ps[0])
	}
}

func TestSpacesInName(t *testing.T) {
	ps, errs := ParseText("vless://u@a.com:443?security=tls#آلمان ریلیتی ۱ trojan://p@b.com:443#Turkey 2\nss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpwdw@5.6.7.8:8388#My SS")
	if len(ps) != 3 || len(errs) != 0 {
		t.Fatalf("%d %v", len(ps), errs)
	}
	if ps[0].Name != "آلمان ریلیتی ۱" || ps[1].Name != "Turkey 2" || ps[2].Name != "My SS" {
		t.Fatalf("%q %q %q", ps[0].Name, ps[1].Name, ps[2].Name)
	}
}
