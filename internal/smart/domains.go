package smart

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Domain verdicts.
const (
	VerdictDirect  = "direct"
	VerdictProxy   = "proxy"
	VerdictUnknown = "unknown"
)

// DomainInfo is what the learner knows about one (base) domain.
type DomainInfo struct {
	Domain  string `json:"domain"`
	Verdict string `json:"verdict"`
	Source  string `json:"source"` // auto | user
	Reason  string `json:"reason"`
	Checked int64  `json:"checked"`
	Hits    int    `json:"hits"`
	Fails   int    `json:"fails"`
}

// ProbeFunc classifies a domain: returns verdict and a human reason.
type ProbeFunc func(domain string) (string, string)

// Domains learns which sites must go through the tunnel.
type Domains struct {
	mu      sync.Mutex
	items   map[string]*DomainInfo
	queue   chan string
	queued  map[string]bool
	dir     string
	probe   ProbeFunc
	OnLearn func(*DomainInfo)
	skip    func(string) bool
	dirty   bool
	writeCh chan struct{}
	started bool
}

// NewDomains loads learned domains from dir.
func NewDomains(dir string) *Domains {
	d := &Domains{items: map[string]*DomainInfo{}, queue: make(chan string, 4096), queued: map[string]bool{}, dir: dir, writeCh: make(chan struct{}, 1)}
	b, err := os.ReadFile(filepath.Join(dir, "domains.json"))
	if err == nil {
		var list []*DomainInfo
		if json.Unmarshal(b, &list) == nil {
			for _, i := range list {
				d.items[i.Domain] = i
			}
		}
	}
	return d
}

// Start launches probe workers.
func (d *Domains) Start(probe ProbeFunc, skip func(string) bool, workers int) {
	d.mu.Lock()
	d.probe = probe
	d.skip = skip
	if d.started {
		d.mu.Unlock()
		return
	}
	d.started = true
	d.mu.Unlock()
	for i := 0; i < workers; i++ {
		go d.worker()
	}
	go d.writer()
	d.WriteRuleFiles()
}

func (d *Domains) worker() {
	for dom := range d.queue {
		d.mu.Lock()
		probe := d.probe
		d.mu.Unlock()
		if probe == nil {
			continue
		}
		v, reason := probe(dom)
		d.mu.Lock()
		delete(d.queued, dom)
		it := d.items[dom]
		if it == nil {
			it = &DomainInfo{Domain: dom, Source: "auto"}
			d.items[dom] = it
		}
		if it.Source == "user" {
			d.mu.Unlock()
			continue
		}
		changed := it.Verdict != v
		if v == VerdictUnknown {
			it.Fails++
			// keep previous verdict; retry later
			if it.Verdict == "" {
				it.Verdict = VerdictUnknown
			}
			changed = false
		} else {
			it.Verdict = v
			it.Fails = 0
		}
		it.Reason = reason
		it.Checked = time.Now().Unix()
		cp := *it
		d.dirty = true
		d.mu.Unlock()
		if changed && d.OnLearn != nil {
			d.OnLearn(&cp)
		}
		d.kick()
	}
}

func (d *Domains) kick() {
	select {
	case d.writeCh <- struct{}{}:
	default:
	}
}

func (d *Domains) writer() {
	for range d.writeCh {
		time.Sleep(1500 * time.Millisecond)
		d.WriteRuleFiles()
	}
}

var twoLevel = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true, "com.au": true, "net.au": true, "co.jp": true,
	"com.br": true, "com.tr": true, "co.ir": true, "ac.ir": true, "org.ir": true, "gov.ir": true, "net.ir": true,
	"com.cn": true, "co.in": true, "co.kr": true, "com.sg": true, "com.hk": true, "co.za": true, "com.mx": true,
	"github.io": true, "pages.dev": true, "workers.dev": true, "vercel.app": true, "herokuapp.com": true,
	"blogspot.com": true, "cloudfront.net": true, "azurewebsites.net": true, "appspot.com": true, "netlify.app": true,
}

// BaseDomain approximates the registrable domain (eTLD+1).
func BaseDomain(host string) string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	last2 := strings.Join(parts[len(parts)-2:], ".")
	if twoLevel[last2] && len(parts) >= 3 {
		return strings.Join(parts[len(parts)-3:], ".")
	}
	return last2
}

