package gocore

// Mihomo on Android: only its proxy adapters (WireGuard/AmneziaWG, SSR, …)
// run in-process behind a local SOCKS5 listener, the same shape as the
// helper process on Windows. No global Mihomo tunnel, so several helpers
// (connection + tests) can run side by side.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/inbound"
	N "github.com/metacubex/mihomo/common/net"
	_ "github.com/metacubex/mihomo/config" // sets dns.ParseNameServer used by WireGuard
	C "github.com/metacubex/mihomo/constant"
	authStore "github.com/metacubex/mihomo/listener/auth"
	LC "github.com/metacubex/mihomo/listener/config"
	"github.com/metacubex/mihomo/listener/socks"
	mlog "github.com/metacubex/mihomo/log"

	"rahgozar/internal/core"
)

var mihomoLog struct {
	once sync.Once
	mu   sync.Mutex
	sink func(name, line string)
	name string
}

func mihomoVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/metacubex/mihomo" {
				return strings.TrimPrefix(d.Version, "v")
			}
		}
	}
	return C.Version
}

func startMihomo(j core.Job, onLine func(name, line string)) (core.Process, error) {
	var cfg struct {
		SocksPort int              `json:"socks-port"`
		MixedPort int              `json:"mixed-port"`
		Proxies   []map[string]any `json:"proxies"`
	}
	if err := json.Unmarshal(j.Config, &cfg); err != nil {
		return nil, fmt.Errorf("mihomo config: %w", err)
	}
	port := cfg.SocksPort
	if port == 0 {
		port = cfg.MixedPort
	}
	if len(cfg.Proxies) == 0 || port == 0 {
		return nil, errors.New("mihomo config: no proxy or port")
	}
	mihomoLog.once.Do(func() {
		mlog.SetLevel(mlog.WARNING)
		sub := mlog.Subscribe()
		go func() {
			for e := range sub {
				mihomoLog.mu.Lock()
				f, n := mihomoLog.sink, mihomoLog.name
				mihomoLog.mu.Unlock()
				if f != nil && e.LogLevel >= mlog.WARNING {
					f(n, e.Payload)
				}
			}
		}()
	})
	if onLine != nil && j.Main {
		mihomoLog.mu.Lock()
		mihomoLog.sink, mihomoLog.name = onLine, j.Name
		mihomoLog.mu.Unlock()
	}

	proxy, err := adapter.ParseProxy(cfg.Proxies[0])
	if err != nil {
		return nil, fmt.Errorf("mihomo: %w", err)
	}
	t := &mtunnel{proxy: proxy, nat: map[string]*udpSession{}, log: onLine, name: j.Name}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	// own listener: no auth, and not the "default" one that applies
	// Mihomo's global LAN allow-list (empty here, so it rejects everything)
	lc := LC.AuthServer{Enable: true, Listen: addr, AuthStore: authStore.Nil}
	in := inbound.WithInName("rahgozar")
	tl, err := socks.NewWithConfig(lc, inbound.NewListenConfig(), t, in)
	if err != nil {
		closeProxy(proxy)
		return nil, err
	}
	ul, err := socks.NewUDPWithConfig(lc, inbound.NewListenConfig(), t, in)
	if err != nil {
		tl.Close()
		closeProxy(proxy)
		return nil, err
	}
	p := newInproc(j.Name)
	p.stop = func() {
		tl.Close()
		ul.Close()
		t.closeAll()
		closeProxy(proxy)
	}
	if onLine != nil {
		onLine(j.Name, fmt.Sprintf("Mihomo %s adapter started (in-process): %s", mihomoVersion(), proxy.Type()))
	}
	return p, nil
}

func closeProxy(p C.Proxy) {
	if c, ok := any(p).(io.Closer); ok {
		c.Close()
	}
}

// mtunnel implements C.Tunnel by sending everything to one proxy.
type mtunnel struct {
	proxy C.Proxy
	log   func(name, line string)
	name  string

	mu  sync.Mutex
	nat map[string]*udpSession
}

func (t *mtunnel) logf(f string, a ...any) {
	if t.log != nil {
		t.log(t.name, fmt.Sprintf(f, a...))
	}
}

func (t *mtunnel) NatTable() C.NatTable { return nil }

func (t *mtunnel) HandleTCPConn(conn net.Conn, md *C.Metadata) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	remote, err := t.proxy.DialContext(ctx, md)
	cancel()
	if err != nil {
		t.logf("[TCP] %s: %v", md.RemoteAddress(), err)
		conn.Close()
		return
	}
	N.Relay(conn, remote)
}

type udpSession struct {
	mu   sync.Mutex
	pc   C.PacketConn
	back C.WriteBack
	err  error
	once sync.Once
	wait chan struct{}
}

func (t *mtunnel) HandleUDPPacket(packet C.UDPPacket, md *C.Metadata) {
	key := packet.LocalAddr().String()
	t.mu.Lock()
	s, ok := t.nat[key]
	if !ok {
		s = &udpSession{wait: make(chan struct{}), back: packet}
		t.nat[key] = s
	}
	t.mu.Unlock()

	if !ok {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			pc, err := t.proxy.ListenPacketContext(ctx, md.Pure())
			cancel()
			s.mu.Lock()
			s.pc, s.err = pc, err
			s.mu.Unlock()
			close(s.wait)
			if err != nil {
				t.logf("[UDP] %s: %v", md.RemoteAddress(), err)
				t.drop(key, s)
				return
			}
			t.readBack(key, s)
		}()
	}
	go func() {
		defer packet.Drop()
		<-s.wait
		s.mu.Lock()
		pc := s.pc
		s.back = packet // latest client address
		s.mu.Unlock()
		if pc == nil {
			return
		}
		m := md.Pure()
		if !m.Resolved() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := pc.ResolveUDP(ctx, m)
			cancel()
			if err != nil {
				return
			}
		}
		pc.WriteTo(packet.Data(), m.UDPAddr())
	}()
}

func (t *mtunnel) readBack(key string, s *udpSession) {
	defer t.drop(key, s)
	buf := make([]byte, 65535)
	for {
		s.pc.SetReadDeadline(time.Now().Add(90 * time.Second))
		n, from, err := s.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		s.mu.Lock()
		back := s.back
		s.mu.Unlock()
		if _, err := back.WriteBack(buf[:n], from); err != nil {
			return
		}
	}
}

func (t *mtunnel) drop(key string, s *udpSession) {
	t.mu.Lock()
	if t.nat[key] == s {
		delete(t.nat, key)
	}
	t.mu.Unlock()
	s.once.Do(func() {
		if s.pc != nil {
			s.pc.Close()
		}
	})
}

func (t *mtunnel) closeAll() {
	t.mu.Lock()
	all := t.nat
	t.nat = map[string]*udpSession{}
	t.mu.Unlock()
	for k, s := range all {
		<-s.wait
		t.drop(k, s)
	}
}
