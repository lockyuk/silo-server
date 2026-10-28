package episodeframes

import (
	"image"
	"math"
)

// A frame whose mean luma (0-255) is under minMeanLuma is dark, a fade or a
// night scene too dim for a thumbnail; one whose luma spread is under
// minLumaSpread is blank, such as a title card on a flat background.
const (
	minMeanLuma   = 40.0
	minLumaSpread = 20.0
)

// framePositions lists the seek times, in seconds, tried for a frame: the
// chosen position first, then a few nearby ones should it be dark. An unknown
// duration takes a frame a minute in.
func framePositions(durationSeconds, position int) []float64 {
	if durationSeconds <= 0 {
		return []float64{60}
	}
	duration := float64(durationSeconds)
	var out []float64
	for _, offset := range []int{0, 5, -5, 10, 15} {
		pct := position + offset
		if pct < 2 || pct > 95 {
			continue
		}
		out = append(out, math.Round(duration*float64(pct)/100*10)/10)
	}
	return out
}

// lumaStats returns the mean and standard deviation of an image's luma,
// sampled on a grid so a 4K frame costs no more than a small one.
func lumaStats(img image.Image) (mean, spread float64) {
	b := img.Bounds()
	step := max(1, min(b.Dx(), b.Dy())/90)
	var sum, sumSq, n float64
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			var l float64
			if yc, ok := img.(*image.YCbCr); ok {
				l = float64(yc.Y[yc.YOffset(x, y)])
			} else {
				r, g, bl, _ := img.At(x, y).RGBA()
				l = (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257
			}
			sum += l
			sumSq += l * l
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	mean = sum / n
	return mean, math.Sqrt(max(0, sumSq/n-mean*mean))
}
