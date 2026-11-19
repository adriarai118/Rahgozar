package builder

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"rahgozar/internal/model"
	"rahgozar/internal/parser"
)

// M is a JSON object.
type M = map[string]any

// Helper describes an auxiliary engine process (xray / mihomo) that exposes
// a local SOCKS port which sing-box chains to.
type Helper struct {
	Engine    string
	ProfileID string
	Port      int
	Config    []byte
	FileName  string
}

// Input for building the main runtime config.
type Input struct {
	Settings     model.Settings
	Profile      *model.Profile
	Resolve      func(id string) *model.Profile
	Mode         string // proxy | tun
	RulesDir     string // folder with .srs rule sets
	LearnedDir   string // folder with learned-*.json
	CacheFile    string
	ClashAddr    string // 127.0.0.1:port
	ClashSecret  string
	PortAlloc    func() int
	ExeName      string   // our own process name
	EngineExes   []string // helper exe names (bypass in TUN)
	HasRuleFiles map[string]bool
	OS           string // runtime.GOOS of the target ("android" changes TUN options)
	OwnPackage   string // android: our package, always outside the tunnel
}

// Result of a build.
type Result struct {
	SingBox M
	Helpers []Helper
}

func splitCSV(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// tlsOptions builds sing-box outbound TLS options.
func tlsOptions(p *model.Profile, st *model.Settings) M {
	if p.Security != "tls" && p.Security != "reality" {
		return nil
	}
	t := M{"enabled": true}
	sni := p.SNI
	if sni == "" && p.Host != "" && p.Security == "tls" {
		sni = strings.Split(p.Host, ",")[0]
	}
	if sni == "" && net.ParseIP(p.Server) == nil {
		sni = p.Server
	}
	if sni != "" {
		t["server_name"] = sni
	}
	if p.Insecure {
		t["insecure"] = true
	}
	if a := splitCSV(p.ALPN); len(a) > 0 {
		t["alpn"] = a
	}
	fp := p.Fingerprint
	if p.Security == "reality" && fp == "" {
		fp = "chrome"
	}
	if fp != "" && fp != "none" && p.Type != model.THysteria && p.Type != model.THysteria2 && p.Type != model.TTUIC {
		if fp == "random" || fp == "randomized" {
			fp = "randomized"
		}
		t["utls"] = M{"enabled": true, "fingerprint": fp}
	}
	if p.Security == "reality" {
		r := M{"enabled": true, "public_key": p.PublicKey}
		if p.ShortID != "" {
			r["short_id"] = p.ShortID
		}
		t["reality"] = r
	} else if st != nil && st.FragmentEnabled {
		t["record_fragment"] = true
	}
	return t
}

func transportOptions(p *model.Profile) (M, error) {
	switch p.Network {
	case "", "tcp", "raw":
		return nil, nil
	case "ws":
		tr := M{"type": "ws"}
		path := fixPercent(p.Path)
		if path == "" {
			path = "/"
		}
		// early data: /path?ed=2048
		if u, err := url.Parse(path); err == nil {
			if ed := u.Query().Get("ed"); ed != "" {
				if n, _ := strconv.Atoi(ed); n > 0 {
					tr["max_early_data"] = n
					tr["early_data_header_name"] = "Sec-WebSocket-Protocol"
					q := u.Query()
					q.Del("ed")
					u.RawQuery = q.Encode()
					path = u.String()
				}
			}
		}
		tr["path"] = fixPercent(path)
		if p.Host != "" {
			tr["headers"] = M{"Host": p.Host}
		}
		return tr, nil
	case "grpc":
		return M{"type": "grpc", "service_name": p.ServiceName}, nil
	case "http", "h2":
		tr := M{"type": "http"}
		if h := splitCSV(p.Host); len(h) > 0 {
			tr["host"] = h
		}
		if p.Path != "" {
			tr["path"] = fixPercent(p.Path)
		}
		return tr, nil
	case "httpupgrade":
		tr := M{"type": "httpupgrade", "path": fixPercent(firstOr(p.Path, "/"))}
		if p.Host != "" {
			tr["host"] = p.Host
		}
		return tr, nil
	case "quic":
		return M{"type": "quic"}, nil
	}
	return nil, fmt.Errorf("transport %q is not supported by sing-box", p.Network)
}

func firstOr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func parsePorts(s string) []string {
	var out []string
	for _, r := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ' ' }) {
		out = append(out, strings.ReplaceAll(r, "-", ":"))
	}
	return out
}

