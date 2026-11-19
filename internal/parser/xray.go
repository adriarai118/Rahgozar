package parser

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"rahgozar/internal/model"
)

// ParseXrayJSON imports the proxy outbounds of an Xray/V2Ray client config
// (a single config object, or an array of them as some subscriptions serve).
func ParseXrayJSON(text string) ([]*model.Profile, []error) {
	text = strings.TrimSpace(text)
	var docs []map[string]any
	if strings.HasPrefix(text, "[") {
		if json.Unmarshal([]byte(text), &docs) != nil {
			return nil, []error{errors.New("xray json: invalid")}
		}
	} else {
		var d map[string]any
		if json.Unmarshal([]byte(text), &d) != nil {
			return nil, []error{errors.New("xray json: invalid")}
		}
		docs = []map[string]any{d}
	}
	var out []*model.Profile
	var errs []error
	for _, d := range docs {
		obs, _ := d["outbounds"].([]any)
		remarks, _ := d["remarks"].(string)
		var proxies []map[string]any
		for _, o := range obs {
			if m, ok := o.(map[string]any); ok {
				switch jstr(m, "protocol") {
				case "vless", "vmess", "trojan", "shadowsocks", "socks", "http", "wireguard":
					proxies = append(proxies, m)
				}
			}
		}
		for i, m := range proxies {
			p, err := xrayOutbound(m)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			switch {
			case remarks != "" && len(proxies) > 1:
				p.Name = fmt.Sprintf("%s #%d", remarks, i+1)
			case remarks != "":
				p.Name = remarks
			default:
				p.Name = firstNonEmpty(jstr(m, "tag"), p.Server)
			}
			p.Raw = ToLink(p)
			out = append(out, p)
		}
	}
	return out, errs
}

func isXrayJSON(t string) bool {
	return strings.Contains(t, `"outbounds"`) && strings.Contains(t, `"protocol"`)
}

func jstr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprint(int64(v))
	case bool:
		return fmt.Sprint(v)
	}
	return ""
}

func jmap(m map[string]any, k string) map[string]any {
	v, _ := m[k].(map[string]any)
	if v == nil {
		return map[string]any{}
	}
	return v
}

func jfirst(m map[string]any, k string) map[string]any {
	if a, ok := m[k].([]any); ok && len(a) > 0 {
		v, _ := a[0].(map[string]any)
		if v != nil {
			return v
		}
	}
	return map[string]any{}
}

