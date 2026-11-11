// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"image"
	"testing"
)

func TestDecodeDDSBlocks(t *testing.T) {
	row := func(pixels ...[4]byte) []byte {
		var out []byte
		for _, p := range pixels {
			out = append(out, p[:]...)
		}
		return out
	}
	// color0 red, color1 blue: color0 > color1 gives the thirds, in the order 0, 1, 2, 3 along every row.
	thirds := row([4]byte{255, 0, 0, 255}, [4]byte{0, 0, 255, 255}, [4]byte{170, 0, 85, 255}, [4]byte{85, 0, 170, 255})
	// color0 <= color1: the midpoint, then transparent black (black in Rgb8).
	halves := row([4]byte{0, 0, 255, 255}, [4]byte{255, 0, 0, 255}, [4]byte{128, 0, 128, 255}, [4]byte{0, 0, 0, 255})
	for _, tc := range []struct {
		name    string
		variant string
		block   []byte
		want    []byte // the first row of pixels
	}{
		{"DXT1 thirds", "DXT1", ddsColorBlock(0xF800, 0x001F), thirds},
		{"DXT1 midpoint and black", "DXT1", ddsColorBlock(0x001F, 0xF800), halves},
		{"DXT3 always interpolates by thirds", "DXT3", ddsBlock("DXT3", 0x001F, 0xF800, 0, 0), row([4]byte{0, 0, 255, 0x00}, [4]byte{255, 0, 0, 0x11}, [4]byte{85, 0, 170, 0x22}, [4]byte{170, 0, 85, 0x33})},
		{"DXT5 eight alphas", "DXT5", ddsBlock("DXT5", 0xF800, 0x001F, 255, 0), row([4]byte{255, 0, 0, 255}, [4]byte{0, 0, 255, 0}, [4]byte{170, 0, 85, 218}, [4]byte{85, 0, 170, 182})},
		{"DXT5 four interpolated alphas, then zero and opaque", "DXT5", ddsBlock("DXT5", 0xF800, 0x001F, 10, 200), row([4]byte{255, 0, 0, 10}, [4]byte{0, 0, 255, 200}, [4]byte{170, 0, 85, 48}, [4]byte{85, 0, 170, 86})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := DecodeDDS(newDDSHeader(4, 4, tc.variant).file(tc.block))
			if err != nil {
				t.Fatal(err)
			}
			if got := img.Pix[:16]; !bytes.Equal(got, tc.want) {
				t.Errorf("first row %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDecodeDDSPlacesBlocksInRows(t *testing.T) {
	var blocks []byte
	for _, c := range []uint16{0xF800, 0x07E0, 0x001F, 0xFFFF} { // 2x2 blocks, each a flat colour (both endpoints equal, index 0)
		blocks = append(blocks, byte(c), byte(c>>8), byte(c), byte(c>>8), 0, 0, 0, 0)
	}
	img, err := DecodeDDS(newDDSHeader(8, 8, "DXT1").file(blocks))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		x, y int
		want [3]byte
	}{{0, 0, [3]byte{255, 0, 0}}, {7, 3, [3]byte{0, 255, 0}}, {3, 4, [3]byte{0, 0, 255}}, {4, 7, [3]byte{255, 255, 255}}} {
		o := img.PixOffset(tc.x, tc.y)
		if got := [3]byte(img.Pix[o : o+3]); got != tc.want || img.Pix[o+3] != 255 {
			t.Errorf("pixel (%d,%d) = %v, want %v", tc.x, tc.y, img.Pix[o:o+4], tc.want)
		}
	}
}

func TestDecodeDDSRefuses(t *testing.T) {
	good := newDDSHeader(4, 4, "DXT1").file(ddsNoise(1, 8))
	if _, err := DecodeDDS(good); err != nil {
		t.Fatal(err)
	}
	for n := range len(good) {
		if _, err := DecodeDDS(good[:n]); err == nil {
			t.Errorf("a file cut at %d of %d bytes decodes", n, len(good))
		}
	}
	for name, h := range map[string]ddsHeader{
		"width 5":    newDDSHeader(5, 4, "DXT1"),
		"height 6":   newDDSHeader(4, 6, "DXT1"),
		"DXT2":       newDDSHeader(4, 4, "DXT2"),
		"no fourcc":  func() ddsHeader { h := newDDSHeader(4, 4, "DXT1"); h.pixelFormatFlags = 0x41; return h }(),
		"flags":      func() ddsHeader { h := newDDSHeader(4, 4, "DXT1"); h.flags = 0x7; return h }(),
		"over limit": newDDSHeader(16384, 10924, "DXT1"),
		"empty 2^30": newDDSHeader(1<<30, 0, "DXT1"),
		"DX10 BC4":   func() ddsHeader { h := newDDSHeader(4, 4, "DX10"); h.dx10, h.dxgiFormat = true, 80; return h }(),
		"DX10 header": func() ddsHeader {
			h := newDDSHeader(4, 4, "DX10")
			h.dx10, h.dxgiFormat, h.resourceDimension = true, 71, 1
			return h
		}(),
	} {
		if _, err := DecodeDDS(h.file(ddsNoise(2, 64))); err == nil {
			t.Errorf("%s decodes", name)
		}
	}
	h := newDDSHeader(4, 4, "DX10")
	h.dx10, h.dxgiFormat = true, 77
	if _, err := DecodeDDS(h.file(ddsNoise(2, 16))); err != nil {
		t.Errorf("BC3 through the DX10 header: %v", err)
	}
}

func TestDecodeRoutesDDS(t *testing.T) {
	img, err := Decode(newDDSHeader(4, 4, "DXT1").file(ddsColorBlock(0xF800, 0xF800)))
	if err != nil {
		t.Fatal(err)
	}
	if nrgba, ok := img.(*image.NRGBA); !ok || !bytes.Equal(nrgba.Pix[:4], []byte{255, 0, 0, 255}) {
		t.Errorf("Decode gave %T %v", img, img)
	}
}
