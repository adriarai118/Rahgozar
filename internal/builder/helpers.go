package builder

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"rahgozar/internal/model"
)

func xrayLogLevel(l string) string {
	switch l {
	case "debug", "info", "warning", "error", "none":
		return l
	case "warn":
		return "warning"
	case "trace":
		return "debug"
	}
	return "warning"
}

// XrayStream returns Xray streamSettings for a profile.
func XrayStream(p *model.Profile) M {
	netw := p.Network
	if netw == "" || netw == "tcp" {
		netw = "raw"
	}
	if netw == "splithttp" {
		netw = "xhttp"
	}
	if netw == "mkcp" {
		netw = "kcp"
	}
	ss := M{"network": netw}
	switch netw {
	case "raw":
		if p.HeaderType == "http" {
			req := M{"path": []string{firstOr(p.Path, "/")}}
			if p.Host != "" {
				req["headers"] = M{"Host": splitCSV(p.Host)}
			}
			ss["rawSettings"] = M{"header": M{"type": "http", "request": req}}
		}
	case "ws":
		ws := M{"path": firstOr(p.Path, "/")}
		if p.Host != "" {
			ws["host"] = p.Host
		}
		ss["wsSettings"] = ws
	case "grpc":
		ss["grpcSettings"] = M{"serviceName": p.ServiceName, "multiMode": p.XMode == "multi"}
	case "httpupgrade":
		h := M{"path": firstOr(p.Path, "/")}
		if p.Host != "" {
			h["host"] = p.Host
		}
		ss["httpupgradeSettings"] = h
	case "xhttp":
		x := M{"path": firstOr(p.Path, "/"), "mode": firstOr(p.XMode, "auto")}
		if p.Host != "" {
			x["host"] = p.Host
		}
		if p.XExtra != "" {
			var extra any
			if json.Unmarshal([]byte(p.XExtra), &extra) == nil {
				x["extra"] = extra
			}
		}
		ss["xhttpSettings"] = x
	case "kcp":
		// Xray 26+: mKCP header & seed moved to finalmask.
		var masks []M
		hdr := map[string]string{"wechat-video": "header-wechat", "srtp": "header-srtp", "utp": "header-utp",
			"dtls": "header-dtls", "wireguard": "header-wireguard", "dns": "header-dns"}
		if h, ok := hdr[p.HeaderType]; ok {
			masks = append(masks, M{"type": h})
		}
		if p.Path != "" {
			masks = append(masks, M{"type": "mkcp-aes128gcm", "settings": M{"password": p.Path}})
		} else {
			masks = append(masks, M{"type": "mkcp-original"})
		}
		ss["kcpSettings"] = M{}
		ss["finalmask"] = M{"udp": masks}
	case "http", "h2":
		ss["network"] = "xhttp"
		ss["xhttpSettings"] = M{"path": firstOr(p.Path, "/"), "host": p.Host, "mode": "stream-one"}
	}
	switch p.Security {
	case "tls":
		t := M{"serverName": firstOr(p.SNI, firstOr(strings.Split(p.Host, ",")[0], p.Server))}
		if a := splitCSV(p.ALPN); len(a) > 0 {
			t["alpn"] = a
		}
		if p.Fingerprint != "" {
			t["fingerprint"] = p.Fingerprint
		}
		if p.Insecure {
			t["allowInsecure"] = true
		}
		ss["security"] = "tls"
		ss["tlsSettings"] = t
	case "reality":
		r := M{"serverName": p.SNI, "fingerprint": firstOr(p.Fingerprint, "chrome"), "publicKey": p.PublicKey}
		if p.ShortID != "" {
			r["shortId"] = p.ShortID
		}
		if p.SpiderX != "" {
			r["spiderX"] = p.SpiderX
		}
		ss["security"] = "reality"
		ss["realitySettings"] = r
	default:
		ss["security"] = "none"
	}
	return ss
}

// XrayOutbound converts a profile to an Xray outbound object.
func XrayOutbound(p *model.Profile, tag string) (M, error) {
	ob := M{"tag": tag}
	switch p.Type {
	case model.TVLESS:
		user := M{"id": p.UUID, "encryption": firstOr(p.Method, "none")}
		if p.Flow != "" {
			user["flow"] = p.Flow
		}
		ob["protocol"] = "vless"
		ob["settings"] = M{"vnext": []M{{"address": p.Server, "port": p.Port, "users": []M{user}}}}
	case model.TVMess:
		ob["protocol"] = "vmess"
		ob["settings"] = M{"vnext": []M{{"address": p.Server, "port": p.Port, "users": []M{{"id": p.UUID, "alterId": p.AlterID, "security": firstOr(p.Method, "auto")}}}}}
	case model.TTrojan:
		ob["protocol"] = "trojan"
		ob["settings"] = M{"servers": []M{{"address": p.Server, "port": p.Port, "password": p.Password}}}
	case model.TShadowsocks:
		ob["protocol"] = "shadowsocks"
		ob["settings"] = M{"servers": []M{{"address": p.Server, "port": p.Port, "method": p.Method, "password": p.Password}}}
	case model.TSocks:
		srv := M{"address": p.Server, "port": p.Port}
		if p.Username != "" {
			srv["users"] = []M{{"user": p.Username, "pass": p.Password}}
		}
		ob["protocol"] = "socks"
		ob["settings"] = M{"servers": []M{srv}}
	default:
		return nil, fmt.Errorf("xray: unsupported protocol %s", p.Type)
	}
	ob["streamSettings"] = XrayStream(p)
	return ob, nil
}