// SingBoxOutbound converts a profile to a sing-box outbound or endpoint.
// isEndpoint reports whether it belongs in "endpoints".
func SingBoxOutbound(p *model.Profile, tag string, st *model.Settings) (ob M, isEndpoint bool, err error) {
	if err := Validate(p); err != nil {
		return nil, false, err
	}
	base := func(t string) M {
		return M{"type": t, "tag": tag, "server": p.Server, "server_port": p.Port}
	}
	switch p.Type {
	case model.TVMess:
		ob = base("vmess")
		ob["uuid"] = p.UUID
		ob["security"] = firstOr(p.Method, "auto")
		if p.AlterID > 0 {
			ob["alter_id"] = p.AlterID
		}
		ob["packet_encoding"] = "xudp"
	case model.TVLESS:
		ob = base("vless")
		ob["uuid"] = p.UUID
		if p.Flow != "" {
			ob["flow"] = p.Flow
		}
		ob["packet_encoding"] = "xudp"
	case model.TTrojan:
		ob = base("trojan")
		ob["password"] = p.Password
	case model.TShadowsocks:
		ob = base("shadowsocks")
		ob["method"] = p.Method
		ob["password"] = p.Password
		if p.Plugin != "" {
			pl := p.Plugin
			if pl == "simple-obfs" || pl == "obfs" {
				pl = "obfs-local"
			}
			ob["plugin"] = pl
			ob["plugin_opts"] = fixPluginOpts(p.PluginOpts)
		}
	case model.TSocks:
		ob = base("socks")
		ob["version"] = "5"
		if p.Username != "" {
			ob["username"] = p.Username
			ob["password"] = p.Password
		}
	case model.THTTP:
		ob = base("http")
		if p.Username != "" {
			ob["username"] = p.Username
			ob["password"] = p.Password
		}
	case model.TSSH:
		ob = base("ssh")
		ob["user"] = firstOr(p.Username, "root")
		if p.Password != "" {
			ob["password"] = p.Password
		}
		if p.PrivateKey != "" {
			ob["private_key"] = lines(p.PrivateKey)
		}
	case model.THysteria:
		ob = base("hysteria")
		ob["up_mbps"] = max(p.UpMbps, 10)
		ob["down_mbps"] = max(p.DownMbps, 50)
		if p.HysteriaAuth != "" {
			ob["auth_str"] = p.HysteriaAuth
		}
		if p.ObfsPassword != "" {
			ob["obfs"] = p.ObfsPassword
		}
		if p.Ports != "" {
			ob["server_ports"] = parsePorts(p.Ports)
			if p.HopInterval != "" {
				ob["hop_interval"] = p.HopInterval
			}
		}
	case model.THysteria2:
		ob = base("hysteria2")
		ob["password"] = p.Password
		if p.UpMbps > 0 {
			ob["up_mbps"] = p.UpMbps
		}
		if p.DownMbps > 0 {
			ob["down_mbps"] = p.DownMbps
		}
		if p.Obfs != "" {
			ob["obfs"] = M{"type": p.Obfs, "password": p.ObfsPassword}
		}
		if p.Ports != "" {
			ob["server_ports"] = parsePorts(p.Ports)
			delete(ob, "server_port")
			if p.HopInterval != "" {
				ob["hop_interval"] = p.HopInterval
			}
		}
	case model.TTUIC:
		ob = base("tuic")
		ob["uuid"] = p.UUID
		ob["password"] = p.Password
		ob["congestion_control"] = firstOr(p.Congestion, "bbr")
		ob["udp_relay_mode"] = firstOr(p.UDPRelayMode, "native")
	case model.TAnyTLS:
		ob = base("anytls")
		ob["password"] = p.Password
	case model.TWireGuard:
		ep := M{"type": "wireguard", "tag": tag, "private_key": p.PrivateKey}
		addr := p.LocalAddress
		if len(addr) == 0 {
			addr = []string{"172.16.0.2/32"}
		}
		ep["address"] = addr
		if p.MTU > 0 {
			ep["mtu"] = p.MTU
		} else {
			ep["mtu"] = 1280
		}
		peer := M{"address": p.Server, "port": p.Port, "public_key": p.PeerPublicKey, "allowed_ips": []string{"0.0.0.0/0", "::/0"}}
		if p.PreSharedKey != "" {
			peer["pre_shared_key"] = p.PreSharedKey
		}
		if len(p.Reserved) == 3 {
			peer["reserved"] = p.Reserved
		}
		ep["peers"] = []M{peer}
		return ep, true, nil
	case model.TOpenVPN:
		ep, err := openVPNEndpoint(p, tag)
		return ep, true, err
	default:
		return nil, false, fmt.Errorf("protocol %s is not handled by sing-box", p.Type)
	}
	if t := tlsOptions(p, st); t != nil {
		ob["tls"] = t
	} else if p.Type == model.TTrojan || p.Type == model.THysteria || p.Type == model.THysteria2 || p.Type == model.TTUIC || p.Type == model.TAnyTLS {
		ob["tls"] = M{"enabled": true, "server_name": firstOr(p.SNI, p.Server), "insecure": p.Insecure}
	}
	switch p.Type {
	case model.TVMess, model.TVLESS, model.TTrojan:
		tr, err := transportOptions(p)
		if err != nil {
			return nil, false, err
		}
		if tr != nil {
			ob["transport"] = tr
		}
		if st != nil && st.MuxEnabled && p.Flow == "" && p.Network != "grpc" {
			ob["multiplex"] = M{"enabled": true, "protocol": "h2mux", "max_connections": 4, "padding": true}
		}
	}
	return ob, false, nil
}

