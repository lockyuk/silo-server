package imageutil

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/h2non/bimg"
)

func encodeTestPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTrimTransparentBorderCropsToVisiblePixels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 500, 194))
	for y := 6; y < 191; y++ {
		for x := 132; x < 368; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	// Haze under the alpha floor does not count as part of the logo.
	img.SetNRGBA(2, 2, color.NRGBA{A: 10})

	out, trimmed, err := TrimTransparentBorder(encodeTestPNG(t, img))
	if err != nil {
		t.Fatal(err)
	}
	if !trimmed {
		t.Fatal("trimmed = false, want true")
	}
	size, err := bimg.NewImage(out).Size()
	if err != nil {
		t.Fatal(err)
	}
	// One pixel of slack on each side keeps antialiased edges intact.
	if size.Width != 238 || size.Height != 187 {
		t.Fatalf("trimmed size = %dx%d, want 238x187", size.Width, size.Height)
	}
}

func TestTrimTransparentBorderLeavesOpaqueAndFullImagesAlone(t *testing.T) {
	opaque := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			opaque.Set(x, y, color.RGBA{R: 200, A: 255})
		}
	}
	data := encodeTestPNG(t, opaque)
	out, trimmed, err := TrimTransparentBorder(data)
	if err != nil {
		t.Fatal(err)
	}
	if trimmed || !bytes.Equal(out, data) {
		t.Fatal("an image without a transparent margin was changed")
	}

	empty := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	data = encodeTestPNG(t, empty)
	if out, trimmed, err = TrimTransparentBorder(data); err != nil || trimmed || !bytes.Equal(out, data) {
		t.Fatalf("a fully transparent image was changed: trimmed=%v err=%v", trimmed, err)
	}
}
