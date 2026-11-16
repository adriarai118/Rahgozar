package core

import (
	"os"
	"path/filepath"

	"rahgozar/internal/engine"
	"rahgozar/internal/model"
)

// Process is a running engine instance (an OS process on Windows, an
// in-process instance on Android).
type Process interface {
	PID() int
	Label() string
	Done() <-chan struct{}
	Alive() bool
	ExitError() error
	Stop()
}

// Job describes an engine to start.
type Job struct {
	Name       string // log name, e.g. "sing-box" or "xray(test)"
	Engine     string // sing-box | xray | mihomo
	ConfigPath string // file the config was written to
	Config     []byte
	RuntimeDir string // working directory for sing-box (cache etc.)
	Main       bool   // the main tunnel instance (owns TUN)
}

// Runner starts engines.
type Runner interface {
	// Available reports why an engine cannot be used (nil if it can).
	Available(engine string) error
	Start(job Job, onLine func(name, line string)) (Process, error)
	// OwnsPath reports whether an executable path belongs to our engines
	// (used to kill leftovers after a crash). In-process runners return false.
	OwnsPath(path string) bool
	// ExeNames lists engine executables to bypass in TUN mode.
	ExeNames() []string
}

// ExecRunner runs engines as child processes from the engine manager.
type ExecRunner struct{ Eng *engine.Manager }

// Available implements Runner.
func (r ExecRunner) Available(eng string) error {
	_, err := r.Eng.ExePath(eng)
	return err
}

// OwnsPath implements Runner.
func (r ExecRunner) OwnsPath(path string) bool {
	return path != "" && len(path) >= len(r.Eng.Dir) && equalFoldPrefix(path, r.Eng.Dir)
}

// ExeNames implements Runner.
func (r ExecRunner) ExeNames() []string { return r.Eng.ExeNames() }

// Start implements Runner.
func (r ExecRunner) Start(j Job, onLine func(name, line string)) (Process, error) {
	exe, err := r.Eng.ExePath(j.Engine)
	if err != nil {
		return nil, err
	}
	var args []string
	switch j.Engine {
	case model.EngineXray:
		args = []string{"run", "-c", j.ConfigPath}
	case model.EngineMihomo:
		dir := filepath.Join(filepath.Dir(j.ConfigPath), "mihomo")
		os.MkdirAll(dir, 0o755)
		args = []string{"-d", dir, "-f", j.ConfigPath}
	default:
		args = []string{"run", "-c", j.ConfigPath, "--disable-color"}
		if j.RuntimeDir != "" {
			args = append(args, "-D", j.RuntimeDir)
		}
	}
	return engine.Start(j.Name, exe, filepath.Dir(exe), args, onLine)
}

func equalFoldPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	a, b := s[:len(prefix)], prefix
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 32
		}
		if 'A' <= y && y <= 'Z' {
			y += 32
		}
		if x != y {
			return false
		}
	}
	return true
}
