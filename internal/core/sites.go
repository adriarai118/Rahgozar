package core

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"rahgozar/internal/builder"
	"rahgozar/internal/model"
	"rahgozar/internal/smart"
)

// Site actions.
const (
	SiteProxy  = "proxy"
	SiteDirect = "direct"
	SiteBlock  = "block"
	SiteAuto   = "auto"
)

// SiteInfo describes how one site is routed.
type SiteInfo struct {
	Host     string `json:"host"`
	Base     string `json:"base"`
	Rule     string `json:"rule"`     // user choice: proxy | direct | block | auto
	RuleOn   string `json:"ruleOn"`   // the domain entry that matched (may be a parent)
	Learned  string `json:"learned"`  // learner verdict
	Reason   string `json:"reason"`   // learner reason
	Route    string `json:"route"`    // effective: proxy | direct | block
	Why      string `json:"why"`      // human explanation
	Routing  string `json:"routing"`  // routing mode
	Iranian  bool   `json:"iranian"`
	Checked  int64  `json:"checked"`
	Learning bool   `json:"learning"` // learner active (smart mode)
}

// NormalizeHost strips scheme, port, path and "www.".
func NormalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	for _, p := range []string{"http://", "https://"} {
		h = strings.TrimPrefix(h, p)
	}
	h = strings.SplitN(h, "/", 2)[0]
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "*."), ".")
	return h
}

func matchList(host string, list []string) string {
	for _, d := range builder.CleanDomains(list) {
		if host == d || strings.HasSuffix(host, "."+d) {
			return d
		}
	}
	return ""
}

// WriteUserRules syncs the per-site user lists to sing-box rule-set files.
func (c *Core) WriteUserRules() error {
	st := c.Store.Settings()
	return builder.WriteUserRules(filepath.Join(c.DataDir, "learned"), st.ProxyDomains, st.DirectDomains, st.BlockDomains)
}

// Site returns routing information for a host.
func (c *Core) Site(host string) SiteInfo {
	host = NormalizeHost(host)
	st := c.Store.Settings()
	base := smart.BaseDomain(host)
	si := SiteInfo{Host: host, Base: base, Rule: SiteAuto, Routing: st.RoutingMode, Learning: st.RoutingMode == "smart" && st.LearnDomains}
	if host == "" {
		return si
	}
	si.Iranian = strings.HasSuffix(host, ".ir") || host == "ir"
	if d := matchList(host, st.BlockDomains); d != "" {
		si.Rule, si.RuleOn = SiteBlock, d
	} else if d := matchList(host, st.ProxyDomains); d != "" {
		si.Rule, si.RuleOn = SiteProxy, d
	} else if d := matchList(host, st.DirectDomains); d != "" {
		si.Rule, si.RuleOn = SiteDirect, d
	}
	for _, d := range c.Domains.List() {
		if d.Domain == base {
			si.Learned, si.Reason, si.Checked = d.Verdict, d.Reason, d.Checked
			break
		}
	}
	switch {
	case si.Rule == SiteBlock:
		si.Route, si.Why = "block", "مسدود شده توسط شما"
	case si.Rule == SiteProxy:
		si.Route, si.Why = "proxy", "شما گفته‌اید همیشه از تونل برود"
	case si.Rule == SiteDirect:
		si.Route, si.Why = "direct", "شما گفته‌اید همیشه مستقیم برود"
	case st.RoutingMode == "global":
		si.Route, si.Why = "proxy", "حالت «همه از تونل»"
	case st.RoutingMode == "direct":
		si.Route, si.Why = "direct", "حالت «مستقیم»"
	case st.RoutingMode == "smart" && si.Learned == smart.VerdictProxy:
		si.Route, si.Why = "proxy", "یادگیری: "+si.Reason
	case st.RoutingMode == "smart" && si.Learned == smart.VerdictDirect:
		si.Route, si.Why = "direct", "یادگیری: "+si.Reason
	case st.BypassIran && si.Iranian:
		si.Route, si.Why = "direct", "سایت ایرانی"
	default:
		si.Route, si.Why = "proxy", "پیش‌فرض از تونل (سایت‌های ایرانی شناخته‌شده مستقیم می‌روند)"
		if st.RoutingMode == "smart" {
			si.Why = "هنوز یاد گرفته نشده؛ فعلاً از تونل"
		}
	}
	return si
}

func removeDomain(list []string, host, base string) []string {
	var out []string
	for _, d := range list {
		n := NormalizeHost(d)
		if n == host || n == base {
			continue
		}
		out = append(out, d)
	}
	return out
}

// SetSite applies a per-site choice. scope "site" uses the base domain
// (example.com covers all subdomains), "host" only the exact host.
func (c *Core) SetSite(host, action, scope string) (SiteInfo, error) {
	host = NormalizeHost(host)
	if host == "" {
		return SiteInfo{}, errBadHost
	}
	base := smart.BaseDomain(host)
	target := base
	if scope == "host" {
		target = host
	}
	st := c.Store.Settings()
	st.ProxyDomains = removeDomain(st.ProxyDomains, host, base)
	st.DirectDomains = removeDomain(st.DirectDomains, host, base)
	st.BlockDomains = removeDomain(st.BlockDomains, host, base)
	switch action {
	case SiteProxy:
		st.ProxyDomains = append(st.ProxyDomains, target)
	case SiteDirect:
		st.DirectDomains = append(st.DirectDomains, target)
	case SiteBlock:
		st.BlockDomains = append(st.BlockDomains, target)
	case SiteAuto, "":
	default:
		return SiteInfo{}, errBadAction
	}
	if err := c.Store.SetSettings(st); err != nil {
		return SiteInfo{}, err
	}
	if err := c.WriteUserRules(); err != nil {
		return SiteInfo{}, err
	}
	c.Log("site", host+" → "+firstOr(action, "auto"))
	go func() {
		// let sing-box reload the rule file, then drop live connections so the new route applies now
		time.Sleep(900 * time.Millisecond)
		c.closeConnections(base)
	}()
	c.Emit("settings", st)
	return c.Site(host), nil
}

// closeConnections closes live connections to a site so they reconnect via the new route.
func (c *Core) closeConnections(base string) {
	if c.Status().State != "connected" || c.clash == "" {
		return
	}
	var snap struct {
		Connections []struct {
			ID       string `json:"id"`
			Metadata struct {
				Host string `json:"host"`
			} `json:"metadata"`
		} `json:"connections"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c.clashGet(ctx, "/connections", &snap) != nil {
		return
	}
	for _, cn := range snap.Connections {
		h := NormalizeHost(cn.Metadata.Host)
		if h == base || strings.HasSuffix(h, "."+base) {
			req, _ := http.NewRequestWithContext(ctx, "DELETE", "http://"+c.clash+"/connections/"+cn.ID, nil)
			req.Header.Set("Authorization", "Bearer "+c.secret)
			if resp, err := localClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
}

// ProbeSite tests a site right now (direct vs tunnel) and records the result.
func (c *Core) ProbeSite(host string) SiteInfo {
	host = NormalizeHost(host)
	base := smart.BaseDomain(host)
	v, reason := c.prober.Probe(base)
	if v == smart.VerdictUnknown && host != base {
		v, reason = c.prober.Probe(host)
	}
	c.Domains.Record(base, v, reason)
	si := c.Site(host)
	if si.Reason == "" {
		si.Learned, si.Reason = v, reason
	}
	return si
}

var _ = model.TChain
