// Package builder turns profiles + settings into engine configurations.
package builder

import (
	"strings"

	"rahgozar/internal/model"
)

// NeedsAWG reports AmneziaWG parameters that the server must share
// (handshake padding, magic headers, v3), as opposed to client-only junk.
func NeedsAWG(p *model.Profile) bool {
	for k := range p.AWG {
		switch k {
		case "jc", "jmin", "jmax", "i1", "i2", "i3", "i4", "i5", "j1", "j2", "j3", "itime":
			continue
		}
		if k == "s1" || k == "s2" || k == "s3" || k == "s4" {
			if v := p.AWG[k]; v == "0" || v == "" {
				continue
			}
		}
		if (k == "h1" || k == "h2" || k == "h3" || k == "h4") && p.AWG[k] == strings.TrimPrefix(k, "h") {
			continue // 1,2,3,4 are the standard WireGuard message types
		}
		return true
	}
	return false
}

// HelperAvailable reports whether a helper engine can run here; the core
// wires it to the engine manager (Android has no Mihomo).
var HelperAvailable = func(engine string) bool { return false }

// EngineFor decides which engine runs a profile.
func EngineFor(p *model.Profile) string {
	if p.IsRAS() {
		return model.EngineWindows
	}
	switch p.Type {
	case model.TSSR:
		return model.EngineMihomo
	case model.TChain:
		return model.EngineSingBox
	case model.TWireGuard:
		// Plain WireGuard is easy prey for DPI. Mihomo adds AmneziaWG junk
		// packets before the handshake (harmless to standard servers, like
		// WireSock does by default) and speaks full AmneziaWG.
		// an explicit choice always wins
		if p.Engine == model.EngineSingBox {
			return model.EngineSingBox
		}
		if HelperAvailable(model.EngineMihomo) || p.Engine == model.EngineMihomo {
			return model.EngineMihomo
		}
		// No Mihomo (Android, or not installed yet): junk packets are optional
		// for a standard server, so fall back to plain WireGuard. Real
		// AmneziaWG servers (S*/H* headers, v3) can't work without it.
		if NeedsAWG(p) {
			return model.EngineMihomo
		}
		return model.EngineSingBox
	case model.TOpenVPN, model.THysteria, model.THysteria2, model.TTUIC, model.TAnyTLS, model.TSSH:
		return model.EngineSingBox
	}
	if p.Engine == model.EngineXray || p.Engine == model.EngineMihomo || p.Engine == model.EngineSingBox {
		if p.Engine == model.EngineSingBox && needsXray(p) {
			return model.EngineXray
		}
		return p.Engine
	}
	if needsXray(p) {
		return model.EngineXray
	}
	return model.EngineSingBox
}

// needsXray reports features sing-box does not implement.
func needsXray(p *model.Profile) bool {
	switch p.Type {
	case model.TVMess, model.TVLESS, model.TTrojan, model.TShadowsocks:
	default:
		return false
	}
	switch p.Network {
	case "xhttp", "splithttp", "kcp", "mkcp", "raw-http":
		return true
	}
	if p.Network == "tcp" && p.HeaderType == "http" {
		return true
	}
	if p.Type == model.TVLESS && p.Method != "" && p.Method != "none" {
		return true // VLESS post-quantum encryption
	}
	return false
}

// EngineLabel is a human label for the UI.
func EngineLabel(e string) string {
	switch e {
	case model.EngineSingBox:
		return "sing-box"
	case model.EngineXray:
		return "Xray"
	case model.EngineMihomo:
		return "Mihomo"
	case model.EngineWindows:
		return "Windows VPN"
	}
	return e
}
