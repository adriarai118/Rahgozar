package builder

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rahgozar/internal/model"
)

// Rule-set files shipped in the rules folder.
var RuleSetFiles = map[string]string{
	"geoip-ir":                 "geoip-ir.srs",
	"geosite-ir":               "geosite-ir.srs",
	"geosite-category-ads-all": "geosite-category-ads-all.srs",
	"geosite-malware":          "geosite-malware.srs",
	"geosite-phishing":         "geosite-phishing.srs",
}

// LearnedFiles are written by the smart learner (sing-box source rule-sets,
// auto-reloaded on change).
var LearnedFiles = map[string]string{
	"learned-direct": "learned-direct.json",
	"learned-proxy":  "learned-proxy.json",
}

// UserFiles hold the user's per-site choices (from settings / browser extension).
var UserFiles = map[string]string{
	"user-proxy":  "user-proxy.json",
	"user-direct": "user-direct.json",
	"user-block":  "user-block.json",
}

// DomainRuleSet renders a source rule-set matching the given domains (and subdomains).
func DomainRuleSet(domains []string) []byte {
	d := CleanDomains(domains)
	if len(d) == 0 {
		d = []string{"rahgozar.invalid"}
	}
	b, _ := json.MarshalIndent(M{"version": 3, "rules": []M{{"domain_suffix": d}}}, "", " ")
	return b
}

