// Package sys contains OS integration: admin rights, system proxy, auto
// start, Windows RAS VPN (PPTP/L2TP/SSTP/IKEv2) and network identification.
package sys

import (
	"crypto/sha1"
	"encoding/hex"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// RASConfig describes a Windows built-in VPN connection.
type RASConfig struct {
	Name     string
	Type     string // pptp | l2tp | sstp | ikev2
	Server   string
	Port     int
	Username string
	Password string
	PSK      string
}

var (
	netMu    sync.Mutex
	netCache string
	netAt    time.Time
)

// NetworkID returns a short stable identifier of the current network
// (Wi-Fi SSID / connection profile + gateway), used by the learner so that
// server quality is remembered per network.
func NetworkID() (id string, label string) {
	netMu.Lock()
	defer netMu.Unlock()
	if netCache != "" && time.Since(netAt) < time.Minute {
		parts := strings.SplitN(netCache, "|", 2)
		return parts[0], parts[1]
	}
	label = networkLabel()
	if label == "" {
		label = "default"
	}
	h := sha1.Sum([]byte(label))
	id = hex.EncodeToString(h[:4])
	netCache, netAt = id+"|"+label, time.Now()
	return id, label
}

// Command creates a hidden command (no console window on Windows).
func Command(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	hide(c)
	return c
}

// NetworkIDFast returns the cached network id without blocking; a refresh
// runs in the background when the cache is stale.
func NetworkIDFast() (string, string) {
	netMu.Lock()
	c, at := netCache, netAt
	netMu.Unlock()
	if c == "" || time.Since(at) > time.Minute {
		go NetworkID()
	}
	if c == "" {
		return "", ""
	}
	parts := strings.SplitN(c, "|", 2)
	return parts[0], parts[1]
}
