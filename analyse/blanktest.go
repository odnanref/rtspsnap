package analyse

import (
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
)

// BlankStats holds the measurements used to decide if an image is blank.
type BlankStats struct {
	Mean       float64 // average luma, 0-255
	StdDev     float64 // brightness variation; ~0 means uniform
	DarkRatio  float64 // fraction of pixels with luma < 16
	LightRatio float64 // fraction of pixels with luma > 240
	Samples    int
}

// Tunable thresholds.
const (
	minStdDev    = 6.0  // below this, the picture is essentially one flat colour
	maxMeanBlack = 12.0 // very dark overall
	minMeanWhite = 243.0
	extremeRatio = 0.995 // share of pixels that are almost pure black or white
)

// analyze samples the image on a grid, so it stays fast even at 4K.
func analyze(img image.Image) BlankStats {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	// Aim for about 20,000 samples.
	step := int(math.Sqrt(float64(w*h) / 20000))
	if step < 1 {
		step = 1
	}

	var sum, sumSq float64
	var n, dark, light int

	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r, g, bl, _ := img.At(x, y).RGBA() // 16-bit values
			luma := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257.0

			sum += luma
			sumSq += luma * luma
			n++
			if luma < 16 {
				dark++
			}
			if luma > 240 {
				light++
			}
		}
	}
	if n == 0 {
		return BlankStats{}
	}

	mean := sum / float64(n)
	variance := sumSq/float64(n) - mean*mean
	if variance < 0 {
		variance = 0
	}
	return BlankStats{
		Mean:       mean,
		StdDev:     math.Sqrt(variance),
		DarkRatio:  float64(dark) / float64(n),
		LightRatio: float64(light) / float64(n),
		Samples:    n,
	}
}

// isBlank returns whether the image looks blank, with a reason.
func isBlank(img image.Image) (bool, string, BlankStats) {
	s := analyze(img)

	switch {
	case s.Samples == 0:
		return true, "empty image", s
	case s.StdDev < minStdDev:
		return true, "uniform image (almost no brightness variation)", s
	case s.Mean < maxMeanBlack && s.DarkRatio > extremeRatio:
		return true, "black image", s
	case s.Mean > minMeanWhite && s.LightRatio > extremeRatio:
		return true, "white image", s
	}
	return false, "ok", s
}

// Public isBlank
func IsBlank(img image.Image) (bool, string, BlankStats) {
	return isBlank(img)
}

// LoadJPEG is used by the CLI below.
func LoadJPEG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return jpeg.Decode(f)
}

func PrintStats(s BlankStats) {
	fmt.Printf("mean=%.1f stddev=%.1f dark=%.1f%% light=%.1f%% samples=%d\n",
		s.Mean, s.StdDev, s.DarkRatio*100, s.LightRatio*100, s.Samples)
}
