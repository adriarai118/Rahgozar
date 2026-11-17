//go:build windows

package main

// System tray (notification area) icon with a right-click menu, written
// against Win32 directly so it needs no extra dependency and runs on its own
// thread beside the Wails window.

import (
	"embed"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"rahgozar/internal/desk"
)

//go:embed trayicons/*.png
var trayIcons embed.FS

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	kernel  = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassEx     = user32.NewProc("RegisterClassExW")
	pCreateWindowEx      = user32.NewProc("CreateWindowExW")
	pDefWindowProc       = user32.NewProc("DefWindowProcW")
	pGetMessage          = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessage     = user32.NewProc("DispatchMessageW")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenu          = user32.NewProc("AppendMenuW")
	pTrackPopupMenuEx    = user32.NewProc("TrackPopupMenuEx")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pSetMenuDefaultItem  = user32.NewProc("SetMenuDefaultItem")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pPostMessage         = user32.NewProc("PostMessageW")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pRegisterWindowMsg   = user32.NewProc("RegisterWindowMessageW")
	pCreateIconFromRes   = user32.NewProc("CreateIconFromResourceEx")
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pGetDpiForSystem     = user32.NewProc("GetDpiForSystem")
	pShellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	pGetModuleHandle     = kernel.NewProc("GetModuleHandleW")
)

const (
	wmApp           = 0x8000
	wmTray          = wmApp + 1
	wmClose         = 0x0010
	wmDestroy       = 0x0002
	wmNull          = 0x0000
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4
	nifInfo    = 0x10

	niifInfo    = 0x1
	niifNoSound = 0x10

	mfString     = 0x0
	mfGrayed     = 0x1
	mfChecked    = 0x8
	mfPopup      = 0x10
	mfSeparator  = 0x800
	mftRadio     = 0x200
	tpmReturnCmd = 0x100
	tpmRightBtn  = 0x2
	tpmRightAlgn = 0x8
	tpmBottom    = 0x20
	tpmLayoutRTL = 0x8000
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct{ x, y int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     uintptr
}

// tray is the notification-area icon of the running app.
type tray struct {
	app  *desk.App
	plat *wailsPlatform

	mu      sync.Mutex
	hwnd    uintptr
	icons   map[string]uintptr
	cur     string // icon key in use
	tip     string
	taskbar uint32 // "TaskbarCreated" message (explorer restarted)
	ready   atomic.Bool
	hinted  atomic.Bool // "still running in the tray" balloon shown

	cmds map[uint32]func()
}

var theTray *tray

// startTray creates the icon on its own OS thread.
func startTray(app *desk.App, plat *wailsPlatform) *tray {
	t := &tray{app: app, plat: plat, icons: map[string]uintptr{}}
	theTray = t
	go t.loop()
	go t.watch()
	return t
}

func (t *tray) loop() {
	runtime.LockOSThread()
	hinst, _, _ := pGetModuleHandle.Call(0)
	cls, _ := windows.UTF16PtrFromString("RahgozarTray")
	wc := wndClassEx{lpfnWndProc: windows.NewCallback(t.wndProc), hInstance: hinst, lpszClassName: cls}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	title, _ := windows.UTF16PtrFromString("Rahgozar tray")
	// a real (never shown) top-level window: menus need a foreground window
	hwnd, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, hinst, 0)
	if hwnd == 0 {
		return
	}
	tb, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := pRegisterWindowMsg.Call(uintptr(unsafe.Pointer(tb)))
	t.mu.Lock()
	t.hwnd = hwnd
	t.taskbar = uint32(r)
	t.loadIcons()
	t.mu.Unlock()
	t.add()
	t.ready.Store(true)

	var m msg
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (t *tray) wndProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	switch {
	case m == wmTray:
		switch uint32(lp) & 0xffff {
		case wmLButtonUp, wmLButtonDblClk:
			go t.plat.Focus()
		case wmRButtonUp:
			t.showMenu()
		}
		return 0
	case m == t.taskbar && m != 0:
		t.add() // explorer restarted: icon is gone
		return 0
	case m == wmDestroy:
		t.remove()
		return 0
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(m), wp, lp)
	return r
}