// WriteUserRules writes the three user rule-set files (only when changed, so
// sing-box reloads them without dropping connections).
func WriteUserRules(dir string, proxy, direct, block []string) error {
	os.MkdirAll(dir, 0o755)
	for name, list := range map[string][]string{"user-proxy.json": proxy, "user-direct.json": direct, "user-block.json": block} {
		b := DomainRuleSet(list)
		path := filepath.Join(dir, name)
		if old, err := os.ReadFile(path); err == nil && string(old) == string(b) {
			continue
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, b, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}
	return nil
}

// EmptyRuleSet is a valid source rule-set with no effective match.
func EmptyRuleSet() []byte {
	b, _ := json.MarshalIndent(M{"version": 3, "rules": []M{{"domain_suffix": []string{"rahgozar.invalid"}}}}, "", " ")
	return b
}

// Build assembles the full runtime configuration.
func Build(in *Input) (*Result, error) {
	st := in.Settings
	p := in.Profile
	if p == nil {
		return nil, errors.New("no profile selected")
	}
	res := &Result{}
	var outbounds, endpoints []M

	// ----- proxy outbound -----
	switch eng := EngineFor(p); eng {
	case model.EngineSingBox:
		if p.Type == model.TChain {
			obs, eps, err := buildChain(in, p)
			if err != nil {
				return nil, err
			}
			outbounds = append(outbounds, obs...)
			endpoints = append(endpoints, eps...)
		} else {
			ob, isEp, err := SingBoxOutbound(p, "proxy", &st)
			if err != nil {
				return nil, err
			}
			if isEp {
				endpoints = append(endpoints, ob)
			} else {
				outbounds = append(outbounds, ob)
			}
		}
	case model.EngineXray, model.EngineMihomo:
		port := in.PortAlloc()
		var cfg []byte
		var err error
		fname := "xray-run.json"
		if eng == model.EngineXray {
			cfg, err = XrayConfig(p, port, st.LogLevel)
		} else {
			cfg, err = MihomoConfig(p, port, st.LogLevel)
			fname = "mihomo-run.yaml"
		}
		if err != nil {
			return nil, err
		}
		res.Helpers = append(res.Helpers, Helper{Engine: eng, ProfileID: p.ID, Port: port, Config: cfg, FileName: fname})
		outbounds = append(outbounds, M{"type": "socks", "tag": "proxy", "server": "127.0.0.1", "server_port": port, "version": "5"})
	default:
		return nil, fmt.Errorf("engine %s cannot run inside sing-box", eng)
	}
	outbounds = append(outbounds, M{"type": "direct", "tag": "direct"})

	// ----- rule sets -----
	var ruleSets []M
	has := func(tag string) bool { return in.HasRuleFiles != nil && in.HasRuleFiles[tag] }
	for tag, file := range RuleSetFiles {
		if has(tag) {
			ruleSets = append(ruleSets, M{"type": "local", "tag": tag, "format": "binary", "path": filepath.Join(in.RulesDir, file)})
		}
	}
	smart := st.RoutingMode == "smart"
	// per-site user choices: hot-reloaded files, changeable without reconnecting
	for tag, file := range UserFiles {
		ruleSets = append(ruleSets, M{"type": "local", "tag": tag, "format": "source", "path": filepath.Join(in.LearnedDir, file)})
	}
	if smart {
		for tag, file := range LearnedFiles {
			ruleSets = append(ruleSets, M{"type": "local", "tag": tag, "format": "source", "path": filepath.Join(in.LearnedDir, file)})
		}
	}

	// ----- DNS -----
	directDNS := dnsServer(st.DirectDNS, "dns-direct", "")
	remoteDNS := dnsServer(st.RemoteDNS, "dns-remote", "proxy")
	if remoteDNS["type"] == "local" {
		remoteDNS = M{"type": "https", "tag": "dns-remote", "server": "1.1.1.1", "detour": "proxy"}
	}
	if h, ok := remoteDNS["server"].(string); ok && !isIP(h) {
		remoteDNS["domain_resolver"] = "dns-direct"
	}
	if h, ok := directDNS["server"].(string); ok && !isIP(h) {
		directDNS["domain_resolver"] = "dns-local"
	}
	dnsServers := []M{remoteDNS, directDNS, {"type": "local", "tag": "dns-local"}}
	var dnsRules []M
	var directDomainRule = M{}
	var directSets []string
	if st.BypassIran && st.RoutingMode != "global" {
		if has("geosite-ir") {
			directSets = append(directSets, "geosite-ir")
		}
	}
	if smart {
		directSets = append(directSets, "learned-direct")
	}
	dsfx := []string{}
	if st.BypassIran && st.RoutingMode != "global" {
		dsfx = append(dsfx, "ir")
	}
	if len(dsfx) > 0 {
		directDomainRule["domain_suffix"] = dsfx
	}
	if len(directSets) > 0 {
		directDomainRule["rule_set"] = directSets
	}
	dnsRules = append(dnsRules, M{"rule_set": []string{"user-proxy"}, "action": "route", "server": "dns-remote"})
	dnsRules = append(dnsRules, M{"rule_set": []string{"user-direct"}, "action": "route", "server": "dns-direct"})
	if smart {
		// a domain the learner says must be proxied wins over generic Iran rules
		dnsRules = append(dnsRules, M{"rule_set": []string{"learned-proxy"}, "action": "route", "server": "dns-remote"})
	}
	if len(directDomainRule) > 0 {
		r := M{"action": "route", "server": "dns-direct"}
		for k, v := range directDomainRule {
			r[k] = v
		}
		dnsRules = append(dnsRules, r)
	}
	dnsFinal := "dns-remote"
	if st.RoutingMode == "direct" {
		dnsFinal = "dns-direct"
	}
	dns := M{"servers": dnsServers, "final": dnsFinal, "strategy": "ipv4_only"}
	if st.IPv6 {
		dns["strategy"] = "prefer_ipv4"
	}
	if in.Mode == "tun" && st.FakeIP {
		dnsServers = append(dnsServers, M{"type": "fakeip", "tag": "dns-fake", "inet4_range": "198.18.0.0/15", "inet6_range": "fc00::/18"})
		dns["servers"] = dnsServers
		dnsRules = append(dnsRules, M{"query_type": []string{"A", "AAAA"}, "action": "route", "server": "dns-fake"})
	}
	if len(dnsRules) > 0 {
		dns["rules"] = dnsRules
	}

	// ----- route -----
	var rules []M
	if in.Mode == "tun" {
		rules = append(rules, M{"inbound": []string{"tun-in"}, "action": "sniff"})
		procs := []string{}
		if in.OS != "android" {
			if in.ExeName != "" {
				procs = append(procs, in.ExeName)
			}
			procs = append(procs, in.EngineExes...)
			procs = append(procs, st.DirectProcesses...)
		}
		if len(procs) > 0 {
			// before DNS hijack: our own probes and helper engines must see the real network
			rules = append(rules, M{"process_name": procs, "outbound": "direct"})
		}
		rules = append(rules, M{"protocol": "dns", "action": "hijack-dns"})
	} else if st.Sniff {
		rules = append(rules, M{"inbound": []string{"mixed-in"}, "action": "sniff"})
	}
	if st.BypassLAN {
		rules = append(rules, M{"ip_is_private": true, "outbound": "direct"})
	}
	rules = append(rules, M{"rule_set": []string{"user-block"}, "action": "reject"})
	if st.BlockAds {
		var sets []string
		for _, t := range []string{"geosite-category-ads-all", "geosite-malware", "geosite-phishing"} {
			if has(t) {
				sets = append(sets, t)
			}
		}
		if len(sets) > 0 {
			rules = append(rules, M{"rule_set": sets, "action": "reject"})
		}
	}
	rules = append(rules, M{"rule_set": []string{"user-proxy"}, "outbound": "proxy"})
	rules = append(rules, M{"rule_set": []string{"user-direct"}, "outbound": "direct"})
	final := "proxy"
	switch st.RoutingMode {
	case "smart":
		rules = append(rules, M{"rule_set": []string{"learned-proxy"}, "outbound": "proxy"})
		rules = append(rules, M{"rule_set": []string{"learned-direct"}, "outbound": "direct"})
		fallthrough
	case "rules":
		if st.BypassIran {
			r := M{"domain_suffix": []string{"ir"}, "outbound": "direct"}
			rules = append(rules, r)
			var sets []string
			for _, t := range []string{"geosite-ir", "geoip-ir"} {
				if has(t) {
					sets = append(sets, t)
				}
			}
			if len(sets) > 0 {
				rules = append(rules, M{"rule_set": sets, "outbound": "direct"})
			}
		}
	case "direct":
		final = "direct"
	}
	route := M{
		"rules":                   rules,
		"final":                   final,
		"auto_detect_interface":   true,
		"default_domain_resolver": "dns-direct",
	}
	if in.Mode == "tun" && in.OS != "android" {
		route["find_process"] = true
	}
	if len(ruleSets) > 0 {
		route["rule_set"] = ruleSets
	}

	// ----- inbounds -----
	listen := "127.0.0.1"
	if st.AllowLAN {
		listen = "0.0.0.0"
	}
	mixed := M{"type": "mixed", "tag": "mixed-in", "listen": listen, "listen_port": st.MixedPort}
	inbounds := []M{mixed}
	if in.Mode == "tun" {
		tun := M{
			"type":         "tun",
			"tag":          "tun-in",
			"address":      []string{"172.19.0.1/30"},
			"mtu":          firstInt(st.TunMTU, 9000),
			"auto_route":   true,
			"strict_route": st.StrictRoute,
			"stack":        firstOr(st.TunStack, "mixed"),
		}
		if st.IPv6 {
			tun["address"] = []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}
		}
		if in.OS == "android" {
			// per-app tunnel; our own app (probes, in-process Xray) never enters it
			var apps []string
			for _, a := range st.PerAppList {
				if a = strings.TrimSpace(a); a != "" && a != in.OwnPackage {
					apps = append(apps, a)
				}
			}
			switch {
			case st.PerAppMode == "include" && len(apps) > 0:
				tun["include_package"] = apps
			default:
				ex := []string{}
				if in.OwnPackage != "" {
					ex = append(ex, in.OwnPackage)
				}
				if st.PerAppMode == "exclude" {
					ex = append(ex, apps...)
				}
				if len(ex) > 0 {
					tun["exclude_package"] = ex
				}
			}
		} else {
			tun["interface_name"] = "Rahgozar"
		}
		inbounds = append([]M{tun}, inbounds...)
	}

	cfg := M{
		"log":       M{"level": firstOr(st.LogLevel, "warn"), "timestamp": true},
		"dns":       dns,
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     route,
	}
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
	}
	exp := M{}
	if in.ClashAddr != "" {
		exp["clash_api"] = M{"external_controller": in.ClashAddr, "secret": in.ClashSecret}
	}
	if in.CacheFile != "" {
		exp["cache_file"] = M{"enabled": true, "path": in.CacheFile, "store_fakeip": st.FakeIP}
	}
	if len(exp) > 0 {
		cfg["experimental"] = exp
	}
	res.SingBox = cfg
	return res, nil
}

