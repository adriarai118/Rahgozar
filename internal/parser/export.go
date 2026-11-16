package parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"rahgozar/internal/model"
)

// ToLink converts a profile back to a share link (where a standard exists).
func ToLink(p *model.Profile) string {
	hp := net.JoinHostPort(p.Server, strconv.Itoa(p.Port))
	frag := "#" + url.PathEscape(p.Name)
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	stdTransport := func() {
		set("type", p.Network)
		set("security", p.Security)
		set("sni", p.SNI)
		set("alpn", p.ALPN)
		set("fp", p.Fingerprint)
		set("pbk", p.PublicKey)
		set("sid", p.ShortID)
		set("spx", p.SpiderX)
		set("host", p.Host)
		set("path", p.Path)
		set("serviceName", p.ServiceName)
		set("headerType", p.HeaderType)
		set("mode", p.XMode)
		set("flow", p.Flow)
		if p.Insecure {
			q.Set("allowInsecure", "1")
		}
	}
	switch p.Type {
	case model.TVLESS:
		stdTransport()
		q.Set("encryption", firstNonEmpty(p.Method, "none"))
		return "vless://" + url.PathEscape(p.UUID) + "@" + hp + "?" + q.Encode() + frag
	case model.TTrojan:
		stdTransport()
		return "trojan://" + url.PathEscape(p.Password) + "@" + hp + "?" + q.Encode() + frag
	case model.TAnyTLS:
		set("sni", p.SNI)
		if p.Insecure {
			q.Set("insecure", "1")
		}
		return "anytls://" + url.PathEscape(p.Password) + "@" + hp + "?" + q.Encode() + frag
	case model.TVMess:
		m := map[string]any{
			"v": "2", "ps": p.Name, "add": p.Server, "port": strconv.Itoa(p.Port), "id": p.UUID,
			"aid": strconv.Itoa(p.AlterID), "scy": firstNonEmpty(p.Method, "auto"), "net": firstNonEmpty(p.Network, "tcp"),
			"type": firstNonEmpty(p.HeaderType, "none"), "host": p.Host, "path": p.Path, "tls": p.Security,
			"sni": p.SNI, "alpn": p.ALPN, "fp": p.Fingerprint,
		}
		if p.Network == "grpc" {
			m["path"] = p.ServiceName
		}
		b, _ := json.Marshal(m)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	case model.TShadowsocks:
		ui := base64.RawURLEncoding.EncodeToString([]byte(p.Method + ":" + p.Password))
		s := "ss://" + ui + "@" + hp
		if p.Plugin != "" {
			s += "?plugin=" + url.QueryEscape(p.Plugin+";"+p.PluginOpts)
		}
		return s + frag
	case model.TSSR:
		main := fmt.Sprintf("%s:%d:%s:%s:%s:%s", p.Server, p.Port, p.Protocol, p.Method, p.Obfs,
			base64.RawURLEncoding.EncodeToString([]byte(p.Password)))
		params := "obfsparam=" + base64.RawURLEncoding.EncodeToString([]byte(p.ObfsParam)) +
			"&protoparam=" + base64.RawURLEncoding.EncodeToString([]byte(p.ProtocolParam)) +
			"&remarks=" + base64.RawURLEncoding.EncodeToString([]byte(p.Name))
		return "ssr://" + base64.RawURLEncoding.EncodeToString([]byte(main+"/?"+params))
	case model.THysteria2:
		set("sni", p.SNI)
		if p.Insecure {
			q.Set("insecure", "1")
		}
		if p.Obfs != "" {
			q.Set("obfs", p.Obfs)
			q.Set("obfs-password", p.ObfsPassword)
		}
		set("mport", p.Ports)
		return "hysteria2://" + url.PathEscape(p.Password) + "@" + hp + "?" + q.Encode() + frag
	case model.THysteria:
		set("auth", p.HysteriaAuth)
		set("peer", p.SNI)
		set("alpn", p.ALPN)
		set("obfsParam", p.ObfsPassword)
		if p.UpMbps > 0 {
			q.Set("upmbps", strconv.Itoa(p.UpMbps))
		}
		if p.DownMbps > 0 {
			q.Set("downmbps", strconv.Itoa(p.DownMbps))
		}
		if p.Insecure {
			q.Set("insecure", "1")
		}
		return "hysteria://" + hp + "?" + q.Encode() + frag
	case model.TTUIC:
		set("congestion_control", p.Congestion)
		set("udp_relay_mode", p.UDPRelayMode)
		set("alpn", p.ALPN)
		set("sni", p.SNI)
		if p.Insecure {
			q.Set("allow_insecure", "1")
		}
		return "tuic://" + url.PathEscape(p.UUID) + ":" + url.PathEscape(p.Password) + "@" + hp + "?" + q.Encode() + frag
	case model.TWireGuard:
		q.Set("publickey", p.PeerPublicKey)
		set("presharedkey", p.PreSharedKey)
		q.Set("address", strings.Join(p.LocalAddress, ","))
		if len(p.Reserved) > 0 {
			var r []string
			for _, x := range p.Reserved {
				r = append(r, strconv.Itoa(x))
			}
			q.Set("reserved", strings.Join(r, ","))
		}
		if p.MTU > 0 {
			q.Set("mtu", strconv.Itoa(p.MTU))
		}
		return "wireguard://" + url.PathEscape(p.PrivateKey) + "@" + hp + "?" + q.Encode() + frag
	case model.TSocks, model.THTTP, model.TSSH:
		scheme := map[string]string{model.TSocks: "socks", model.THTTP: "http", model.TSSH: "ssh"}[p.Type]
		if p.Type == model.THTTP && p.Security == "tls" {
			scheme = "https"
		}
		auth := ""
		if p.Username != "" {
			auth = url.UserPassword(p.Username, p.Password).String() + "@"
		}
		return scheme + "://" + auth + hp + frag
	case model.TPPTP, model.TL2TP, model.TSSTP, model.TIKEv2:
		auth := ""
		if p.Username != "" {
			auth = url.UserPassword(p.Username, p.Password).String() + "@"
		}
		s := p.Type + "://" + auth + p.Server
		if p.Port > 0 {
			s += ":" + strconv.Itoa(p.Port)
		}
		if p.PSK != "" {
			s += "?psk=" + url.QueryEscape(p.PSK)
		}
		return s + frag
	}
	return p.Raw
}
