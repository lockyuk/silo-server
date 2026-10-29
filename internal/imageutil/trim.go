package imageutil

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"

	"github.com/h2non/bimg"
)

// trimAlphaFloor is the alpha at or under which a pixel counts as empty, so
// soft glows and compression haze around a logo do not hold its border open.
const trimAlphaFloor = 16

// trimDetectDimension caps the copy the visible area is measured on. The crop
// itself runs on the full-size image.
const trimDetectDimension = 1024

// TrimTransparentBorder crops an image with an alpha channel to its visible
// pixels. Provider clear logos often carry a wide transparent margin (one
// Top Boy logo starts 132 of 500 pixels in), which leaves them floating away
// from the edge every client lines them up on. It returns data unchanged, with
// trimmed false, when the image has no alpha, nothing visible, or no margin.
func TrimTransparentBorder(data []byte) (out []byte, trimmed bool, err error) {
	img := bimg.NewImage(data)
	meta, err := img.Metadata()
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrInvalidImage, err)
	}
	if !meta.Alpha || meta.Size.Width <= 0 || meta.Size.Height <= 0 {
		return data, false, nil
	}
	opts := bimg.Options{Type: bimg.PNG, StripMetadata: true}
	fitWithin(&opts, meta.Size, trimDetectDimension)
	detect, err := bimg.NewImage(data).Process(opts)
	if err != nil {
		return nil, false, fmt.Errorf("imageutil: trim detect copy: %w", err)
	}
	decoded, err := png.Decode(bytes.NewReader(detect))
	if err != nil {
		return nil, false, fmt.Errorf("imageutil: trim detect decode: %w", err)
	}
	visible, ok := visibleBounds(decoded)
	if !ok {
		return data, false, nil
	}
	// Map the detect copy's bounds onto the original, rounding outward so a
	// downscaled measurement never clips the logo's edge.
	bounds := decoded.Bounds()
	sx := float64(meta.Size.Width) / float64(bounds.Dx())
	sy := float64(meta.Size.Height) / float64(bounds.Dy())
	left := max(int(math.Floor(float64(visible.Min.X-bounds.Min.X)*sx))-1, 0)
	top := max(int(math.Floor(float64(visible.Min.Y-bounds.Min.Y)*sy))-1, 0)
	right := min(int(math.Ceil(float64(visible.Max.X-bounds.Min.X)*sx))+1, meta.Size.Width)
	bottom := min(int(math.Ceil(float64(visible.Max.Y-bounds.Min.Y)*sy))+1, meta.Size.Height)
	if left == 0 && top == 0 && right == meta.Size.Width && bottom == meta.Size.Height {
		return data, false, nil
	}
	out, err = bimg.NewImage(data).Extract(top, left, right-left, bottom-top)
	if err != nil {
		return nil, false, fmt.Errorf("imageutil: trim extract: %w", err)
	}
	return out, true, nil
}

// visibleBounds is the smallest rectangle holding every pixel above
// trimAlphaFloor; ok is false when there is none.
func visibleBounds(img image.Image) (image.Rectangle, bool) {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a>>8 <= trimAlphaFloor {
				continue
			}
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
		}
	}
	if maxX < minX {
		return image.Rectangle{}, false
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), true
}
