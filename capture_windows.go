//go:build windows

package main

import (
	"errors"
	"image"
	"time"
	"unsafe"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

var (
	gdi32                   = windows.NewLazySystemDLL("gdi32.dll")
	pGetDC                  = user32.NewProc("GetDC")
	pReleaseDC              = user32.NewProc("ReleaseDC")
	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pGetDIBits              = gdi32.NewProc("GetDIBits")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
)

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

// CaptureScreens grabs the whole virtual desktop (all monitors). Our own
// window is hidden for a moment so it doesn't cover the code.
func (p *wailsPlatform) CaptureScreens() ([]image.Image, error) {
	if p.ctx != nil {
		wr.WindowHide(p.ctx)
		time.Sleep(350 * time.Millisecond) // let the window disappear
		defer wr.WindowShow(p.ctx)
	}
	img, err := captureVirtualScreen()
	if err != nil {
		return nil, err
	}
	return []image.Image{img}, nil
}

func captureVirtualScreen() (*image.RGBA, error) {
	x, _, _ := pGetSystemMetrics.Call(76) // SM_XVIRTUALSCREEN
	y, _, _ := pGetSystemMetrics.Call(77)
	w, _, _ := pGetSystemMetrics.Call(78) // SM_CXVIRTUALSCREEN
	h, _, _ := pGetSystemMetrics.Call(79)
	if int32(w) <= 0 || int32(h) <= 0 {
		return nil, errors.New("اندازه صفحه نامعتبر است")
	}
	W, H := int(int32(w)), int(int32(h))

	screen, _, _ := pGetDC.Call(0)
	if screen == 0 {
		return nil, errors.New("دسترسی به صفحه ممکن نشد")
	}
	defer pReleaseDC.Call(0, screen)
	mem, _, _ := pCreateCompatibleDC.Call(screen)
	defer pDeleteDC.Call(mem)
	bmp, _, _ := pCreateCompatibleBitmap.Call(screen, uintptr(W), uintptr(H))
	if bmp == 0 {
		return nil, errors.New("ساخت تصویر صفحه ممکن نشد")
	}
	defer pDeleteObject.Call(bmp)
	old, _, _ := pSelectObject.Call(mem, bmp)
	const srcCopy, captureBlt = 0x00CC0020, 0x40000000
	ok, _, _ := pBitBlt.Call(mem, 0, 0, uintptr(W), uintptr(H), screen, uintptr(int32(x)), uintptr(int32(y)), srcCopy|captureBlt)
	pSelectObject.Call(mem, old)
	if ok == 0 {
		return nil, errors.New("گرفتن تصویر صفحه ممکن نشد")
	}

	bi := bitmapInfoHeader{biWidth: int32(W), biHeight: -int32(H), biPlanes: 1, biBitCount: 32}
	bi.biSize = uint32(unsafe.Sizeof(bi))
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	r, _, _ := pGetDIBits.Call(mem, bmp, 0, uintptr(H), uintptr(unsafe.Pointer(&img.Pix[0])), uintptr(unsafe.Pointer(&bi)), 0)
	if r == 0 {
		return nil, errors.New("خواندن تصویر صفحه ممکن نشد")
	}
	// BGRA -> RGBA, opaque
	px := img.Pix
	for i := 0; i+3 < len(px); i += 4 {
		px[i], px[i+2], px[i+3] = px[i+2], px[i], 255
	}
	return img, nil
}
