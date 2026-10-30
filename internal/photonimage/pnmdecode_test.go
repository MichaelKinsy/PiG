// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"
	"testing"
)

func TestDecodePNMPixels(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		w, h int
		want []byte
	}{
		{"P1 black is one", "P1\n2 1\n1 0\n", 2, 1, []byte{0, 0, 0, 255, 255, 255, 255, 255}},
		{"P1 without separators", "P1\n2 1\n10", 2, 1, []byte{0, 0, 0, 255, 255, 255, 255, 255}},
		{"P4 padding is ignored", "P4\n3 2\n\xA0\x40", 3, 2, []byte{0, 0, 0, 255, 255, 255, 255, 255, 0, 0, 0, 255, 255, 255, 255, 255, 0, 0, 0, 255, 255, 255, 255, 255}},
		{"P2 scales by maxval", "P2\n3 1\n10\n0 5 10\n", 3, 1, []byte{0, 0, 0, 255, 128, 128, 128, 255, 255, 255, 255, 255}},
		{"P2 above the maxval saturates", "P2\n2 1\n10\n11 200\n", 2, 1, []byte{255, 255, 255, 255, 255, 255, 255, 255}},
		{"P5 16 bit", "P5\n2 1\n65535\n\x00\x80\xff\xff", 2, 1, []byte{0, 0, 0, 255, 255, 255, 255, 255}},
		{"P3", "P3\n1 1\n255\n1 2 3\n", 1, 1, []byte{1, 2, 3, 255}},
		{"P6 16 bit maxval 1000", "P6\n1 1\n1000\n\x03\xe8\x01\xf4\x00\x00", 1, 1, []byte{255, 128, 0, 255}},
		{"PAM black and white", "P7\nWIDTH 2\nHEIGHT 1\nDEPTH 1\nMAXVAL 1\nTUPLTYPE BLACKANDWHITE\nENDHDR\n\x01\x00", 2, 1, []byte{255, 255, 255, 255, 0, 0, 0, 255}},
		{"PAM rgb", "P7\nWIDTH 1\nHEIGHT 1\nDEPTH 3\nMAXVAL 255\nTUPLTYPE RGB\nENDHDR\n\x09\x08\x07", 1, 1, []byte{9, 8, 7, 255}},
		{"preamble comment glues numbers", "P5\n1#c\n2 1\n255\n\x09", 12, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := DecodePNM([]byte(tc.data))
			if tc.want == nil {
				// "1#c\n2" is the number 12: a 12x1 image that has one byte too few.
				if err == nil {
					t.Fatalf("decoded a %v image", img.Rect)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if img.Rect != image.Rect(0, 0, tc.w, tc.h) || !bytes.Equal(img.Pix, tc.want) {
				t.Errorf("%v %v, want %dx%d %v", img.Rect, img.Pix, tc.w, tc.h, tc.want)
			}
		})
	}
}

func TestDecodePNMRefuses(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"maxval zero", "P5\n1 1\n0\n\x01"},
		{"maxval too big", "P5\n1 1\n65536\n\x01\x02"},
		{"p2 sample over 255 in 8 bit", "P2\n1 1\n100\n256\n"},
		{"p1 bad digit", "P1\n1 1\n2\n"},
		{"short raster", "P5\n2 2\n255\n\x01\x02\x03"},
		{"pam leading space", "P7\n WIDTH 1\nHEIGHT 1\nDEPTH 1\nMAXVAL 255\nENDHDR\n\x01"},
		{"pam rgb alpha", "P7\nWIDTH 1\nHEIGHT 1\nDEPTH 4\nMAXVAL 255\nTUPLTYPE RGB_ALPHA\nENDHDR\n\x01\x02\x03\x04"},
		{"pam bw sample 2", "P7\nWIDTH 1\nHEIGHT 1\nDEPTH 1\nMAXVAL 1\nTUPLTYPE BLACKANDWHITE\nENDHDR\n\x02"},
		{"over the limit", "P5\n32769 16384\n255\n"},
		{"empty image 2^30 wide", "P5\n1073741824 0\n255\n"},
	} {
		if _, err := DecodePNM([]byte(tc.data)); err == nil {
			t.Errorf("%s decodes", tc.name)
		}
	}
}

func TestDecodeRoutesPNM(t *testing.T) {
	img, err := Decode([]byte("P6\n1 1\n255\n\x01\x02\x03"))
	if err != nil {
		t.Fatal(err)
	}
	if nrgba, ok := img.(*image.NRGBA); !ok || !bytes.Equal(nrgba.Pix, []byte{1, 2, 3, 255}) {
		t.Errorf("Decode gave %T %v", img, img)
	}
}
