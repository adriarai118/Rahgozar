package smart

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Prober decides whether a domain is reachable directly from this network.
type Prober struct {
	DirectDNS string        // e.g. 8.8.8.8 (plain UDP: poisoned answers are visible)
	ProxyURL  func() string // http://127.0.0.1:10808 when connected, "" otherwise
	Timeout   time.Duration
}

// Iranian filtering answers poisoned queries with these addresses.
var poisonNets = []*net.IPNet{
	mustCIDR("10.10.34.0/24"),
	mustCIDR("0.0.0.0/8"),
	mustCIDR("127.0.0.0/8"),
	mustCIDR("192.168.0.0/16"),
	mustCIDR("172.16.0.0/12"),
}

func mustCIDR(s string) *net.IPNet {
	_, n, _ := net.ParseCIDR(s)
	return n
}

func poisoned(ips []net.IP) bool {
	for _, ip := range ips {
		for _, n := range poisonNets {
			if n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// ResolveUDP sends a raw DNS A query (no caching, no system resolver).
func ResolveUDP(ctx context.Context, server, host string) ([]net.IP, error) {
	if !strings.Contains(server, ":") {
		server += ":53"
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	id := uint16(rand.Intn(65535))
	q := make([]byte, 12, 512)
	binary.BigEndian.PutUint16(q[0:], id)
	binary.BigEndian.PutUint16(q[2:], 0x0100) // recursion desired
	binary.BigEndian.PutUint16(q[4:], 1)
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 {
			return nil, errors.New("bad host")
		}
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0, 0, 1, 0, 1)
	if _, err := conn.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		r := buf[:n]
		if n < 12 || binary.BigEndian.Uint16(r[0:]) != id {
			continue
		}
		rcode := r[3] & 0x0f
		if rcode == 3 {
			return nil, errors.New("nxdomain")
		}
		an := int(binary.BigEndian.Uint16(r[6:]))
		off := 12
		// skip question
		off = skipName(r, off) + 4
		var ips []net.IP
		for i := 0; i < an && off < n; i++ {
			off = skipName(r, off)
			if off+10 > n {
				break
			}
			typ := binary.BigEndian.Uint16(r[off:])
			rdl := int(binary.BigEndian.Uint16(r[off+8:]))
			off += 10
			if off+rdl > n {
				break
			}
			if typ == 1 && rdl == 4 {
				ips = append(ips, net.IPv4(r[off], r[off+1], r[off+2], r[off+3]))
			}
			off += rdl
		}
		if len(ips) == 0 {
			return nil, errors.New("no A record")
		}
		return ips, nil
	}
}

func skipName(b []byte, off int) int {
	for off < len(b) {
		l := int(b[off])
		if l == 0 {
			return off + 1
		}
		if l&0xc0 == 0xc0 {
			return off + 2
		}
		off += l + 1
	}
	return off
}

type httpResult struct {
	status int
	ms     int
	err    error
	body   string
}

func doGet(client *http.Client, u string) httpResult {
	start := time.Now()
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return httpResult{err: err, ms: int(time.Since(start).Milliseconds())}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return httpResult{status: resp.StatusCode, ms: int(time.Since(start).Milliseconds()), body: strings.ToLower(string(b))}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func geoBlocked(r httpResult) bool {
	if r.status == 451 {
		return true
	}
	if r.status == 403 {
		for _, k := range []string{"sanction", "not available in your", "restricted", "your country", "region", "export", "embargo", "unavailable"} {
			if strings.Contains(r.body, k) {
				return true
			}
		}
		return true
	}
	return false
}

// Probe classifies a domain as direct / proxy / unknown.
func (p *Prober) Probe(domain string) (string, string) {
	to := p.Timeout
	if to == 0 {
		to = 6 * time.Second
	}
	host := domain
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	ips, derr := ResolveUDP(ctx, p.DirectDNS, host)
	cancel()
	if derr != nil || len(ips) == 0 {
		// try www. — many base domains only have records on www
		ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
		ips, derr = ResolveUDP(ctx2, p.DirectDNS, "www."+host)
		cancel2()
		if derr == nil {
			host = "www." + host
		}
	}
	proxyRes := func() httpResult {
		pu := ""
		if p.ProxyURL != nil {
			pu = p.ProxyURL()
		}
		if pu == "" {
			return httpResult{err: errors.New("not connected")}
		}
		purl, _ := url.Parse(pu)
		c := &http.Client{Timeout: to + 4*time.Second, CheckRedirect: noRedirect,
			Transport: &http.Transport{Proxy: http.ProxyURL(purl), TLSHandshakeTimeout: to}}
		return doGet(c, "https://"+host+"/")
	}
	if derr != nil {
		pr := proxyRes()
		if pr.err == nil {
			return VerdictProxy, "DNS مستقیم جواب نداد ولی از تونل باز شد"
		}
		return VerdictUnknown, "DNS: " + derr.Error()
	}
	if poisoned(ips) {
		return VerdictProxy, fmt.Sprintf("DNS مسموم (%s)", ips[0])
	}
	ip := ips[0].String()
	dialer := &net.Dialer{Timeout: to}
	direct := &http.Client{
		Timeout:       to + 2*time.Second,
		CheckRedirect: noRedirect,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, _ := net.SplitHostPort(addr)
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
			},
			TLSClientConfig:     &tls.Config{ServerName: host},
			TLSHandshakeTimeout: to,
		},
	}
	dr := doGet(direct, "https://"+host+"/")
	if dr.err != nil {
		pr := proxyRes()
		if pr.err == nil {
			return VerdictProxy, "اتصال مستقیم قطع/مسدود شد: " + shortErr(dr.err)
		}
		if strings.Contains(pr.err.Error(), "not connected") {
			return VerdictUnknown, "مستقیم ناموفق؛ برای مقایسه باید وصل باشید"
		}
		return VerdictUnknown, "هر دو مسیر ناموفق"
	}
	if geoBlocked(dr) {
		pr := proxyRes()
		if pr.err == nil && !geoBlocked(pr) {
			return VerdictProxy, fmt.Sprintf("تحریم/محدودیت جغرافیایی (HTTP %d)", dr.status)
		}
		return VerdictDirect, fmt.Sprintf("HTTP %d در هر دو مسیر", dr.status)
	}
	if dr.ms > 2500 {
		pr := proxyRes()
		if pr.err == nil && pr.ms*3 < dr.ms {
			return VerdictProxy, fmt.Sprintf("مستقیم بسیار کند (%dms در برابر %dms)", dr.ms, pr.ms)
		}
	}
	return VerdictDirect, fmt.Sprintf("مستقیم در دسترس (%dms)", dr.ms)
}

func shortErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "reset"):
		return "connection reset"
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return "timeout"
	case strings.Contains(s, "refused"):
		return "refused"
	case strings.Contains(s, "EOF"):
		return "EOF"
	case strings.Contains(s, "certificate"):
		return "certificate"
	}
	if len(s) > 60 {
		s = s[len(s)-60:]
	}
	return s
}
