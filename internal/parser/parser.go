// Package parser converts share links, subscription bodies, WireGuard .conf,
// OpenVPN .ovpn and Clash YAML into Rahgozar profiles.
package parser

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rahgozar/internal/model"
)

// DecodeB64 decodes standard/url-safe base64 with or without padding.
func DecodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, " ", "")
	encs := []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}
	t := strings.TrimRight(s, "=")
	for _, e := range encs {
		in := s
		if e == base64.RawStdEncoding || e == base64.RawURLEncoding {
			in = t
		}
		if b, err := e.DecodeString(in); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64")
}

func looksB64(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 8 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=' || c == '-' || c == '_' || c == '\n' || c == '\r') {
			return false
		}
	}
	return true
}

// ParseText parses any text blob: a single link, many links, a base64
// subscription, a WireGuard config, an OpenVPN config, or Clash YAML.
func ParseText(text string) ([]*model.Profile, []error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\uFEFF"))
	if text == "" {
		return nil, []error{errors.New("empty input")}
	}
	// Whole-document formats first.
	if isOVPN(text) {
		p, err := ParseOVPN(text, "")
		if err != nil {
			return nil, []error{err}
		}
		return []*model.Profile{p}, nil
	}
	if isWGConf(text) {
		p, err := ParseWGConf(text, "")
		if err != nil {
			return nil, []error{err}
		}
		return []*model.Profile{p}, nil
	}
	if isClashYAML(text) {
		return ParseClashYAML(text)
	}
	if strings.HasPrefix(text, "{") {
		if ps, errs := ParseSingBoxJSON(text); len(ps) > 0 {
			return ps, errs
		}
	}
	// Base64 subscription body.
	if !strings.Contains(text, "://") && looksB64(text) {
		if b, err := DecodeB64(text); err == nil {
			return ParseText(string(b))
		}
	}
	var out []*model.Profile
	var errs []error
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ' ' || r == '\t' }) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if !strings.Contains(line, "://") {
			continue
		}
		p, err := ParseLink(line)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", shorten(line), err))
			continue
		}
		out = append(out, p)
	}
	return out, errs
}

func shorten(s string) string {
	if len(s) > 48 {
		return s[:48] + "…"
	}
	return s
}

// ParseLink parses a single share link.
func ParseLink(link string) (*model.Profile, error) {
	link = strings.TrimSpace(link)
	i := strings.Index(link, "://")
	if i < 0 {
		return nil, errors.New("not a link")
	}
	scheme := strings.ToLower(link[:i])
	var p *model.Profile
	var err error
	switch scheme {
	case "vmess":
		p, err = parseVMess(link)
	case "vless":
		p, err = parseStdURL(link, model.TVLESS)
	case "trojan", "trojan-go":
		p, err = parseStdURL(link, model.TTrojan)
	case "ss":
		p, err = parseSS(link)
	case "ssr":
		p, err = parseSSR(link)
	case "socks", "socks5", "socks4", "socks5h":
		p, err = parseSimpleProxy(link, model.TSocks)
	case "http", "https":
		p, err = parseSimpleProxy(link, model.THTTP)
	case "hysteria", "hy":
		p, err = parseHysteria(link)
	case "hysteria2", "hy2":
		p, err = parseHysteria2(link)
	case "tuic":
		p, err = parseTUIC(link)
	case "wireguard", "wg":
		p, err = parseWGLink(link)
	case "anytls":
		p, err = parseStdURL(link, model.TAnyTLS)
	case "ssh":
		p, err = parseSimpleProxy(link, model.TSSH)
	case "pptp", "l2tp", "sstp", "ikev2":
		p, err = parseRASLink(link, scheme)
	default:
		return nil, fmt.Errorf("unsupported scheme %q", scheme)
	}
	if err != nil {
		return nil, err
	}
	if p.ID == "" {
		p.ID = model.NewID()
	}
	p.Raw = link
	p.Created = time.Now().Unix()
	if p.Name == "" {
		p.Name = fmt.Sprintf("%s %s:%d", strings.ToUpper(p.Type), p.Server, p.Port)
	}
	if p.Server == "" {
		return nil, errors.New("missing server address")
	}
	return p, nil
}