func firstInt(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

func isIP(s string) bool {
	return strings.Count(s, ".") == 3 && strings.Trim(s, "0123456789.") == "" || strings.Contains(s, ":")
}

func CleanDomains(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		d = strings.TrimPrefix(d, "*.")
		d = strings.TrimPrefix(d, "http://")
		d = strings.TrimPrefix(d, "https://")
		d = strings.TrimSuffix(strings.SplitN(d, "/", 2)[0], ".")
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// TestInput describes a batch latency test.
type TestInput struct {
	Profiles    []*model.Profile
	ClashAddr   string
	ClashSecret string
	Settings    model.Settings
}

// BuildTest returns a sing-box config with one outbound per sing-box-native
// profile (tag = "t-<id>"), plus the ids that could not be included.
func BuildTest(in *TestInput) (M, []string) {
	var obs, eps []M
	var skipped []string
	for _, p := range in.Profiles {
		if EngineFor(p) != model.EngineSingBox || p.Type == model.TChain {
			skipped = append(skipped, p.ID)
			continue
		}
		ob, isEp, err := SingBoxOutbound(p, "t-"+p.ID, &in.Settings)
		if err != nil {
			skipped = append(skipped, p.ID)
			continue
		}
		if isEp {
			eps = append(eps, ob)
		} else {
			obs = append(obs, ob)
		}
	}
	obs = append(obs, M{"type": "direct", "tag": "direct"})
	cfg := M{
		"log":          M{"level": "error"},
		"dns":          M{"servers": []M{{"type": "udp", "tag": "d", "server": "8.8.8.8"}, {"type": "local", "tag": "l"}}},
		"outbounds":    obs,
		"route":        M{"final": "direct", "auto_detect_interface": true, "default_domain_resolver": "d"},
		"experimental": M{"clash_api": M{"external_controller": in.ClashAddr, "secret": in.ClashSecret}},
	}
	if len(eps) > 0 {
		cfg["endpoints"] = eps
	}
	return cfg, skipped
}
