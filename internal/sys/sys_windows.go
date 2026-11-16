//go:build windows

package sys

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const createNoWindow = 0x08000000

func hide(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// IsAdmin reports whether the process runs elevated.
func IsAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RelaunchAsAdmin restarts the current executable elevated (UAC prompt).
func RelaunchAsAdmin(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(args, " "))
	cwd, _ := os.Getwd()
	dir, _ := windows.UTF16PtrFromString(cwd)
	return windows.ShellExecute(0, verb, file, params, dir, windows.SW_NORMAL)
}

const inetKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

var (
	wininet               = windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOption = wininet.NewProc("InternetSetOptionW")
)

func refreshInternetSettings() {
	const optSettingsChanged = 39
	const optRefresh = 37
	procInternetSetOption.Call(0, optSettingsChanged, 0, 0)
	procInternetSetOption.Call(0, optRefresh, 0, 0)
}

// SystemProxy returns the current WinINet proxy state.
func SystemProxy() (enabled bool, server string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.QUERY_VALUE)
	if err != nil {
		return false, ""
	}
	defer k.Close()
	v, _, _ := k.GetIntegerValue("ProxyEnable")
	s, _, _ := k.GetStringValue("ProxyServer")
	return v == 1, s
}

// SetSystemProxy points WinINet at host:port.
func SetSystemProxy(addr string, bypass string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	k.SetDWordValue("ProxyEnable", 1)
	k.SetStringValue("ProxyServer", addr)
	if bypass == "" {
		bypass = "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.2*;172.30.*;172.31.*;192.168.*;<local>"
	}
	k.SetStringValue("ProxyOverride", bypass)
	refreshInternetSettings()
	return nil
}

// ClearSystemProxyIf disables the system proxy if it points at addr
// (crash recovery: never leave the user without internet).
func ClearSystemProxyIf(addr string) bool {
	en, s := SystemProxy()
	if !en || !strings.Contains(s, addr) {
		return false
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.SET_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	k.SetDWordValue("ProxyEnable", 0)
	refreshInternetSettings()
	return true
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// SetAutoStart enables or disables starting with Windows.
func SetAutoStart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		k.DeleteValue("Rahgozar")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue("Rahgozar", `"`+exe+`" --minimized`)
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func powershell(script string) (string, error) {
	c := Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func networkLabel() string {
	out, err := powershell(`$p = Get-NetConnectionProfile | Sort-Object -Property InterfaceIndex | Select-Object -First 1; if ($p) { $p.Name + '@' + $p.InterfaceAlias }`)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// RASConnect creates/updates a Windows VPN entry and dials it.
func RASConnect(c RASConfig) error {
	tunnel := map[string]string{"pptp": "Pptp", "l2tp": "L2tp", "sstp": "Sstp", "ikev2": "Ikev2"}[c.Type]
	if tunnel == "" {
		return fmt.Errorf("unsupported VPN type %s", c.Type)
	}
	server := c.Server
	if c.Port > 0 && c.Type == "sstp" && c.Port != 443 {
		server = server + ":" + strconv.Itoa(c.Port)
	}
	args := []string{
		"-Name", psQuote(c.Name), "-ServerAddress", psQuote(server), "-TunnelType", tunnel,
		"-Force", "-RememberCredential", "-PassThru",
	}
	switch c.Type {
	case "l2tp":
		args = append(args, "-EncryptionLevel", "Optional", "-AuthenticationMethod", "MSChapv2")
		if c.PSK != "" {
			args = append(args, "-L2tpPsk", psQuote(c.PSK))
		}
	case "ikev2":
		args = append(args, "-EncryptionLevel", "Required", "-AuthenticationMethod", "Eap")
	default:
		args = append(args, "-EncryptionLevel", "Optional", "-AuthenticationMethod", "MSChapv2")
	}
	script := "$ErrorActionPreference='Stop'; Remove-VpnConnection -Name " + psQuote(c.Name) +
		" -Force -ErrorAction SilentlyContinue; Add-VpnConnection " + strings.Join(args, " ") + " | Out-Null"
	if out, err := powershell(script); err != nil {
		return fmt.Errorf("Add-VpnConnection: %s", firstLine(out, err))
	}
	out, err := Command("rasdial.exe", c.Name, c.Username, c.Password).CombinedOutput()
	if err != nil {
		return fmt.Errorf("rasdial: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// RASDisconnect hangs up a RAS connection.
func RASDisconnect(name string) error {
	out, err := Command("rasdial.exe", name, "/disconnect").CombinedOutput()
	if err != nil {
		return fmt.Errorf("rasdial: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// RASConnected reports whether the named RAS connection is up.
func RASConnected(name string) bool {
	out, _ := Command("rasdial.exe").CombinedOutput()
	return strings.Contains(string(out), name)
}

// RASRemove deletes the phonebook entry.
func RASRemove(name string) {
	powershell("Remove-VpnConnection -Name " + psQuote(name) + " -Force -ErrorAction SilentlyContinue")
}

// FixL2TPBehindNAT sets AssumeUDPEncapsulationContextOnSendRule=2 (needs admin + reboot).
func FixL2TPBehindNAT() error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\PolicyAgent`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue("AssumeUDPEncapsulationContextOnSendRule", 2)
}

func firstLine(out string, err error) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return err.Error()
}

// OpenPath opens a folder or URL with the shell.
func OpenPath(p string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(p)
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_NORMAL)
}

// KillPID terminates a process by pid (best effort).
func KillPID(pid int) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	windows.TerminateProcess(h, 1)
	windows.CloseHandle(h)
}

// ProcessExePath returns the executable path of a pid ("" if gone).
func ProcessExePath(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH*2)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

var _ = unsafe.Pointer(nil)
