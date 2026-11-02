// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"embed"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strings"
)

//go:embed testdata/bmp-real/*.bmp testdata/bmp-real/*.bad_bmp
var bmpRealFiles embed.FS

// bmpSpec is a BMP file: the file header (with the fields that a decoder may or may not read), the bitmap header, palette and pixels of the dib.
type bmpSpec struct {
	d         dib
	signature string  // "BM" unless set
	reserved  [4]byte // the two reserved words of the file header
	fileSize  *uint32 // the size field, the real length unless set
	offset    *uint32 // the pixel array offset, the end of the headers and palette unless set
	trailing  []byte
}

// newBMPDIB is a bitmap of the file's height (the ICO corpus doubles it).
func newBMPDIB(width, height int, bitCount uint16) dib {
	return dib{headerSize: 40, width: int32(width), height: int32(height), planes: 1, bitCount: bitCount}
}

func (s bmpSpec) bytes() []byte {
	prefix := s.d
	prefix.pixels, prefix.mask = nil, nil
	body := s.d.bytes()
	offset := uint32(14 + len(prefix.bytes()))
	if s.offset != nil {
		offset = *s.offset
	}
	signature := s.signature
	if signature == "" {
		signature = "BM"
	}
	out := []byte(signature)
	size := uint32(14 + len(body) + len(s.trailing))
	if s.fileSize != nil {
		size = *s.fileSize
	}
	out = append(out, le32(size)...)
	out = append(out, s.reserved[:]...)
	out = append(out, le32(offset)...)
	out = append(out, body...)
	return append(out, s.trailing...)
}

// bmpRLEStream is a pseudo-random RLE stream of the pixel array: runs, absolute runs, end of row, deltas and end of bitmap, with run lengths
// that overrun the width. The stream is cut or ended according to the seed.
func bmpRLEStream(seed uint32, width, height int, rle4 bool) []byte {
	noise := icoNoise(seed, 64+width*height*2)
	at := 0
	next := func() byte {
		v := noise[at%len(noise)]
		at++
		return v
	}
	var out []byte
	rows := 0
	for ops := 0; rows < height+2 && ops < 40+width*height; ops++ {
		switch next() % 8 {
		case 0, 1, 2: // a run
			n := int(next())%(width+2) + 1
			out = append(out, byte(n), next())
		case 3: // an absolute run
			n := int(next())%(width+2) + 3
			count := n
			if rle4 {
				count = (n + 1) / 2
			}
			count += count & 1
			out = append(out, 0, byte(n))
			for range count {
				out = append(out, next())
			}
		case 4, 5: // the end of a row
			out = append(out, 0, 0)
			rows++
		case 6: // a delta
			out = append(out, 0, 2, next()%byte(width+2), next()%3)
		case 7: // the end of the bitmap
			if next()%4 == 0 {
				out = append(out, 0, 1)
				return cutRLE(out, next())
			}
		}
	}
	return cutRLE(out, next())
}

// cutRLE cuts every fourth stream at a pseudo-random byte.
func cutRLE(stream []byte, v byte) []byte {
	if v%4 == 0 && len(stream) > 0 {
		return stream[:int(v)%len(stream)]
	}
	return stream
}

// bmpMasks are the colour masks of the BITFIELDS corpus (every layout image's Bitfield tells apart).
type bmpMasks struct {
	name       string
	r, g, b, a uint32
}

var bmpMasks16 = []bmpMasks{
	{"565", 0xF800, 0x07E0, 0x001F, 0}, {"555", 0x7C00, 0x03E0, 0x001F, 0}, {"1555", 0x7C00, 0x03E0, 0x001F, 0x8000}, {"4444", 0x0F00, 0x00F0, 0x000F, 0xF000},
	{"332", 0xE0, 0x1C, 0x03, 0}, {"111", 0x4, 0x2, 0x1, 0}, {"222", 0x30, 0x0C, 0x03, 0}, {"777", 0x3F80, 0x007F, 0x7F, 0},
	{"bgr565", 0x001F, 0x07E0, 0xF800, 0}, {"gap", 0x0F00, 0x00F0, 0x0007, 0}, {"nocontig", 0xF0F0, 0x0F00, 0x000F, 0}, {"nored", 0, 0x07E0, 0x001F, 0},
	{"nogreen", 0xF800, 0, 0x001F, 0}, {"noblue", 0xF800, 0x07E0, 0, 0}, {"toolong", 0x1FFFF, 0x0F00, 0x000F, 0}, {"all-same", 0xFFFF, 0xFFFF, 0xFFFF, 0},
	{"alpha-only", 0, 0, 0, 0xFFFF}, {"overlap", 0xFF00, 0x0FF0, 0x00FF, 0}, {"r1-g1-b1-a1", 0x8, 0x4, 0x2, 0x1}, {"3-bit", 0x7000, 0x0E00, 0x01C0, 0},
	{"6-bit", 0xFC00, 0x03F0, 0x000F, 0}, {"alpha-8", 0x00E0, 0x001C, 0x0003, 0xFF00}, {"alpha-2", 0x7C00, 0x03E0, 0x001F, 0xC000},
}

