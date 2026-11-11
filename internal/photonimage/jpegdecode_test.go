// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"
	"image/jpeg"
	"math"
	"strings"
	"testing"
)

// f2f is the `stbi_f2f` of jpeg-decoder: `(x * (1 << bits) as f32 + 0.5) as i32` in f32 (the cast truncates toward zero).
func f2f(x float32, bits uint) int32 {
	scaled := float32(x * float32(uint32(1)<<bits))
	return int32(float32(scaled + 0.5))
}

func TestJPEGDecodeFixedPointConstants(t *testing.T) {
	for _, c := range []struct {
		x    float32
		bits uint
		want int32
	}{
		{0.5411961, 12, jpegIDCTc0541}, {-1.847759065, 12, jpegIDCTc1847}, {0.765366865, 12, jpegIDCTc0765},
		{1.175875602, 12, jpegIDCTc1175}, {0.298631336, 12, jpegIDCTc0298}, {2.053119869, 12, jpegIDCTc2053},
		{3.072711026, 12, jpegIDCTc3072}, {1.501321110, 12, jpegIDCTc1501}, {-0.899976223, 12, jpegIDCTc0899},
		{-2.562915447, 12, jpegIDCTc2562}, {-1.961570560, 12, jpegIDCTc1961}, {-0.390180644, 12, jpegIDCTc0390},
		{1.40200, jpegFixedPointOffset, jpegYCbCrR}, {0.34414, jpegFixedPointOffset, jpegYCbCrGCb},
		{0.71414, jpegFixedPointOffset, jpegYCbCrGCr}, {1.77200, jpegFixedPointOffset, jpegYCbCrB},
	} {
		if got := f2f(c.x, c.bits); got != c.want {
			t.Errorf("stbi_f2f(%v) at %d bits is %d, the constant is %d", c.x, c.bits, got, c.want)
		}
	}
	if jpegIDCTXScale != 65536+(128<<17) {
		t.Errorf("X_SCALE = %d", jpegIDCTXScale)
	}
}

// TestJPEGNearFarRowsMatchTheF32Formula compares jpegNearFarRows with the f32 evaluation of upsampler.rs:172-177 for every row of
// every image height up to 4000 and for the tallest ones.
func TestJPEGNearFarRowsMatchTheF32Formula(t *testing.T) {
	check := func(outputHeight int) {
		inputHeight := (outputHeight + 1) / 2
		for row := range outputHeight {
			rowNear := float32(row) / 2
			fract := rowNear - float32(math.Trunc(float64(rowNear)))
			rowFar := min(float32(rowNear+float32(fract*3))-0.25, float32(inputHeight-1))
			wantNear := int(rowNear)
			wantFar := 0
			if rowFar > 0 {
				wantFar = int(rowFar)
			}
			near, far := jpegNearFarRows(row, inputHeight)
			if near != wantNear || far != wantFar {
				t.Fatalf("height %d row %d: near %d far %d, f32 gives %d and %d", outputHeight, row, near, far, wantNear, wantFar)
			}
		}
	}
	for h := 1; h <= 4000; h++ {
		check(h)
	}
	for _, h := range []int{8191, 8192, 8193, 16383, 16384, 32767, 32768, 65534, 65535} {
		check(h)
	}
}

// TestJPEGWriterProgressiveEqualsSequential decodes the same coefficients written as a sequential and as a progressive stream
// (every script, with and without restart intervals): the pixels must be the same, which tests the progressive writer, the
// refinement decoding and the plane assembly together.
func TestJPEGWriterProgressiveEqualsSequential(t *testing.T) {
	var b jdcBuilder
	for _, name := range []string{"444", "420", "422", "440", "411", "mixed", "h3"} {
		for _, sz := range [][2]int{{9, 17}, {33, 31}, {65, 20}} {
			s := jdcSamplingSpec(sz[0], sz[1], name, jdcYCbCrIDs)
			s.fillImage(b.image("photo", sz[0], sz[1]), "ycbcr")
			s.scans = jdcSeqInterleaved(3)
			want, err := DecodeJPEG(s.build())
			if err != nil {
				t.Fatalf("%s %v sequential: %v", name, sz, err)
			}
			for script := range 6 {
				for _, restart := range []int{0, 3} {
					s.sof = 0xC2
					s.restart = restart
					s.scans = jdcProgScript(3, script, jdcInterleavedFits(s))
					got, err := DecodeJPEG(s.build())
					if err != nil {
						t.Fatalf("%s %v script %d restart %d: %v", name, sz, script, restart, err)
					}
					if !bytes.Equal(got.Pix, want.Pix) {
						t.Errorf("%s %v script %d restart %d: the progressive pixels differ from the sequential ones", name, sz, script, restart)
					}
				}
			}
		}
	}
}