// ---------------- icon ----------------

func (t *tray) loadIcons() {
	// pick the PNG closest to the small-icon size at the current DPI
	want := 16
	if dpi, _, _ := pGetDpiForSystem.Call(); dpi > 0 {
		want = int(dpi) * 16 / 96
	} else if w, _, _ := pGetSystemMetrics.Call(49); w > 0 { // SM_CXSMICON
		want = int(w)
	}
	size := 48
	for _, s := range []int{16, 20, 24, 32, 40, 48} {
		if s >= want {
			size = s
			break
		}
	}
	for _, k := range []string{"on", "off", "busy"} {
		b, err := trayIcons.ReadFile(fmt.Sprintf("trayicons/%s-%d.png", k, size))
		if err != nil {
			continue
		}
		// Vista+ accepts PNG data here
		h, _, _ := pCreateIconFromRes.Call(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 1, 0x00030000, uintptr(size), uintptr(size), 0)
		if h != 0 {
			t.icons[k] = h
		}
	}
	t.cur = "off"
	t.tip = "رهگذر"
}

func (t *tray) nid() notifyIconData {
	var d notifyIconData
	d.cbSize = uint32(unsafe.Sizeof(d))
	d.hWnd = t.hwnd
	d.uID = 1
	return d
}

func copyUTF16(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	if len(u) > len(dst) {
		u = u[:len(dst)-1]
		u = append(u, 0)
	}
	copy(dst, u)
}

func (t *tray) add() {
	t.mu.Lock()
	d := t.nid()
	d.uFlags = nifMessage | nifIcon | nifTip
	d.uCallbackMessage = wmTray
	d.hIcon = t.icons[t.cur]
	copyUTF16(d.szTip[:], t.tip)
	t.mu.Unlock()
	pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&d)))
}

func (t *tray) remove() {
	t.mu.Lock()
	d := t.nid()
	t.mu.Unlock()
	pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
}

// close removes the icon (called on app shutdown).
func (t *tray) close() {
	if t != nil && t.ready.Load() {
		t.remove()
	}
}

func (t *tray) update(icon, tip string) {
	t.mu.Lock()
	if !t.ready.Load() || (icon == t.cur && tip == t.tip) {
		t.mu.Unlock()
		return
	}
	t.cur, t.tip = icon, tip
	d := t.nid()
	d.uFlags = nifIcon | nifTip
	d.hIcon = t.icons[icon]
	copyUTF16(d.szTip[:], tip)
	t.mu.Unlock()
	pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&d)))
}

// balloon shows a notification next to the icon.
func (t *tray) balloon(title, text string) {
	if t == nil || !t.ready.Load() {
		return
	}
	t.mu.Lock()
	d := t.nid()
	d.uFlags = nifInfo
	d.dwInfoFlags = niifInfo | niifNoSound
	copyUTF16(d.szInfoTitle[:], title)
	copyUTF16(d.szInfo[:], text)
	t.mu.Unlock()
	pShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&d)))
}

// watch keeps the icon and tooltip in step with the connection.
func (t *tray) watch() {
	for {
		time.Sleep(1500 * time.Millisecond)
		if !t.ready.Load() {
			continue
		}
		st := t.app.GetStatus()
		icon, tip := "off", "رهگذر — قطع"
		switch st.State {
		case "connected":
			icon = "on"
			tip = "رهگذر — متصل: " + st.ProfileName
			if st.DownSpeed > 0 || st.UpSpeed > 0 {
				tip += fmt.Sprintf("\n↓ %s/s  ↑ %s/s", human(st.DownSpeed), human(st.UpSpeed))
			}
		case "connecting", "disconnecting":
			icon, tip = "busy", "رهگذر — در حال اتصال…"
		case "error":
			tip = "رهگذر — خطا: " + st.Error
		}
		t.update(icon, tip)
	}
}

