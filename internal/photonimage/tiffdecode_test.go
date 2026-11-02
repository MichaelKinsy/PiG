// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"math/rand/v2"
	"testing"
)

// The pixels of a few small files, worked out by hand from tiff 0.9.1 and image 0.24.9 (the corpus of TestDecodeTIFFMatchesPhoton
// compares thousands of files with Photon itself).
func TestDecodeTIFFPixels(t *testing.T) {
	for _, little := range []bool{true, false} {
		for _, tc := range []struct {
			name string
			spec tiffSpec
			gen  func(x, y, c int) uint32
			want func(x, y int) color.NRGBA
		}{
			{
				name: "gray8", spec: tiffSpec{width: 3, height: 2, samples: 1, bits: 8, photometric: tiffBlackIsZero},
				gen:  func(x, y, _ int) uint32 { return uint32(10*x + y) },
				want: func(x, y int) color.NRGBA { v := uint8(10*x + y); return color.NRGBA{v, v, v, 255} },
			},
			{
				name: "white-is-zero gray8", spec: tiffSpec{width: 3, height: 2, samples: 1, bits: 8, photometric: tiffWhiteIsZero},
				gen:  func(x, y, _ int) uint32 { return uint32(10*x + y) },
				want: func(x, y int) color.NRGBA { v := 255 - uint8(10*x+y); return color.NRGBA{v, v, v, 255} },
			},
			{
				// A 16-bit sample is scaled to eight bits as round(v * 255 / 65535).
				name: "gray16", spec: tiffSpec{width: 3, height: 2, samples: 1, bits: 16, photometric: tiffBlackIsZero},
				gen: func(x, y, _ int) uint32 { return uint32(0x1234 + 0x2000*x + y) },
				want: func(x, y int) color.NRGBA {
					v := uint8((0x1234 + 0x2000*x + y + 128) / 257)
					return color.NRGBA{v, v, v, 255}
				},
			},
			{
				name: "rgba16 keeps the alpha straight", spec: tiffSpec{width: 3, height: 2, samples: 4, bits: 16, photometric: tiffRGB, extraSamples: true},
				gen: func(x, y, c int) uint32 { return uint32(0xFFFF - 0x1111*c - 0x300*x - y) },
				want: func(x, y int) color.NRGBA {
					ch := func(c int) uint8 { return uint8((0xFFFF - 0x1111*c - 0x300*x - y + 128) / 257) }
					return color.NRGBA{ch(0), ch(1), ch(2), ch(3)}
				},
			},
			{
				// image's cmyk_to_rgb: (255 - c) * (1 - k / 255), truncated, in f32.
				name: "cmyk8", spec: tiffSpec{width: 3, height: 2, samples: 4, bits: 8, photometric: tiffCMYK},
				gen: func(x, y, c int) uint32 { return uint32([]int{10 + 40*x, 5 * y, 200, 77 * (x + y)}[c]) },
				want: func(x, y int) color.NRGBA {
					cmyk := [4]byte{byte(10 + 40*x), byte(5 * y), 200, byte(77 * (x + y))}
					r, g, b := tiffCMYKToRGB(cmyk[0], cmyk[1], cmyk[2], cmyk[3])
					return color.NRGBA{r, g, b, 255}
				},
			},
		} {
			for _, compression := range []int{tiffCompressionNone, tiffCompressionLZW, tiffCompressionPackBits, tiffCompressionDeflate} {
				for _, predictor := range []int{0, tiffPredictorHorizontal} {
					s := tc.spec
					s.little, s.gen, s.compression, s.predictor = little, tc.gen, compression, predictor
					img, err := Decode(tiffBuild(s))
					if err != nil {
						t.Errorf("%s little=%v compression=%d predictor=%d: %v", tc.name, little, compression, predictor, err)
						continue
					}
					got, ok := img.(*image.NRGBA)
					if !ok || got.Rect != image.Rect(0, 0, 3, 2) {
						t.Errorf("%s: %T %v", tc.name, img, img.Bounds())
						continue
					}
					for y := range 2 {
						for x := range 3 {
							if g, w := got.NRGBAAt(x, y), tc.want(x, y); g != w {
								t.Errorf("%s little=%v compression=%d predictor=%d: (%d,%d) is %v, want %v", tc.name, little, compression, predictor, x, y, g, w)
							}
						}
					}
				}
			}
		}
	}
}

