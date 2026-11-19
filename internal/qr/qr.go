// Package qr reads config links from QR codes in images and screenshots,
// and renders a link as a QR code.
package qr

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/multi/qrcode"
	zqr "github.com/makiuchi-d/gozxing/qrcode"
	qrgen "github.com/skip2/go-qrcode"
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// ErrNone means no QR code was found.
var ErrNone = errors.New("هیچ QR codeی در تصویر پیدا نشد")

// Decode returns the texts of all QR codes in img (deduplicated, in order).
func Decode(img image.Image) []string {
	var out []string
	seen := map[string]bool{}
	add := func(rs []*gozxing.Result) {
		for _, r := range rs {
			t := strings.TrimSpace(r.GetText())
			if t != "" && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	try := func(im image.Image) {
		bmp, err := gozxing.NewBinaryBitmapFromImage(im)
		if err != nil {
			return
		}
		hints := map[gozxing.DecodeHintType]any{gozxing.DecodeHintType_TRY_HARDER: true}
		if rs, err := qrcode.NewQRCodeMultiReader().DecodeMultiple(bmp, hints); err == nil {
			add(rs)
		}
		if len(out) == 0 {
			if r, err := zqr.NewQRCodeReader().Decode(bmp, hints); err == nil {
				add([]*gozxing.Result{r})
			}
		}
	}
	try(img)
	b := img.Bounds()
	if len(out) == 0 && (b.Dx() > 1600 || b.Dy() > 1600) {
		// huge screenshots: the detector does better at a smaller scale
		try(scale(img, 0.5))
	}
	if len(out) == 0 && b.Dx() < 500 && b.Dy() < 500 {
		try(scale(img, 2)) // tiny images
	}
	if len(out) == 0 {
		try(invert(img)) // light-on-dark codes
	}
	return out
}

// DecodeBytes decodes an encoded image (PNG, JPEG, GIF, BMP, WebP) or a
// data: URL.
func DecodeBytes(b []byte) ([]string, error) {
	if s := string(b[:min(len(b), 64)]); strings.HasPrefix(s, "data:") {
		i := bytes.IndexByte(b, ',')
		if i < 0 {
			return nil, errors.New("bad data URL")
		}
		d, err := base64.StdEncoding.DecodeString(string(b[i+1:]))
		if err != nil {
			return nil, err
		}
		b = d
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("فایل تصویر قابل خواندن نیست")
	}
	t := Decode(img)
	if len(t) == 0 {
		return nil, ErrNone
	}
	return t, nil
}

// PNG renders text as a QR code image.
func PNG(text string, size int) ([]byte, error) {
	q, err := qrgen.New(text, qrgen.Medium)
	if err != nil {
		q, err = qrgen.New(text, qrgen.Low)
	}
	if err != nil {
		return nil, errors.New("این کانفیگ برای QR code بیش از حد طولانی است")
	}
	return q.PNG(size)
}

// DataURL renders text as a PNG data URL.
func DataURL(text string, size int) (string, error) {
	b, err := PNG(text, size)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b), nil
}

func scale(img image.Image, f float64) image.Image {
	b := img.Bounds()
	w, h := int(float64(b.Dx())*f), int(float64(b.Dy())*f)
	if w < 1 || h < 1 {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

func invert(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			g := color.GrayModel.Convert(img.At(x, y)).(color.Gray)
			dst.SetGray(x, y, color.Gray{Y: 255 - g.Y})
		}
	}
	return dst
}
