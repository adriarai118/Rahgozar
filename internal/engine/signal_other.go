//go:build !windows

package engine

import (
	"os"
	"os/exec"
)

func interrupt(c *exec.Cmd) {
	c.Process.Signal(os.Interrupt)
}
