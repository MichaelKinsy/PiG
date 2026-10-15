package imageprocessing

import (
	"bytes"
	"image"
	"image/png"
	"testing"
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