func atoi(s string) int {
	s = strings.TrimSpace(s)
	n, _ := strconv.Atoi(s)
	return n
}

func truthy(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}

func fragmentName(u *url.URL) string {
	n := u.Fragment
	if dec, err := url.PathUnescape(n); err == nil {
		n = dec
	}
	return strings.TrimSpace(n)
}

func hostPort(u *url.URL) (string, int) {
	h := u.Hostname()
	p := atoi(u.Port())
	return h, p
}

// ---------- VMess ----------

func anyStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return ""
	}
	return fmt.Sprint(v)
}

func parseVMess(link string) (*model.Profile, error) {
	body := strings.TrimPrefix(link[len("vmess://"):], "")
	if strings.Contains(body, "@") { // vmess in standard URI form
		return parseStdURL(link, model.TVMess)
	}
	if k := strings.Index(body, "#"); k >= 0 {
		body = body[:k]
	}
	raw, err := DecodeB64(body)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("vmess json: %w", err)
	}
	p := &model.Profile{Type: model.TVMess}
	p.Name = anyStr(m["ps"])
	p.Server = anyStr(m["add"])
	p.Port = atoi(anyStr(m["port"]))
	p.UUID = anyStr(m["id"])
	p.AlterID = atoi(anyStr(m["aid"]))
	p.Method = anyStr(m["scy"])
	if p.Method == "" {
		p.Method = "auto"
	}
	p.Network = strings.ToLower(anyStr(m["net"]))
	p.HeaderType = anyStr(m["type"])
	p.Host = anyStr(m["host"])
	p.Path = anyStr(m["path"])
	tls := strings.ToLower(anyStr(m["tls"]))
	if tls == "tls" || tls == "reality" {
		p.Security = tls
	}
	p.SNI = anyStr(m["sni"])
	p.ALPN = anyStr(m["alpn"])
	p.Fingerprint = anyStr(m["fp"])
	p.Insecure = truthy(anyStr(m["allowInsecure"])) || truthy(anyStr(m["insecure"]))
	p.PublicKey = anyStr(m["pbk"])
	p.ShortID = anyStr(m["sid"])
	switch p.Network {
	case "grpc":
		p.ServiceName = p.Path
		p.XMode = p.HeaderType
		p.Path = ""
	case "h2":
		p.Network = "http"
	case "splithttp":
		p.Network = "xhttp"
	case "xhttp":
		p.XMode = anyStr(m["mode"])
		if p.XMode == "" && p.HeaderType != "none" {
			p.XMode = p.HeaderType
		}
	case "", "raw":
		p.Network = "tcp"
	}
	if p.Network == "tcp" && p.HeaderType != "http" {
		p.HeaderType = ""
	}
	return p, nil
}

// ---------- VLESS / Trojan / AnyTLS / URI-VMess ----------

