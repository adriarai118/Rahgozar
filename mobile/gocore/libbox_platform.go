package gocore

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/sagernet/sing-box/experimental/libbox"
)

// libboxPlatform implements libbox.PlatformInterface; only the VPN-relevant
// parts are forwarded to the Android app.
type libboxPlatform struct{}

var (
	monMu    sync.Mutex
	listener libbox.InterfaceUpdateListener
)

// UpdateDefaultInterface is called by the app's network callback.
// index -1 = no network.
func UpdateDefaultInterface(name string, index int32, expensive, constrained bool) {
	monMu.Lock()
	l := listener
	monMu.Unlock()
	if l != nil {
		l.UpdateDefaultInterface(name, index, expensive, constrained)
	}
}

func (p *libboxPlatform) LocalDNSTransport() libbox.LocalDNSTransport { return nil }
func (p *libboxPlatform) UsePlatformAutoDetectInterfaceControl() bool { return true }
func (p *libboxPlatform) AutoDetectInterfaceControl(fd int32) error {
	if host == nil || !host.Protect(fd) {
		return errors.New("protect failed")
	}
	return nil
}

type prefix struct {
	Address string `json:"address"`
	Prefix  int32  `json:"prefix"`
}

func prefixes(it libbox.RoutePrefixIterator) []prefix {
	var out []prefix
	if it == nil {
		return out
	}
	for it.HasNext() {
		p := it.Next()
		out = append(out, prefix{p.Address(), p.Prefix()})
	}
	return out
}

func strs(it libbox.StringIterator) []string {
	var out []string
	if it == nil {
		return out
	}
	for it.HasNext() {
		out = append(out, it.Next())
	}
	return out
}

type tunJSON struct {
	MTU          int32    `json:"mtu"`
	AutoRoute    bool     `json:"autoRoute"`
	StrictRoute  bool     `json:"strictRoute"`
	Inet4Address []prefix `json:"inet4Address"`
	Inet6Address []prefix `json:"inet6Address"`
	Inet4Route   []prefix `json:"inet4Route"`
	Inet6Route   []prefix `json:"inet6Route"`
	DNSServers   []string `json:"dnsServers"`
	Include      []string `json:"includePackage"`
	Exclude      []string `json:"excludePackage"`
	HTTPProxy    bool     `json:"httpProxy"`
	ProxyHost    string   `json:"proxyHost"`
	ProxyPort    int32    `json:"proxyPort"`
	ProxyBypass  []string `json:"proxyBypass"`
}

func (p *libboxPlatform) OpenTun(o libbox.TunOptions) (int32, error) {
	if host == nil {
		return -1, errors.New("no host")
	}
	t := tunJSON{
		MTU: o.GetMTU(), AutoRoute: o.GetAutoRoute(), StrictRoute: o.GetStrictRoute(),
		Inet4Address: prefixes(o.GetInet4Address()), Inet6Address: prefixes(o.GetInet6Address()),
		Inet4Route: prefixes(o.GetInet4RouteRange()), Inet6Route: prefixes(o.GetInet6RouteRange()),
		Include: strs(o.GetIncludePackage()), Exclude: strs(o.GetExcludePackage()),
		HTTPProxy: o.IsHTTPProxyEnabled(),
	}
	if dns, err := o.GetDNSServerAddress(); err == nil {
		t.DNSServers = strs(dns)
	}
	if t.HTTPProxy {
		t.ProxyHost, t.ProxyPort = o.GetHTTPProxyServer(), o.GetHTTPProxyServerPort()
		t.ProxyBypass = strs(o.GetHTTPProxyBypassDomain())
	}
	b, _ := json.Marshal(t)
	return host.OpenTun(string(b))
}

func (p *libboxPlatform) FindConnectionOwner(int32, string, int32, string, int32) (*libbox.ConnectionOwner, error) {
	return nil, errors.New("not supported")
}

func (p *libboxPlatform) StartDefaultInterfaceMonitor(l libbox.InterfaceUpdateListener) error {
	monMu.Lock()
	listener = l
	monMu.Unlock()
	if host != nil {
		host.StartNetworkMonitor()
	}
	return nil
}

func (p *libboxPlatform) CloseDefaultInterfaceMonitor(l libbox.InterfaceUpdateListener) error {
	monMu.Lock()
	if listener == l {
		listener = nil
	}
	monMu.Unlock()
	if host != nil {
		host.StopNetworkMonitor()
	}
	return nil
}