func openVPNEndpoint(p *model.Profile, tag string) (M, error) {
	o, err := parser.ParseOVPNOptions(p.OVPN)
	if err != nil {
		return nil, err
	}
	if len(o.Remotes) == 0 {
		return nil, errors.New("openvpn: no remote")
	}
	ep := M{"type": "openvpn-client", "tag": tag}
	var servers []M
	for _, r := range o.Remotes {
		netw := r.Proto
		if p.OVPNNet != "" {
			netw = p.OVPNNet
		}
		servers = append(servers, M{"server": r.Host, "server_port": r.Port, "network": netw})
	}
	ep["servers"] = servers
	if o.RemoteRandom {
		ep["remote_random"] = true
	}
	if o.AuthUserPass || p.Username != "" {
		ep["username"] = p.Username
		ep["password"] = p.Password
	}
	tls := M{}
	if o.CA != "" {
		tls["certificate"] = lines(o.CA)
	}
	if o.Cert != "" {
		tls["client_certificate"] = lines(o.Cert)
	}
	if o.Key != "" {
		tls["client_key"] = lines(o.Key)
	}
	switch {
	case o.TLSCryptV2 != "":
		tls["control_wrap"] = M{"type": "tls_crypt_v2", "key": lines(o.TLSCryptV2)}
	case o.TLSCrypt != "":
		tls["control_wrap"] = M{"type": "tls_crypt", "key": lines(o.TLSCrypt)}
	case o.TLSAuth != "":
		cw := M{"type": "tls_auth", "key": lines(o.TLSAuth)}
		switch o.KeyDirection {
		case "1":
			cw["direction"] = "client"
		case "0":
			cw["direction"] = "server"
		}
		tls["control_wrap"] = cw
	}
	if o.RemoteCertTLS != "" {
		tls["remote_certificate_tls"] = o.RemoteCertTLS
	}
	if o.VerifyX509 != "" {
		tls["server_name"] = o.VerifyX509
		switch o.VerifyX509Type {
		case "name-prefix":
			tls["server_name_type"] = "name-prefix"
		case "subject":
			tls["server_name_type"] = "subject"
		default:
			tls["server_name_type"] = "name"
		}
	}
	if o.TLSVersionMin != "" {
		v := strings.Fields(o.TLSVersionMin)[0]
		if v == "1.0" || v == "1.1" || v == "1.2" || v == "1.3" {
			tls["version_min"] = v
		}
	}
	ep["tls"] = tls
	var dc []string
	for _, c := range o.DataCiphers {
		if c = strings.TrimSpace(c); c != "" {
			dc = append(dc, c)
		}
	}
	if o.Cipher != "" {
		found := false
		for _, c := range dc {
			if strings.EqualFold(c, o.Cipher) {
				found = true
			}
		}
		if !found {
			if len(dc) == 0 {
				dc = []string{"AES-256-GCM", "AES-128-GCM", "CHACHA20-POLY1305"}
			}
			ep["data_ciphers_fallback"] = o.Cipher
		}
	}
	if o.DataFallback != "" {
		ep["data_ciphers_fallback"] = o.DataFallback
	}
	if len(dc) > 0 {
		ep["data_ciphers"] = dc
	}
	if o.Auth != "" {
		ep["auth"] = o.Auth
	}
	if o.CompLZO != "" {
		ep["compression_lzo"] = o.CompLZO
		ep["allow_compression"] = "asym"
	}
	if o.Compress != "" {
		ep["compression"] = o.Compress
		ep["allow_compression"] = "asym"
	}
	if o.MssFix > 0 {
		ep["mss_fix"] = o.MssFix
	}
	if o.Fragment > 0 {
		ep["fragment"] = o.Fragment
	}
	// We route everything ourselves; never let the server change routing.
	ep["route_no_pull"] = false
	ep["pull_filters"] = []M{{"action": "ignore", "text": "redirect-gateway"}, {"action": "ignore", "text": "route "}}
	return ep, nil
}

