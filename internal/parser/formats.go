package parser

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"rahgozar/internal/model"
)

// ---------- OpenVPN ----------

func isOVPN(t string) bool {
	return regexp.MustCompile(`(?m)^\s*remote\s+\S+`).MatchString(t) &&
		(strings.Contains(t, "<ca>") || strings.Contains(t, "ca ") || regexp.MustCompile(`(?m)^\s*client\s*$`).MatchString(t))
}

// ParseOVPN parses an OpenVPN client config. The raw text is kept on the profile
// and translated to a sing-box openvpn-client endpoint at connect time.
func ParseOVPN(text, name string) (*model.Profile, error) {
	o, err := ParseOVPNOptions(text)
	if err != nil {
		return nil, err
	}
	if len(o.Remotes) == 0 {
		return nil, errors.New("ovpn: no remote")
	}
	p := &model.Profile{ID: model.NewID(), Type: model.TOpenVPN, OVPN: text, Created: time.Now().Unix()}
	p.Server, p.Port = o.Remotes[0].Host, o.Remotes[0].Port
	p.OVPNNet = o.Remotes[0].Proto
	p.Name = name
	if p.Name == "" {
		p.Name = fmt.Sprintf("OpenVPN %s:%d/%s", p.Server, p.Port, p.OVPNNet)
	}
	return p, nil
}

// OVPNRemote is one "remote" line.
type OVPNRemote struct {
	Host  string
	Port  int
	Proto string
}

// OVPNOptions is the subset of OpenVPN directives Rahgozar understands.
type OVPNOptions struct {
	Remotes        []OVPNRemote
	Proto          string
	CA, Cert, Key  string
	TLSAuth        string
	TLSCrypt       string
	TLSCryptV2     string
	KeyDirection   string
	Cipher         string
	DataCiphers    []string
	DataFallback   string
	Auth           string
	AuthUserPass   bool
	RemoteCertTLS  string
	VerifyX509     string
	VerifyX509Type string
	CompLZO        string
	Compress       string
	TLSVersionMin  string
	RemoteRandom   bool
	MssFix         int
	Fragment       int
	Inline         map[string]string
}

// ParseOVPNOptions extracts directives and inline blocks.
func ParseOVPNOptions(text string) (*OVPNOptions, error) {
	o := &OVPNOptions{Proto: "udp", Inline: map[string]string{}}
	port := 1194
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	var block string
	var buf strings.Builder
	type pending struct{ host, port, proto string }
	var rem []pending
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if block != "" {
			if strings.EqualFold(line, "</"+block+">") {
				o.Inline[block] = strings.TrimSpace(buf.String()) + "\n"
				block = ""
				buf.Reset()
				continue
			}
			buf.WriteString(line + "\n")
			continue
		}
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") && !strings.HasPrefix(line, "</") {
			block = strings.ToLower(strings.Trim(line, "<>"))
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		f := strings.Fields(line)
		arg := func(i int) string {
			if i < len(f) {
				return strings.Trim(f[i], `"`)
			}
			return ""
		}
		switch strings.ToLower(f[0]) {
		case "remote":
			rem = append(rem, pending{arg(1), arg(2), arg(3)})
		case "port":
			port = atoi(arg(1))
		case "proto":
			o.Proto = arg(1)
		case "cipher":
			o.Cipher = arg(1)
		case "data-ciphers", "ncp-ciphers":
			o.DataCiphers = strings.Split(arg(1), ":")
		case "data-ciphers-fallback":
			o.DataFallback = arg(1)
		case "auth":
			o.Auth = arg(1)
		case "auth-user-pass":
			o.AuthUserPass = true
		case "key-direction":
			o.KeyDirection = arg(1)
		case "tls-auth":
			if arg(2) != "" {
				o.KeyDirection = arg(2)
			}
		case "remote-cert-tls":
			o.RemoteCertTLS = arg(1)
		case "verify-x509-name":
			o.VerifyX509, o.VerifyX509Type = arg(1), arg(2)
		case "comp-lzo":
			o.CompLZO = firstNonEmpty(arg(1), "adaptive")
		case "compress":
			o.Compress = firstNonEmpty(arg(1), "stub")
		case "tls-version-min":
			o.TLSVersionMin = arg(1)
		case "remote-random":
			o.RemoteRandom = true
		case "mssfix":
			o.MssFix = atoi(arg(1))
		case "fragment":
			o.Fragment = atoi(arg(1))
		}
	}
	for _, r := range rem {
		pr := r.proto
		if pr == "" {
			pr = o.Proto
		}
		pt := atoi(r.port)
		if pt == 0 {
			pt = port
		}
		o.Remotes = append(o.Remotes, OVPNRemote{Host: r.host, Port: pt, Proto: normOVPNProto(pr)})
	}
	o.Proto = normOVPNProto(o.Proto)
	o.CA = o.Inline["ca"]
	o.Cert = o.Inline["cert"]
	o.Key = o.Inline["key"]
	o.TLSAuth = o.Inline["tls-auth"]
	o.TLSCrypt = o.Inline["tls-crypt"]
	o.TLSCryptV2 = o.Inline["tls-crypt-v2"]
	return o, nil
}

