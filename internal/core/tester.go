package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"rahgozar/internal/builder"
	"rahgozar/internal/engine"
	"rahgozar/internal/model"
	"rahgozar/internal/smart"
	"rahgozar/internal/sys"
)

// TestResult is the latency test outcome of one profile.
type TestResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Ms    int    `json:"ms"`
	Error string `json:"error,omitempty"`
	NA    bool   `json:"na,omitempty"`
}

var testMu sync.Mutex

// TestProfiles measures real latency (HTTP through each server). Results are
// streamed with the "test" event and fed to the learner.
func (c *Core) TestProfiles(ids []string) []TestResult {
	testMu.Lock()
	defer testMu.Unlock()
	set := c.Store.Settings()
	netID, netLabel := sys.NetworkID()
	var sb, helpers []*model.Profile
	var results []TestResult
	var rmu sync.Mutex
	emit := func(r TestResult, p *model.Profile) {
		rmu.Lock()
		results = append(results, r)
		rmu.Unlock()
		if !r.NA && p != nil {
			c.Learner.Record(p.ID, p.Family(), netID, netLabel, r.OK, r.Ms)
		}
		c.Emit("test", r)
	}
	byID := map[string]*model.Profile{}
	for _, id := range ids {
		p := c.Store.Profile(id)
		if p == nil {
			continue
		}
		byID[id] = p
		switch eng := builder.EngineFor(p); {
		case p.IsRAS() || p.Type == model.TChain:
			emit(TestResult{ID: id, NA: true, Error: "تست فقط با اتصال"}, nil)
		case eng == model.EngineSingBox:
			sb = append(sb, p)
		default:
			helpers = append(helpers, p)
		}
	}
	c.Emit("testStart", map[string]any{"ids": ids})
	var wg sync.WaitGroup
	if len(sb) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.testSingBox(sb, set, func(r TestResult) { emit(r, byID[r.ID]) })
		}()
	}
	if len(helpers) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem := make(chan struct{}, 4)
			var hw sync.WaitGroup
			for _, p := range helpers {
				hw.Add(1)
				sem <- struct{}{}
				go func(p *model.Profile) {
					defer hw.Done()
					defer func() { <-sem }()
					emit(c.testHelper(p, set), p)
				}(p)
			}
			hw.Wait()
		}()
	}
	wg.Wait()
	c.Emit("testDone", nil)
	c.SaveLearning()
	return results
}

func (c *Core) testSingBox(ps []*model.Profile, set model.Settings, out func(TestResult)) {
	exe, err := c.Eng.ExePath("sing-box")
	if err != nil {
		for _, p := range ps {
			out(TestResult{ID: p.ID, Error: err.Error()})
		}
		return
	}
	addr := "127.0.0.1:" + strconv.Itoa(FreePort())
	secret := randomHex(8)
	cfg, skipped := builder.BuildTest(&builder.TestInput{Profiles: ps, ClashAddr: addr, ClashSecret: secret, Settings: set})
	skip := map[string]bool{}
	for _, id := range skipped {
		skip[id] = true
		out(TestResult{ID: id, Error: "پیکربندی نامعتبر"})
	}
	dir := c.runtimeDir()
	path := filepath.Join(dir, "test-"+randomHex(3)+".json")
	b, _ := json.Marshal(cfg)
	os.WriteFile(path, b, 0o600)
	defer os.Remove(path)
	proc, err := engine.Start("sing-box(test)", exe, filepath.Dir(exe), []string{"run", "-c", path, "--disable-color"}, nil)
	if err != nil {
		for _, p := range ps {
			if !skip[p.ID] {
				out(TestResult{ID: p.ID, Error: err.Error()})
			}
		}
		return
	}
	defer proc.Stop()
	tc := &Core{clash: addr, secret: secret}
	if err := tc.waitClash(proc, 12*time.Second); err != nil {
		for _, p := range ps {
			if !skip[p.ID] {
				out(TestResult{ID: p.ID, Error: err.Error()})
			}
		}
		return
	}
	to := set.TestTimeoutMs
	if to <= 0 {
		to = 5000
	}
	conc := set.TestConcurrency
	if conc <= 0 {
		conc = 16
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for _, p := range ps {
		if skip[p.ID] {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(p *model.Profile) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(to+3000)*time.Millisecond)
			defer cancel()
			var r struct {
				Delay int `json:"delay"`
			}
			q := url.Values{"url": {set.TestURL}, "timeout": {strconv.Itoa(to)}}
			err := tc.clashGet(ctx, "/proxies/"+url.PathEscape("t-"+p.ID)+"/delay?"+q.Encode(), &r)
			if err != nil || r.Delay <= 0 {
				msg := "timeout"
				if err != nil {
					msg = err.Error()
				}
				out(TestResult{ID: p.ID, Error: msg})
				return
			}
			out(TestResult{ID: p.ID, OK: true, Ms: r.Delay})
		}(p)
	}
	wg.Wait()
}