var bmpMasks32 = []bmpMasks{
	{"888-888", 0xFF0000, 0x00FF00, 0x0000FF, 0}, {"rgba", 0xFF0000, 0x00FF00, 0x0000FF, 0xFF000000}, {"r8g8b8-shifted", 0xFF000000, 0x00FF0000, 0x0000FF00, 0},
	{"r8g8b8a8-shifted", 0xFF000000, 0x00FF0000, 0x0000FF00, 0x000000FF}, {"bgra", 0x0000FF, 0x00FF00, 0xFF0000, 0xFF000000}, {"10-10-10-2", 0x3FF00000, 0xFFC00, 0x3FF, 0xC0000000},
	{"11-11-10", 0xFFE00000, 0x1FFC00, 0x3FF, 0}, {"16-bit-channel", 0xFFFF0000, 0xFF00, 0xFF, 0}, {"nocontig", 0xF0F00000, 0x0F0000, 0xFF, 0}, {"toolong", 0, 0xFF00, 0xFF, 0},
	{"a-only-8", 0xFF, 0xFF00, 0xFF0000, 0xFF000000}, {"zero", 0, 0, 0, 0}, {"rgb-no-alpha-bits", 0xF800, 0x07E0, 0x001F, 0}, {"wide-alpha", 0xFF0000, 0xFF00, 0xFF, 0xFFFFFF00},
	{"top-bits", 0x80000000, 0x40000000, 0x20000000, 0x10000000}, {"r8g8b8a8-r8g8b8", 0xFF000000, 0x00FF0000, 0x0000FF00, 0}, {"5-6-5-in-32", 0xF800, 0x07E0, 0x001F, 0}, {"alpha-4", 0xFF0000, 0xFF00, 0xFF, 0xF0000000},
	{"7-bit", 0x7F0000, 0x7F00, 0x7F, 0x7F000000}, {"1-bit", 0x80, 0x40, 0x20, 0x10}, {"split-alpha", 0xFF0000, 0xFF00, 0xFF, 0xFF00FF00},
}

// bmpMutationValues are the values every byte of the headers of the base bitmaps takes in the mutation sets.
var bmpMutationValues = []byte{0x00, 0x01, 0x02, 0x20, 0x80, 0xFF}