func normOVPNProto(p string) string {
	p = strings.ToLower(p)
	if strings.HasPrefix(p, "tcp") {
		return "tcp"
	}
	return "udp"
}

// ---------- WireGuard .conf ----------

func isWGConf(t string) bool {
	return strings.Contains(t, "[Interface]") && strings.Contains(t, "[Peer]")
}

// ParseWGConf parses a standard WireGuard configuration file.
func ParseWGConf(text, name string) (*model.Profile, error) {
	p := &model.Profile{ID: model.NewID(), Type: model.TWireGuard, Created: time.Now().Unix()}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		// WireSock-only directives are hidden from other clients as "#@ws:"
		if strings.HasPrefix(strings.ToLower(line), "#@ws:") {
			line = strings.TrimSpace(line[5:])
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		v := strings.TrimSpace(kv[1])
		switch section + "." + k {
		case "interface.privatekey":
			p.PrivateKey = v
		case "interface.address":
			p.LocalAddress = append(p.LocalAddress, splitList(v)...)
		case "interface.mtu":
			p.MTU = atoi(v)
		case "peer.publickey":
			p.PeerPublicKey = v
		case "peer.presharedkey":
			p.PreSharedKey = v
		case "peer.endpoint":
			h, pt, err := net.SplitHostPort(v)
			if err == nil {
				p.Server, p.Port = h, atoi(pt)
			}
		case "peer.reserved", "interface.reserved":
			p.Reserved = parseReserved(v)
		default:
			if isAWGKey(k) {
				if p.AWG == nil {
					p.AWG = map[string]string{}
				}
				p.AWG[k] = v
			}
		}
	}
	if p.PrivateKey == "" || p.PeerPublicKey == "" || p.Server == "" {
		return nil, errors.New("wireguard: incomplete config")
	}
	p.Name = name
	if p.Name == "" {
		p.Name = fmt.Sprintf("WireGuard %s:%d", p.Server, p.Port)
	}
	return p, nil
}

// isAWGKey reports AmneziaWG obfuscation parameters.
func isAWGKey(k string) bool {
	switch k {
	case "jc", "jmin", "jmax", "s1", "s2", "s3", "s4", "h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5",
		"j1", "j2", "j3", "itime", // AWG 1.5
		// AWG 3
		"headerprotectionkey", "contentpaddingaddition", "rekeyaftertime", "rekeytimeout",
		"rejectaftertime", "keepalivetimeout", "maxhandshakeattempts", "randomtrailers", "disablecookies":
		return true
	}
	return false
}

// ---------- Clash / Mihomo YAML ----------

func isClashYAML(t string) bool {
	return regexp.MustCompile(`(?m)^proxies:\s*$`).MatchString(t)
}

func ystr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch t := v.(type) {
			case string:
				return t
			case int:
				return strconv.Itoa(t)
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			case bool:
				return strconv.FormatBool(t)
			}
		}
	}
	return ""
}

