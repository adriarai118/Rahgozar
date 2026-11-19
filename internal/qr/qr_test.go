package qr

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"
)

const link = "vless://b831381d-6324-4d53-ad4f-8cda48b30811@example.com:443?security=reality&sni=www.microsoft.com&fp=chrome&pbk=7xhH4b_VkliBxGulljcyPOH-bYUA2dl-XAdZAsfhk04&sid=6ba85179e30d4fc2&type=tcp&flow=xtls-rprx-vision#Server%2059%20%F0%9F%87%A9%F0%9F%87%AA"

func TestRoundTrip(t *testing.T) {
	b, err := PNG(link, 320)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeBytes(b)
	if err != nil || len(got) != 1 || got[0] != link {
		t.Fatalf("got %v %v", got, err)
	}
}

// two codes on a big "screenshot" with other content around them
func TestScreenshotWithTwoCodes(t *testing.T) {
	second := "trojan://pass@host.example:443?sni=host.example#B"
	screen := image.NewRGBA(image.Rect(0, 0, 2560, 1440))
	draw.Draw(screen, screen.Bounds(), &image.Uniform{color.RGBA{30, 34, 40, 255}}, image.Point{}, draw.Src)
	put := func(text string, at image.Point) {
		b, _ := PNG(text, 300)
		im, _ := png.Decode(bytes.NewReader(b))
		draw.Draw(screen, im.Bounds().Add(at), im, image.Point{}, draw.Src)
	}
	put(link, image.Pt(400, 300))
	put(second, image.Pt(1700, 800))
	got := Decode(screen)
	if len(got) != 2 {
		t.Fatalf("want 2 codes, got %d: %v", len(got), got)
	}
}

func TestDataURL(t *testing.T) {
	u, err := DataURL("ss://YWVzLTI1Ni1nY206cGFzcw@1.2.3.4:8388#x", 200)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeBytes([]byte(u))
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestNoCode(t *testing.T) {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewGray(image.Rect(0, 0, 100, 100)))
	if _, err := DecodeBytes(buf.Bytes()); err != ErrNone {
		t.Fatalf("want ErrNone, got %v", err)
	}
}