// Photon refuses what tiff does not read: palettes, bilevel images, YCbCr, floating point samples, the floating point predictor.
func TestDecodeTIFFRefusesWhatTiffRefuses(t *testing.T) {
	base := tiffSpec{width: 4, height: 4, samples: 1, bits: 8, photometric: tiffBlackIsZero}
	for name, mutate := range map[string]func(*tiffSpec){
		"palette":    func(s *tiffSpec) { s.photometric = 3 },
		"bilevel":    func(s *tiffSpec) { s.bits = 1 },
		"4 bit":      func(s *tiffSpec) { s.bits = 4 },
		"32 bit":     func(s *tiffSpec) { s.bits = 32 },
		"gray alpha": func(s *tiffSpec) { s.samples = 2 },
		"YCbCr":      func(s *tiffSpec) { s.photometric, s.samples = tiffYCbCr, 3 },
		"float":      func(s *tiffSpec) { s.sampleFormat = []uint16{3} },
		"signed":     func(s *tiffSpec) { s.sampleFormat = []uint16{2} },
		"float pred": func(s *tiffSpec) { s.predictor = 3 },
		"fax":        func(s *tiffSpec) { s.compression = 4 },
		"old jpeg":   func(s *tiffSpec) { s.compression = 6 },
		"planar RGB": func(s *tiffSpec) { s.photometric, s.samples, s.planar = tiffRGB, 3, true },
		"zero samples": func(s *tiffSpec) {
			s.tweak = func(p *tiffParts) { p.set(tiffOrder{true}.shorts(tiffTagSamplesPerPixel, 0)) }
		},
	} {
		s := base
		s.little = true
		mutate(&s)
		if img, err := Decode(tiffBuild(s)); err == nil {
			t.Errorf("%s: decoded a %v image", name, img.Bounds())
		}
	}
	// A BigTIFF is not found by image's guess_format.
	big := tiffBuild(tiffSpec{little: true, width: 2, height: 2, samples: 1, bits: 8, photometric: tiffBlackIsZero})
	copy(big, "II+\x00")
	if img, err := Decode(big); err == nil {
		t.Errorf("BigTIFF decoded as a %v image", img.Bounds())
	}
}

// The LZW reader gives back what an encoder wrote, whatever the sizes of the reads, and ends at the end code.
func TestTIFFLZWRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, opt := range []tiffLZWOptions{{}, {clearAt: 4096}, {clearAt: 700}, {noInitialClear: true}} {
		for _, n := range []int{1, 2, 255, 256, 5000, 70000} {
			raw := make([]byte, n)
			for i := range raw {
				if rng.IntN(3) == 0 {
					raw[i] = byte(rng.IntN(256))
				} else {
					raw[i] = byte(i / 7)
				}
			}
			for _, step := range []int{1, 7, 4096, n} {
				z := newTIFFLZWReader(tiffLZWEncode(raw, opt))
				var got []byte
				buf := make([]byte, step)
				for len(got) < n {
					c, err := z.read(buf[:min(step, n-len(got))])
					if err != nil || c == 0 {
						t.Fatalf("%+v n=%d step=%d: %d bytes read of %d: %v", opt, n, step, len(got), n, err)
					}
					got = append(got, buf[:c]...)
				}
				if !bytes.Equal(got, raw) {
					t.Fatalf("%+v n=%d step=%d: data differs", opt, n, step)
				}
				if c, err := z.read(buf); c != 0 || err != nil {
					t.Fatalf("%+v n=%d step=%d: read after the end code gave %d, %v", opt, n, step, c, err)
				}
			}
		}
	}
}

// A zlib stream of several windows of 32 KiB, read in odd pieces, is the data that was compressed.
func TestTIFFInflateRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for _, n := range []int{1, 100, 32767, 32768, 32769, 100000} {
		raw := make([]byte, n)
		for i := range raw {
			raw[i] = byte(rng.IntN(5) * (i % 3))
		}
		stream := zlibCompress(raw, -1)
		for _, step := range []int{1, 1000, 32768, n} {
			z := newTIFFInflater(stream)
			var got []byte
			buf := make([]byte, step)
			for {
				c, err := z.read(buf)
				if err != nil {
					t.Fatalf("n=%d step=%d: %v", n, step, err)
				}
				if c == 0 {
					break
				}
				got = append(got, buf[:c]...)
			}
			if !bytes.Equal(got, raw) {
				t.Fatalf("n=%d step=%d: data differs (%d bytes)", n, step, len(got))
			}
		}
	}
}

func zlibCompress(raw []byte, level int) []byte {
	var out bytes.Buffer
	zw, err := zlib.NewWriterLevel(&out, level)
	if err != nil {
		panic(err)
	}
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return out.Bytes()
}