// bmpCorpus is the files of TestDecodeBMPMatchesPhoton: image 0.24.9's BMP decoder (and what Go's x/image/bmp makes of the same files).
func bmpCorpus() []formatCase {
	var b formatCorpusBuilder
	add := func(name string, s bmpSpec) { b.add(name, s.bytes()) }

	// Real files: the BMP Suite of image-rs and files with a damaged header or pixels.
	entries, err := fs.ReadDir(bmpRealFiles, "testdata/bmp-real")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		data, err := bmpRealFiles.ReadFile("testdata/bmp-real/" + e.Name())
		if err != nil {
			panic(err)
		}
		name := "real-" + e.Name()[:strings.LastIndexByte(e.Name(), '.')]
		b.add(name, data)
		cuts := []int{1, 2, 13, 14, 15, 17, 18, 53, 54, 55, 60, 100, len(data) / 2, len(data) - 1}
		slices.Sort(cuts)
		for _, cut := range slices.Compact(cuts) {
			if cut > 0 && cut < len(data) {
				b.add(fmt.Sprintf("%s-cut%d", name, cut), data[:cut])
			}
		}
	}

	// 24-bit, 32-bit (BI_RGB, which has no alpha channel in a BMP file) and 16-bit bitmaps, in sizes that need row padding, bottom-up and top-down.
	sizes := [][2]int{{1, 1}, {2, 1}, {1, 2}, {3, 3}, {4, 4}, {5, 2}, {7, 5}, {16, 3}, {17, 9}, {31, 3}, {32, 2}, {33, 31}, {64, 4}, {255, 3}, {256, 2}, {3, 256}, {100, 100}}
	for _, size := range sizes {
		w, h := size[0], size[1]
		for _, bits := range []uint16{16, 24, 32} {
			for _, topDown := range []bool{false, true} {
				d := newBMPDIB(w, h, bits)
				if topDown {
					d.height = -d.height
				}
				d.pixels = icoNoise(uint32(w*31+h)+uint32(bits), dibRowBytes(w, int(bits))*h)
				add(fmt.Sprintf("rgb%d-%dx%d-topdown%v", bits, w, h, topDown), bmpSpec{d: d})
			}
		}
	}
	// A BI_RGB bitmap of 32 bits ignores its fourth byte; the header versions with an alpha mask do not use it either.
	for _, header := range []uint32{40, 52, 56, 108, 124} {
		for _, bits := range []uint16{16, 24, 32} {
			d := newBMPDIB(5, 3, bits)
			d.headerSize = header
			d.pixels = icoNoise(uint32(header)+uint32(bits), dibRowBytes(5, int(bits))*3)
			add(fmt.Sprintf("rgb%d-header%d", bits, header), bmpSpec{d: d})
		}
	}

	// Palettes: bit counts, palette sizes (colors used), indices past the palette, short palettes.
	for _, bits := range []uint16{1, 2, 4, 8} {
		for _, used := range []uint32{0, 1, 2, 3, 4, 15, 16, 17, 255, 256, 257, 1000, 0xFFFFFFFF} {
			w, h := 7, 5
			d := newBMPDIB(w, h, bits)
			d.colorsUsed = used
			entries := int(min(used, 1<<bits))
			if used == 0 {
				entries = 1 << bits
			}
			d.palette = dibPalette(entries, 4, uint32(bits)*7+used)
			d.pixels = icoNoise(uint32(bits), dibRowBytes(w, int(bits))*h)
			add(fmt.Sprintf("palette-%dbit-used-%d", bits, used), bmpSpec{d: d})
			// A palette that holds the colours the header names, followed by the rest as pixels.
			if used > 1<<bits && used < 2000 {
				d.palette = dibPalette(int(used), 4, 3)
				add(fmt.Sprintf("palette-%dbit-used-%d-long-palette", bits, used), bmpSpec{d: d})
			}
		}
		w, h := 9, 4
		d := newBMPDIB(w, h, bits)
		d.colorsUsed = 2
		d.palette = dibPalette(2, 4, 99)
		d.pixels = icoNoise(uint32(bits)+50, dibRowBytes(w, int(bits))*h)
		add(fmt.Sprintf("palette-%dbit-indices-beyond", bits), bmpSpec{d: d})
		d.palette = d.palette[:5]
		add(fmt.Sprintf("palette-%dbit-short", bits), bmpSpec{d: d})
		d.palette = nil
		add(fmt.Sprintf("palette-%dbit-none", bits), bmpSpec{d: d})
		for _, size := range [][2]int{{1, 1}, {8, 2}, {9, 3}, {31, 2}, {32, 2}, {33, 2}, {64, 3}, {65, 2}, {100, 7}} {
			w, h := size[0], size[1]
			for _, topDown := range []bool{false, true} {
				d := newBMPDIB(w, h, bits)
				if topDown {
					d.height = -d.height
				}
				d.palette = dibPalette(1<<bits, 4, uint32(bits)+uint32(w))
				d.pixels = icoNoise(uint32(w*int(bits)+h), dibRowBytes(w, int(bits))*h)
				add(fmt.Sprintf("palette-%dbit-%dx%d-topdown%v", bits, w, h, topDown), bmpSpec{d: d})
			}
		}
		// A palette whose entries have the reserved byte set (the decoder ignores it).
		d = newBMPDIB(5, 5, bits)
		d.palette = icoNoise(7, 4<<bits)
		d.pixels = icoNoise(8, dibRowBytes(5, int(bits))*5)
		add(fmt.Sprintf("palette-%dbit-reserved-bytes", bits), bmpSpec{d: d})
	}

	// BITFIELDS: every mask layout, with every header version, in 16 and 32 bits (alpha masks are read from V3 headers on).
	for _, header := range []uint32{40, 52, 56, 108, 124} {
		for bits, set := range map[uint16][]bmpMasks{16: bmpMasks16, 32: bmpMasks32} {
			for _, m := range set {
				w, h := 5, 3
				d := newBMPDIB(w, h, bits)
				d.headerSize = header
				d.compression = 3
				d.masks = []uint32{m.r, m.g, m.b}
				if header >= 56 {
					d.masks = append(d.masks, m.a)
				}
				d.pixels = icoNoise(m.r+m.g+uint32(bits), dibRowBytes(w, int(bits))*h)
				add(fmt.Sprintf("bitfields%d-h%d-%s", bits, header, m.name), bmpSpec{d: d})
				if header == 56 || header == 40 {
					d.height = -d.height
					add(fmt.Sprintf("bitfields%d-h%d-%s-topdown", bits, header, m.name), bmpSpec{d: d})
				}
			}
		}
	}
	// BITFIELDS with the 40-byte header: the masks follow the header but the palette area starts at its end (the masks are then data or palette).
	for _, bits := range []uint16{16, 32} {
		d := newBMPDIB(3, 2, bits)
		d.compression = 3
		d.masks = []uint32{0xFF0000, 0xFF00, 0xFF}
		if bits == 16 {
			d.masks = []uint32{0xF800, 0x07E0, 0x001F}
		}
		d.pixels = icoNoise(5, dibRowBytes(3, int(bits))*2)
		add(fmt.Sprintf("bitfields%d-masks-after-info", bits), bmpSpec{d: d})
		add(fmt.Sprintf("bitfields%d-masks-after-info-offset-at-pixels", bits), bmpSpec{d: d, offset: new(uint32(14 + 40))})
		add(fmt.Sprintf("bitfields%d-masks-after-info-offset-0", bits), bmpSpec{d: d, offset: new(uint32(0))})
		d.masks = d.masks[:2]
		add(fmt.Sprintf("bitfields%d-two-masks", bits), bmpSpec{d: d})
		d.masks = nil
		add(fmt.Sprintf("bitfields%d-no-masks", bits), bmpSpec{d: d})
	}
	// Bitfields with the wrong bit count, and the compression of a bitfield bitmap on a palette.
	for _, bits := range []uint16{1, 2, 4, 8, 24, 0, 64} {
		d := newBMPDIB(3, 2, bits)
		d.compression = 3
		d.masks = []uint32{0xFF0000, 0xFF00, 0xFF}
		d.pixels = icoNoise(5, 64)
		add(fmt.Sprintf("bitfields-with-%dbits", bits), bmpSpec{d: d})
	}

	// RLE: hand-written streams of every escape, and random ones.
	rle := func(name string, bits uint16, width, height int, stream []byte) {
		d := newBMPDIB(width, height, bits)
		if bits == 8 {
			d.compression = 1
		} else {
			d.compression = 2
		}
		d.palette = dibPalette(1<<bits, 4, uint32(bits)*3)
		d.pixels = stream
		add(name, bmpSpec{d: d})
	}
	rle("rle8-runs", 8, 6, 3, []byte{6, 1, 0, 0, 3, 2, 3, 4, 0, 0, 0, 5, 0, 1, 6, 0, 0, 0, 1})
	rle("rle8-runs-then-eof", 8, 6, 3, []byte{6, 1, 0, 0, 3, 2, 3, 4, 0, 0, 0, 0, 5, 5, 5, 5, 5, 5, 0, 0, 0, 1})
	rle("rle8-absolute", 8, 5, 2, []byte{0, 5, 1, 2, 3, 4, 5, 0, 0, 0, 0, 3, 7, 8, 9, 0, 2, 5, 0, 0, 1})
	rle("rle8-absolute-odd-padding", 8, 4, 2, []byte{0, 3, 1, 2, 3, 0, 1, 4, 0, 0, 4, 1, 2, 3, 4, 0, 1})
	rle("rle8-delta", 8, 6, 4, []byte{2, 1, 0, 2, 3, 0, 3, 1, 0, 0, 0, 2, 0, 1, 2, 2, 0, 1})
	rle("rle8-delta-y-only", 8, 6, 4, []byte{2, 1, 0, 2, 0, 1, 3, 1, 0, 0, 0, 1})
	rle("rle8-delta-skips-rows", 8, 4, 6, []byte{4, 1, 0, 2, 0, 3, 4, 2, 0, 1})
	rle("rle8-delta-too-far-x", 8, 4, 2, []byte{0, 2, 9, 0, 4, 1, 0, 1})
	rle("rle8-delta-too-far-y", 8, 4, 2, []byte{4, 1, 0, 2, 0, 5, 0, 1})
	rle("rle8-run-over-row", 8, 4, 2, []byte{5, 1, 0, 0, 4, 2, 0, 1})
	rle("rle8-absolute-over-row", 8, 3, 2, []byte{0, 4, 1, 2, 3, 4, 0, 0, 3, 2, 0, 1})
	rle("rle8-early-eof", 8, 4, 4, []byte{4, 1, 0, 0, 0, 1})
	rle("rle8-eof-first", 8, 4, 4, []byte{0, 1})
	rle("rle8-eol-first", 8, 4, 3, []byte{0, 0, 4, 1, 0, 0, 4, 2, 0, 0, 0, 1})
	rle("rle8-no-end", 8, 4, 2, []byte{4, 1, 4, 2})
	rle("rle8-no-end-eol", 8, 4, 2, []byte{4, 1, 0, 0, 4, 2, 0, 0})
	rle("rle8-extra-after-last-row", 8, 4, 2, []byte{4, 1, 0, 0, 4, 2, 0, 0, 0, 1})
	rle("rle8-empty", 8, 4, 2, nil)
	rle("rle8-truncated-pair", 8, 4, 2, []byte{4})
	rle("rle8-truncated-absolute", 8, 4, 2, []byte{0, 4, 1, 2})
	rle("rle8-truncated-delta", 8, 4, 2, []byte{0, 2, 1})
	rle("rle8-absolute-1", 8, 4, 2, []byte{0, 3, 9, 9, 9, 0, 0, 0, 1})
	rle("rle8-absolute-2", 8, 4, 2, []byte{0, 2, 9, 9, 0, 0, 0, 1})
	rle("rle8-delta-zero", 8, 4, 2, []byte{0, 2, 0, 0, 4, 1, 0, 1})
	rle("rle4-runs", 4, 8, 3, []byte{8, 0x12, 0, 0, 4, 0x34, 4, 0x56, 0, 0, 0, 1})
	rle("rle4-run-odd", 4, 5, 3, []byte{5, 0x12, 0, 0, 3, 0x34, 2, 0x56, 0, 0, 0, 1})
	rle("rle4-absolute", 4, 8, 2, []byte{0, 6, 0x12, 0x34, 0x56, 0, 0, 0, 4, 0x12, 0x34, 0, 0, 1})
	rle("rle4-absolute-odd", 4, 8, 2, []byte{0, 5, 0x12, 0x34, 0x50, 0, 0, 0, 3, 0x12, 0x30, 0, 0, 1})
	rle("rle4-absolute-3", 4, 4, 2, []byte{0, 3, 0x12, 0x30, 0, 0, 0, 4, 0xAB, 0xCD, 0, 1})
	rle("rle4-delta", 4, 8, 4, []byte{2, 0x12, 0, 2, 3, 1, 4, 0x34, 0, 0, 0, 1})
	rle("rle4-run-over-row", 4, 4, 2, []byte{6, 0x12, 0, 0, 4, 0x34, 0, 1})
	rle("rle4-absolute-over-row", 4, 3, 2, []byte{0, 4, 0x12, 0x34, 0, 0, 3, 0x12, 0, 1})
	rle("rle4-single", 4, 1, 1, []byte{1, 0x12, 0, 1})
	rle("rle4-zero-run", 4, 4, 1, []byte{0, 0x12, 4, 0x34, 0, 1})
	for seed := range uint32(160) {
		for _, size := range [][2]int{{4, 3}, {9, 5}, {17, 2}, {33, 4}} {
			w, h := size[0], size[1]
			rle(fmt.Sprintf("rle8-random-%d-%dx%d", seed, w, h), 8, w, h, bmpRLEStream(seed, w, h, false))
			rle(fmt.Sprintf("rle4-random-%d-%dx%d", seed, w, h), 4, w, h, bmpRLEStream(seed+1000, w, h, true))
		}
	}
	// RLE with the wrong bit count, top-down (a compressed bitmap cannot be), and the unsupported compression types.
	{
		d := newBMPDIB(4, 2, 4)
		d.compression = 1
		d.palette = dibPalette(16, 4, 1)
		d.pixels = []byte{4, 1, 0, 0, 4, 2, 0, 1}
		add("rle8-with-4-bits", bmpSpec{d: d})
		d = newBMPDIB(4, 2, 8)
		d.compression = 2
		d.palette = dibPalette(256, 4, 1)
		d.pixels = []byte{4, 0x11, 0, 0, 4, 0x22, 0, 1}
		add("rle4-with-8-bits", bmpSpec{d: d})
		for _, comp := range []uint32{1, 2} {
			bits := map[uint32]uint16{1: 8, 2: 4}[comp]
			d = newBMPDIB(4, 2, bits)
			d.compression = comp
			d.height = -d.height
			d.palette = dibPalette(1<<bits, 4, 1)
			d.pixels = []byte{4, 1, 0, 0, 4, 2, 0, 1}
			add(fmt.Sprintf("rle-compression-%d-top-down", comp), bmpSpec{d: d})
		}
		for _, comp := range []uint32{4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 100, 0xFFFFFFFF} {
			d = newBMPDIB(4, 2, 8)
			d.compression = comp
			d.palette = dibPalette(256, 4, 1)
			d.pixels = icoNoise(1, 8)
			add(fmt.Sprintf("compression-%d", comp), bmpSpec{d: d})
		}
		for _, comp := range []uint32{0, 3, 4, 5} {
			d = newBMPDIB(2, 2, 24)
			d.compression = comp
			d.height = -2
			d.pixels = icoNoise(1, 16)
			add(fmt.Sprintf("topdown-24bit-compression-%d", comp), bmpSpec{d: d})
		}
	}

	// Core (BITMAPCOREHEADER) bitmaps: 3-byte palette entries and 16-bit sizes; bit counts the header does not allow.
	for _, bits := range []uint16{1, 4, 8, 24, 16, 32, 2, 0} {
		for _, size := range [][2]int{{4, 4}, {5, 3}, {1, 1}, {33, 2}} {
			w, h := size[0], size[1]
			d := newBMPDIB(w, h, bits)
			d.headerSize = 12
			if bits <= 8 && bits != 0 && bits != 2 {
				d.palette = dibPalette(1<<bits, 3, uint32(bits))
			}
			d.pixels = icoNoise(uint32(bits)+uint32(w), dibRowBytes(w, int(bits))*h)
			add(fmt.Sprintf("core-%dbit-%dx%d", bits, w, h), bmpSpec{d: d})
			if bits == 8 || bits == 4 {
				d.palette = dibPalette(1<<bits, 4, uint32(bits)) // a four-byte palette read as three
				add(fmt.Sprintf("core-%dbit-%dx%d-4-byte-palette", bits, w, h), bmpSpec{d: d})
				d.palette = d.palette[:3*(1<<bits)-5]
				add(fmt.Sprintf("core-%dbit-%dx%d-short-palette", bits, w, h), bmpSpec{d: d})
			}
		}
	}
	for name, mutate := range map[string]func(*dib){
		"two-planes":  func(d *dib) { d.planes = 2 },
		"zero-planes": func(d *dib) { d.planes = 0 },
		"width-0":     func(d *dib) { d.width = 0 },
		"height-0":    func(d *dib) { d.height = 0 },
		"height-neg":  func(d *dib) { d.height = -4 },
		"width-65535": func(d *dib) { d.width = 65535 },
		"height-big":  func(d *dib) { d.height = 0xFFFF },
	} {
		d := newBMPDIB(4, 4, 24)
		d.headerSize = 12
		d.pixels = icoNoise(1, dibRowBytes(4, 24)*4)
		mutate(&d)
		add("core-"+name, bmpSpec{d: d})
	}

	// Header sizes: every size the decoder knows (OS/2 2.x headers of 16 and 64 bytes and the others are errors), planes and bit counts.
	for _, size := range []uint32{0, 1, 4, 8, 11, 12, 13, 15, 16, 20, 24, 36, 39, 40, 41, 44, 48, 52, 53, 55, 56, 57, 60, 64, 72, 100, 107, 108, 109, 120, 123, 124, 125, 128, 256, 4096, 0xFFFF, 0x10000, 0xFFFFFFFF} {
		d := newBMPDIB(4, 3, 24)
		d.headerSize = size
		d.pixels = icoNoise(size, dibRowBytes(4, 24)*3)
		add(fmt.Sprintf("header-size-%d", size), bmpSpec{d: d})
		d = newBMPDIB(4, 3, 8)
		d.headerSize = size
		d.palette = dibPalette(256, 4, 2)
		d.pixels = icoNoise(size, dibRowBytes(4, 8)*3)
		add(fmt.Sprintf("header-size-%d-palette8", size), bmpSpec{d: d})
	}
	// A header with extra bytes after the fields the decoder reads (garbage in the V4 and V5 colour space fields).
	for _, header := range []uint32{52, 56, 108, 124} {
		d := newBMPDIB(4, 3, 24)
		d.headerSize = header
		d.extraHeader = icoNoise(header, 200)
		d.pixels = icoNoise(header, dibRowBytes(4, 24)*3)
		add(fmt.Sprintf("header-%d-garbage-fields", header), bmpSpec{d: d})
		d = newBMPDIB(4, 3, 8)
		d.headerSize = header
		d.extraHeader = icoNoise(header+1, 200)
		d.palette = dibPalette(256, 4, 2)
		d.pixels = icoNoise(header, dibRowBytes(4, 8)*3)
		add(fmt.Sprintf("header-%d-garbage-fields-palette8", header), bmpSpec{d: d})
	}
	for _, planes := range []uint16{0, 2, 3, 0xFFFF} {
		d := newBMPDIB(4, 3, 24)
		d.planes = planes
		d.pixels = icoNoise(1, dibRowBytes(4, 24)*3)
		add(fmt.Sprintf("planes-%d", planes), bmpSpec{d: d})
	}
	for _, bits := range []uint16{0, 3, 5, 6, 7, 9, 12, 15, 17, 23, 25, 31, 33, 48, 64, 128, 255, 256, 0xFFFF} {
		d := newBMPDIB(4, 3, bits)
		d.palette = dibPalette(256, 4, 2)
		d.pixels = icoNoise(uint32(bits), 200)
		add(fmt.Sprintf("bitcount-%d", bits), bmpSpec{d: d})
	}

	// Dimensions: zero, negative, the soft limit of 0xFFFF, the allocation limit and the heights that are not rejected for being negative.
	for _, w := range []int32{0, 1, 2, 3, -1, -2, 0xFFFF, 0x10000, 0x7FFFFFFF, math.MinInt32, 0x1000} {
		for _, h := range []int32{0, 1, -1, -2, 0xFFFF, -0xFFFF, 0x10000, -0x10000, 0x7FFFFFFF, math.MinInt32, math.MinInt32 + 1, 3, -3} {
			d := newBMPDIB(0, 0, 24)
			d.width, d.height = w, h
			d.pixels = icoNoise(uint32(w)+uint32(h), 64)
			add(fmt.Sprintf("dim-%d-%d-24bit", w, h), bmpSpec{d: d})
		}
	}
	// A 1 x 70000 bitmap: the height field -70000 is not over the limit of 0xFFFF, as the field of 70000 is.
	{
		d := newBMPDIB(1, -70000, 24)
		d.pixels = icoNoise(1, 4*70000)
		b.addLazy("dim-1x-70000-topdown-data", func() []byte { return bmpSpec{d: d}.bytes() })
		d.height = 70000
		b.addLazy("dim-1x70000-data", func() []byte { return bmpSpec{d: d}.bytes() })
		d = newBMPDIB(1, -65535, 24)
		d.pixels = icoNoise(1, 4*65535)
		b.addLazy("dim-1x-65535-topdown-data", func() []byte { return bmpSpec{d: d}.bytes() })
		d = newBMPDIB(65535, 1, 8)
		d.palette = dibPalette(256, 4, 1)
		d.pixels = icoNoise(1, dibRowBytes(65535, 8))
		b.addLazy("dim-65535x1-8bit", func() []byte { return bmpSpec{d: d}.bytes() })
	}
	// The 512 MiB limit on the RGB8 size (3 bytes a pixel): 1-bit palettes whose pixels cost only 1/8 byte of file.
	for _, h := range []int{2729, 2730, 2731} {
		d := newBMPDIB(65535, h, 1)
		d.palette = dibPalette(2, 4, 1)
		d.pixels = icoNoise(uint32(h), dibRowBytes(65535, 1)*h)
		b.addLazy(fmt.Sprintf("limit-1bit-65535x%d", h), func() []byte { return bmpSpec{d: d}.bytes() })
	}
	for _, size := range [][2]int{{65535, 65535}, {20000, 9000}, {13000, 13000}, {14000, 13000}} {
		d := newBMPDIB(size[0], size[1], 1)
		d.palette = dibPalette(2, 4, 1)
		d.pixels = icoNoise(3, 1<<12)
		add(fmt.Sprintf("limit-short-%dx%d", size[0], size[1]), bmpSpec{d: d})
	}
	// 32-bit with an alpha mask makes four channels: the limit is 4 bytes a pixel.
	for _, h := range []int{2047, 2048} {
		d := newBMPDIB(65535, h, 1)
		d.headerSize = 56
		d.palette = dibPalette(2, 4, 1)
		d.pixels = icoNoise(uint32(h), dibRowBytes(65535, 1)*h)
		b.addLazy(fmt.Sprintf("limit-1bit-v3-65535x%d", h), func() []byte { return bmpSpec{d: d}.bytes() })
		d = newBMPDIB(16384, h, 32)
		d.headerSize = 56
		d.compression = 3
		d.masks = []uint32{0xFF0000, 0xFF00, 0xFF, 0xFF000000}
		d.pixels = nil
		add(fmt.Sprintf("limit-short-bitfields32-alpha-16384x%d", h), bmpSpec{d: d})
	}

	// The pixel array offset and the file header: offsets before, inside and after the headers and past the end, every flavour of reserved
	// field and size, non-BM signatures, trailing bytes.
	{
		d := newBMPDIB(5, 3, 24)
		d.pixels = icoNoise(9, dibRowBytes(5, 24)*3)
		full := bmpSpec{d: d}.bytes()
		for _, offset := range []uint32{0, 1, 2, 13, 14, 15, 30, 53, 54, 55, 56, 57, 58, 60, 64, 100, 54 + 44, uint32(len(full)) - 1, uint32(len(full)), uint32(len(full)) + 1, 0x10000, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFF} {
			add(fmt.Sprintf("offset-%d-24bit", offset), bmpSpec{d: d, offset: new(offset)})
		}
		d8 := newBMPDIB(5, 3, 8)
		d8.palette = dibPalette(256, 4, 3)
		d8.pixels = icoNoise(9, dibRowBytes(5, 8)*3)
		for _, offset := range []uint32{0, 14, 54, 54 + 4, 54 + 1024 - 1, 54 + 1024, 54 + 1024 + 1, 54 + 512, 60 + 1024, 0xFFFFFFFF} {
			add(fmt.Sprintf("offset-%d-8bit", offset), bmpSpec{d: d8, offset: new(offset)})
		}
		for _, reserved := range [][4]byte{{1, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 1, 0}, {0, 0, 0, 1}, {0xFF, 0xFF, 0xFF, 0xFF}, {'P', 'i', 'G', '!'}, {0x80, 0, 0x80, 0}} {
			add(fmt.Sprintf("reserved-%x-24bit", reserved[:]), bmpSpec{d: d, reserved: reserved})
			add(fmt.Sprintf("reserved-%x-8bit", reserved[:]), bmpSpec{d: d8, reserved: reserved})
			dv := newBMPDIB(4, 3, 8)
			dv.headerSize = 108
			dv.palette = dibPalette(256, 4, 3)
			dv.pixels = icoNoise(9, dibRowBytes(4, 8)*3)
			add(fmt.Sprintf("reserved-%x-v4-8bit", reserved[:]), bmpSpec{d: dv, reserved: reserved})
		}
		for _, size := range []uint32{0, 1, 14, 54, uint32(len(full)) - 1, uint32(len(full)) + 1, 0xFFFFFFFF, 0x80000000} {
			add(fmt.Sprintf("filesize-%d-24bit", size), bmpSpec{d: d, fileSize: new(size)})
		}
		for _, sig := range []string{"BA", "CI", "CP", "IC", "PT", "bm", "Bm", "bM", "MB", "B", "BMP"} {
			add("signature-"+sig, bmpSpec{d: d, signature: sig})
		}
		add("trailing-garbage", bmpSpec{d: d, trailing: icoNoise(5, 100)})
		add("trailing-ico-signature", bmpSpec{d: d, trailing: []byte{0, 0, 1, 0}})
		add("trailing-zero", bmpSpec{d: d, trailing: make([]byte, 4096)})
		// A file that ends after each prefix of the headers (the first 70 bytes) and in the pixels.
		for cut := range 70 {
			b.add(fmt.Sprintf("cut-24bit-%d", cut), full[:min(cut, len(full))])
		}
		for cut := 70; cut < len(full); cut++ {
			b.add(fmt.Sprintf("cut-24bit-%d", cut), full[:cut])
		}
		b.add("empty-file", nil)
		b.add("bm-only", []byte("BM"))
		b.add("bm-and-offset", append([]byte("BM"), make([]byte, 12)...))
		b.add("bm-and-size-zero", append([]byte("BM"), make([]byte, 16)...))
	}
	// Truncations of a palette file at each byte of its headers and palette.
	{
		d := newBMPDIB(5, 3, 8)
		d.palette = dibPalette(256, 4, 3)
		d.pixels = icoNoise(9, dibRowBytes(5, 8)*3)
		full := bmpSpec{d: d}.bytes()
		for cut := 54; cut < len(full); cut += 37 {
			b.add(fmt.Sprintf("cut-8bit-%d", cut), full[:cut])
		}
		b.add("cut-8bit-end-1", full[:len(full)-1])
		b.add("cut-8bit-end-8", full[:len(full)-8])
	}

	// Mutations: every byte of the file header and of the bitmap header of a base file of each layout takes each of a few values.
	mutate := func(name string, data []byte, upto int) {
		for off := 0; off < min(upto, len(data)); off++ {
			for _, v := range bmpMutationValues {
				if data[off] == v {
					continue
				}
				m := append([]byte(nil), data...)
				m[off] = v
				b.add(fmt.Sprintf("mut-%s-%d-%02x", name, off, v), m)
			}
		}
	}
	type base struct {
		name   string
		header uint32
		bits   uint16
		comp   uint32
		masks  []uint32
		stream []byte
	}
	for _, c := range []base{
		{name: "24", header: 40, bits: 24}, {name: "32", header: 40, bits: 32}, {name: "16", header: 40, bits: 16}, {name: "8", header: 40, bits: 8},
		{name: "4", header: 40, bits: 4}, {name: "1", header: 40, bits: 1}, {name: "2", header: 40, bits: 2}, {name: "core8", header: 12, bits: 8}, {name: "core24", header: 12, bits: 24},
		{name: "rle8", header: 40, bits: 8, comp: 1, stream: []byte{4, 1, 0, 0, 0, 3, 5, 6, 7, 0, 0, 2, 0, 1, 2, 3, 0, 1}},
		{name: "rle4", header: 40, bits: 4, comp: 2, stream: []byte{4, 0x12, 0, 0, 0, 3, 0x56, 0x70, 0, 0, 2, 0x34, 0, 1}},
		{name: "bf16", header: 40, bits: 16, comp: 3, masks: []uint32{0xF800, 0x07E0, 0x001F}},
		{name: "bf32v3", header: 56, bits: 32, comp: 3, masks: []uint32{0xFF0000, 0xFF00, 0xFF, 0xFF000000}},
		{name: "bf32v4", header: 108, bits: 32, comp: 3, masks: []uint32{0xFF0000, 0xFF00, 0xFF, 0xFF000000}},
		{name: "v5-24", header: 124, bits: 24}, {name: "v2-8", header: 52, bits: 8}, {name: "v3-16", header: 56, bits: 16},
	} {
		w, h := 4, 3
		d := newBMPDIB(w, h, c.bits)
		d.headerSize, d.compression, d.masks = c.header, c.comp, c.masks
		if c.bits <= 8 {
			count := 1 << c.bits
			per := 4
			if c.header == 12 {
				per = 3
			}
			d.palette = dibPalette(count, per, uint32(c.bits))
		}
		d.pixels = icoNoise(uint32(c.bits)+c.header, dibRowBytes(w, int(c.bits))*h)
		if c.stream != nil {
			d.pixels = c.stream
		}
		data := bmpSpec{d: d}.bytes()
		mutate(c.name, data, 14+int(c.header)+16)
	}
	return b.cases
}
