// Package model holds the data types shared by every part of Rahgozar.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// Protocol type identifiers.
const (
	TVMess       = "vmess"
	TVLESS       = "vless"
	TTrojan      = "trojan"
	TShadowsocks = "shadowsocks"
	TSSR         = "ssr"
	TSocks       = "socks"
	THTTP        = "http"
	THysteria    = "hysteria"
	THysteria2   = "hysteria2"
	TTUIC        = "tuic"
	TWireGuard   = "wireguard"
	TAnyTLS      = "anytls"
	TSSH         = "ssh"
	TOpenVPN     = "openvpn"
	TPPTP        = "pptp"
	TL2TP        = "l2tp"
	TSSTP        = "sstp"
	TIKEv2       = "ikev2"
	TChain       = "chain"
)

// Engine identifiers.
const (
	EngineAuto    = "auto"
	EngineSingBox = "sing-box"
	EngineXray    = "xray"
	EngineMihomo  = "mihomo"
	EngineWindows = "windows" // built-in Windows RAS (PPTP/L2TP/SSTP/IKEv2)
)

// Profile is one server / connection definition.
type Profile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  string `json:"group"` // "manual" or subscription id
	Type   string `json:"type"`
	Engine string `json:"engine,omitempty"` // auto | sing-box | xray | mihomo

	Server string `json:"server,omitempty"`
	Port   int    `json:"port,omitempty"`
	Ports  string `json:"ports,omitempty"` // port hopping range e.g. "20000-30000"

	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
	Username string `json:"username,omitempty"`
	Method   string `json:"method,omitempty"` // ss cipher / vmess security
	Flow     string `json:"flow,omitempty"`
	AlterID  int    `json:"alterId,omitempty"`

	// Transport
	Network     string `json:"network,omitempty"` // tcp ws grpc http httpupgrade xhttp kcp quic
	Path        string `json:"path,omitempty"`
	Host        string `json:"host,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`
	HeaderType  string `json:"headerType,omitempty"`
	XMode       string `json:"xmode,omitempty"` // xhttp mode / grpc mode
	XExtra      string `json:"xextra,omitempty"`

	// TLS
	Security    string `json:"security,omitempty"` // "" | tls | reality
	SNI         string `json:"sni,omitempty"`
	ALPN        string `json:"alpn,omitempty"`
	Fingerprint string `json:"fp,omitempty"`
	Insecure    bool   `json:"insecure,omitempty"`
	PublicKey   string `json:"pbk,omitempty"`
	ShortID     string `json:"sid,omitempty"`
	SpiderX     string `json:"spx,omitempty"`
	ECH         string `json:"ech,omitempty"`

	// Shadowsocks plugin
	Plugin     string `json:"plugin,omitempty"`
	PluginOpts string `json:"pluginOpts,omitempty"`

	// ShadowsocksR
	Protocol      string `json:"protocol,omitempty"`
	ProtocolParam string `json:"protocolParam,omitempty"`
	Obfs          string `json:"obfs,omitempty"`
	ObfsParam     string `json:"obfsParam,omitempty"`

	// Hysteria / TUIC
	UpMbps        int    `json:"upMbps,omitempty"`
	DownMbps      int    `json:"downMbps,omitempty"`
	ObfsPassword  string `json:"obfsPassword,omitempty"`
	HopInterval   string `json:"hopInterval,omitempty"`
	Congestion    string `json:"congestion,omitempty"`
	UDPRelayMode  string `json:"udpRelayMode,omitempty"`
	HysteriaAuth  string `json:"hyAuth,omitempty"`
	HysteriaProto string `json:"hyProto,omitempty"`

	// WireGuard
	PrivateKey    string   `json:"privateKey,omitempty"`
	PeerPublicKey string   `json:"peerPublicKey,omitempty"`
	PreSharedKey  string   `json:"preSharedKey,omitempty"`
	LocalAddress  []string `json:"localAddress,omitempty"`
	Reserved      []int    `json:"reserved,omitempty"`
	MTU           int      `json:"mtu,omitempty"`

	// OpenVPN (raw .ovpn) / RAS
	OVPN     string `json:"ovpn,omitempty"`
	PSK      string `json:"psk,omitempty"`
	OVPNNet  string `json:"ovpnNet,omitempty"` // override udp/tcp
	UseSysVP bool   `json:"useSysVpn,omitempty"`

	// Chain (proxy chain): ordered list of profile ids, first hop first.
	Chain []string `json:"chain,omitempty"`

	Raw     string `json:"raw,omitempty"`
	Note    string `json:"note,omitempty"`
	Created int64  `json:"created,omitempty"`
}

// Family returns a coarse protocol "family" label used by the protocol-health
// learner, e.g. "vless+reality", "vmess+ws+tls", "hysteria2".
func (p *Profile) Family() string {
	switch p.Type {
	case TVMess, TVLESS, TTrojan:
		parts := []string{p.Type}
		n := p.Network
		if n == "" {
			n = "tcp"
		}
		if n != "tcp" {
			parts = append(parts, n)
		}
		if p.Security != "" && p.Security != "none" {
			parts = append(parts, p.Security)
		}
		return strings.Join(parts, "+")
	case TShadowsocks:
		if p.Plugin != "" {
			return "ss+" + strings.SplitN(p.Plugin, ";", 2)[0]
		}
		return "ss"
	}
	return p.Type
}

