package episodeframes

import (
	"image"
	"image/color"
	"slices"
	"testing"
)

func TestFramePositions(t *testing.T) {
	if got, want := framePositions(1000, 20), []float64{200, 250, 150, 300, 350}; !slices.Equal(got, want) {
		t.Errorf("framePositions(1000, 20) = %v, want %v", got, want)
	}
	if got, want := framePositions(1000, 90), []float64{900, 950, 850}; !slices.Equal(got, want) {
		t.Errorf("framePositions(1000, 90) = %v, want %v (none past 95%%)", got, want)
	}
	if got, want := framePositions(0, 20), []float64{60}; !slices.Equal(got, want) {
		t.Errorf("framePositions(0, 20) = %v, want %v", got, want)
	}
}

func TestLumaStats(t *testing.T) {
	flat := func(v uint8) image.Image {
		img := image.NewGray(image.Rect(0, 0, 64, 36))
		for i := range img.Pix {
			img.Pix[i] = v
		}
		return img
	}
	if mean, spread := lumaStats(flat(10)); mean >= minMeanLuma || spread != 0 {
		t.Errorf("black frame = %v/%v, want dark and flat", mean, spread)
	}
	if _, spread := lumaStats(flat(128)); spread >= minLumaSpread {
		t.Errorf("grey card spread = %v, want blank", spread)
	}

	scene := image.NewRGBA(image.Rect(0, 0, 64, 36))
	for x := range 64 {
		for y := range 36 {
			v := uint8(x * 4)
			scene.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	if mean, spread := lumaStats(scene); mean < minMeanLuma || spread < minLumaSpread {
		t.Errorf("gradient = %v/%v, want a usable frame", mean, spread)
	}
}

func TestIsFullHD(t *testing.T) {
	for _, tt := range []struct {
		w, h int
		want bool
	}{
		{1920, 1080, true},
		{1920, 800, true},  // scope
		{1440, 1080, true}, // 4:3
		{1280, 720, false},
		{640, 360, false},
	} {
		if got := isFullHD(tt.w, tt.h); got != tt.want {
			t.Errorf("isFullHD(%d, %d) = %v, want %v", tt.w, tt.h, got, tt.want)
		}
	}
}