func parseStdURL(link string, typ string) (*model.Profile, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	p := &model.Profile{Type: typ, Name: fragmentName(u)}
	p.Server, p.Port = hostPort(u)
	user := ""
	if u.User != nil {
		user = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			user = user + ":" + pw
		}
	}
	if dec, err := url.PathUnescape(user); err == nil {
		user = dec
	}
	switch typ {
	case model.TVLESS, model.TVMess:
		p.UUID = user
		p.Method = q.Get("encryption")
		if typ == model.TVMess {
			p.Method = firstNonEmpty(q.Get("security_cipher"), "auto")
		}
	case model.TTrojan, model.TAnyTLS:
		p.Password = user
	}
	p.Flow = q.Get("flow")
	p.Network = strings.ToLower(firstNonEmpty(q.Get("type"), "tcp"))
	sec := strings.ToLower(q.Get("security"))
	if typ == model.TTrojan || typ == model.TAnyTLS {
		if sec == "" {
			sec = "tls"
		}
	}
	if sec == "none" {
		sec = ""
	}
	if sec == "xtls" {
		sec = "tls"
	}
	p.Security = sec
	p.SNI = firstNonEmpty(q.Get("sni"), q.Get("peer"))
	p.ALPN = q.Get("alpn")
	p.Fingerprint = q.Get("fp")
	p.Insecure = truthy(q.Get("allowInsecure")) || truthy(q.Get("insecure")) || truthy(q.Get("allow_insecure"))
	p.PublicKey = q.Get("pbk")
	p.ShortID = q.Get("sid")
	p.SpiderX = q.Get("spx")
	p.ECH = q.Get("ech")
	p.Host = q.Get("host")
	p.Path = q.Get("path")
	p.HeaderType = q.Get("headerType")
	switch p.Network {
	case "grpc":
		p.ServiceName = firstNonEmpty(q.Get("serviceName"), q.Get("path"))
		p.XMode = q.Get("mode")
		p.Path = ""
	case "h2":
		p.Network = "http"
	case "splithttp":
		p.Network = "xhttp"
		p.XMode = q.Get("mode")
	case "xhttp":
		p.XMode = q.Get("mode")
		p.XExtra = q.Get("extra")
	case "kcp", "mkcp":
		p.Network = "kcp"
		p.Path = q.Get("seed")
	case "raw":
		p.Network = "tcp"
	}
	if p.Network == "tcp" && p.HeaderType != "http" {
		p.HeaderType = ""
	}
	if p.Port == 0 {
		p.Port = 443
	}
	return p, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ---------- Shadowsocks ----------

func parseSS(link string) (*model.Profile, error) {
	body := link[len("ss://"):]
	name := ""
	if k := strings.Index(body, "#"); k >= 0 {
		name, _ = url.PathUnescape(body[k+1:])
		body = body[:k]
	}
	query := ""
	if k := strings.Index(body, "?"); k >= 0 {
		query = body[k+1:]
		body = body[:k]
	}
	body = strings.TrimSuffix(body, "/")
	p := &model.Profile{Type: model.TShadowsocks, Name: strings.TrimSpace(name)}
	var userinfo, hostport string
	if at := strings.LastIndex(body, "@"); at >= 0 {
		userinfo, hostport = body[:at], body[at+1:]
		if dec, err := url.PathUnescape(userinfo); err == nil {
			userinfo = dec
		}
		if !strings.Contains(userinfo, ":") {
			b, err := DecodeB64(userinfo)
			if err != nil {
				return nil, err
			}
			userinfo = string(b)
		}
	} else {
		b, err := DecodeB64(body)
		if err != nil {
			return nil, err
		}
		s := string(b)
		at := strings.LastIndex(s, "@")
		if at < 0 {
			return nil, errors.New("bad ss link")
		}
		userinfo, hostport = s[:at], s[at+1:]
	}
	ci := strings.Index(userinfo, ":")
	if ci < 0 {
		return nil, errors.New("bad ss userinfo")
	}
	p.Method, p.Password = userinfo[:ci], userinfo[ci+1:]
	h, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return nil, err
	}
	p.Server, p.Port = h, atoi(port)
	if query != "" {
		q, _ := url.ParseQuery(query)
		if pl := q.Get("plugin"); pl != "" {
			parts := strings.SplitN(pl, ";", 2)
			p.Plugin = parts[0]
			if len(parts) > 1 {
				p.PluginOpts = parts[1]
			}
		}
	}
	return p, nil
}

// ---------- ShadowsocksR ----------

