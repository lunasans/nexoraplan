package server

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{220, 38, 38, 255})
		}
	}
	return img
}

func TestNormalizeLogo(t *testing.T) {
	var jpg bytes.Buffer
	jpeg.Encode(&jpg, testImage(600, 300), nil) // breites Logo
	out, err := normalizeLogo(&jpg)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
		t.Fatalf("Größe %v", img.Bounds())
	}
	// 600×300 → 128×64 mittig: oben und unten transparent, Mitte rot
	if _, _, _, a := img.At(64, 5).RGBA(); a != 0 {
		t.Error("oben muss transparent sein")
	}
	if r, _, _, a := img.At(64, 64).RGBA(); a == 0 || r < 0xc000 {
		t.Error("Mitte muss rot sein")
	}

	// kleines Logo wird nicht vergrößert
	var small bytes.Buffer
	png.Encode(&small, testImage(32, 32))
	out, _ = normalizeLogo(&small)
	img, _ = png.Decode(bytes.NewReader(out))
	if _, _, _, a := img.At(40, 40).RGBA(); a != 0 {
		t.Error("32×32 darf nicht hochskaliert werden")
	}

	for _, bad := range []string{"", "kein Bild", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`} {
		if _, err := normalizeLogo(strings.NewReader(bad)); err == nil {
			t.Errorf("%q angenommen", bad)
		}
	}
}

func TestCardNumberRe(t *testing.T) {
	for note, blocked := range map[string]bool{
		"Visa …1234":                     false,
		"Girokonto Sparkasse":            false,
		"Mastercard 5555 5555 5555 4444": true,
		"4111111111111111":               true,
		"DE89 3704 0044 0532 0130 00":    true,
		"DE89370400440532013000":         true,
		"Kundennr. 2026-11":              false,
	} {
		if cardNumberRe.MatchString(note) != blocked {
			t.Errorf("%q: gesperrt = %v, erwartet %v", note, !blocked, blocked)
		}
	}
}