// TestJPEGWriterBaselineMatchesGo decodes the writer's baseline streams with Go's image/jpeg: the writer's streams are valid
// JPEG files, and the pixels of the two decoders agree to within the rounding of their IDCTs, for grayscale and 4:4:4, where the
// two decoders do no upsampling.
func TestJPEGWriterBaselineMatchesGo(t *testing.T) {
	var b jdcBuilder
	for _, c := range []struct {
		name  string
		space string
		hv    [][2]uint8
	}{{"gray", "gray", [][2]uint8{{1, 1}}}, {"444", "ycbcr", [][2]uint8{{1, 1}, {1, 1}, {1, 1}}}} {
		for _, sz := range [][2]int{{16, 16}, {33, 31}, {100, 75}} {
			s := jdcNewSpec(sz[0], sz[1], c.hv, jdcYCbCrIDs[:len(c.hv)], 90)
			s.fillImage(b.image("photo", sz[0], sz[1]), c.space)
			s.scans = jdcSeqInterleaved(len(c.hv))
			data := s.build()
			ours, err := DecodeJPEG(data)
			if err != nil {
				t.Fatal(err)
			}
			goImg, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s %v: image/jpeg: %v", c.name, sz, err)
			}
			worst := 0
			for y := range sz[1] {
				for x := range sz[0] {
					r, g, bl, _ := goImg.At(x, y).RGBA()
					o := ours.PixOffset(x, y)
					for i, v := range []uint32{r >> 8, g >> 8, bl >> 8} {
						worst = max(worst, jdwAbs(int(v)-int(ours.Pix[o+i])))
					}
				}
			}
			if worst > 3 {
				t.Errorf("%s %v: the pixels differ from image/jpeg's by up to %d", c.name, sz, worst)
			}
		}
	}
}

// TestDecodeJPEGLimits: an image whose width * height * bytes per pixel is over the 512 MiB of image 0.24.9's default limits is
// refused before any scan is read.
func TestDecodeJPEGLimits(t *testing.T) {
	for _, c := range []struct {
		w, h    int
		hv      [][2]uint8
		limited bool
	}{
		{24000, 24000, [][2]uint8{{1, 1}}, true},
		{23170, 23170, [][2]uint8{{1, 1}}, false},
		{23171, 23170, [][2]uint8{{1, 1}}, true},
		{10000, 17900, [][2]uint8{{1, 1}, {1, 1}, {1, 1}}, true},
		{10000, 17895, [][2]uint8{{1, 1}, {1, 1}, {1, 1}}, false},
		{10000, 17895, [][2]uint8{{1, 1}, {1, 1}, {1, 1}, {1, 1}}, false},
		{10000, 17900, [][2]uint8{{1, 1}, {1, 1}, {1, 1}, {1, 1}}, true},
	} {
		s := jdcNewSpec(c.w, c.h, c.hv, []uint8{1, 2, 3, 4}[:len(c.hv)], 50)
		s.scans = nil
		_, err := DecodeJPEG(jdwJoin(s.segments()))
		if err == nil {
			t.Fatalf("%dx%d with %d components: no error without scans", c.w, c.h, len(c.hv))
		}
		if limited := strings.Contains(err.Error(), "512 MiB"); limited != c.limited {
			t.Errorf("%dx%d with %d components: limited = %v, want %v (%v)", c.w, c.h, len(c.hv), limited, c.limited, err)
		}
	}
}

// TestDecodeJPEGOutputIsNRGBA: grayscale, colour and CMYK streams give opaque NRGBA images of the frame's size.
func TestDecodeJPEGOutputIsNRGBA(t *testing.T) {
	var b jdcBuilder
	s := jdcNewSpec(5, 3, [][2]uint8{{1, 1}}, []uint8{1}, 90)
	s.fillImage(b.image("hgrad", 5, 3), "gray")
	s.scans = jdcSeqInterleaved(1)
	img, err := DecodeJPEG(s.build())
	if err != nil {
		t.Fatal(err)
	}
	if img.Rect != image.Rect(0, 0, 5, 3) || img.Stride != 20 || len(img.Pix) != 60 {
		t.Fatalf("got %v stride %d len %d", img.Rect, img.Stride, len(img.Pix))
	}
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] != img.Pix[i+1] || img.Pix[i] != img.Pix[i+2] || img.Pix[i+3] != 255 {
			t.Fatalf("pixel %d is %v, want gray with alpha 255", i/4, img.Pix[i:i+4])
		}
	}
}