// Observe is called for every host seen in traffic.
func (d *Domains) Observe(host string) {
	if host == "" || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return
	}
	base := BaseDomain(host)
	if strings.HasSuffix(base, ".ir") || strings.HasSuffix(base, ".local") || strings.HasSuffix(base, ".lan") || base == "rahgozar.invalid" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.skip != nil && d.skip(base) {
		return
	}
	it := d.items[base]
	now := time.Now().Unix()
	if it != nil {
		it.Hits++
		if it.Source == "user" {
			return
		}
		age := now - it.Checked
		var ttl int64
		switch it.Verdict {
		case VerdictDirect:
			ttl = 24 * 3600
		case VerdictProxy:
			ttl = 5 * 24 * 3600
		default:
			ttl = int64(min(3600, 120*(1<<min(it.Fails, 5))))
		}
		if age < ttl {
			return
		}
	} else {
		d.items[base] = &DomainInfo{Domain: base, Verdict: VerdictUnknown, Source: "auto", Hits: 1}
	}
	if d.queued[base] || d.probe == nil {
		return
	}
	select {
	case d.queue <- base:
		d.queued[base] = true
	default:
	}
}

// Record stores a probe result made outside the worker queue.
func (d *Domains) Record(domain, verdict, reason string) {
	if verdict == VerdictUnknown || domain == "" {
		return
	}
	d.mu.Lock()
	it := d.items[domain]
	if it == nil {
		it = &DomainInfo{Domain: domain, Source: "auto"}
		d.items[domain] = it
	}
	if it.Source == "user" {
		d.mu.Unlock()
		return
	}
	it.Verdict, it.Reason, it.Checked, it.Fails = verdict, reason, time.Now().Unix(), 0
	cp := *it
	d.mu.Unlock()
	if d.OnLearn != nil {
		d.OnLearn(&cp)
	}
	d.WriteRuleFiles()
}

// Set forces a verdict chosen by the user ("" or "auto" = relearn).
func (d *Domains) Set(domain, verdict string) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return
	}
	d.mu.Lock()
	if verdict == "" || verdict == "auto" {
		delete(d.items, domain)
	} else {
		d.items[domain] = &DomainInfo{Domain: domain, Verdict: verdict, Source: "user", Reason: "user", Checked: time.Now().Unix()}
	}
	d.dirty = true
	d.mu.Unlock()
	d.WriteRuleFiles()
}

// Reset forgets all automatically learned domains (keeps user choices).
func (d *Domains) Reset() {
	d.mu.Lock()
	for k, v := range d.items {
		if v.Source != "user" {
			delete(d.items, k)
		}
	}
	d.dirty = true
	d.mu.Unlock()
	d.WriteRuleFiles()
}

// List returns all known domains, most used first.
func (d *Domains) List() []DomainInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]DomainInfo, 0, len(d.items))
	for _, v := range d.items {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hits != out[j].Hits {
			return out[i].Hits > out[j].Hits
		}
		return out[i].Domain < out[j].Domain
	})
	return out
}

// Counts returns direct/proxy/unknown counts.
func (d *Domains) Counts() (direct, proxy, unknown int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, v := range d.items {
		switch v.Verdict {
		case VerdictDirect:
			direct++
		case VerdictProxy:
			proxy++
		default:
			unknown++
		}
	}
	return
}

// WriteRuleFiles writes learned-direct.json / learned-proxy.json (sing-box
// source rule-sets, hot-reloaded by sing-box) and domains.json.
func (d *Domains) WriteRuleFiles() {
	d.mu.Lock()
	var direct, proxy []string
	list := make([]*DomainInfo, 0, len(d.items))
	for _, v := range d.items {
		list = append(list, v)
		switch v.Verdict {
		case VerdictDirect:
			direct = append(direct, v.Domain)
		case VerdictProxy:
			proxy = append(proxy, v.Domain)
		}
	}
	d.dirty = false
	d.mu.Unlock()
	sort.Strings(direct)
	sort.Strings(proxy)
	os.MkdirAll(d.dir, 0o755)
	write := func(name string, doms []string) {
		if len(doms) == 0 {
			doms = []string{"rahgozar.invalid"}
		}
		b, _ := json.MarshalIndent(map[string]any{"version": 3, "rules": []map[string]any{{"domain_suffix": doms}}}, "", " ")
		path := filepath.Join(d.dir, name)
		if old, err := os.ReadFile(path); err == nil && string(old) == string(b) {
			return
		}
		tmp := path + ".tmp"
		os.WriteFile(tmp, b, 0o644)
		os.Rename(tmp, path)
	}
	write("learned-direct.json", direct)
	write("learned-proxy.json", proxy)
	b, _ := json.MarshalIndent(list, "", " ")
	os.WriteFile(filepath.Join(d.dir, "domains.json"), b, 0o644)
}

// Import adds domains this device hasn't learned yet (backup restore); it
// never overrides what is already known here. Returns how many were added.
func (d *Domains) Import(list []DomainInfo) int {
	d.mu.Lock()
	n := 0
	for i := range list {
		v := list[i]
		v.Domain = strings.ToLower(strings.TrimSpace(v.Domain))
		if v.Domain == "" || d.items[v.Domain] != nil {
			continue
		}
		d.items[v.Domain] = &v
		n++
	}
	if n > 0 {
		d.dirty = true
	}
	d.mu.Unlock()
	if n > 0 {
		d.WriteRuleFiles()
	}
	return n
}