func parseSSR(link string) (*model.Profile, error) {
	b, err := DecodeB64(link[len("ssr://"):])
	if err != nil {
		return nil, err
	}
	s := string(b)
	main, params := s, ""
	if k := strings.Index(s, "/?"); k >= 0 {
		main, params = s[:k], s[k+2:]
	} else if k := strings.Index(s, "?"); k >= 0 {
		main, params = s[:k], s[k+1:]
	}
	// host may be IPv6 with colons; parse from the right.
	parts := strings.Split(main, ":")
	if len(parts) < 6 {
		return nil, errors.New("bad ssr link")
	}
	n := len(parts)
	p := &model.Profile{Type: model.TSSR}
	p.Server = strings.Join(parts[:n-5], ":")
	p.Port = atoi(parts[n-5])
	p.Protocol = parts[n-4]
	p.Method = parts[n-3]
	p.Obfs = parts[n-2]
	pw, _ := DecodeB64(parts[n-1])
	p.Password = string(pw)
	q, _ := url.ParseQuery(params)
	dec := func(k string) string {
		v := q.Get(k)
		if v == "" {
			return ""
		}
		d, err := DecodeB64(v)
		if err != nil {
			return v
		}
		return string(d)
	}
	p.ObfsParam = dec("obfsparam")
	p.ProtocolParam = dec("protoparam")
	p.Name = dec("remarks")
	return p, nil
}

// ---------- SOCKS / HTTP / SSH ----------

func parseSimpleProxy(link string, typ string) (*model.Profile, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	if typ == model.THTTP && u.Path != "" && u.Path != "/" {
		return nil, errors.New("looks like a web URL, not a proxy")
	}
	p := &model.Profile{Type: typ, Name: fragmentName(u)}
	p.Server, p.Port = hostPort(u)
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
		if p.Password == "" && typ == model.TSocks {
			if b, err := DecodeB64(p.Username); err == nil && strings.Contains(string(b), ":") {
				kv := strings.SplitN(string(b), ":", 2)
				p.Username, p.Password = kv[0], kv[1]
			}
		}
	}
	if typ == model.THTTP && strings.HasPrefix(strings.ToLower(link), "https://") {
		p.Security = "tls"
		p.SNI = u.Query().Get("sni")
	}
	if p.Port == 0 {
		switch typ {
		case model.TSocks:
			p.Port = 1080
		case model.TSSH:
			p.Port = 22
		case model.THTTP:
			if p.Security == "tls" {
				p.Port = 443
			} else {
				p.Port = 8080
			}
		}
	}
	return p, nil
}

// ---------- Hysteria (v1) ----------

func parseHysteria(link string) (*model.Profile, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	p := &model.Profile{Type: model.THysteria, Name: fragmentName(u)}
	p.Server, p.Port = hostPort(u)
	p.HysteriaAuth = firstNonEmpty(q.Get("auth"), q.Get("auth_str"))
	p.HysteriaProto = q.Get("protocol")
	p.SNI = firstNonEmpty(q.Get("peer"), q.Get("sni"))
	p.Insecure = truthy(q.Get("insecure"))
	p.UpMbps = atoi(firstNonEmpty(q.Get("upmbps"), q.Get("up")))
	p.DownMbps = atoi(firstNonEmpty(q.Get("downmbps"), q.Get("down")))
	p.ALPN = q.Get("alpn")
	p.ObfsPassword = firstNonEmpty(q.Get("obfsParam"), q.Get("obfs-password"))
	p.Ports = q.Get("mport")
	p.Security = "tls"
	return p, nil
}

// ---------- Hysteria2 ----------

