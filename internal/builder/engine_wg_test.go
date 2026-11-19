package builder

import (
	"testing"

	"rahgozar/internal/model"
)

func TestWireGuardEngine(t *testing.T) {
	old := HelperAvailable
	defer func() { HelperAvailable = old }()
	junk := map[string]string{"jc": "6", "jmin": "10", "jmax": "50"}
	awg := map[string]string{"jc": "4", "s1": "122", "h1": "1318795025-1318795156"}
	cases := []struct {
		mihomo bool
		engine string
		awg    map[string]string
		want   string
	}{
		{false, model.EngineSingBox, junk, model.EngineSingBox}, // Android, user forced sing-box
		{false, "", junk, model.EngineSingBox},                  // Android, auto: junk is optional
		{false, "", nil, model.EngineSingBox},
		{false, "", awg, model.EngineMihomo}, // real AmneziaWG server needs Mihomo
		{false, model.EngineSingBox, awg, model.EngineSingBox},
		{true, "", junk, model.EngineMihomo},
		{true, "", nil, model.EngineMihomo},
		{true, model.EngineSingBox, junk, model.EngineSingBox},
		{false, "", map[string]string{"jc": "4", "s1": "0", "h1": "1", "h2": "2"}, model.EngineSingBox},
	}
	for i, c := range cases {
		HelperAvailable = func(string) bool { return c.mihomo }
		p := &model.Profile{Type: model.TWireGuard, Engine: c.engine, AWG: c.awg}
		if got := EngineFor(p); got != c.want {
			t.Errorf("case %d: got %s want %s", i, got, c.want)
		}
	}
}