func human(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(b)/(1<<10))
	}
	return fmt.Sprintf("%d B", b)
}

// ---------------- menu ----------------

type menu struct {
	h    uintptr
	t    *tray
	next *uint32
}

func (m menu) item(text string, flags uintptr, fn func()) {
	id := uintptr(0)
	if fn != nil {
		*m.next++
		id = uintptr(*m.next)
		m.t.cmds[*m.next] = fn
	}
	s, _ := windows.UTF16PtrFromString(text)
	pAppendMenu.Call(m.h, mfString|flags, id, uintptr(unsafe.Pointer(s)))
}

func (m menu) check(text string, on bool, fn func()) {
	f := uintptr(mftRadio)
	if on {
		f |= mfChecked
	}
	m.item(text, f, fn)
}

func (m menu) sep() { pAppendMenu.Call(m.h, mfSeparator, 0, 0) }

func (m menu) sub(text string) menu {
	h, _, _ := pCreatePopupMenu.Call()
	s, _ := windows.UTF16PtrFromString(text)
	pAppendMenu.Call(m.h, mfPopup, h, uintptr(unsafe.Pointer(s)))
	return menu{h: h, t: m.t, next: m.next}
}

// run executes a menu command off the UI thread; errors become balloons.
func (t *tray) run(fn func() error) func() {
	return func() {
		go func() {
			if err := fn(); err != nil {
				if err.Error() == "NEED_ADMIN" {
					t.plat.Focus()
					desk.Emit(t.app, "needAdmin", nil)
					return
				}
				t.balloon("رهگذر", err.Error())
			}
			desk.Emit(t.app, "settingsChanged", nil)
		}()
	}
}