// IsRAS reports whether the profile is handled by the Windows built-in VPN stack.
func (p *Profile) IsRAS() bool {
	switch p.Type {
	case TPPTP, TL2TP, TSSTP, TIKEv2:
		return true
	}
	return false
}

// Subscription is a remote list of profiles.
type Subscription struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	AutoUpdate  bool   `json:"autoUpdate"`
	IntervalH   int    `json:"intervalH"`
	LastUpdate  int64  `json:"lastUpdate"`
	Count       int    `json:"count"`
	UserAgent   string `json:"userAgent,omitempty"`
	ViaProxy    bool   `json:"viaProxy"`
	Upload      int64  `json:"upload,omitempty"`
	Download    int64  `json:"download,omitempty"`
	Total       int64  `json:"total,omitempty"`
	Expire      int64  `json:"expire,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

// Settings are user preferences.
type Settings struct {
	Language        string   `json:"language"`        // fa | en
	Theme           string   `json:"theme"`           // dark | light | auto
	Mode            string   `json:"mode"`            // proxy | tun
	RoutingMode     string   `json:"routingMode"`     // smart | global | rules | direct
	SelectedID      string   `json:"selectedId"`      // selected profile id or "auto"
	AutoSelect      bool     `json:"autoSelect"`      // smart best-server choice on connect
	AutoFailover    bool     `json:"autoFailover"`    // switch servers when current dies
	LearnDomains    bool     `json:"learnDomains"`    // smart routing learner
	MixedPort       int      `json:"mixedPort"`       // local socks+http port
	AllowLAN        bool     `json:"allowLan"`
	SetSystemProxy  bool     `json:"setSystemProxy"`
	BypassLAN       bool     `json:"bypassLan"`
	BypassIran      bool     `json:"bypassIran"`
	BlockAds        bool     `json:"blockAds"`
	RemoteDNS       string   `json:"remoteDns"` // e.g. https://1.1.1.1/dns-query
	DirectDNS       string   `json:"directDns"` // e.g. 8.8.8.8 or local
	FakeIP          bool     `json:"fakeIp"`
	TunStack        string   `json:"tunStack"` // mixed | system | gvisor
	TunMTU          int      `json:"tunMtu"`
	StrictRoute     bool     `json:"strictRoute"`
	IPv6            bool     `json:"ipv6"`
	Sniff           bool     `json:"sniff"`
	TestURL         string   `json:"testUrl"`
	TestTimeoutMs   int      `json:"testTimeoutMs"`
	TestConcurrency int      `json:"testConcurrency"`
	GitHubMirror    string   `json:"githubMirror"` // prefix like https://ghfast.top/
	UpdateViaProxy  bool     `json:"updateViaProxy"`
	AppRepo         string   `json:"appRepo"` // owner/repo for self update
	CheckUpdates    bool     `json:"checkUpdates"`
	AutoStart       bool     `json:"autoStart"`
	ConnectOnStart  bool     `json:"connectOnStart"`
	MinimizeToTray  bool     `json:"minimizeToTray"`
	LogLevel        string   `json:"logLevel"`
	ProxyDomains    []string `json:"proxyDomains"`  // user rules: always proxy
	DirectDomains   []string `json:"directDomains"` // user rules: always direct
	BlockDomains    []string `json:"blockDomains"`
	DirectProcesses []string `json:"directProcesses"`
	FragmentEnabled bool     `json:"fragment"` // TLS fragment for direct-to-server (anti DPI)
	FragmentLen     string   `json:"fragmentLen"`
	FragmentDelay   string   `json:"fragmentDelay"`
	MuxEnabled      bool     `json:"mux"`
}

// DefaultSettings returns sensible defaults for Iranian networks.
func DefaultSettings() Settings {
	return Settings{
		Language:        "fa",
		Theme:           "dark",
		Mode:            "proxy",
		RoutingMode:     "smart",
		SelectedID:      "auto",
		AutoSelect:      true,
		AutoFailover:    true,
		LearnDomains:    true,
		MixedPort:       10808,
		SetSystemProxy:  true,
		BypassLAN:       true,
		BypassIran:      true,
		BlockAds:        false,
		RemoteDNS:       "https://1.1.1.1/dns-query",
		DirectDNS:       "8.8.8.8",
		FakeIP:          false,
		TunStack:        "mixed",
		TunMTU:          9000,
		StrictRoute:     true,
		IPv6:            false,
		Sniff:           true,
		TestURL:         "https://www.gstatic.com/generate_204",
		TestTimeoutMs:   5000,
		TestConcurrency: 16,
		GitHubMirror:    "",
		UpdateViaProxy:  true,
		AppRepo:         "adriarai118/Rahgozar",
		CheckUpdates:    true,
		MinimizeToTray:  true,
		LogLevel:        "warn",
		FragmentLen:     "10-30",
		FragmentDelay:   "10-20",
	}
}

// NewID returns a short random identifier.
func NewID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