// dnsServer converts "https://1.1.1.1/dns-query", "tls://...", "8.8.8.8",
// "local" etc. to a new-format sing-box DNS server.
func dnsServer(addr, tag, detour string) M {
	addr = strings.TrimSpace(addr)
	if addr == "" || addr == "local" || addr == "system" {
		return M{"type": "local", "tag": tag}
	}
	s := M{"tag": tag}
	if detour != "" {
		s["detour"] = detour
	}
	if !strings.Contains(addr, "://") {
		s["type"] = "udp"
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		s["server"] = host
		if port != "" {
			s["server_port"], _ = strconv.Atoi(port)
		}
		return s
	}
	u, err := url.Parse(addr)
	if err != nil {
		return M{"type": "local", "tag": tag}
	}
	switch u.Scheme {
	case "https", "h3":
		if u.Scheme == "h3" {
			s["type"] = "h3"
		} else {
			s["type"] = "https"
		}
		if u.Path != "" && u.Path != "/dns-query" {
			s["path"] = u.Path
		}
	case "tls", "dot":
		s["type"] = "tls"
	case "quic", "doq":
		s["type"] = "quic"
	case "tcp":
		s["type"] = "tcp"
	default:
		s["type"] = "udp"
	}
	s["server"] = u.Hostname()
	if p := u.Port(); p != "" {
		s["server_port"], _ = strconv.Atoi(p)
	}
	return s
}

// BuildChain builds the outbound(s) for a chain profile. The last hop is
// tagged "proxy".
//
// Only the first hop is visible to the local network, so it may run on a
// helper engine (Mihomo: WireGuard junk packets/AmneziaWG, SSR; Xray:
// XHTTP, mKCP, …) behind a local SOCKS port; later hops travel inside it and
// must be sing-box protocols. A plain WireGuard hop there just uses
// sing-box's own WireGuard.
func buildChain(in *Input, p *model.Profile) (obs []M, eps []M, helpers []Helper, err error) {
	if len(p.Chain) < 2 {
		return nil, nil, nil, errors.New("chain needs at least 2 hops")
	}
	prev := ""
	for i, id := range p.Chain {
		hop := in.Resolve(id)
		if hop == nil {
			return nil, nil, nil, fmt.Errorf("chain hop %d not found", i+1)
		}
		tag := fmt.Sprintf("hop-%d", i)
		if i == len(p.Chain)-1 {
			tag = "proxy"
		}
		if hop.Type == model.TChain || hop.IsRAS() {
			return nil, nil, nil, fmt.Errorf("«%s» را نمی‌شود داخل زنجیره گذاشت", hop.Name)
		}
		eng := EngineFor(hop)
		if eng != model.EngineSingBox && i > 0 && hop.Type == model.TWireGuard && !NeedsAWG(hop) {
			eng = model.EngineSingBox // inside the tunnel junk packets aren't needed
		}
		if eng == model.EngineXray || eng == model.EngineMihomo {
			if i > 0 {
				return nil, nil, nil, fmt.Errorf("«%s» (موتور %s) فقط می‌تواند اولین حلقه زنجیره باشد", hop.Name, eng)
			}
			port := in.PortAlloc()
			var cfg []byte
			fname := "xray-chain.json"
			if eng == model.EngineXray {
				cfg, err = XrayConfig(hop, port, in.Settings.LogLevel)
			} else {
				cfg, err = MihomoConfig(hop, port, in.Settings.LogLevel)
				fname = "mihomo-chain.yaml"
			}
			if err != nil {
				return nil, nil, nil, err
			}
			helpers = append(helpers, Helper{Engine: eng, ProfileID: hop.ID, Port: port, Config: cfg, FileName: fname})
			obs = append(obs, M{"type": "socks", "tag": tag, "server": "127.0.0.1", "server_port": port, "version": "5"})
			prev = tag
			continue
		}
		if eng != model.EngineSingBox {
			return nil, nil, nil, fmt.Errorf("«%s» را نمی‌شود داخل زنجیره گذاشت", hop.Name)
		}
		ob, isEp, err := SingBoxOutbound(hop, tag, &in.Settings)
		if err != nil {
			return nil, nil, nil, err
		}
		if prev != "" {
			ob["detour"] = prev
		}
		prev = tag
		if isEp {
			eps = append(eps, ob)
		} else {
			obs = append(obs, ob)
		}
	}
	return obs, eps, helpers, nil
}
