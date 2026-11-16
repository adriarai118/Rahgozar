//go:build !windows

package sys

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func hide(c *exec.Cmd) {}

// IsAdmin reports whether the process runs as root.
func IsAdmin() bool { return os.Geteuid() == 0 }

// RelaunchAsAdmin is not supported here.
func RelaunchAsAdmin(args ...string) error { return errors.New("not supported on this OS") }

// SystemProxy is a stub.
func SystemProxy() (bool, string) { return false, "" }

// SetSystemProxy is a stub.
func SetSystemProxy(addr, bypass string) error { return nil }

// ClearSystemProxyIf is a stub.
func ClearSystemProxyIf(addr string) bool { return false }

// SetAutoStart is a stub.
func SetAutoStart(on bool) error { return nil }

func networkLabel() string {
	b, _ := os.ReadFile("/proc/net/route")
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) > 2 && f[1] == "00000000" {
			return f[0] + "@" + f[2]
		}
	}
	return ""
}

// RASConnect is Windows only.
func RASConnect(c RASConfig) error { return errors.New("PPTP/L2TP/SSTP/IKEv2 need Windows") }

// RASDisconnect is Windows only.
func RASDisconnect(name string) error { return nil }

// RASConnected is Windows only.
func RASConnected(name string) bool { return false }

// RASRemove is Windows only.
func RASRemove(name string) {}

// FixL2TPBehindNAT is Windows only.
func FixL2TPBehindNAT() error { return errors.New("windows only") }

// OpenPath opens with xdg-open.
func OpenPath(p string) error { return exec.Command("xdg-open", p).Start() }

// StartDetached launches a program without waiting.
func StartDetached(exe string, args ...string) error {
	c := exec.Command(exe, args...)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// WaitPID waits until a process exits (or the timeout passes).
func WaitPID(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// DescendantPIDs is not implemented here.
func DescendantPIDs(pid int) []int { return nil }

// KillPID terminates a process.
func KillPID(pid int) { syscall.Kill(pid, syscall.SIGKILL) }

// ProcessExePath returns the executable path of a pid.
func ProcessExePath(pid int) string {
	p, _ := os.Readlink("/proc/" + itoa(pid) + "/exe")
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