func (c *Core) testHelper(p *model.Profile, set model.Settings) TestResult {
	eng := builder.EngineFor(p)
	exe, err := c.Eng.ExePath(eng)
	if err != nil {
		return TestResult{ID: p.ID, Error: err.Error()}
	}
	port := FreePort()
	var cfg []byte
	name := "xt-" + randomHex(3)
	var args []string
	dir := c.runtimeDir()
	if eng == model.EngineXray {
		cfg, err = builder.XrayConfig(p, port, "warning")
		name += ".json"
		args = []string{"run", "-c", filepath.Join(dir, name)}
	} else {
		cfg, err = builder.MihomoConfig(p, port, "warning")
		name += ".yaml"
		md := filepath.Join(dir, "mihomo")
		os.MkdirAll(md, 0o755)
		args = []string{"-d", md, "-f", filepath.Join(dir, name)}
	}
	if err != nil {
		return TestResult{ID: p.ID, Error: err.Error()}
	}
	os.WriteFile(filepath.Join(dir, name), cfg, 0o600)
	defer os.Remove(filepath.Join(dir, name))
	proc, err := engine.Start(eng+"(test)", exe, filepath.Dir(exe), args, nil)
	if err != nil {
		return TestResult{ID: p.ID, Error: err.Error()}
	}
	defer proc.Stop()
	if err := waitPort(proc, port, 6*time.Second); err != nil {
		return TestResult{ID: p.ID, Error: err.Error()}
	}
	to := set.TestTimeoutMs
	if to <= 0 {
		to = 5000
	}
	ms, err := httpDelay("socks5://127.0.0.1:"+strconv.Itoa(port), set.TestURL, time.Duration(to)*time.Millisecond)
	if err != nil {
		return TestResult{ID: p.ID, Error: err.Error()}
	}
	return TestResult{ID: p.ID, OK: true, Ms: ms}
}

// httpDelay performs two requests through a proxy and reports the second
// (connection-warm) round trip, like sing-box's URL test.
func httpDelay(proxy, target string, timeout time.Duration) (int, error) {
	pu, _ := url.Parse(proxy)
	tr := &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: false}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: timeout}
	start := time.Now()
	resp, err := cl.Get(target)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	first := time.Since(start)
	start = time.Now()
	resp, err = cl.Get(target)
	if err != nil {
		return int(first.Milliseconds()), nil
	}
	resp.Body.Close()
	return int(time.Since(start).Milliseconds()), nil
}

// ChooseBest ranks profiles with the learner, live-tests the top candidates
// and returns the best one.
func (c *Core) ChooseBest(netID string, exclude map[string]bool) (*model.Profile, error) {
	all := c.Store.Profiles()
	var cands []smart.Candidate
	byID := map[string]*model.Profile{}
	c.mu.Lock()
	for id, t := range c.exclude {
		if time.Since(t) > 10*time.Minute {
			delete(c.exclude, id)
		}
	}
	recentBad := map[string]bool{}
	for id := range c.exclude {
		recentBad[id] = true
	}
	c.mu.Unlock()
	for _, p := range all {
		if p.IsRAS() || p.Type == model.TChain || exclude[p.ID] {
			continue
		}
		if _, err := c.Eng.ExePath(builder.EngineFor(p)); err != nil {
			continue
		}
		cands = append(cands, smart.Candidate{ID: p.ID, Family: p.Family()})
		byID[p.ID] = p
	}
	if len(cands) == 0 {
		if len(all) == 0 {
			return nil, errors.New("هیچ سروری اضافه نشده است")
		}
		return nil, errors.New("سرور قابل استفاده‌ای نیست (موتورها را نصب کنید)")
	}
	ranked := c.Learner.Rank(cands, netID)
	// top candidates by learned score + a few unexplored ones
	pick := []string{}
	seen := map[string]bool{}
	for _, r := range ranked {
		if len(pick) >= 10 {
			break
		}
		if recentBad[r.ID] && len(ranked) > 3 {
			continue
		}
		pick = append(pick, r.ID)
		seen[r.ID] = true
	}
	explore := 0
	for _, r := range ranked {
		if explore >= 4 {
			break
		}
		if !seen[r.ID] && r.Samples == 0 {
			pick = append(pick, r.ID)
			seen[r.ID] = true
			explore++
		}
	}
	c.Emit("toast", map[string]string{"kind": "info", "text": fmt.Sprintf("تست %d سرور برتر برای انتخاب بهترین…", len(pick))})
	res := c.TestProfiles(pick)
	scoreOf := map[string]float64{}
	for _, r := range ranked {
		scoreOf[r.ID] = r.Score
	}
	type cand struct {
		id    string
		value float64
	}
	var ok []cand
	for _, r := range res {
		if !r.OK {
			continue
		}
		// live latency dominates, learned score breaks ties / stabilises choice
		v := 1/(1+float64(r.Ms)/300)*0.7 + scoreOf[r.ID]*0.3
		ok = append(ok, cand{r.ID, v})
	}
	if len(ok) == 0 {
		return nil, errors.New("هیچ‌کدام از سرورها پاسخ ندادند")
	}
	sort.Slice(ok, func(i, j int) bool { return ok[i].value > ok[j].value })
	best := byID[ok[0].id]
	c.Log("smart", fmt.Sprintf("best server: %s (%s)", best.Name, best.Family()))
	return best, nil
}
