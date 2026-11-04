package photonimage

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

// BenchmarkEncodeJPEG compares EncodeJPEG (Photon's bytes) with Go's image/jpeg at the quality Pi tries first, on a 12 MP photo and a
// full-HD screenshot (the generators of jpeg_oracle_test.go).
func BenchmarkEncodeJPEG(b *testing.B) {
	for _, source := range []struct {
		name string
		img  *image.NRGBA
	}{
		{"photo12MP", corpusImage("photo-4000x3000", 0, 4000, 3000)},
		{"screenshot", corpusImage("screenshot-1920x1080", 0, 1920, 1080)},
	} {
		b.Run(source.name+"/photon", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(source.img.Pix)))
			for b.Loop() {
				if _, err := EncodeJPEG(source.img, 80); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(source.name+"/imagejpeg", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(source.img.Pix)))
			for b.Loop() {
				var out bytes.Buffer
				if err := jpeg.Encode(&out, source.img, &jpeg.Options{Quality: 80}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