type ifaceJSON struct {
	Index     int32    `json:"index"`
	MTU       int32    `json:"mtu"`
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
	Flags     int32    `json:"flags"`
	Type      int32    `json:"type"`
	DNS       []string `json:"dns"`
	Gateways  []string `json:"gateways"`
	Metered   bool     `json:"metered"`
}

type ifaceIter struct{ list []*libbox.NetworkInterface }

func (i *ifaceIter) HasNext() bool { return len(i.list) > 0 }
func (i *ifaceIter) Next() *libbox.NetworkInterface {
	if len(i.list) == 0 {
		return nil
	}
	v := i.list[0]
	i.list = i.list[1:]
	return v
}

type strIter struct{ list []string }

func (i *strIter) Len() int32    { return int32(len(i.list)) }
func (i *strIter) HasNext() bool { return len(i.list) > 0 }
func (i *strIter) Next() string {
	if len(i.list) == 0 {
		return ""
	}
	v := i.list[0]
	i.list = i.list[1:]
	return v
}

func (p *libboxPlatform) GetInterfaces() (libbox.NetworkInterfaceIterator, error) {
	if host == nil {
		return &ifaceIter{}, nil
	}
	var in []ifaceJSON
	if err := json.Unmarshal([]byte(host.Interfaces()), &in); err != nil {
		return nil, err
	}
	it := &ifaceIter{}
	for _, x := range in {
		it.list = append(it.list, &libbox.NetworkInterface{
			Index: x.Index, MTU: x.MTU, Name: x.Name, Addresses: &strIter{x.Addresses}, Flags: x.Flags,
			Type: x.Type, DNSServer: &strIter{x.DNS}, Gateway: &strIter{x.Gateways}, Metered: x.Metered,
		})
	}
	return it, nil
}

func (p *libboxPlatform) UnderNetworkExtension() bool                              { return false }
func (p *libboxPlatform) IncludeAllNetworks() bool                                 { return false }
func (p *libboxPlatform) ReadWIFIState() *libbox.WIFIState                         { return nil }
func (p *libboxPlatform) ClearDNSCache()                                           {}
func (p *libboxPlatform) SendNotification(*libbox.Notification) error              { return nil }
func (p *libboxPlatform) CancelNotification(string, int32) error                   { return nil }
func (p *libboxPlatform) StartNeighborMonitor(libbox.NeighborUpdateListener) error { return nil }
func (p *libboxPlatform) CloseNeighborMonitor(libbox.NeighborUpdateListener) error { return nil }
func (p *libboxPlatform) RegisterMyInterface(string)                               {}
func (p *libboxPlatform) UsePlatformShell() bool                                   { return false }
func (p *libboxPlatform) CheckPlatformShell() error                                { return errors.New("not supported") }
func (p *libboxPlatform) OpenShellSession(*libbox.PlatformUser, string, libbox.StringIterator, string, int32, int32) (libbox.ShellSession, error) {
	return nil, errors.New("not supported")
}
func (p *libboxPlatform) LookupUser(string) (*libbox.PlatformUser, error) {
	return nil, errors.New("not supported")
}
func (p *libboxPlatform) LookupSFTPServer() (string, error) { return "", errors.New("not supported") }
func (p *libboxPlatform) ReadSystemSSHHostKey() (string, error) {
	return "", errors.New("not supported")
}
func (p *libboxPlatform) TailscaleHostname() string { return "" }
func (p *libboxPlatform) UsePlatformBridge() bool   { return false }
func (p *libboxPlatform) CreateBridge(*libbox.BridgeOptions) (libbox.BridgeSession, error) {
	return nil, errors.New("not supported")
}

// cmdHandler implements libbox.CommandServerHandler.
type cmdHandler struct{}

func (h *cmdHandler) ServiceStop() error {
	Disconnect()
	return nil
}
func (h *cmdHandler) ServiceReload() error { return nil }
func (h *cmdHandler) GetSystemProxyStatus() (*libbox.SystemProxyStatus, error) {
	return &libbox.SystemProxyStatus{}, nil
}
func (h *cmdHandler) SetSystemProxyEnabled(bool) error { return nil }
func (h *cmdHandler) TriggerNativeCrash() error        { return nil }
func (h *cmdHandler) WriteDebugMessage(msg string) {
	if host != nil {
		host.Log(msg)
	}
}
func (h *cmdHandler) ConnectSSHAgent() (int32, error) { return -1, errors.New("not supported") }
