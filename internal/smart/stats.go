// Package smart implements Rahgozar's learning features:
//   - server quality learning per network and time of day (best-server choice)
//   - protocol-family health per network (which protocols currently work)
//   - domain learning for smart routing (which sites need the tunnel)
package smart

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Stat is the learned quality of one server (or protocol family) in a context.
type Stat struct {
	N        int     `json:"n"`        // samples
	OK       int     `json:"ok"`       // successes
	Success  float64 `json:"success"`  // EMA success rate 0..1
	Latency  float64 `json:"latency"`  // EMA latency ms (successful samples)
	LastOK   int64   `json:"lastOk"`   // unix
	LastFail int64   `json:"lastFail"` // unix
	Streak   int     `json:"streak"`   // consecutive failures
	Uptime   float64 `json:"uptime"`   // seconds connected while healthy
	PeakBps  float64 `json:"peakBps"`  // best observed download rate
}

const alpha = 0.3

func (s *Stat) add(ok bool, latency int, now int64) {
	s.N++
	v := 0.0
	if ok {
		v = 1
		s.OK++
		s.LastOK = now
		s.Streak = 0
		if latency > 0 {
			if s.Latency == 0 {
				s.Latency = float64(latency)
			} else {
				s.Latency = s.Latency*(1-alpha) + float64(latency)*alpha
			}
		}
	} else {
		s.LastFail = now
		s.Streak++
	}
	if s.N == 1 {
		s.Success = v
	} else {
		s.Success = s.Success*(1-alpha) + v*alpha
	}
}

// Learner holds all learned server/protocol statistics.
type Learner struct {
	mu sync.Mutex
	// Servers[profileID][ctxKey]
	Servers map[string]map[string]*Stat `json:"servers"`
	// Families[netID][family]
	Families map[string]map[string]*Stat `json:"families"`
	Networks map[string]string           `json:"networks"` // netID -> label
	Total    int                         `json:"total"`
	dirty    bool
}

// NewLearner returns an empty learner.
func NewLearner() *Learner {
	return &Learner{Servers: map[string]map[string]*Stat{}, Families: map[string]map[string]*Stat{}, Networks: map[string]string{}}
}

func hourBucket(t time.Time) string { return strconv.Itoa(t.Hour() / 6) }

func (l *Learner) ensure() {
	if l.Servers == nil {
		l.Servers = map[string]map[string]*Stat{}
	}
	if l.Families == nil {
		l.Families = map[string]map[string]*Stat{}
	}
	if l.Networks == nil {
		l.Networks = map[string]string{}
	}
}

func (l *Learner) stat(m map[string]map[string]*Stat, a, b string) *Stat {
	if m[a] == nil {
		m[a] = map[string]*Stat{}
	}
	if m[a][b] == nil {
		m[a][b] = &Stat{}
	}
	return m[a][b]
}

// Record stores one test / health-check result.
func (l *Learner) Record(profileID, family, netID, netLabel string, ok bool, latencyMs int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensure()
	now := time.Now()
	ts := now.Unix()
	l.Total++
	l.dirty = true
	if netLabel != "" {
		l.Networks[netID] = netLabel
	}
	l.stat(l.Servers, profileID, "all").add(ok, latencyMs, ts)
	l.stat(l.Servers, profileID, "n:"+netID).add(ok, latencyMs, ts)
	l.stat(l.Servers, profileID, "n:"+netID+":h"+hourBucket(now)).add(ok, latencyMs, ts)
	if family != "" {
		l.stat(l.Families, netID, family).add(ok, latencyMs, ts)
	}
}

// RecordUsage adds connected time and observed throughput.
func (l *Learner) RecordUsage(profileID, netID string, seconds float64, bps float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensure()
	s := l.stat(l.Servers, profileID, "n:"+netID)
	s.Uptime += seconds
	if bps > s.PeakBps {
		s.PeakBps = bps
	}
	l.dirty = true
}

// Forget removes stats of deleted profiles.
func (l *Learner) Forget(ids []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, id := range ids {
		delete(l.Servers, id)
	}
	l.dirty = true
}

// Candidate is a profile considered by Rank.
type Candidate struct {
	ID     string
	Family string
}

// Ranked is a scored candidate.
type Ranked struct {
	ID      string  `json:"id"`
	Score   float64 `json:"score"`
	Success float64 `json:"success"`
	Latency float64 `json:"latency"`
	Samples int     `json:"samples"`
	Reason  string  `json:"reason"`
}