func (t *tray) showMenu() {
	st := t.app.GetState()
	set := st.Settings
	status := st.Status
	t.cmds = map[uint32]func(){}
	var next uint32
	h, _, _ := pCreatePopupMenu.Call()
	m := menu{h: h, t: t, next: &next}

	head := "رهگذر — قطع"
	switch status.State {
	case "connected":
		head = "متصل: " + status.ProfileName
	case "connecting":
		head = "در حال اتصال…"
	case "error":
		head = "خطا: " + trim(status.Error, 60)
	}
	m.item(head, mfGrayed, nil)
	if status.State == "connected" {
		m.item(fmt.Sprintf("↓ %s/s   ↑ %s/s   %s", human(status.DownSpeed), human(status.UpSpeed), latency(status.Latency)), mfGrayed, nil)
	}
	m.sep()

	if status.State == "connected" || status.State == "connecting" {
		m.item("قطع اتصال", 0, t.run(t.app.Disconnect))
	} else {
		m.item("اتصال", 0, t.run(t.app.Connect))
	}

	// servers: smart choice + the best known ones
	sm := m.sub("سرور")
	sm.check("انتخاب هوشمند (بهترین سرور)", set.SelectedID == "auto" || set.SelectedID == "", func() {
		t.run(func() error { return t.app.Select("auto") })()
	})
	sm.sep()
	profiles := st.Profiles
	sort.SliceStable(profiles, func(i, j int) bool { return score(profiles[i]) > score(profiles[j]) })
	shown := 0
	for _, p := range profiles {
		if shown >= 20 && p.ID != set.SelectedID {
			continue
		}
		shown++
		id := p.ID
		label := trim(p.Name, 48)
		if p.Country != "" {
			label = strings.ToUpper(p.Country) + "   " + label
		}
		if p.Latency > 0 {
			label += "   " + latency(int(p.Latency))
		}
		sm.check(label, set.SelectedID == id, func() {
			t.run(func() error {
				if err := t.app.Select(id); err != nil {
					return err
				}
				if s := t.app.GetStatus(); s.State != "connected" && s.State != "connecting" {
					return t.app.Connect()
				}
				return nil
			})()
		})
	}
	if len(profiles) == 0 {
		sm.item("سروری اضافه نشده", mfGrayed, nil)
	} else if len(profiles) > shown {
		sm.sep()
		sm.item(fmt.Sprintf("همه %d سرور…", len(profiles)), 0, func() { t.openPage("servers") })
	}

	mm := m.sub("حالت")
	mm.check("TUN (کل سیستم)", set.Mode == "tun", t.run(func() error { return t.app.SetMode("tun") }))
	mm.check("پروکسی سیستم", set.Mode == "proxy", t.run(func() error { return t.app.SetMode("proxy") }))

	rm := m.sub("مسیریابی")
	for _, r := range [][2]string{{"smart", "هوشمند"}, {"rules", "فقط قوانین"}, {"global", "همه از تونل"}} {
		id := r[0]
		rm.check(r[1], set.RoutingMode == id, t.run(func() error { return t.app.SetRouting(id) }))
	}
	m.sep()

	m.item("تست همه سرورها", 0, func() {
		go func() {
			t.balloon("رهگذر", "تست سرورها شروع شد…")
			ok, total := desk.TestAll(t.app)
			t.balloon("تست سرورها تمام شد", fmt.Sprintf("%d سرور از %d سالم است", ok, total))
		}()
	})
	m.item("وارد کردن از کلیپ‌بورد", 0, func() {
		go func() {
			r := t.app.ImportClipboard()
			desk.Emit(t.app, "imported", r)
			if r.Added == 0 && len(r.Errors) > 0 {
				t.balloon("وارد کردن", r.Errors[0])
			} else {
				t.balloon("وارد کردن", fmt.Sprintf("%d سرور اضافه شد", r.Added))
			}
		}()
	})
	m.item(fmt.Sprintf("کپی آدرس پروکسی (127.0.0.1:%d)", set.MixedPort), 0, func() {
		go func() {
			if err := t.plat.ClipboardSet(fmt.Sprintf("127.0.0.1:%d", set.MixedPort)); err == nil {
				t.balloon("رهگذر", "آدرس پروکسی کپی شد")
			}
		}()
	})
	m.sep()
	m.item("یادگیری و سایت‌ها", 0, func() { t.openPage("learn") })
	m.item("گزارش‌ها", 0, func() { t.openPage("logs") })
	m.item("تنظیمات", 0, func() { t.openPage("settings") })
	m.sep()
	m.item("نمایش رهگذر", 0, func() { go t.plat.Focus() })
	pSetMenuDefaultItem.Call(h, uintptr(next), 0) // bold, same as left click
	m.item("خروج", 0, func() { go t.app.Quit() })

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(t.hwnd) // otherwise the menu won't close on outside click
	cmd, _, _ := pTrackPopupMenuEx.Call(h, tpmReturnCmd|tpmRightBtn|tpmBottom|tpmRightAlgn|tpmLayoutRTL,
		uintptr(pt.x), uintptr(pt.y), t.hwnd, 0)
	pPostMessage.Call(t.hwnd, wmNull, 0, 0)
	pDestroyMenu.Call(h)
	if fn := t.cmds[uint32(cmd)]; fn != nil {
		fn()
	}
}

func (t *tray) openPage(page string) {
	go func() {
		t.plat.Focus()
		desk.Emit(t.app, "navigate", page)
	}()
}

func score(p *desk.ProfileView) float64 {
	if p.Samples == 0 {
		return -1
	}
	s := p.Success * 1000
	if p.Latency > 0 {
		s -= p.Latency / 10
	}
	return s
}

func latency(ms int) string {
	if ms <= 0 {
		return ""
	}
	return fmt.Sprintf("%dms", ms)
}

func trim(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "&", "&&")) // & marks mnemonics in menus
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
