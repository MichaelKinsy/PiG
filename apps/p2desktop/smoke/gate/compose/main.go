// Command compose puts the native window's capture and the web UI's screenshot side by side for the visual gate:
//
//	compose native.png web.png out.png [titlebar-points]
//
// The native capture is a window capture at the display's scale (2x on retina) with the title bar on top; the tool cuts the
// title bar, scales the capture down to the web screenshot's width (a box filter) and draws native on the left and web on the
// right, with a one-pixel gap, so a reader compares the same region of the same window size.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strconv"
)

func load(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, path, err)
		os.Exit(1)
	}
	return img
}

// box scales src to w by h averaging the source pixels each destination pixel covers.
func box(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+(y+1)*b.Dy()/h
		for x := range w {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+(x+1)*b.Dx()/w
			var r, g, bl, n uint32
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r, g, bl, n = r+cr, g+cg, bl+cb, n+1
				}
			}
			out.Set(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
		}
	}
	return out
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: compose native.png web.png out.png [titlebar-points]")
		os.Exit(2)
	}
	titlebar := 28.0
	if len(os.Args) > 4 {
		titlebar, _ = strconv.ParseFloat(os.Args[4], 64)
	}
	native, web := load(os.Args[1]), load(os.Args[2])
	webB := web.Bounds()
	// The native capture's scale: its width over the web's (the window and the viewport have the same width in points).
	scale := float64(native.Bounds().Dx()) / float64(webB.Dx())
	cut := int(titlebar * scale)
	nb := native.Bounds()
	body := native.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(image.Rect(nb.Min.X, nb.Min.Y+cut, nb.Max.X, nb.Max.Y))
	scaled := box(body, webB.Dx(), int(float64(body.Bounds().Dy())/scale))
	h := max(scaled.Bounds().Dy(), webB.Dy())
	out := image.NewRGBA(image.Rect(0, 0, webB.Dx()*2+1, h))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.RGBA{128, 128, 128, 255}), image.Point{}, draw.Src)
	draw.Draw(out, image.Rect(0, 0, webB.Dx(), scaled.Bounds().Dy()), scaled, image.Point{}, draw.Src)
	draw.Draw(out, image.Rect(webB.Dx()+1, 0, webB.Dx()*2+1, webB.Dy()), web, webB.Min, draw.Src)
	f, err := os.Create(os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
