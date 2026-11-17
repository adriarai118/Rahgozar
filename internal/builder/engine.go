// Package builder turns profiles + settings into engine configurations.
package builder

import (
	"rahgozar/internal/model"
)

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
		if p.Engine == model.EngineSingBox && len(p.AWG) == 0 {
			return model.EngineSingBox
		}
		// real AmneziaWG servers need it even if Mihomo still has to be installed
		if HelperAvailable(model.EngineMihomo) || len(p.AWG) > 0 {
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
