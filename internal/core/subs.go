package core

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rahgozar/internal/model"
	"rahgozar/internal/parser"
)

// HTTPClient returns a client that goes through the tunnel when connected
// (and viaProxy is requested), otherwise direct.
func (c *Core) HTTPClient(viaProxy bool, timeout time.Duration) *http.Client {
	tr := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 60 * time.Second}
	if viaProxy && c.Status().State == "connected" && c.Status().Mode != "system" {
		pu, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(c.Store.Settings().MixedPort))
		tr.Proxy = http.ProxyURL(pu)
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// UpdateSubscription downloads and re-parses one subscription.
func (c *Core) UpdateSubscription(id string) (int, error) {
	var sub *model.Subscription
	for _, s := range c.Store.Subscriptions() {
		if s.ID == id {
			sub = s
		}
	}
	if sub == nil {
		return 0, errors.New("subscription not found")
	}
	n, err := c.fetchSub(sub)
	sub.LastUpdate = time.Now().Unix()
	if err != nil {
		sub.LastError = err.Error()
	} else {
		sub.LastError = ""
		sub.Count = n
	}
	c.Store.UpsertSub(sub)
	c.Emit("subs", nil)
	if err == nil && sub.AutoPrune {
		go func() {
			if k, perr := c.PruneDead(c.groupIDs(sub.ID)); perr != nil {
				c.Log("subs", sub.Name+": prune skipped: "+perr.Error())
			} else if k > 0 {
				c.Log("subs", fmt.Sprintf("%s: removed %d dead servers", sub.Name, k))
			}
		}()
	}
	return n, err
}

func (c *Core) groupIDs(group string) []string {
	var ids []string
	for _, p := range c.Store.Profiles() {
		if p.Group == group {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// ErrAllDead is returned by PruneDead when every tested server failed, which
// almost always means the network is down rather than the servers.
var ErrAllDead = errors.New("همه سرورها قطع بودند؛ احتمالاً اینترنت یا شبکه مشکل دارد، چیزی حذف نشد")

// PruneDead tests the given servers and removes the ones that failed.
// The selected and the connected server are never removed.
func (c *Core) PruneDead(ids []string) (int, error) {
	return c.RemoveDead(c.TestProfiles(ids), true)
}

// RemoveDead deletes servers whose test failed. With guardAll it refuses
// when every tested server failed.
func (c *Core) RemoveDead(res []TestResult, guardAll bool) (int, error) {
	keep := map[string]bool{c.Store.Settings().SelectedID: true, c.Status().ProfileID: true}
	var dead []string
	tested := 0
	for _, r := range res {
		if r.NA {
			continue
		}
		tested++
		if !r.OK && !keep[r.ID] {
			dead = append(dead, r.ID)
		}
	}
	if tested == 0 || len(dead) == 0 {
		return 0, nil
	}
	if guardAll && len(dead) >= tested && tested > 1 {
		return 0, ErrAllDead
	}
	c.Learner.Forget(dead)
	if err := c.Store.DeleteProfiles(dead); err != nil {
		return 0, err
	}
	c.recountSubs()
	c.Emit("profiles", nil)
	return len(dead), nil
}

// RecountSubs refreshes each subscription's server count.
func (c *Core) RecountSubs() { c.recountSubs() }

func (c *Core) recountSubs() {
	n := map[string]int{}
	for _, p := range c.Store.Profiles() {
		n[p.Group]++
	}
	for _, s := range c.Store.Subscriptions() {
		if s.Count != n[s.ID] {
			s.Count = n[s.ID]
			c.Store.UpsertSub(s)
		}
	}
	c.Emit("subs", nil)
}

func (c *Core) fetchSub(sub *model.Subscription) (int, error) {
	try := func(via bool) (*http.Response, error) {
		req, err := http.NewRequest("GET", strings.TrimSpace(sub.URL), nil)
		if err != nil {
			return nil, err
		}
		ua := sub.UserAgent
		if ua == "" {
			ua = "v2rayN/7.4"
		}
		req.Header.Set("User-Agent", ua)
		return c.HTTPClient(via, 45*time.Second).Do(req)
	}
	resp, err := try(sub.ViaProxy)
	if err != nil && sub.ViaProxy {
		resp, err = try(false)
	} else if err != nil && !sub.ViaProxy && c.Status().State == "connected" {
		resp, err = try(true)
	}
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return 0, err
	}
	if info := resp.Header.Get("subscription-userinfo"); info != "" {
		for _, kv := range strings.Split(info, ";") {
			p := strings.SplitN(strings.TrimSpace(kv), "=", 2)
			if len(p) != 2 {
				continue
			}
			v, _ := strconv.ParseInt(strings.TrimSpace(p[1]), 10, 64)
			switch strings.TrimSpace(p[0]) {
			case "upload":
				sub.Upload = v
			case "download":
				sub.Download = v
			case "total":
				sub.Total = v
			case "expire":
				sub.Expire = v
			}
		}
	}
	ps, errs := parser.ParseText(string(body))
	if len(ps) == 0 {
		if len(errs) > 0 {
			return 0, errs[0]
		}
		return 0, errors.New("هیچ سروری در اشتراک پیدا نشد")
	}
	if err := c.Store.ReplaceGroup(sub.ID, ps); err != nil {
		return 0, err
	}
	c.Log("subs", fmt.Sprintf("%s: %d servers", sub.Name, len(ps)))
	return len(ps), nil
}

func (c *Core) subscriptionLoop() {
	time.Sleep(20 * time.Second)
	for {
		for _, s := range c.Store.Subscriptions() {
			iv := s.IntervalH
			if iv <= 0 {
				iv = 12
			}
			if s.AutoUpdate && time.Since(time.Unix(s.LastUpdate, 0)) > time.Duration(iv)*time.Hour {
				c.UpdateSubscription(s.ID)
				c.Emit("profiles", nil)
			}
		}
		time.Sleep(30 * time.Minute)
	}
}
