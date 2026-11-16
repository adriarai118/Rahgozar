//go:build windows

package engine

import (
	"os/exec"
)

// On Windows a hidden child has no console to receive Ctrl+C; sing-box
// restores the system proxy itself only on graceful exit, so the core clears
// it explicitly after Kill.
func interrupt(c *exec.Cmd) {
	c.Process.Kill()
}
