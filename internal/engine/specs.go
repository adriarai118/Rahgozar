// Package engine manages the external VPN engines (sing-box, Xray, Mihomo,
// rule-sets): install, version detection, per-engine updates and processes.
package engine

import (
	"regexp"
	"runtime"
)

// Spec describes an updatable component.
type Spec struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Repo        string   `json:"repo"`
	Desc        string   `json:"desc"`
	Protocols   []string `json:"protocols"`
	Kind        string   `json:"kind"` // binary | rules
	Required    bool     `json:"required"`
	exe         string
	assetRe     []*regexp.Regexp
	versionArgs []string
	versionRe   *regexp.Regexp
	rename      map[*regexp.Regexp]string
	files       []string // for rules
}

func exeName(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

func osArch() (string, string) {
	return runtime.GOOS, runtime.GOARCH
}

// Specs returns all components for the current platform.
func Specs() []*Spec {
	goos, arch := osArch()
	a64 := arch
	if goos == "windows" && arch == "amd64" {
		a64 = "amd64"
	}
	xrayArch := map[string]string{"amd64": "64", "386": "32", "arm64": "arm64-v8a"}[arch]
	xrayOS := map[string]string{"windows": "windows", "linux": "linux", "darwin": "macos", "android": "android"}[goos]
	return []*Spec{
		{
			ID: "sing-box", Name: "sing-box", Repo: "SagerNet/sing-box", Kind: "binary", Required: true,
			Desc:      "هسته اصلی: TUN، مسیریابی هوشمند، DNS و اکثر پروتکل‌ها",
			Protocols: []string{"VMess", "VLESS", "Reality", "Trojan", "Shadowsocks", "Hysteria", "Hysteria2", "TUIC", "WireGuard", "OpenVPN", "AnyTLS", "SOCKS5", "HTTP(S)", "SSH", "TUN"},
			exe:       exeName("sing-box"),
			assetRe: []*regexp.Regexp{
				regexp.MustCompile(`^sing-box-[\d.]+-` + goos + `-` + a64 + `\.(zip|tar\.gz)$`),
			},
			versionArgs: []string{"version"},
			versionRe:   regexp.MustCompile(`(?i)sing-box version ([\w.\-+]+)`),
		},
		{
			ID: "xray", Name: "Xray-core", Repo: "XTLS/Xray-core", Kind: "binary",
			Desc:      "برای XHTTP، mKCP، VLESS Encryption و ترفندهای ضد فیلتر Xray",
			Protocols: []string{"XHTTP", "VLESS", "VMess", "Reality", "Trojan", "mKCP"},
			exe:       exeName("xray"),
			assetRe: []*regexp.Regexp{
				regexp.MustCompile(`^Xray-` + xrayOS + `-` + xrayArch + `\.zip$`),
			},
			versionArgs: []string{"version"},
			versionRe:   regexp.MustCompile(`Xray ([\w.\-+]+)`),
		},
		{
			ID: "mihomo", Name: "Mihomo (Clash.Meta)", Repo: "MetaCubeX/mihomo", Kind: "binary",
			Desc:      "برای ShadowsocksR و سازگاری با اشتراک‌های Clash",
			Protocols: []string{"ShadowsocksR", "Clash"},
			exe:       exeName("mihomo"),
			assetRe: []*regexp.Regexp{
				regexp.MustCompile(`^mihomo-` + goos + `-` + a64 + `-compatible-v[\d.]+\.(zip|gz)$`),
				regexp.MustCompile(`^mihomo-` + goos + `-` + a64 + `-v[\d.]+\.(zip|gz)$`),
			},
			versionArgs: []string{"-v"},
			versionRe:   regexp.MustCompile(`Mihomo Meta (v?[\w.\-+]+)`),
			rename:      map[*regexp.Regexp]string{regexp.MustCompile(`^mihomo.*(\.exe)?$`): exeName("mihomo")},
		},
		{
			ID: "rules-ir", Name: "قوانین ایران (Iran rules)", Repo: "Chocolate4U/Iran-sing-box-rules", Kind: "rules", Required: true,
			Desc:      "لیست سایت‌ها و IPهای ایرانی، تبلیغات و بدافزار برای مسیریابی هوشمند",
			Protocols: []string{"geosite-ir", "geoip-ir", "ads", "malware"},
			files:     []string{"geoip-ir.srs", "geosite-ir.srs", "geosite-category-ads-all.srs", "geosite-malware.srs", "geosite-phishing.srs"},
		},
	}
}
