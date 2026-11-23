//go:build ignore

// Generates the app icons: a simple white headset on black.
// Run: go run tools/genicon.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	for _, out := range []struct {
		path string
		size int
	}{
		{"internal/server/web/icon-180.png", 180},
		{"internal/server/web/icon-512.png", 512},
		{"Icon.png", 512},
	} {
		write(out.path, draw(out.size))
	}
}

func draw(n int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	s := float64(n)
	white := color.RGBA{240, 240, 240, 255}
	bg := color.RGBA{10, 10, 14, 255}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			c := bg
			// Headset body: rounded rectangle.
			if roundRect(fx, fy, 0.14*s, 0.32*s, 0.86*s, 0.68*s, 0.12*s) {
				c = white
				// Two lenses cut out.
				if math.Hypot(fx-0.35*s, fy-0.5*s) < 0.11*s || math.Hypot(fx-0.65*s, fy-0.5*s) < 0.11*s {
					c = bg
				}
				// Nose notch.
				if fy > 0.58*s && math.Abs(fx-0.5*s) < 0.07*s-(0.68*s-fy)*0.5 {
					c = bg
				}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func roundRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(x, x1-r))
	cy := math.Max(y0+r, math.Min(y, y1-r))
	return math.Hypot(x-cx, y-cy) <= r
}

func write(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
