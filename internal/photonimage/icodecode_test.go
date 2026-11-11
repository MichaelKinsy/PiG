// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"
	"testing"
)

func TestDecodeICOBitmapPixelsAndMask(t *testing.T) {
	// 2x2, 32 bits (B, G, R, A), bottom row first; the AND mask (rows bottom first as well, high bit first) clears the alpha of the pixels
	// (0,1) and (1,0).
	d := newDIB(2, 2, 32)
	d.pixels = []byte{
		1, 2, 3, 200, 4, 5, 6, 201, // the bottom row: (0,1) and (1,1)
		7, 8, 9, 202, 10, 11, 12, 203, // the top row: (0,0) and (1,0)
	}
	d.mask = []byte{0x80, 0, 0, 0, 0x40, 0, 0, 0}
	img, err := DecodeICO(icoSingle(2, 2, 32, d.bytes()))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		9, 8, 7, 202, 12, 11, 10, 0,
		3, 2, 1, 0, 6, 5, 4, 201,
	}
	if img.Rect != image.Rect(0, 0, 2, 2) || !bytes.Equal(img.Pix, want) {
		t.Errorf("%v %v, want %v", img.Rect, img.Pix, want)
	}
}

func TestDecodeICOPalettesAndRLE(t *testing.T) {
	// 4-bit palette: the indices 0..3 of the top row, then the bottom row; the palette is (b, g, r, 0).
	d := newDIB(4, 2, 4)
	d.colorsUsed = 4
	d.palette = []byte{0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 0, 1, 2, 3, 0}
	d.pixels = []byte{0x32, 0x10, 0, 0, 0x01, 0x23, 0, 0} // bottom row 3 2 1 0 (file order), then top row 0 1 2 3
	img, err := DecodeICO(icoSingle(4, 2, 4, d.bytes()))
	if err != nil {
		t.Fatal(err)
	}
	red, green, blue, other := []byte{255, 0, 0, 255}, []byte{0, 255, 0, 255}, []byte{0, 0, 255, 255}, []byte{3, 2, 1, 255}
	want := bytes.Join([][]byte{red, green, blue, other, other, blue, green, red}, nil)
	if !bytes.Equal(img.Pix, want) {
		t.Errorf("pixels %v, want %v", img.Pix, want)
	}

	// RLE8: a run of four pixels of index 1 in the bottom row, an end of line, an absolute run of three, a delta of one pixel, the end of the
	// file. The RLE pixels keep the zero alpha of the buffer: only the palette's colour is written.
	r := newDIB(4, 2, 8)
	r.compression = 1
	r.colorsUsed = 3
	r.palette = []byte{0, 0, 0, 0, 0, 0, 255, 0, 0, 255, 0, 0}
	r.pixels = []byte{4, 1, 0, 0, 0, 3, 2, 1, 2, 0, 0, 2, 1, 0, 0, 1}
	img, err = DecodeICO(icoSingle(4, 2, 8, r.bytes()))
	if err != nil {
		t.Fatal(err)
	}
	wantRLE := []byte{
		0, 255, 0, 0, 255, 0, 0, 0, 0, 255, 0, 0, 0, 0, 0, 0,
		255, 0, 0, 0, 255, 0, 0, 0, 255, 0, 0, 0, 255, 0, 0, 0,
	}
	if !bytes.Equal(img.Pix, wantRLE) {
		t.Errorf("RLE pixels %v, want %v", img.Pix, wantRLE)
	}
}

func TestDecodeICOPicksTheBestEntry(t *testing.T) {
	small := newDIB(2, 2, 32)
	small.pixels = bytes.Repeat([]byte{1, 2, 3, 255}, 4)
	big := newDIB(4, 4, 32)
	big.pixels = bytes.Repeat([]byte{9, 8, 7, 255}, 16)
	entry := func(w, h int) icoEntry { return icoEntry{width: byte(w), height: byte(h), planes: 1, bpp: 32} }
	for name, file := range map[string][]byte{
		"big last":  icoFile(1, []icoEntry{entry(2, 2), entry(4, 4)}, [][]byte{small.bytes(), big.bytes()}),
		"big first": icoFile(1, []icoEntry{entry(4, 4), entry(2, 2)}, [][]byte{big.bytes(), small.bytes()}),
	} {
		img, err := DecodeICO(file)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if img.Rect.Dx() != 4 || !bytes.Equal(img.Pix[:4], []byte{7, 8, 9, 255}) {
			t.Errorf("%s: %v %v", name, img.Rect, img.Pix[:4])
		}
	}
}

func TestDecodeICOPNGEntry(t *testing.T) {
	src := nrgbaNoise(5, 3, 1)
	img, err := DecodeICO(icoSingle(5, 3, 32, pngFile(src)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(img.Pix, src.Pix) {
		t.Errorf("pixels differ from the PNG's")
	}
	// A PNG that is whole up to the header of the chunk after its data decodes whatever follows (the IEND chunk is not read).
	png := pngFile(src)
	cut := png[:len(png)-4]
	if _, err := DecodeICO(icoSingle(5, 3, 32, cut)); err != nil {
		t.Errorf("a PNG without the checksum of its IEND chunk: %v", err)
	}
	// The PNG has to be RGBA: a gray one is refused.
	gray := image.NewGray(image.Rect(0, 0, 4, 4))
	if _, err := DecodeICO(icoSingle(4, 4, 32, pngFile(gray))); err == nil {
		t.Error("a gray PNG decodes")
	}
}

func TestDecodeICORefuses(t *testing.T) {
	d := newDIB(2, 2, 32)
	d.pixels = bytes.Repeat([]byte{1, 2, 3, 255}, 4)
	good := icoSingle(2, 2, 32, d.bytes())
	if _, err := DecodeICO(good); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeICO(icoSingle(3, 2, 32, d.bytes())); err == nil {
		t.Error("an entry of another width decodes")
	}
	if _, err := DecodeICO(icoFile(1, nil, nil)); err == nil {
		t.Error("an empty directory decodes")
	}
	for n := range len(good) - len(d.mask) - 1 {
		if _, err := DecodeICO(good[:n]); err == nil {
			t.Errorf("a file cut at %d of %d bytes decodes", n, len(good))
		}
	}
	big := newDIB(16384, 8193, 8)
	big.palette = dibPalette(256, 4, 1)
	if _, err := DecodeICO(icoFile(1, []icoEntry{{planes: 1, bpp: 8}}, [][]byte{big.bytes()})); err == nil {
		t.Error("an image of more than 2^29 bytes decodes")
	}
}

func TestDecodeRoutesICO(t *testing.T) {
	d := newDIB(1, 1, 32)
	d.pixels = []byte{1, 2, 3, 255}
	img, err := Decode(icoSingle(1, 1, 32, d.bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if nrgba, ok := img.(*image.NRGBA); !ok || !bytes.Equal(nrgba.Pix, []byte{3, 2, 1, 255}) {
		t.Errorf("Decode gave %T %v", img, img)
	}
}