func ymap(m map[string]any, k string) map[string]any {
	if v, ok := m[k].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func ybool(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

// ParseClashYAML parses the proxies section of a Clash/Mihomo config.
func ParseClashYAML(text string) ([]*model.Profile, []error) {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, []error{err}
	}
	var out []*model.Profile
	var errs []error
	for _, m := range doc.Proxies {
		p := &model.Profile{ID: model.NewID(), Name: ystr(m, "name"), Server: ystr(m, "server"), Port: atoi(ystr(m, "port")), Created: time.Now().Unix()}
		typ := ystr(m, "type")
		p.UUID = ystr(m, "uuid")
		p.Password = ystr(m, "password")
		p.Username = ystr(m, "username")
		p.SNI = ystr(m, "servername", "sni")
		p.Insecure = ybool(m, "skip-cert-verify")
		p.Fingerprint = ystr(m, "client-fingerprint")
		if ybool(m, "tls") {
			p.Security = "tls"
		}
		if alpn, ok := m["alpn"].([]any); ok {
			var a []string
			for _, x := range alpn {
				a = append(a, fmt.Sprint(x))
			}
			p.ALPN = strings.Join(a, ",")
		}
		p.Network = firstNonEmpty(ystr(m, "network"), "tcp")
		ws := ymap(m, "ws-opts")
		p.Path = ystr(ws, "path")
		if h := ymap(ws, "headers"); len(h) > 0 {
			p.Host = ystr(h, "Host", "host")
		}
		if g := ymap(m, "grpc-opts"); len(g) > 0 {
			p.ServiceName = ystr(g, "grpc-service-name")
		}
		if h2 := ymap(m, "h2-opts"); len(h2) > 0 {
			p.Path = ystr(h2, "path")
			p.Network = "http"
		}
		if r := ymap(m, "reality-opts"); len(r) > 0 {
			p.Security = "reality"
			p.PublicKey = ystr(r, "public-key")
			p.ShortID = ystr(r, "short-id")
		}
		switch typ {
		case "vmess":
			p.Type = model.TVMess
			p.AlterID = atoi(ystr(m, "alterId"))
			p.Method = firstNonEmpty(ystr(m, "cipher"), "auto")
		case "vless":
			p.Type = model.TVLESS
			p.Flow = ystr(m, "flow")
		case "trojan":
			p.Type = model.TTrojan
			if p.Security == "" {
				p.Security = "tls"
			}
		case "ss":
			p.Type = model.TShadowsocks
			p.Method = ystr(m, "cipher")
			if pl := ystr(m, "plugin"); pl != "" {
				po := ymap(m, "plugin-opts")
				if pl == "obfs" {
					p.Plugin = "obfs-local"
					p.PluginOpts = "obfs=" + ystr(po, "mode") + ";obfs-host=" + ystr(po, "host")
				} else if pl == "v2ray-plugin" {
					p.Plugin = "v2ray-plugin"
					opts := "mode=" + ystr(po, "mode") + ";host=" + ystr(po, "host") + ";path=" + ystr(po, "path")
					if ybool(po, "tls") {
						opts += ";tls"
					}
					p.PluginOpts = opts
				}
			}
		case "ssr":
			p.Type = model.TSSR
			p.Method = ystr(m, "cipher")
			p.Protocol = ystr(m, "protocol")
			p.ProtocolParam = ystr(m, "protocol-param")
			p.Obfs = ystr(m, "obfs")
			p.ObfsParam = ystr(m, "obfs-param")
		case "socks5":
			p.Type = model.TSocks
		case "http":
			p.Type = model.THTTP
		case "hysteria":
			p.Type = model.THysteria
			p.HysteriaAuth = ystr(m, "auth-str", "auth_str", "auth")
			p.UpMbps = atoi(strings.Fields(ystr(m, "up") + " 0")[0])
			p.DownMbps = atoi(strings.Fields(ystr(m, "down") + " 0")[0])
			p.ObfsPassword = ystr(m, "obfs")
			p.Ports = ystr(m, "ports")
			p.Security = "tls"
		case "hysteria2":
			p.Type = model.THysteria2
			if o := ystr(m, "obfs"); o != "" {
				p.Obfs = o
				p.ObfsPassword = ystr(m, "obfs-password")
			}
			p.Ports = ystr(m, "ports")
			p.Security = "tls"
		case "tuic":
			p.Type = model.TTUIC
			p.Congestion = firstNonEmpty(ystr(m, "congestion-controller"), "bbr")
			p.UDPRelayMode = firstNonEmpty(ystr(m, "udp-relay-mode"), "native")
			p.Security = "tls"
		case "wireguard":
			p.Type = model.TWireGuard
			p.PrivateKey = ystr(m, "private-key")
			p.PeerPublicKey = ystr(m, "public-key")
			p.PreSharedKey = ystr(m, "pre-shared-key")
			if ip := ystr(m, "ip"); ip != "" {
				if !strings.Contains(ip, "/") {
					ip += "/32"
				}
				p.LocalAddress = append(p.LocalAddress, ip)
			}
			if ip := ystr(m, "ipv6"); ip != "" {
				if !strings.Contains(ip, "/") {
					ip += "/128"
				}
				p.LocalAddress = append(p.LocalAddress, ip)
			}
			p.MTU = atoi(ystr(m, "mtu"))
			if o, ok := m["amnezia-wg-option"].(map[string]any); ok {
				for k, v := range o {
					if k = strings.ToLower(k); isAWGKey(k) && v != nil {
						if p.AWG == nil {
							p.AWG = map[string]string{}
						}
						p.AWG[k] = fmt.Sprint(v)
					}
				}
			}
			if r, ok := m["reserved"].([]any); ok {
				for _, x := range r {
					p.Reserved = append(p.Reserved, atoi(fmt.Sprint(x)))
				}
			}
		case "anytls":
			p.Type = model.TAnyTLS
			p.Security = "tls"
		case "ssh":
			p.Type = model.TSSH
		default:
			errs = append(errs, fmt.Errorf("clash: unsupported type %q (%s)", typ, p.Name))
			continue
		}
		if p.Name == "" {
			p.Name = fmt.Sprintf("%s %s:%d", strings.ToUpper(p.Type), p.Server, p.Port)
		}
		out = append(out, p)
	}
	return out, errs
}

// ---------- sing-box JSON (outbounds) ----------

// ParseSingBoxJSON imports outbounds from a sing-box configuration.
func ParseSingBoxJSON(text string) ([]*model.Profile, []error) {
	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
		Endpoints []map[string]any `json:"endpoints"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil, []error{err}
	}
	var out []*model.Profile
	var errs []error
	all := append(doc.Outbounds, doc.Endpoints...)
	for _, m := range all {
		typ := ystr(m, "type")
		switch typ {
		case "direct", "block", "dns", "selector", "urltest", "":
			continue
		}
		p := &model.Profile{ID: model.NewID(), Name: ystr(m, "tag"), Server: ystr(m, "server"), Port: atoi(ystr(m, "server_port")), Created: time.Now().Unix()}
		p.UUID = ystr(m, "uuid")
		p.Password = ystr(m, "password")
		p.Username = ystr(m, "username")
		p.Method = ystr(m, "method", "security")
		p.Flow = ystr(m, "flow")
		tls := ymap(m, "tls")
		if ybool(tls, "enabled") {
			p.Security = "tls"
			p.SNI = ystr(tls, "server_name")
			p.Insecure = ybool(tls, "insecure")
			if r := ymap(tls, "reality"); ybool(r, "enabled") {
				p.Security = "reality"
				p.PublicKey = ystr(r, "public_key")
				p.ShortID = ystr(r, "short_id")
			}
			if u := ymap(tls, "utls"); ybool(u, "enabled") {
				p.Fingerprint = ystr(u, "fingerprint")
			}
		}
		tr := ymap(m, "transport")
		switch ystr(tr, "type") {
		case "ws":
			p.Network = "ws"
			p.Path = ystr(tr, "path")
			p.Host = ystr(ymap(tr, "headers"), "Host")
		case "grpc":
			p.Network = "grpc"
			p.ServiceName = ystr(tr, "service_name")
		case "httpupgrade":
			p.Network = "httpupgrade"
			p.Path = ystr(tr, "path")
			p.Host = ystr(tr, "host")
		case "http":
			p.Network = "http"
			p.Path = ystr(tr, "path")
		default:
			p.Network = "tcp"
		}
		switch typ {
		case "vmess", "vless", "trojan", "hysteria", "hysteria2", "tuic", "socks", "http", "anytls", "ssh", "shadowsocks":
			p.Type = typ
		case "wireguard":
			p.Type = model.TWireGuard
			p.PrivateKey = ystr(m, "private_key")
			if peers, ok := m["peers"].([]any); ok && len(peers) > 0 {
				pm, _ := peers[0].(map[string]any)
				p.Server = ystr(pm, "address")
				p.Port = atoi(ystr(pm, "port"))
				p.PeerPublicKey = ystr(pm, "public_key")
				p.PreSharedKey = ystr(pm, "pre_shared_key")
			}
			if a, ok := m["address"].([]any); ok {
				for _, x := range a {
					p.LocalAddress = append(p.LocalAddress, fmt.Sprint(x))
				}
			}
		default:
			errs = append(errs, fmt.Errorf("sing-box: unsupported type %q", typ))
			continue
		}
		if typ == "hysteria2" {
			if o := ymap(m, "obfs"); ystr(o, "type") != "" {
				p.Obfs = ystr(o, "type")
				p.ObfsPassword = ystr(o, "password")
			}
		}
		if p.Server == "" {
			continue
		}
		out = append(out, p)
	}
	return out, errs
}
