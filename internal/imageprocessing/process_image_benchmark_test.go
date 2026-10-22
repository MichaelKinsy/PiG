package imageprocessing

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// BenchmarkProcessImagePassThrough measures ProcessImage on a 1600x1000 PNG (about 2.6 MB) that is already within the limits,
// the common screenshot case: the bytes pass through without a base64 round trip.
func BenchmarkProcessImagePassThrough(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1000))
	seed := uint32(1)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		if seed%12 == 0 {
			img.Pix[i] = byte(seed >> 24)
		} else {
			img.Pix[i] = byte(i)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		b.Fatal(err)
	}
	in := buf.Bytes()
	b.ReportAllocs()
	for b.Loop() {
		out, _, _, err := ProcessImage(in, "image/png", true, nil)
		if err != nil || len(out) != len(in) {
			b.Fatalf("not passed through: %d bytes of %d, %v", len(out), len(in), err)
		}
	}
}

// BenchmarkResizeImageDownscale measures ResizeImage on a 2600x2600 PNG that must shrink to 1000x1000 and then fits as a PNG on
// the first rung: the resize and the first fitting encode are the whole cost, so a JPEG encode made for a candidate that is
// never used shows up here.
func BenchmarkResizeImageDownscale(b *testing.B) {
	img := image.NewNRGBA(image.Rect(0, 0, 2600, 2600))
	for y := 0; y < 2600; y += 1 + 2600/40 {
		for x := 0; x < 2600; x += 1 + 2600/40 {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		b.Fatal(err)
	}
	in := buf.Bytes()
	options := &ai.ModelImageResizeOptions{MaxWidth: 1000, MaxHeight: 1000}
	b.ReportAllocs()
	for b.Loop() {
		out, err := ResizeImage(in, "image/png", options)
		if err != nil || out.Width != 1000 || out.Height != 1000 {
			b.Fatalf("not resized to 1000x1000: %+v, %v", out, err)
		}
	}
}
