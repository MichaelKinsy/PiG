// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"

	xbmp "golang.org/x/image/bmp"
)

// benchPicture is a w x h picture with gradients, edges and some noise, so that its PNG and GIF compress as pictures do.
func benchPicture(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	noise := icoNoise(9, 4096)
	for y := range h {
		for x := range w {
			n := noise[(x*31+y*17)%len(noise)] >> 6
			v := byte((x*255/w + y*255/h) / 2)
			if (x/64+y/64)%2 == 0 {
				v = 255 - v
			}
			img.SetNRGBA(x, y, color.NRGBA{v + n, byte(y * 255 / h), byte(x*255/w) + n, 255})
		}
	}
	return img
}

func benchPNG(img image.Image, interlaced bool) []byte {
	var buf bytes.Buffer
	if interlaced {
		// Go's encoder does not interlace: write the image data of a hand-made file.
		raw := pngRawInterlacedGradient(img.Bounds().Dx(), img.Bounds().Dy(), 32)
		return pngChunks([2]any{"IHDR", pngIHDR(uint32(img.Bounds().Dx()), uint32(img.Bounds().Dy()), 8, 6, 1)}, [2]any{"IDAT", zlibOf(raw)}, [2]any{"IEND", []byte{}})
	}
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// BenchmarkDecodePNGFiles, BenchmarkDecodeBMPFiles and BenchmarkDecodeGIFFiles compare the Photon ports behind Decode with the decoders of the
// Go distribution on pictures of 1920x1080 (and 4000x3000 for a PNG photograph). Go's decoders give other image types than RGBA (and other
// pixels or errors for some files), so the work is not identical: the RGBA conversion of Photon's ports is part of their time.
func BenchmarkDecodePNGFiles(b *testing.B) {
	full := benchPicture(1920, 1080)
	paletted := image.NewPaletted(full.Rect, color.Palette{color.NRGBA{0, 0, 0, 255}, color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 255, 0, 255}, color.NRGBA{255, 255, 255, 128}})
	for i := range paletted.Pix {
		paletted.Pix[i] = byte(i/7%4) ^ byte(i/1920%3)
	}
	gray16 := image.NewGray16(image.Rect(0, 0, 1920, 1080))
	for i := range gray16.Pix {
		gray16.Pix[i] = byte(i*7/13) ^ byte(i>>9)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"rgba8-1920x1080", benchPNG(full, false)},
		{"rgba8-adam7-640x360", benchPNG(benchPicture(640, 360), true)},
		{"paletted8-1920x1080", benchPNG(paletted, false)},
		{"gray16-1920x1080", benchPNG(gray16, false)},
		{"rgba8-4000x3000", benchPNG(benchPicture(4000, 3000), false)},
	} {
		b.Run(tc.name+"/photon", func(b *testing.B) {
			benchmarkDecode(b, tc.data, func(d []byte) error { _, err := Decode(d); return err })
		})
		b.Run(tc.name+"/go", func(b *testing.B) {
			benchmarkDecode(b, tc.data, func(d []byte) error { _, err := png.Decode(bytes.NewReader(d)); return err })
		})
	}
}

func BenchmarkDecodeBMPFiles(b *testing.B) {
	var buf bytes.Buffer
	if err := xbmp.Encode(&buf, benchPicture(1920, 1080)); err != nil {
		b.Fatal(err)
	}
	data := buf.Bytes()
	b.Run("bmp32-1920x1080/photon", func(b *testing.B) { benchmarkDecode(b, data, func(d []byte) error { _, err := Decode(d); return err }) })
	b.Run("bmp32-1920x1080/go", func(b *testing.B) {
		benchmarkDecode(b, data, func(d []byte) error { _, err := xbmp.Decode(bytes.NewReader(d)); return err })
	})
}

func BenchmarkDecodeGIFFiles(b *testing.B) {
	var buf bytes.Buffer
	src := benchPicture(1920, 1080)
	if err := gif.Encode(&buf, src, &gif.Options{NumColors: 256}); err != nil {
		b.Fatal(err)
	}
	data := buf.Bytes()
	b.Run("gif256-1920x1080/photon", func(b *testing.B) { benchmarkDecode(b, data, func(d []byte) error { _, err := Decode(d); return err }) })
	b.Run("gif256-1920x1080/go", func(b *testing.B) {
		benchmarkDecode(b, data, func(d []byte) error { _, err := gif.Decode(bytes.NewReader(d)); return err })
	})
}

func benchmarkDecode(b *testing.B, data []byte, decode func([]byte) error) {
	b.Helper()
	if err := decode(data); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if err := decode(data); err != nil {
			b.Fatal(err)
		}
	}
}