// XrayConfig builds a helper config: local SOCKS inbound -> profile outbound.
func XrayConfig(p *model.Profile, port int, logLevel string) ([]byte, error) {
	ob, err := XrayOutbound(p, "proxy")
	if err != nil {
		return nil, err
	}
	cfg := M{
		"log": M{"loglevel": xrayLogLevel(logLevel)},
		"inbounds": []M{{
			"tag": "in", "listen": "127.0.0.1", "port": port, "protocol": "socks",
			"settings": M{"udp": true, "auth": "noauth"},
		}},
		"outbounds": []M{ob, {"tag": "direct", "protocol": "freedom"}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// MihomoProxy converts a profile to a Clash/Mihomo proxy entry.
func MihomoProxy(p *model.Profile, name string) (M, error) {
	m := M{"name": name, "server": p.Server, "port": p.Port, "udp": true}
	switch p.Type {
	case model.TSSR:
		m["type"] = "ssr"
		m["cipher"] = p.Method
		m["password"] = p.Password
		m["protocol"] = p.Protocol
		m["obfs"] = p.Obfs
		if p.ProtocolParam != "" {
			m["protocol-param"] = p.ProtocolParam
		}
		if p.ObfsParam != "" {
			m["obfs-param"] = p.ObfsParam
		}
	case model.TShadowsocks:
		m["type"] = "ss"
		m["cipher"] = p.Method
		m["password"] = p.Password
	case model.TVMess, model.TVLESS, model.TTrojan:
		m["type"] = p.Type
		if p.Type == model.TTrojan {
			m["password"] = p.Password
		} else {
			m["uuid"] = p.UUID
		}
		if p.Type == model.TVMess {
			m["alterId"] = p.AlterID
			m["cipher"] = firstOr(p.Method, "auto")
		}
		if p.Flow != "" {
			m["flow"] = p.Flow
		}
		if p.Security == "tls" || p.Security == "reality" {
			m["tls"] = true
			if p.SNI != "" {
				m["servername"] = p.SNI
				m["sni"] = p.SNI
			}
			m["skip-cert-verify"] = p.Insecure
			if p.Fingerprint != "" {
				m["client-fingerprint"] = p.Fingerprint
			}
		}
		if p.Security == "reality" {
			m["reality-opts"] = M{"public-key": p.PublicKey, "short-id": p.ShortID}
			if p.Fingerprint == "" {
				m["client-fingerprint"] = "chrome"
			}
		}
		switch p.Network {
		case "ws":
			m["network"] = "ws"
			o := M{"path": firstOr(p.Path, "/")}
			if p.Host != "" {
				o["headers"] = M{"Host": p.Host}
			}
			m["ws-opts"] = o
		case "grpc":
			m["network"] = "grpc"
			m["grpc-opts"] = M{"grpc-service-name": p.ServiceName}
		case "http", "h2":
			m["network"] = "h2"
			m["h2-opts"] = M{"path": firstOr(p.Path, "/"), "host": splitCSV(p.Host)}
		}
	default:
		return nil, fmt.Errorf("mihomo: unsupported protocol %s", p.Type)
	}
	return m, nil
}

// MihomoConfig builds a helper config (JSON is valid YAML).
func MihomoConfig(p *model.Profile, port int, logLevel string) ([]byte, error) {
	px, err := MihomoProxy(p, "p")
	if err != nil {
		return nil, err
	}
	ll := logLevel
	if ll == "warn" {
		ll = "warning"
	}
	if ll == "" || ll == "trace" {
		ll = "warning"
	}
	cfg := M{
		"socks-port":        port,
		"bind-address":      "127.0.0.1",
		"allow-lan":         false,
		"mode":              "rule",
		"log-level":         ll,
		"ipv6":              false,
		"geo-auto-update":   false,
		"unified-delay":     true,
		"find-process-mode": "off",
		"proxies":           []M{px},
		"rules":             []string{"MATCH,p"},
		"dns":               M{"enable": false},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// HostOf returns the server host of a profile (for bypass lists).
func HostOf(p *model.Profile) string {
	if h, _, err := net.SplitHostPort(p.Server); err == nil {
		return h
	}
	return p.Server
}
