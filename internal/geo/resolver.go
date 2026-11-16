package geo

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Resolver caches host → country lookups (DNS + embedded database) and
// resolves unknown hosts in the background.
type Resolver struct {
	mu      sync.Mutex
	cache   map[string]string // host -> cc ("-" = unknown)
	path    string
	pending map[string]bool
	OnNew   func()
	sem     chan struct{}
	timer   *time.Timer
}

// NewResolver loads the cache file.
func NewResolver(path string) *Resolver {
	r := &Resolver{cache: map[string]string{}, path: path, pending: map[string]bool{}, sem: make(chan struct{}, 8)}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &r.cache)
	}
	return r
}

// Country returns the country for a server: name hint first, then the IP
// database. ok=false means a background lookup was started.
func (r *Resolver) Country(name, host string) (cc string, source string) {
	if cc := FromName(name); cc != "" {
		return cc, "name"
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "" {
		return "", ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return LookupIP(ip), "ip"
	}
	r.mu.Lock()
	v, ok := r.cache[host]
	if !ok && !r.pending[host] {
		r.pending[host] = true
		go r.resolve(host)
	}
	r.mu.Unlock()
	if v == "-" {
		v = ""
	}
	return v, "ip"
}

func (r *Resolver) resolve(host string) {
	r.sem <- struct{}{}
	defer func() { <-r.sem }()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	cc := "-"
	if err == nil {
		for _, ip := range ips {
			// Iranian DNS hijack answers are not the real server location
			if ip.To4() != nil && !(ip[len(ip)-4] == 10 && ip[len(ip)-3] == 10 && ip[len(ip)-2] == 34) {
				if c := LookupIP(ip); c != "" {
					cc = c
					break
				}
			}
		}
	}
	r.mu.Lock()
	delete(r.pending, host)
	if err == nil || cc != "-" {
		r.cache[host] = cc
	}
	if r.timer == nil {
		r.timer = time.AfterFunc(700*time.Millisecond, r.flush)
	}
	r.mu.Unlock()
}

func (r *Resolver) flush() {
	r.mu.Lock()
	r.timer = nil
	b, _ := json.Marshal(r.cache)
	r.mu.Unlock()
	if r.path != "" {
		os.WriteFile(r.path, b, 0o644)
	}
	if r.OnNew != nil {
		r.OnNew()
	}
}
