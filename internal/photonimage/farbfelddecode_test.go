// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"
	"testing"
)

func TestDecodeFarbfeldConvertsSixteenBitSamplesLikeToRGBA8(t *testing.T) {
	// (v + 128) / 257 is image color.rs:408-421; the pairs are where the rounding switches.
	for _, tc := range []struct {
		sample uint16
		want   byte
	}{{0, 0}, {128, 0}, {129, 1}, {384, 1}, {385, 1}, {386, 2}, {0x7FFF, 127}, {0x8000, 128}, {0xFF7E, 254}, {0xFF7F, 255}, {0xFFFF, 255}} {
		img, err := DecodeFarbfeld(farbfeldFile(1, 1, []uint16{tc.sample, 0, 0xFFFF, tc.sample}))
		if err != nil {
			t.Fatal(err)
		}
		if want := []byte{tc.want, 0, 255, tc.want}; !bytes.Equal(img.Pix, want) {
			t.Errorf("sample %#x: pixel %v, want %v", tc.sample, img.Pix, want)
		}
	}
}

func TestDecodeFarbfeldReadsBigEndianRowMajor(t *testing.T) {
	img, err := DecodeFarbfeld(farbfeldFile(2, 2, []uint16{
		0x0100, 0x0200, 0x0300, 0x0400, 0x0500, 0x0600, 0x0700, 0x0800,
		0x0900, 0x0A00, 0x0B00, 0x0C00, 0x0D00, 0x0E00, 0x0F00, 0x1000,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if img.Rect != image.Rect(0, 0, 2, 2) {
		t.Fatalf("bounds %v", img.Rect)
	}
	// 0x0100 is 256: (256+128)/257 = 1; 0x1000 is 4096: (4096+128)/257 = 16.
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	if !bytes.Equal(img.Pix, want) {
		t.Errorf("pixels %v, want %v", img.Pix, want)
	}
}

func TestDecodeFarbfeldRefusesWhatImageRefuses(t *testing.T) {
	whole := farbfeldFile(2, 1, []uint16{1, 2, 3, 4, 5, 6, 7, 8})
	for n := range len(whole) {
		if _, err := DecodeFarbfeld(whole[:n]); err == nil {
			t.Errorf("a file cut at %d of %d bytes decodes", n, len(whole))
		}
	}
	if _, err := DecodeFarbfeld(append(whole, 1, 2, 3)); err != nil {
		t.Errorf("trailing bytes: %v", err)
	}
	if _, err := DecodeFarbfeld(farbfeldFile(1<<26, 2, nil)); err == nil {
		t.Error("an image of 2^30 bytes decodes")
	}
	if _, err := DecodeFarbfeld(farbfeldFile(1<<30, 0, nil)); err == nil {
		t.Error("an empty image 2^30 wide decodes: the RGBA buffer length overflows wasm32's usize")
	}
	if img, err := DecodeFarbfeld(farbfeldFile(1<<30-1, 0, nil)); err != nil || img.Rect.Dx() != 1<<30-1 {
		t.Errorf("an empty image 2^30-1 wide: %v %v", img, err)
	}
}

func TestDecodeRoutesFarbfeld(t *testing.T) {
	img, err := Decode(farbfeldFile(1, 1, []uint16{0xFFFF, 0, 0, 0xFFFF}))
	if err != nil {
		t.Fatal(err)
	}
	nrgba, ok := img.(*image.NRGBA)
	if !ok || !bytes.Equal(nrgba.Pix, []byte{255, 0, 0, 255}) {
		t.Errorf("Decode gave %T %v", img, img)
	}
}