func jlist(v any) []string {
	switch x := v.(type) {
	case string:
		if x != "" {
			return []string{x}
		}
	case []any:
		var out []string
		for _, s := range x {
			if s, ok := s.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func jint(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case string:
		return atoi(v)
	}
	return 0
}

func xrayOutbound(o map[string]any) (*model.Profile, error) {
	proto := jstr(o, "protocol")
	set := jmap(o, "settings")
	p := &model.Profile{}
	// server: classic (vnext/servers/peers) or the flat form newer Xray accepts
	srv := set
	switch proto {
	case "vless", "vmess":
		if v := jfirst(set, "vnext"); len(v) > 0 {
			srv = v
		}
		user := jfirst(srv, "users")
		if len(user) == 0 {
			user = srv
		}
		p.UUID = jstr(user, "id")
		p.Flow = jstr(user, "flow")
		if proto == "vmess" {
			p.Type, p.Method, p.AlterID = model.TVMess, firstNonEmpty(jstr(user, "security"), "auto"), jint(user, "alterId")
		} else {
			p.Type = model.TVLESS
			if e := jstr(user, "encryption"); e != "" && e != "none" {
				p.Method = e
			}
		}
	case "trojan", "shadowsocks", "socks", "http":
		if v := jfirst(set, "servers"); len(v) > 0 {
			srv = v
		}
		p.Password = jstr(srv, "password")
		switch proto {
		case "trojan":
			p.Type = model.TTrojan
		case "shadowsocks":
			p.Type, p.Method = model.TShadowsocks, jstr(srv, "method")
		default:
			p.Type = model.TSocks
			if proto == "http" {
				p.Type = model.THTTP
			}
			u := jfirst(srv, "users")
			p.Username, p.Password = firstNonEmpty(jstr(u, "user"), jstr(srv, "user")), firstNonEmpty(jstr(u, "pass"), jstr(srv, "pass"))
		}
	case "wireguard":
		p.Type = model.TWireGuard
		p.PrivateKey = jstr(set, "secretKey")
		p.LocalAddress = jlist(set["address"])
		p.MTU = jint(set, "mtu")
		if r, ok := set["reserved"].([]any); ok {
			for _, x := range r {
				if f, ok := x.(float64); ok {
					p.Reserved = append(p.Reserved, int(f))
				}
			}
		}
		peer := jfirst(set, "peers")
		p.PeerPublicKey, p.PreSharedKey = jstr(peer, "publicKey"), jstr(peer, "preSharedKey")
		host, port, ok := strings.Cut(jstr(peer, "endpoint"), ":")
		if i := strings.LastIndex(jstr(peer, "endpoint"), ":"); i > 0 {
			host, port, ok = jstr(peer, "endpoint")[:i], jstr(peer, "endpoint")[i+1:], true
		}
		if !ok {
			return nil, errors.New("xray wireguard: no endpoint")
		}
		p.Server, p.Port = strings.Trim(host, "[]"), atoi(port)
		return p, nil
	default:
		return nil, fmt.Errorf("xray: %s not supported", proto)
	}
	p.Server = jstr(srv, "address")
	p.Port = jint(srv, "port")
	if p.Server == "" || p.Port == 0 {
		return nil, fmt.Errorf("xray %s: no address", proto)
	}

	ss := jmap(o, "streamSettings")
	netw := firstNonEmpty(jstr(ss, "network"), "tcp")
	switch netw {
	case "raw":
		netw = "tcp"
	case "splithttp":
		netw = "xhttp"
	case "h2":
		netw = "http"
	}
	if proto != "shadowsocks" && proto != "socks" && proto != "http" {
		p.Network = netw
	}
	switch netw {
	case "tcp":
		tcp := jmap(ss, "tcpSettings")
		if len(tcp) == 0 {
			tcp = jmap(ss, "rawSettings")
		}
		h := jmap(tcp, "header")
		if jstr(h, "type") == "http" {
			p.HeaderType = "http"
			req := jmap(h, "request")
			p.Path = strings.Join(jlist(req["path"]), ",")
			p.Host = strings.Join(jlist(jmap(req, "headers")["Host"]), ",")
		}
	case "ws":
		w := jmap(ss, "wsSettings")
		p.Path = jstr(w, "path")
		p.Host = firstNonEmpty(jstr(w, "host"), jstr(jmap(w, "headers"), "Host"))
	case "httpupgrade":
		w := jmap(ss, "httpupgradeSettings")
		p.Path, p.Host = jstr(w, "path"), jstr(w, "host")
	case "xhttp":
		w := jmap(ss, "xhttpSettings")
		if len(w) == 0 {
			w = jmap(ss, "splithttpSettings")
		}
		p.Path, p.Host, p.XMode = jstr(w, "path"), jstr(w, "host"), jstr(w, "mode")
		if e, ok := w["extra"]; ok && e != nil {
			b, _ := json.Marshal(e)
			p.XExtra = string(b)
		}
	case "grpc":
		g := jmap(ss, "grpcSettings")
		p.ServiceName = jstr(g, "serviceName")
		if jstr(g, "multiMode") == "true" {
			p.XMode = "multi"
		}
	case "http":
		h := jmap(ss, "httpSettings")
		p.Path, p.Host = jstr(h, "path"), strings.Join(jlist(h["host"]), ",")
	case "kcp":
		k := jmap(ss, "kcpSettings")
		p.HeaderType, p.Path = jstr(jmap(k, "header"), "type"), jstr(k, "seed")
	}
	switch jstr(ss, "security") {
	case "tls":
		t := jmap(ss, "tlsSettings")
		p.Security, p.SNI, p.Fingerprint = "tls", jstr(t, "serverName"), jstr(t, "fingerprint")
		p.ALPN = strings.Join(jlist(t["alpn"]), ",")
		p.Insecure = jstr(t, "allowInsecure") == "true"
	case "reality":
		r := jmap(ss, "realitySettings")
		p.Security, p.SNI, p.Fingerprint = "reality", jstr(r, "serverName"), firstNonEmpty(jstr(r, "fingerprint"), "chrome")
		p.PublicKey = firstNonEmpty(jstr(r, "publicKey"), jstr(r, "password"))
		p.ShortID, p.SpiderX = jstr(r, "shortId"), jstr(r, "spiderX")
	}
	return p, nil
}
