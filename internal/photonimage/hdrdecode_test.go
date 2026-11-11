// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"
	"math"
	"testing"
)

func TestExp2fIsExactForTheExponentsOfRGBE(t *testing.T) {
	for e := 1; e < 256; e++ {
		x := float32(e) - 136
		if got, want := exp2f(x), float32(math.Ldexp(1, e-136)); got != want {
			t.Errorf("exp2f(%v) = %v, want %v", x, got, want)
		}
	}
}

// TestHDRConversionTableIsLibms is the proof that math.Pow stands in for libm's powf: the decoder's table of 8-bit values equals the one
// the literal port of libm's exp2f and powf gives, for every exponent and mantissa byte.
func TestHDRConversionTableIsLibms(t *testing.T) {
	ldr := hdrLDR()
	var differ int
	for e := 1; e < 256; e++ {
		for c := range 256 {
			v := float32(exp2f(float32(float32(e)-float32(128.0+8.0))) * float32(c))
			fv := float32(float32(powf(v*1.0, 2.2)*255) + 0.5)
			want := byte(0)
			switch {
			case fv < 0:
			case fv > 255:
				want = 255
			default:
				want = byte(fv)
			}
			if ldr[e][c] != want {
				differ++
				t.Errorf("e=%d c=%d: %d, libm %d", e, c, ldr[e][c], want)
			}
		}
	}
	for c := range 256 {
		if ldr[0][c] != 0 {
			t.Fatalf("exponent 0 gives %d", ldr[0][c])
		}
	}
	t.Logf("%d of 65536 pairs differ from libm", differ)
}

func TestDecodeHDRReadsBothRunLengthEncodingsAndFlatPixels(t *testing.T) {
	row := []hdrPixel{{10, 20, 30, 136}, {200, 100, 50, 136}, {200, 100, 50, 136}, {200, 100, 50, 136}, {0, 0, 0, 0}}
	header := hdrHeader("FORMAT=32-bit_rle_rgbe\n", len(row), 1)
	ldr := hdrLDR()
	var want []byte
	for _, p := range row {
		want = append(want, ldr[p[3]][p[0]], ldr[p[3]][p[1]], ldr[p[3]][p[2]], 255)
	}
	for name, file := range map[string][]byte{
		"flat":    hdrFlat(header, row),
		"old-rle": hdrOldRLE(header, row),
		"new-rle": hdrNewRLE(header, row),
	} {
		img, err := DecodeHDR(file)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if img.Rect != image.Rect(0, 0, len(row), 1) || !bytes.Equal(img.Pix, want) {
			t.Errorf("%s: %v %v, want %v", name, img.Rect, img.Pix, want)
		}
	}
}

func TestDecodeHDRRefusesWhatImageRefuses(t *testing.T) {
	good := hdrFlat(hdrHeader("", 2, 1), []hdrPixel{{9, 9, 9, 130}, {9, 9, 9, 130}})
	if _, err := DecodeHDR(good); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"xyze", hdrFlat(hdrHeader("FORMAT=32-bit_rle_xyze\n", 2, 1), []hdrPixel{{9, 9, 9, 130}, {9, 9, 9, 130}})},
		{"flipped", append([]byte("#?RADIANCE\n\n+Y 1 +X 2\n"), good[len(hdrHeader("", 2, 1)):]...)},
		{"truncated", good[:len(good)-1]},
		{"no empty line", []byte("#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n-Y 1 +X 1\n")},
		{"over the limit", hdrHeader("", 16384, 10923)},
		{"first pixel is a marker", hdrFlat(hdrHeader("", 2, 1), []hdrPixel{{1, 1, 1, 3}, {9, 9, 9, 130}})},
	} {
		if _, err := DecodeHDR(tc.data); err == nil {
			t.Errorf("%s decodes", tc.name)
		}
	}
	if _, err := DecodeHDR(hdrHeader("", 0, 1<<30)); err != nil {
		t.Errorf("a 0-wide image: %v", err)
	}
	if _, err := DecodeHDR(hdrHeader("", 1<<30, 0)); err == nil {
		t.Error("an empty image 2^30 wide decodes")
	}
}

func TestDecodeRoutesHDR(t *testing.T) {
	img, err := Decode(hdrFlat(hdrHeader("", 1, 1), []hdrPixel{{255, 255, 255, 136}}))
	if err != nil {
		t.Fatal(err)
	}
	if nrgba, ok := img.(*image.NRGBA); !ok || !bytes.Equal(nrgba.Pix, []byte{255, 255, 255, 255}) {
		t.Errorf("Decode gave %T %v", img, img)
	}
}