// Rank orders candidates by expected quality on the given network now.
// It blends: per-network-and-hour stats, per-network stats, global stats,
// and the health of the protocol family on this network as a prior; an
// exploration bonus (UCB) makes it occasionally retry less-known servers.
func (l *Learner) Rank(cands []Candidate, netID string) []Ranked {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensure()
	now := time.Now()
	hourKey := "n:" + netID + ":h" + hourBucket(now)
	netKey := "n:" + netID
	totalN := 1.0
	for _, c := range cands {
		if s := l.Servers[c.ID]["all"]; s != nil {
			totalN += float64(s.N)
		}
	}
	var out []Ranked
	for _, c := range cands {
		ss := l.Servers[c.ID]
		var weightSum, succ, lat, latW float64
		samples := 0
		blend := func(s *Stat, w float64) {
			if s == nil || s.N == 0 {
				return
			}
			conf := math.Min(1, float64(s.N)/5)
			ww := w * conf
			weightSum += ww
			succ += s.Success * ww
			if s.Latency > 0 {
				lat += s.Latency * ww
				latW += ww
			}
			samples += s.N
		}
		blend(ss[hourKey], 3)
		blend(ss[netKey], 2)
		blend(ss["all"], 0.5)
		// family prior on this network
		prior := 0.45
		if fs := l.Families[netID][c.Family]; fs != nil && fs.N > 0 {
			prior = 0.2 + 0.6*fs.Success
		}
		success := prior
		if weightSum > 0 {
			success = (succ + prior*0.5) / (weightSum + 0.5)
		}
		latency := 800.0
		if latW > 0 {
			latency = lat / latW
		}
		latScore := 1 / (1 + latency/400)
		score := success*success*0.75 + latScore*0.25
		// recent failures penalty
		if st := ss[netKey]; st != nil {
			if st.Streak > 0 {
				score *= math.Pow(0.6, float64(min(st.Streak, 5)))
			}
			if st.LastFail > 0 && now.Unix()-st.LastFail < 120 {
				score *= 0.5
			}
		}
		// exploration bonus (UCB1)
		n := 0.0
		if st := ss["all"]; st != nil {
			n = float64(st.N)
		}
		bonus := 0.05 * math.Sqrt(2*math.Log(totalN+1)/(n+1))
		reason := "learned"
		if samples == 0 {
			reason = "unknown"
		}
		out = append(out, Ranked{ID: c.ID, Score: score + bonus, Success: success, Latency: latency, Samples: samples, Reason: reason})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// FamilyHealth describes how a protocol family performs on a network.
type FamilyHealth struct {
	Family   string  `json:"family"`
	Success  float64 `json:"success"`
	Latency  float64 `json:"latency"`
	Samples  int     `json:"samples"`
	LastOK   int64   `json:"lastOk"`
	LastFail int64   `json:"lastFail"`
	State    string  `json:"state"` // good | weak | blocked | unknown
}

// Families returns protocol health for a network, best first.
func (l *Learner) FamilyHealth(netID string) []FamilyHealth {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []FamilyHealth
	for fam, s := range l.Families[netID] {
		st := "unknown"
		switch {
		case s.N < 2:
			st = "unknown"
		case s.Success >= 0.7:
			st = "good"
		case s.Success >= 0.3:
			st = "weak"
		default:
			st = "blocked"
		}
		out = append(out, FamilyHealth{Family: fam, Success: s.Success, Latency: s.Latency, Samples: s.N, LastOK: s.LastOK, LastFail: s.LastFail, State: st})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Success != out[j].Success {
			return out[i].Success > out[j].Success
		}
		return out[i].Family < out[j].Family
	})
	return out
}

// ServerStat returns stats of one profile on a network (may be nil).
func (l *Learner) ServerStat(id, netID string) *Stat {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.Servers[id]["n:"+netID]; s != nil {
		cp := *s
		return &cp
	}
	if s := l.Servers[id]["all"]; s != nil {
		cp := *s
		return &cp
	}
	return nil
}

// Snapshot returns a deep-enough copy for persistence and a dirty flag.
func (l *Learner) Snapshot() (*Learner, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := &Learner{Servers: map[string]map[string]*Stat{}, Families: map[string]map[string]*Stat{}, Networks: map[string]string{}, Total: l.Total}
	for a, m := range l.Servers {
		cp.Servers[a] = map[string]*Stat{}
		for b, s := range m {
			v := *s
			cp.Servers[a][b] = &v
		}
	}
	for a, m := range l.Families {
		cp.Families[a] = map[string]*Stat{}
		for b, s := range m {
			v := *s
			cp.Families[a][b] = &v
		}
	}
	for k, v := range l.Networks {
		cp.Networks[k] = v
	}
	d := l.dirty
	l.dirty = false
	return cp, d
}

// Reset clears everything.
func (l *Learner) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Servers = map[string]map[string]*Stat{}
	l.Families = map[string]map[string]*Stat{}
	l.Total = 0
	l.dirty = true
}

// MergeJSON adds statistics from a backup (a serialised Learner) that this
// learner doesn't have yet; existing local stats win.
func (l *Learner) MergeJSON(b []byte) error {
	var o Learner
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensure()
	merge := func(dst, src map[string]map[string]*Stat) {
		for a, m := range src {
			if dst[a] == nil {
				dst[a] = map[string]*Stat{}
			}
			for k, s := range m {
				if s != nil && dst[a][k] == nil {
					dst[a][k] = s
				}
			}
		}
	}
	merge(l.Servers, o.Servers)
	merge(l.Families, o.Families)
	for k, v := range o.Networks {
		if l.Networks[k] == "" {
			l.Networks[k] = v
		}
	}
	if o.Total > l.Total {
		l.Total = o.Total
	}
	l.dirty = true
	return nil
}