func parseHysteria2(link string) (*model.Profile, error) {
	// hy2 links may contain port ranges in host (host:1000-2000) which url.Parse rejects.
	l := link
	var ports string
	if i := strings.Index(l, "://"); i >= 0 {
		rest := l[i+3:]
		end := len(rest)
		for _, c := range []string{"/", "?", "#"} {
			if k := strings.Index(rest, c); k >= 0 && k < end {
				end = k
			}
		}
		auth := rest[:end]
		at := strings.LastIndex(auth, "@")
		hp := auth[at+1:]
		if c := strings.LastIndex(hp, ":"); c >= 0 && (strings.ContainsAny(hp[c+1:], "-,")) {
			ports = hp[c+1:]
			first := strings.FieldsFunc(ports, func(r rune) bool { return r == '-' || r == ',' })[0]
			hp = hp[:c+1] + first
			l = l[:i+3] + auth[:at+1] + hp + rest[end:]
		}
	}
	u, err := url.Parse(l)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	p := &model.Profile{Type: model.THysteria2, Name: fragmentName(u)}
	p.Server, p.Port = hostPort(u)
	if p.Port == 0 {
		p.Port = 443
	}
	if u.User != nil {
		p.Password = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			p.Password = p.Password + ":" + pw
		}
	}
	p.SNI = q.Get("sni")
	p.Insecure = truthy(q.Get("insecure"))
	if q.Get("obfs") == "salamander" || q.Get("obfs-password") != "" {
		p.Obfs = "salamander"
		p.ObfsPassword = q.Get("obfs-password")
	}
	p.Ports = firstNonEmpty(q.Get("mport"), ports)
	p.UpMbps = atoi(q.Get("up"))
	p.DownMbps = atoi(q.Get("down"))
	p.ALPN = q.Get("alpn")
	p.Fingerprint = q.Get("pinSHA256")
	p.Security = "tls"
	return p, nil
}

// ---------- TUIC ----------

func parseTUIC(link string) (*model.Profile, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	p := &model.Profile{Type: model.TTUIC, Name: fragmentName(u)}
	p.Server, p.Port = hostPort(u)
	if u.User != nil {
		p.UUID = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	p.Congestion = firstNonEmpty(q.Get("congestion_control"), q.Get("congestion"), "bbr")
	p.UDPRelayMode = firstNonEmpty(q.Get("udp_relay_mode"), "native")
	p.ALPN = firstNonEmpty(q.Get("alpn"), "h3")
	p.SNI = q.Get("sni")
	p.Insecure = truthy(q.Get("allow_insecure")) || truthy(q.Get("insecure"))
	p.Security = "tls"
	return p, nil
}

// ---------- WireGuard link ----------

func splitList(s string) []string {
	var out []string
	for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func parseReserved(s string) []int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.Contains(s, ",") {
		var r []int
		for _, x := range splitList(s) {
			r = append(r, atoi(x))
		}
		return r
	}
	if b, err := DecodeB64(s); err == nil && len(b) == 3 {
		return []int{int(b[0]), int(b[1]), int(b[2])}
	}
	return nil
}

func parseWGLink(link string) (*model.Profile, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	p := &model.Profile{Type: model.TWireGuard, Name: fragmentName(u)}
	p.Server, p.Port = hostPort(u)
	if u.User != nil {
		p.PrivateKey, _ = url.PathUnescape(u.User.Username())
	}
	p.PrivateKey = firstNonEmpty(p.PrivateKey, q.Get("privatekey"), q.Get("secretKey"))
	p.PeerPublicKey = firstNonEmpty(q.Get("publickey"), q.Get("publicKey"), q.Get("peer_public_key"))
	p.PreSharedKey = firstNonEmpty(q.Get("presharedkey"), q.Get("preSharedKey"), q.Get("psk"))
	p.LocalAddress = splitList(firstNonEmpty(q.Get("address"), q.Get("ip")))
	p.Reserved = parseReserved(q.Get("reserved"))
	p.MTU = atoi(q.Get("mtu"))
	if p.Port == 0 {
		p.Port = 51820
	}
	return p, nil
}

// ---------- RAS convenience links ----------

func parseRASLink(link, scheme string) (*model.Profile, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	p := &model.Profile{Type: scheme, Name: fragmentName(u), Engine: model.EngineWindows}
	p.Server, p.Port = hostPort(u)
	if u.User != nil {
		p.Username = u.User.Username()
		p.Password, _ = u.User.Password()
	}
	p.PSK = u.Query().Get("psk")
	return p, nil
}
