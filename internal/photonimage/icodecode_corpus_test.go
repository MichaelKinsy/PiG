// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
)

// icoEntry is a directory entry of an ICO file; length and offset of zero are filled in by icoFile.
type icoEntry struct {
	width, height, colors, reserved byte
	planes, bpp                     uint16
	length, offset                  uint32
	setLength, setOffset            bool
}

func le16(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }
func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// icoFile writes an ICO file with one image per entry, laid out after the directory in order. The entry's length and offset are those of its
// image unless set.
func icoFile(typ uint16, entries []icoEntry, images [][]byte) []byte {
	out := []byte{0, 0}
	out = append(out, le16(typ)...)
	out = append(out, le16(uint16(len(entries)))...)
	offset := 6 + 16*len(entries)
	for i, e := range entries {
		length, off := uint32(len(images[i])), uint32(offset)
		if e.setLength {
			length = e.length
		}
		if e.setOffset {
			off = e.offset
		}
		out = append(out, e.width, e.height, e.colors, e.reserved)
		out = append(out, le16(e.planes)...)
		out = append(out, le16(e.bpp)...)
		out = append(out, le32(length)...)
		out = append(out, le32(off)...)
		offset += len(images[i])
	}
	for _, img := range images {
		out = append(out, img...)
	}
	return out
}

// icoSingle is an ICO file of one image; the entry's size bytes are the image's (0 for 256).
func icoSingle(width, height int, bpp uint16, data []byte) []byte {
	return icoFile(1, []icoEntry{{width: byte(width), height: byte(height), planes: 1, bpp: bpp}}, [][]byte{data})
}

// dib describes the bitmap of an ICO entry: its headers, palette, pixels and AND mask.
type dib struct {
	headerSize     uint32 // 12, 40, 52, 56, 108, 124 (or any other)
	width, height  int32  // the height is the double of the image's, as ICO stores it
	planes         uint16
	bitCount       uint16
	compression    uint32
	colorsUsed     uint32
	masks          []uint32 // r, g, b (and a) written inside the header (V2 and later) or after it (BITFIELDS with the 40 byte header)
	palette        []byte   // 4 bytes (3 with the core header) per entry
	pixels         []byte
	mask           []byte
	extraHeader    []byte // garbage after the fields up to headerSize
	masksAfterInfo bool
}

func newDIB(width, height int, bitCount uint16) dib {
	return dib{headerSize: 40, width: int32(width), height: int32(2 * height), planes: 1, bitCount: bitCount}
}

func (d dib) bytes() []byte {
	var out []byte
	out = append(out, le32(d.headerSize)...)
	if d.headerSize == 12 {
		out = append(out, le16(uint16(d.width))...)
		out = append(out, le16(uint16(d.height))...)
		out = append(out, le16(d.planes)...)
		out = append(out, le16(d.bitCount)...)
	} else {
		out = append(out, le32(uint32(d.width))...)
		out = append(out, le32(uint32(d.height))...)
		out = append(out, le16(d.planes)...)
		out = append(out, le16(d.bitCount)...)
		out = append(out, le32(d.compression)...)
		out = append(out, le32(0)...) // image size
		out = append(out, le32(0)...) // x pixels per metre
		out = append(out, le32(0)...) // y pixels per metre
		out = append(out, le32(d.colorsUsed)...)
		out = append(out, le32(0)...) // important colours
		if d.headerSize > 40 {
			for _, m := range d.masks {
				out = append(out, le32(m)...)
			}
		}
	}
	// Pad or cut the header to its declared size.
	for uint32(len(out)) < d.headerSize && d.headerSize <= 4096 {
		out = append(out, d.extraHeader...)
		if len(d.extraHeader) == 0 {
			out = append(out, 0)
		}
	}
	if uint32(len(out)) > d.headerSize && d.headerSize >= 40 && d.headerSize <= 124 && !d.masksAfterInfo {
		out = out[:d.headerSize]
	}
	if d.headerSize == 40 {
		for _, m := range d.masks {
			out = append(out, le32(m)...)
		}
	}
	out = append(out, d.palette...)
	out = append(out, d.pixels...)
	out = append(out, d.mask...)
	return out
}

// icoNoise is count bytes of a xorshift sequence.
func icoNoise(seed uint32, count int) []byte {
	state := seed*2654435761 + 11
	out := make([]byte, count)
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		out[i] = byte(state >> 10)
	}
	return out
}

// rowBytes is the size of a row of a bitmap, padded to four bytes.
func dibRowBytes(width, bitCount int) int { return (bitCount*width + 31) / 32 * 4 }

// dibMask is an AND mask for a width x height image: rows padded to four bytes, bit set where the pixel is transparent.
func dibMask(width, height int, seed uint32) []byte {
	return icoNoise(seed, ((width+31)/32*4)*height)
}

// dibPalette is n palette entries of four bytes (b, g, r, reserved) or three.
func dibPalette(n, bytesPerColor int, seed uint32) []byte {
	noise := icoNoise(seed, n*bytesPerColor)
	if bytesPerColor == 4 {
		for i := 3; i < len(noise); i += 4 {
			noise[i] = 0
		}
	}
	return noise
}

// pngFile encodes img with Go's encoder.
func pngFile(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func pngChunk(typ string, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, typ...)
	out = append(out, data...)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	return binary.BigEndian.AppendUint32(out, crc.Sum32())
}

// pngSpec describes a PNG written by hand: any colour type and bit depth, a palette, a tRNS chunk and Adam7 interlacing.
type pngSpec struct {
	width, height int
	depth, color  byte
	palette       []byte
	trns          []byte
	interlace     bool
	seed          uint32
	// dropIEND leaves out the end chunk, dropCRC corrupts the first IDAT's checksum.
	dropIEND bool
	extra    [][2]any // chunks (type string, data []byte) after IHDR
	trnsLate bool     // write tRNS after the IDAT instead of before it
}

func pngChannels(color byte) int {
	switch color {
	case 0, 3:
		return 1
	case 2:
		return 3
	case 4:
		return 2
	}
	return 4
}

// bytes writes the PNG; the samples are noise, filter type 0 for every scanline.
func (s pngSpec) bytes() []byte {
	out := []byte("\x89PNG\r\n\x1a\n")
	ihdr := binary.BigEndian.AppendUint32(nil, uint32(s.width))
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(s.height))
	interlace := byte(0)
	if s.interlace {
		interlace = 1
	}
	ihdr = append(ihdr, s.depth, s.color, 0, 0, interlace)
	out = append(out, pngChunk("IHDR", ihdr)...)
	for _, c := range s.extra {
		out = append(out, pngChunk(c[0].(string), c[1].([]byte))...)
	}
	if s.palette != nil {
		out = append(out, pngChunk("PLTE", s.palette)...)
	}
	if s.trns != nil && !s.trnsLate {
		out = append(out, pngChunk("tRNS", s.trns)...)
	}
	bitsPerPixel := int(s.depth) * pngChannels(s.color)
	rowBytes := func(w int) int { return (w*bitsPerPixel + 7) / 8 }
	var raw []byte
	noise := icoNoise(s.seed+1, 1<<20)
	n := 0
	emit := func(w, h int) {
		for range h {
			raw = append(raw, 0)
			rb := rowBytes(w)
			row := make([]byte, rb)
			for i := range row {
				row[i] = noise[n%len(noise)]
				n++
			}
			if s.color == 3 {
				// Keep the palette indices below four.
				mask := byte(0x03)
				if s.depth < 8 {
					mask = 0x33
				}
				for i := range row {
					row[i] &= mask
				}
			}
			raw = append(raw, row...)
		}
	}
	if !s.interlace {
		emit(s.width, s.height)
	} else {
		for _, p := range [][4]int{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}} {
			w := (s.width - p[0] + p[2] - 1) / p[2]
			h := (s.height - p[1] + p[3] - 1) / p[3]
			if s.width <= p[0] || s.height <= p[1] {
				continue
			}
			emit(w, h)
		}
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	out = append(out, pngChunk("IDAT", z.Bytes())...)
	if s.trns != nil && s.trnsLate {
		out = append(out, pngChunk("tRNS", s.trns)...)
	}
	if !s.dropIEND {
		out = append(out, pngChunk("IEND", nil)...)
	}
	return out
}

// nrgbaNoise is a w x h NRGBA image of noise, with some transparent and some opaque pixels.
func nrgbaNoise(w, h int, seed uint32) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, icoNoise(seed, len(img.Pix)))
	for i := 3; i < len(img.Pix); i += 4 {
		switch img.Pix[i] & 3 {
		case 0:
			img.Pix[i] = 0
		case 1:
			img.Pix[i] = 255
		}
	}
	return img
}

// icoCorpus is the files whose decoding the test compares with Photon: bitmaps of every layout an ICO may hold (palettes of 1, 2, 4 and 8
// bits, RLE, 16-bit with and without bitfields, 24 and 32 bits, bitfield masks of every kind, every header size, top-down, AND masks),
// PNG images of every colour type, entries (the choice of the best, mismatching sizes, lengths and offsets), damaged and truncated files,
// seeded corruptions of small files, and the size limits.
func icoCorpus() []formatCase {
	var b formatCorpusBuilder

	// 32-bit bitmaps with and without a mask, sizes, and heights that halve to other values.
	for _, size := range [][2]int{{1, 1}, {2, 1}, {1, 2}, {3, 3}, {4, 4}, {7, 5}, {16, 16}, {17, 9}, {32, 32}, {33, 31}, {64, 4}, {255, 3}, {256, 2}, {3, 256}, {100, 100}} {
		w, h := size[0], size[1]
		d := newDIB(w, h, 32)
		d.pixels = icoNoise(uint32(w*31+h), w*h*4)
		name := fmt.Sprintf("%dx%d", w, h)
		b.add("bmp32-nomask-"+name, icoSingle(w, h, 32, d.bytes()))
		d.mask = dibMask(w, h, uint32(w+h))
		b.add("bmp32-mask-"+name, icoSingle(w, h, 32, d.bytes()))
	}
	// A 24-bit bitmap pads its rows to four bytes; the AND mask pads its rows to four bytes as well.
	for _, size := range [][2]int{{1, 1}, {2, 2}, {3, 2}, {5, 5}, {16, 16}, {31, 3}, {32, 3}, {33, 3}} {
		w, h := size[0], size[1]
		d := newDIB(w, h, 24)
		d.pixels = icoNoise(uint32(w*17+h), dibRowBytes(w, 24)*h)
		d.mask = dibMask(w, h, uint32(w*3+h))
		b.add(fmt.Sprintf("bmp24-mask-%dx%d", w, h), icoSingle(w, h, 24, d.bytes()))
		d.mask = nil
		b.add(fmt.Sprintf("bmp24-nomask-%dx%d", w, h), icoSingle(w, h, 24, d.bytes()))
	}
	// Palettes: bit counts, palette sizes and the pixel indices.
	for _, bits := range []uint16{1, 2, 4, 8} {
		for _, used := range []uint32{0, 1, 2, 3, 4, 15, 16, 17, 255, 256, 257, 1000} {
			if used > 1<<bits && used != 257 && used != 1000 {
				continue
			}
			w, h := 7, 5
			d := newDIB(w, h, bits)
			d.colorsUsed = used
			entries := int(used)
			if used == 0 {
				entries = 1 << bits
			}
			if used > 1<<bits {
				entries = 1 << bits
			}
			d.palette = dibPalette(entries, 4, uint32(bits)*7+used)
			d.pixels = icoNoise(uint32(bits), dibRowBytes(w, int(bits))*h)
			d.mask = dibMask(w, h, 1)
			b.add(fmt.Sprintf("palette-%dbit-used-%d", bits, used), icoSingle(w, h, bits, d.bytes()))
		}
		// Indices past the palette (colors_used small) read the zero-filled rest of the palette table.
		w, h := 9, 4
		d := newDIB(w, h, bits)
		d.colorsUsed = 2
		d.palette = dibPalette(2, 4, 99)
		d.pixels = icoNoise(uint32(bits)+50, dibRowBytes(w, int(bits))*h)
		b.add(fmt.Sprintf("palette-%dbit-indices-beyond", bits), icoSingle(w, h, bits, d.bytes()))
		// Short palette.
		d.palette = d.palette[:5]
		b.add(fmt.Sprintf("palette-%dbit-short", bits), icoSingle(w, h, bits, d.bytes()))
		for _, size := range [][2]int{{1, 1}, {8, 2}, {9, 3}, {31, 2}, {32, 2}, {33, 2}, {64, 3}} {
			w, h := size[0], size[1]
			d := newDIB(w, h, bits)
			d.palette = dibPalette(1<<bits, 4, uint32(bits)+uint32(w))
			d.pixels = icoNoise(uint32(w*bits2(bits)+h), dibRowBytes(w, int(bits))*h)
			d.mask = dibMask(w, h, uint32(w))
			b.add(fmt.Sprintf("palette-%dbit-%dx%d", bits, w, h), icoSingle(w, h, bits, d.bytes()))
		}
	}
	// 16-bit: RGB555 and BITFIELDS masks (every layout of image's Bitfield).
	for _, size := range [][2]int{{1, 1}, {2, 2}, {3, 3}, {7, 4}} {
		w, h := size[0], size[1]
		d := newDIB(w, h, 16)
		d.pixels = icoNoise(uint32(w+h), dibRowBytes(w, 16)*h)
		d.mask = dibMask(w, h, 5)
		b.add(fmt.Sprintf("rgb16-%dx%d", w, h), icoSingle(w, h, 16, d.bytes()))
	}
	type masks struct {
		name       string
		r, g, b, a uint32
	}
	for _, header := range []uint32{40, 52, 56, 108, 124} {
		for _, m := range []masks{
			{"565", 0xF800, 0x07E0, 0x001F, 0}, {"555", 0x7C00, 0x03E0, 0x001F, 0}, {"1555", 0x7C00, 0x03E0, 0x001F, 0x8000}, {"4444", 0x0F00, 0x00F0, 0x000F, 0xF000},
			{"332", 0xE0, 0x1C, 0x03, 0}, {"111", 0x4, 0x2, 0x1, 0}, {"222", 0x30, 0x0C, 0x03, 0}, {"777", 0x3F80, 0x007F << 7 >> 7, 0x7F << 0, 0},
			{"bgr565", 0x001F, 0x07E0, 0xF800, 0}, {"gap", 0x0F00, 0x00F0, 0x0007, 0}, {"nocontig", 0xF0F0, 0x0F00, 0x000F, 0}, {"nored", 0, 0x07E0, 0x001F, 0},
			{"nogreen", 0xF800, 0, 0x001F, 0}, {"noblue", 0xF800, 0x07E0, 0, 0}, {"toolong", 0x1FFFF, 0x0F00, 0x000F, 0}, {"all-same", 0xFFFF, 0xFFFF, 0xFFFF, 0},
			{"alpha-only", 0, 0, 0, 0xFFFF}, {"overlap", 0xFF00, 0x0FF0, 0x00FF, 0}, {"r1-g1-b1-a1", 0x8, 0x4, 0x2, 0x1}, {"3-bit", 0x7000, 0x0E00, 0x01C0, 0},
		} {
			w, h := 5, 3
			d := newDIB(w, h, 16)
			d.headerSize = header
			d.compression = 3
			d.masks = []uint32{m.r, m.g, m.b}
			if header >= 56 {
				d.masks = append(d.masks, m.a)
			}
			d.pixels = icoNoise(uint32(m.r+m.g), dibRowBytes(w, 16)*h)
			d.mask = dibMask(w, h, 2)
			b.add(fmt.Sprintf("bitfields16-h%d-%s", header, m.name), icoSingle(w, h, 16, d.bytes()))
		}
		for _, m := range []masks{
			{"888-888", 0xFF0000, 0x00FF00, 0x0000FF, 0}, {"rgba", 0xFF0000, 0x00FF00, 0x0000FF, 0xFF000000}, {"r8g8b8-shifted", 0xFF000000, 0x00FF0000, 0x0000FF00, 0},
			{"r8g8b8a8-shifted", 0xFF000000, 0x00FF0000, 0x0000FF00, 0x000000FF}, {"bgra", 0x0000FF, 0x00FF00, 0xFF0000, 0xFF000000}, {"10-10-10-2", 0x3FF00000, 0xFFC00, 0x3FF, 0xC0000000},
			{"11-11-10", 0xFFE00000, 0x1FFC00, 0x3FF, 0}, {"16-bit-channel", 0xFFFF0000, 0xFF00, 0xFF, 0}, {"nocontig", 0xF0F00000, 0x0F0000, 0xFF, 0}, {"toolong", 0, 0xFF00, 0xFF, 0},
			{"a-only-8", 0xFF, 0xFF00, 0xFF0000, 0xFF000000}, {"zero", 0, 0, 0, 0}, {"rgb-no-alpha-bits", 0xF800, 0x07E0, 0x001F, 0}, {"wide-alpha", 0xFF0000, 0xFF00, 0xFF, 0xFFFFFF00}, {"top-bits", 0x80000000, 0x40000000, 0x20000000, 0x10000000},
		} {
			w, h := 5, 3
			d := newDIB(w, h, 32)
			d.headerSize = header
			d.compression = 3
			d.masks = []uint32{m.r, m.g, m.b}
			if header >= 56 {
				d.masks = append(d.masks, m.a)
			}
			d.pixels = icoNoise(uint32(m.r+m.g+7), w*h*4)
			d.mask = dibMask(w, h, 3)
			b.add(fmt.Sprintf("bitfields32-h%d-%s", header, m.name), icoSingle(w, h, 32, d.bytes()))
		}
	}
	// BITFIELDS with the 40-byte header: the masks sit after the header, but the data starts at the end of the header, so the masks are pixels.
	{
		d := newDIB(3, 2, 32)
		d.compression = 3
		d.masks = []uint32{0xFF0000, 0xFF00, 0xFF}
		d.pixels = icoNoise(5, 3*2*4)
		b.add("bitfields32-masks-are-pixels", icoSingle(3, 2, 32, d.bytes()))
		d.pixels = icoNoise(5, 3*2*4-12)
		b.add("bitfields32-masks-are-pixels-short", icoSingle(3, 2, 32, d.bytes()))
	}
	// RLE.
	rle := func(name string, bits uint16, bitmap func(*dib), width, height int, stream []byte) {
		d := newDIB(width, height, bits)
		if bits == 8 {
			d.compression = 1
		} else {
			d.compression = 2
		}
		d.palette = dibPalette(1<<bits, 4, uint32(bits)*3)
		d.pixels = stream
		if bitmap != nil {
			bitmap(&d)
		}
		b.add(name, icoSingle(width, height, bits, d.bytes()))
	}
	rle("rle8-runs", 8, nil, 6, 3, []byte{6, 1, 0, 0, 3, 2, 3, 4, 0, 0, 0, 5, 0, 1, 6, 0, 0, 0, 1})
	rle("rle8-runs-then-eof", 8, nil, 6, 3, []byte{6, 1, 0, 0, 3, 2, 3, 4, 0, 0, 0, 0, 5, 5, 5, 5, 5, 5, 0, 0, 0, 1})
	rle("rle8-absolute", 8, nil, 5, 2, []byte{0, 5, 1, 2, 3, 4, 5, 0, 0, 0, 0, 3, 7, 8, 9, 0, 2, 5, 0, 0, 1})
	rle("rle8-absolute-odd-padding", 8, nil, 4, 2, []byte{0, 3, 1, 2, 3, 0, 1, 4, 0, 0, 4, 1, 2, 3, 4, 0, 1})
	rle("rle8-delta", 8, nil, 6, 4, []byte{2, 1, 0, 2, 3, 0, 3, 1, 0, 0, 0, 2, 0, 1, 2, 2, 0, 1})
	rle("rle8-delta-y-only", 8, nil, 6, 4, []byte{2, 1, 0, 2, 0, 1, 3, 1, 0, 0, 0, 1})
	rle("rle8-delta-skips-rows", 8, nil, 4, 6, []byte{4, 1, 0, 2, 0, 3, 4, 2, 0, 1})
	rle("rle8-delta-too-far-x", 8, nil, 4, 2, []byte{0, 2, 9, 0, 4, 1, 0, 1})
	rle("rle8-delta-too-far-y", 8, nil, 4, 2, []byte{4, 1, 0, 2, 0, 5, 0, 1})
	rle("rle8-run-over-row", 8, nil, 4, 2, []byte{5, 1, 0, 0, 4, 2, 0, 1})
	rle("rle8-absolute-over-row", 8, nil, 3, 2, []byte{0, 4, 1, 2, 3, 4, 0, 0, 3, 2, 0, 1})
	rle("rle8-early-eof", 8, nil, 4, 4, []byte{4, 1, 0, 0, 0, 1})
	rle("rle8-eol-first", 8, nil, 4, 3, []byte{0, 0, 4, 1, 0, 0, 4, 2, 0, 0, 0, 1})
	rle("rle8-no-end", 8, nil, 4, 2, []byte{4, 1, 4, 2})
	rle("rle8-no-end-eol", 8, nil, 4, 2, []byte{4, 1, 0, 0, 4, 2, 0, 0})
	rle("rle8-extra-after-last-row", 8, nil, 4, 2, []byte{4, 1, 0, 0, 4, 2, 0, 0, 0, 1})
	rle("rle8-truncated-pair", 8, nil, 4, 2, []byte{4})
	rle("rle8-truncated-absolute", 8, nil, 4, 2, []byte{0, 4, 1, 2})
	rle("rle8-truncated-delta", 8, nil, 4, 2, []byte{0, 2, 1})
	rle("rle4-runs", 4, nil, 8, 3, []byte{8, 0x12, 0, 0, 4, 0x34, 4, 0x56, 0, 0, 0, 1})
	rle("rle4-run-odd", 4, nil, 5, 3, []byte{5, 0x12, 0, 0, 3, 0x34, 2, 0x56, 0, 0, 0, 1})
	rle("rle4-absolute", 4, nil, 8, 2, []byte{0, 6, 0x12, 0x34, 0x56, 0, 0, 0, 4, 0x12, 0x34, 0, 0, 1})
	rle("rle4-absolute-odd", 4, nil, 8, 2, []byte{0, 5, 0x12, 0x34, 0x50, 0, 0, 0, 3, 0x12, 0x30, 0, 0, 1})
	rle("rle4-absolute-3", 4, nil, 4, 2, []byte{0, 3, 0x12, 0x30, 0, 0, 0, 4, 0xAB, 0xCD, 0, 1})
	rle("rle4-delta", 4, nil, 8, 4, []byte{2, 0x12, 0, 2, 3, 1, 4, 0x34, 0, 0, 0, 1})
	rle("rle4-run-over-row", 4, nil, 4, 2, []byte{6, 0x12, 0, 0, 4, 0x34, 0, 1})
	rle("rle4-absolute-over-row", 4, nil, 3, 2, []byte{0, 4, 0x12, 0x34, 0, 0, 3, 0x12, 0, 1})
	rle("rle4-single", 4, nil, 1, 1, []byte{1, 0x12, 0, 1})
	rle("rle4-zero-run", 4, nil, 4, 1, []byte{0, 0x12, 4, 0x34, 0, 1})
	// RLE with the wrong bit count, top-down and the core header.
	{
		d := newDIB(4, 2, 4)
		d.compression = 1
		d.palette = dibPalette(16, 4, 1)
		d.pixels = []byte{4, 1, 0, 0, 4, 2, 0, 1}
		b.add("rle8-with-4-bits", icoSingle(4, 2, 4, d.bytes()))
		d = newDIB(4, 2, 8)
		d.compression = 2
		d.palette = dibPalette(256, 4, 1)
		d.pixels = []byte{4, 0x11, 0, 0, 4, 0x22, 0, 1}
		b.add("rle4-with-8-bits", icoSingle(4, 2, 8, d.bytes()))
		d = newDIB(4, 2, 8)
		d.compression = 1
		d.height = -d.height
		d.palette = dibPalette(256, 4, 1)
		d.pixels = []byte{4, 1, 0, 0, 4, 2, 0, 1}
		b.add("rle8-top-down", icoSingle(4, 2, 8, d.bytes()))
		for _, comp := range []uint32{4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 100, 0xFFFFFFFF} {
			d = newDIB(4, 2, 8)
			d.compression = comp
			d.palette = dibPalette(256, 4, 1)
			d.pixels = icoNoise(1, 8)
			b.add(fmt.Sprintf("compression-%d", comp), icoSingle(4, 2, 8, d.bytes()))
		}
	}
	// Core headers.
	for _, bits := range []uint16{1, 4, 8, 24, 16, 32, 2, 0} {
		for _, size := range [][2]int{{4, 4}, {5, 3}} {
			w, h := size[0], size[1]
			d := newDIB(w, h, bits)
			d.headerSize = 12
			if bits <= 8 && bits != 0 && bits != 2 {
				d.palette = dibPalette(1<<bits, 3, uint32(bits))
			}
			d.pixels = icoNoise(uint32(bits)+uint32(w), dibRowBytes(w, int(bits))*h)
			d.mask = dibMask(w, h, 4)
			b.add(fmt.Sprintf("core-%dbit-%dx%d", bits, w, h), icoSingle(w, h, bits, d.bytes()))
		}
	}
	{
		d := newDIB(4, 4, 24)
		d.headerSize = 12
		d.planes = 2
		d.pixels = icoNoise(1, dibRowBytes(4, 24)*4)
		b.add("core-two-planes", icoSingle(4, 4, 24, d.bytes()))
		d = newDIB(0, 4, 24)
		d.headerSize = 12
		d.pixels = icoNoise(1, 12)
		b.add("core-width-0", icoSingle(1, 4, 24, d.bytes()))
		d = newDIB(4, 0, 24)
		d.headerSize = 12
		b.add("core-height-0", icoSingle(4, 1, 24, d.bytes()))
		d = newDIB(4, 4, 24)
		d.headerSize = 12
		d.height = 9 // odd total height
		d.pixels = icoNoise(1, dibRowBytes(4, 24)*8)
		b.add("core-odd-height", icoSingle(4, 4, 24, d.bytes()))
	}
	// Header sizes and the fields of the info header.
	for _, size := range []uint32{0, 1, 11, 13, 16, 20, 39, 41, 48, 51, 53, 55, 57, 64, 100, 107, 109, 123, 125, 200, 0xFFFFFFFF} {
		d := newDIB(4, 4, 32)
		d.headerSize = size
		d.pixels = icoNoise(1, 4*4*4)
		b.add(fmt.Sprintf("header-size-%d", size), icoSingle(4, 4, 32, d.bytes()))
	}
	for _, size := range []uint32{40, 52, 56, 108, 124} {
		d := newDIB(4, 4, 32)
		d.headerSize = size
		d.pixels = icoNoise(1, 4*4*4)
		d.extraHeader = icoNoise(2, 64)
		d.mask = dibMask(4, 4, 9)
		b.add(fmt.Sprintf("header-%d-32bit", size), icoSingle(4, 4, 32, d.bytes()))
	}
	for _, planes := range []uint16{0, 2, 3, 256, 0xFFFF} {
		d := newDIB(4, 4, 32)
		d.planes = planes
		d.pixels = icoNoise(1, 4*4*4)
		b.add(fmt.Sprintf("planes-%d", planes), icoSingle(4, 4, 32, d.bytes()))
	}
	for _, bits := range []uint16{0, 3, 5, 6, 7, 9, 12, 15, 17, 23, 25, 31, 33, 48, 64, 255, 256, 0xFFFF} {
		d := newDIB(4, 4, bits)
		d.palette = dibPalette(256, 4, 1)
		d.pixels = icoNoise(1, 4*dibRowBytes(4, 32))
		b.add(fmt.Sprintf("bitcount-%d", bits), icoSingle(4, 4, 32, d.bytes()))
	}
	for _, dims := range [][2]int32{{0, 8}, {4, 0}, {0, 0}, {-4, 8}, {4, 1}, {4, 2}, {4, 3}, {4, 9}, {4, 7}, {65535, 8}, {65536, 8}, {4, 65535}, {4, 65536}, {4, -65535}, {4, -65536}, {4, -8}, {4, -2}, {4, -1},
		{1 << 30, 8}, {4, 1 << 30}, {4, -1 << 31}, {4, -1<<31 + 1}, {1<<31 - 1, 2}, {4, 1<<31 - 1}, {65535, 65535}, {65535, 131070}, {32767, 65534}} {
		d := newDIB(1, 1, 32)
		d.width, d.height = dims[0], dims[1]
		d.pixels = icoNoise(1, 4*8*4)
		b.add(fmt.Sprintf("dims-%dx%d", dims[0], dims[1]), icoSingle(4, 4, 32, d.bytes()))
	}
	// Top-down bitmaps.
	for _, comp := range []uint32{0, 3} {
		for _, bits := range []uint16{32, 24, 16, 8, 4, 1} {
			if comp == 3 && bits != 16 && bits != 32 {
				continue
			}
			w, h := 5, 4
			d := newDIB(w, h, bits)
			d.height = -d.height
			d.compression = comp
			if comp == 3 {
				d.headerSize = 56
				if bits == 16 {
					d.masks = []uint32{0xF800, 0x07E0, 0x001F, 0}
				} else {
					d.masks = []uint32{0xFF0000, 0xFF00, 0xFF, 0xFF000000}
				}
			}
			if bits <= 8 {
				d.palette = dibPalette(1<<bits, 4, 1)
			}
			d.pixels = icoNoise(uint32(bits), dibRowBytes(w, int(bits))*h)
			d.mask = dibMask(w, h, 6)
			b.add(fmt.Sprintf("topdown-c%d-%dbit", comp, bits), icoSingle(w, h, bits, d.bytes()))
		}
	}
	// A mask that is bigger, smaller or absent relative to the entry's length.
	{
		w, h := 6, 4
		d := newDIB(w, h, 32)
		d.pixels = icoNoise(3, w*h*4)
		plain := d.bytes()
		withMask := append(append([]byte(nil), plain...), dibMask(w, h, 8)...)
		maskLen := len(dibMask(w, h, 8))
		for _, delta := range []int{-maskLen - 1, -maskLen, -maskLen + 1, -1, 0, 1, maskLen, maskLen + 1, 100} {
			entry := icoEntry{width: byte(w), height: byte(h), planes: 1, bpp: 32, setLength: true, length: uint32(len(plain) + maskLen + delta)}
			b.add(fmt.Sprintf("mask-length-delta-%d-with-mask", delta), icoFile(1, []icoEntry{entry}, [][]byte{withMask}))
			entry.length = uint32(len(plain) + delta)
			b.add(fmt.Sprintf("mask-length-delta-%d-without-mask", delta), icoFile(1, []icoEntry{entry}, [][]byte{plain}))
		}
		short := withMask[:len(withMask)-1]
		b.add("mask-last-byte-missing", icoFile(1, []icoEntry{{width: byte(w), height: byte(h), planes: 1, bpp: 32}}, [][]byte{short}))
		b.add("mask-last-byte-missing-length-of-image", icoFile(1, []icoEntry{{width: byte(w), height: byte(h), planes: 1, bpp: 32, setLength: true, length: uint32(len(withMask))}}, [][]byte{short}))
		b.add("mask-length-zero", icoFile(1, []icoEntry{{width: byte(w), height: byte(h), planes: 1, bpp: 32, setLength: true, length: 0}}, [][]byte{withMask}))
	}
	// Entries: the best of several, mismatching sizes, offsets and lengths, damaged directories.
	mkImage := func(w, h int, bits uint16, seed uint32) []byte {
		d := newDIB(w, h, bits)
		if bits <= 8 {
			d.palette = dibPalette(1<<bits, 4, seed)
		}
		d.pixels = icoNoise(seed, dibRowBytes(w, int(bits))*h)
		return d.bytes()
	}
	{
		i1, i2, i3 := mkImage(4, 4, 32, 1), mkImage(8, 8, 32, 2), mkImage(8, 8, 8, 3)
		e := func(w, h int, bpp uint16) icoEntry {
			return icoEntry{width: byte(w), height: byte(h), planes: 1, bpp: bpp}
		}
		b.add("entries-bigger-wins", icoFile(1, []icoEntry{e(4, 4, 32), e(8, 8, 32)}, [][]byte{i1, i2}))
		b.add("entries-bigger-first", icoFile(1, []icoEntry{e(8, 8, 32), e(4, 4, 32)}, [][]byte{i2, i1}))
		b.add("entries-depth-over-size", icoFile(1, []icoEntry{e(8, 8, 8), e(4, 4, 32)}, [][]byte{i3, i1}))
		b.add("entries-tie-takes-last", icoFile(1, []icoEntry{e(8, 8, 32), e(8, 8, 32)}, [][]byte{i2, i2}))
		b.add("entries-tie-first-differs", icoFile(1, []icoEntry{e(8, 8, 32), e(8, 8, 32)}, [][]byte{mkImage(8, 8, 32, 7), i2}))
		b.add("entries-three", icoFile(1, []icoEntry{e(4, 4, 32), e(8, 8, 8), e(8, 8, 32)}, [][]byte{i1, i3, i2}))
		b.add("entries-zero-bpp-all", icoFile(1, []icoEntry{e(4, 4, 0), e(8, 8, 0)}, [][]byte{i1, i2}))
		b.add("entries-last-is-bad", icoFile(1, []icoEntry{e(8, 8, 32), e(8, 8, 32)}, [][]byte{i2, []byte{1, 2, 3}}))
		b.add("entries-first-is-bad-but-unused", icoFile(1, []icoEntry{e(4, 4, 8), e(8, 8, 32)}, [][]byte{{1, 2, 3}, i2}))
		b.add("entries-zero-count", icoFile(1, nil, nil))
		for _, planes := range []uint16{0, 1, 2, 256, 257, 0xFFFF} {
			b.add(fmt.Sprintf("entry-planes-%d", planes), icoFile(1, []icoEntry{{width: 4, height: 4, planes: planes, bpp: 32}}, [][]byte{i1}))
			b.add(fmt.Sprintf("entry-bpp-%d", planes), icoFile(1, []icoEntry{{width: 4, height: 4, planes: 1, bpp: planes}}, [][]byte{i1}))
		}
		for _, typ := range []uint16{0, 1, 2, 3, 0xFFFF} {
			b.add(fmt.Sprintf("type-%d", typ), icoFile(typ, []icoEntry{e(4, 4, 32)}, [][]byte{i1}))
		}
		for _, size := range [][2]int{{4, 4}, {0, 0}, {5, 4}, {4, 5}, {3, 4}, {255, 4}, {4, 255}, {1, 1}} {
			b.add(fmt.Sprintf("entry-size-%dx%d", size[0], size[1]), icoFile(1, []icoEntry{e(size[0], size[1], 32)}, [][]byte{i1}))
		}
		b.add("entry-reserved-colors", icoFile(1, []icoEntry{{width: 4, height: 4, colors: 255, reserved: 7, planes: 1, bpp: 32}}, [][]byte{i1}))
		for _, off := range []uint32{0, 5, 6, 21, 22, 23, 24, 100, 0xFFFFFFFF, 0x7FFFFFFF, 0x100000000 - 8} {
			b.add(fmt.Sprintf("entry-offset-%d", off), icoFile(1, []icoEntry{{width: 4, height: 4, planes: 1, bpp: 32, setOffset: true, offset: off}}, [][]byte{i1}))
		}
		for _, length := range []uint32{0, 1, 7, 8, 9, 100, uint32(len(i1) - 1), uint32(len(i1)), uint32(len(i1) + 1), 0xFFFFFFFF} {
			b.add(fmt.Sprintf("entry-length-%d", length), icoFile(1, []icoEntry{{width: 4, height: 4, planes: 1, bpp: 32, setLength: true, length: length}}, [][]byte{i1}))
		}
		// An entry pointing into the middle of another image's data and an offset into the directory.
		b.add("entry-offset-into-directory", icoFile(1, []icoEntry{{width: 4, height: 4, planes: 1, bpp: 32, setOffset: true, offset: 6}}, [][]byte{i1}))
		// The count claims more entries than the file holds.
		file := icoFile(1, []icoEntry{e(4, 4, 32)}, [][]byte{i1})
		over := append([]byte(nil), file...)
		over[4], over[5] = 3, 0
		b.add("entries-count-too-high", over)
		over[4], over[5] = 0xFF, 0xFF
		b.add("entries-count-max", over)
		// Not an ICO.
		b.add("header-cursor-type", append([]byte{0, 0, 2, 0}, file[4:]...))
		b.add("header-reserved-nonzero", append([]byte{1, 0}, file[2:]...))
		b.add("header-only-signature", []byte{0, 0, 1, 0})
		b.add("header-signature-and-count", []byte{0, 0, 1, 0, 1, 0})
		b.add("header-signature-and-short-entry", append([]byte{0, 0, 1, 0, 1, 0}, make([]byte, 15)...))
		for n := range len(file) {
			b.add(fmt.Sprintf("truncated-bmp32-%03d", n), file[:n:n])
		}
		for _, cut := range []int{len(i1) - 1, len(i1) - 10, 20} {
			b.add(fmt.Sprintf("image-cut-%d", cut), icoFile(1, []icoEntry{e(4, 4, 32)}, [][]byte{i1[:cut]}))
		}
	}

	// PNG images.
	{
		e := func(w, h int) icoEntry { return icoEntry{width: byte(w), height: byte(h), planes: 1, bpp: 32} }
		for _, size := range [][2]int{{1, 1}, {2, 2}, {3, 5}, {16, 16}, {32, 32}, {64, 2}, {255, 3}, {256, 256}, {256, 1}, {1, 256}} {
			w, h := size[0], size[1]
			data := pngFile(nrgbaNoise(w, h, uint32(w*h)))
			b.add(fmt.Sprintf("png-rgba8-%dx%d", w, h), icoSingle(w, h, 32, data))
			b.add(fmt.Sprintf("png-rgba8-%dx%d-no-entry-size", w, h), icoFile(1, []icoEntry{{planes: 1, bpp: 32}}, [][]byte{data}))
		}
		// Larger than 256: the entry stores 0 and only the minimum with 256 is compared.
		for _, size := range [][2]int{{257, 10}, {300, 300}, {1000, 2}, {10, 300}, {256, 300}, {300, 256}} {
			w, h := size[0], size[1]
			data := pngFile(nrgbaNoise(w, h, uint32(w+h)))
			for i, entry := range []icoEntry{e(0, 0), e(w, h), e(256, 2), e(0, h), e(w, 0)} {
				b.add(fmt.Sprintf("png-rgba8-big-%dx%d-entry-%d-%d-%d", w, h, entry.width, entry.height, i), icoFile(1, []icoEntry{entry}, [][]byte{data}))
			}
		}
		data := pngFile(nrgbaNoise(16, 16, 5))
		for _, size := range [][2]int{{15, 16}, {16, 15}, {17, 16}, {0, 16}, {16, 0}, {0, 0}, {1, 1}} {
			b.add(fmt.Sprintf("png-entry-mismatch-%dx%d", size[0], size[1]), icoFile(1, []icoEntry{e(size[0], size[1])}, [][]byte{data}))
		}
		for _, length := range []uint32{0, 1, 7, 8, 9, 20, uint32(len(data)), 0xFFFFFFFF} {
			b.add(fmt.Sprintf("png-entry-length-%d", length), icoFile(1, []icoEntry{{width: 16, height: 16, planes: 1, bpp: 32, setLength: true, length: length}}, [][]byte{data}))
		}
		// Go's encoder: every colour type.
		gray := image.NewGray(image.Rect(0, 0, 8, 8))
		copy(gray.Pix, icoNoise(1, 64))
		gray16 := image.NewGray16(image.Rect(0, 0, 8, 8))
		copy(gray16.Pix, icoNoise(2, 128))
		rgba := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for i := 0; i < len(rgba.Pix); i += 4 {
			a := byte(i)
			rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2], rgba.Pix[i+3] = a/3, a/5, a/7, a
		}
		rgba64 := image.NewNRGBA64(image.Rect(0, 0, 8, 8))
		copy(rgba64.Pix, icoNoise(3, 512))
		opaque := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for i := 0; i < len(opaque.Pix); i += 4 {
			opaque.Pix[i], opaque.Pix[i+1], opaque.Pix[i+2], opaque.Pix[i+3] = byte(i), byte(i*3), byte(i*7), 255
		}
		var pal color.Palette
		for i := range 16 {
			pal = append(pal, color.NRGBA{byte(i * 16), byte(255 - i*16), byte(i * 7), 255})
		}
		paletted := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
		for i := range paletted.Pix {
			paletted.Pix[i] = byte(i % 16)
		}
		var palAlpha color.Palette
		for i := range 16 {
			palAlpha = append(palAlpha, color.NRGBA{byte(i * 16), byte(255 - i*16), byte(i * 7), byte(i * 17)})
		}
		palettedAlpha := image.NewPaletted(image.Rect(0, 0, 8, 8), palAlpha)
		copy(palettedAlpha.Pix, paletted.Pix)
		grayAlpha := image.NewNRGBA(image.Rect(0, 0, 8, 8))
		for i := 0; i < len(grayAlpha.Pix); i += 4 {
			v := byte(i * 5)
			grayAlpha.Pix[i], grayAlpha.Pix[i+1], grayAlpha.Pix[i+2], grayAlpha.Pix[i+3] = v, v, v, byte(i*3)
		}
		for name, img := range map[string]image.Image{"gray8": gray, "gray16": gray16, "rgba8-premultiplied": rgba, "nrgba64": rgba64, "rgb8-opaque": opaque, "paletted16": paletted,
			"paletted16-alpha": palettedAlpha, "gray-alpha8": grayAlpha} {
			b.add("png-go-"+name, icoSingle(8, 8, 32, pngFile(img)))
		}
		// By hand: colour types, bit depths, tRNS, interlacing.
		trnsFor := map[byte][]byte{0: {0, 3}, 2: {0, 1, 0, 2, 0, 3}, 3: {255, 128, 0, 77}}
		for _, color := range []byte{0, 2, 3, 4, 6} {
			for _, depth := range []byte{1, 2, 4, 8, 16} {
				for _, interlace := range []bool{false, true} {
					for _, trns := range []bool{false, true} {
						spec := pngSpec{width: 8, height: 8, depth: depth, color: color, interlace: interlace, seed: uint32(color)*10 + uint32(depth)}
						if color == 3 {
							spec.palette = icoNoise(7, 3*(1<<min(int(depth), 8)))
							if depth > 4 {
								spec.palette = icoNoise(7, 3*16)
							}
						}
						if trns {
							t, has := trnsFor[color]
							if !has {
								continue
							}
							if color == 2 && depth == 16 {
								t = []byte{0, 1, 0, 2, 0, 3}
							}
							spec.trns = t
						}
						name := fmt.Sprintf("png-hand-c%d-d%d-trns%t-adam%t", color, depth, trns, interlace)
						b.add(name, icoSingle(8, 8, 32, spec.bytes()))
					}
				}
			}
		}
		// tRNS shapes on RGB8, palette and gray.
		for name, spec := range map[string]pngSpec{
			"rgb8-trns-short":        {width: 4, height: 4, depth: 8, color: 2, trns: []byte{0, 1}},
			"rgb8-trns-long":         {width: 4, height: 4, depth: 8, color: 2, trns: []byte{0, 1, 0, 2, 0, 3, 0, 4}},
			"rgb8-trns-empty":        {width: 4, height: 4, depth: 8, color: 2, trns: []byte{}},
			"rgb8-trns-late":         {width: 4, height: 4, depth: 8, color: 2, trns: []byte{0, 1, 0, 2, 0, 3}, trnsLate: true},
			"rgba8-trns":             {width: 4, height: 4, depth: 8, color: 6, trns: []byte{0, 1, 0, 2, 0, 3}},
			"palette-trns-longer":    {width: 4, height: 4, depth: 8, color: 3, palette: icoNoise(1, 12), trns: []byte{1, 2, 3, 4, 5, 6}},
			"palette-trns-shorter":   {width: 4, height: 4, depth: 8, color: 3, palette: icoNoise(1, 12), trns: []byte{9}},
			"palette-trns-late":      {width: 4, height: 4, depth: 8, color: 3, palette: icoNoise(1, 12), trns: []byte{1, 2}, trnsLate: true},
			"palette-trns-empty":     {width: 4, height: 4, depth: 8, color: 3, palette: icoNoise(1, 12), trns: []byte{}},
			"palette-no-plte-trns":   {width: 4, height: 4, depth: 8, color: 3, trns: []byte{1, 2}},
			"palette-no-plte":        {width: 4, height: 4, depth: 8, color: 3},
			"palette-empty-plte":     {width: 4, height: 4, depth: 8, color: 3, palette: []byte{}},
			"palette-odd-plte":       {width: 4, height: 4, depth: 8, color: 3, palette: icoNoise(1, 10), trns: []byte{1, 2}},
			"palette-257-entries":    {width: 4, height: 4, depth: 8, color: 3, palette: icoNoise(1, 3*257), trns: []byte{1, 2}},
			"gray8-trns":             {width: 4, height: 4, depth: 8, color: 0, trns: []byte{0, 5}},
			"gray16-trns":            {width: 4, height: 4, depth: 16, color: 0, trns: []byte{0, 5}},
			"rgb16-trns":             {width: 4, height: 4, depth: 16, color: 2, trns: []byte{0, 1, 0, 2, 0, 3}},
			"rgba8-gamma":            {width: 4, height: 4, depth: 8, color: 6, extra: [][2]any{{"gAMA", []byte{0, 0, 0xB1, 0x8F}}}},
			"rgba8-srgb":             {width: 4, height: 4, depth: 8, color: 6, extra: [][2]any{{"sRGB", []byte{0}}}},
			"rgba8-chrm":             {width: 4, height: 4, depth: 8, color: 6, extra: [][2]any{{"cHRM", make([]byte, 32)}}},
			"rgba8-text":             {width: 4, height: 4, depth: 8, color: 6, extra: [][2]any{{"tEXt", []byte("Comment\x00hello")}}},
			"rgba8-private-chunk":    {width: 4, height: 4, depth: 8, color: 6, extra: [][2]any{{"prVt", []byte("x")}}},
			"rgba8-unknown-critical": {width: 4, height: 4, depth: 8, color: 6, extra: [][2]any{{"CRIT", []byte("x")}}},
			"rgba8-actl":             {width: 4, height: 4, depth: 8, color: 6, extra: [][2]any{{"acTL", []byte{0, 0, 0, 2, 0, 0, 0, 0}}}},
			"rgba8-no-iend":          {width: 4, height: 4, depth: 8, color: 6, dropIEND: true},
			"rgba8-interlaced":       {width: 9, height: 7, depth: 8, color: 6, interlace: true},
			"rgba8-1x1-interlaced":   {width: 1, height: 1, depth: 8, color: 6, interlace: true},
			"rgba8-trns-with-alpha":  {width: 4, height: 4, depth: 8, color: 6, trns: []byte{1}},
		} {
			b.add("png-hand-"+name, icoSingle(spec.width, spec.height, 32, spec.bytes()))
		}
		// Damage to the PNG stream.
		base := pngSpec{width: 4, height: 4, depth: 8, color: 6, seed: 9}.bytes()
		ico := icoSingle(4, 4, 32, base)
		for pos := range len(ico) {
			m := append([]byte(nil), ico...)
			m[pos] ^= 0xFF
			b.add(fmt.Sprintf("png-flip-ff-%03d", pos), m)
		}
		for n := range len(ico) {
			b.add(fmt.Sprintf("png-truncated-%03d", n), ico[:n:n])
		}
		b.add("png-trailing-garbage", append(append([]byte(nil), ico...), 1, 2, 3, 4, 5))
		b.add("png-trailing-garbage-in-length", icoFile(1, []icoEntry{{width: 4, height: 4, planes: 1, bpp: 32, setLength: true, length: uint32(len(base) + 5)}}, [][]byte{append(append([]byte(nil), base...), 1, 2, 3, 4, 5)}))
		// Dimensions in the IHDR.
		for _, dims := range [][2]uint32{{0, 4}, {4, 0}, {0, 0}, {1 << 31, 1}, {1<<31 - 1, 1}, {1, 1<<31 - 1}, {65536, 65536}, {16384, 8192}, {16385, 8192}, {16384, 8193}, {1 << 30, 1}, {1 << 29, 1}, {1<<29 + 1, 1}, {1 << 28, 1}} {
			spec := pngSpec{width: 4, height: 4, depth: 8, color: 6, seed: 1}
			data := spec.bytes()
			binary.BigEndian.PutUint32(data[16:], dims[0])
			binary.BigEndian.PutUint32(data[20:], dims[1])
			crc := crc32.ChecksumIEEE(data[12:29])
			binary.BigEndian.PutUint32(data[29:], crc)
			b.add(fmt.Sprintf("png-ihdr-%dx%d", dims[0], dims[1]), icoFile(1, []icoEntry{{planes: 1, bpp: 32}}, [][]byte{data}))
		}
		// An IHDR with other fields.
		for _, f := range []struct {
			name  string
			index int
			value byte
		}{{"depth-3", 24, 3}, {"depth-0", 24, 0}, {"depth-16", 24, 16}, {"color-1", 25, 1}, {"color-5", 25, 5}, {"color-7", 25, 7}, {"compression-1", 26, 1}, {"filter-1", 27, 1}, {"interlace-2", 28, 2}, {"interlace-255", 28, 255}} {
			data := pngSpec{width: 4, height: 4, depth: 8, color: 6, seed: 1}.bytes()
			data[f.index] = f.value
			binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))
			b.add("png-ihdr-"+f.name, icoSingle(4, 4, 32, data))
		}
		b.add("png-signature-only", icoSingle(4, 4, 32, []byte("\x89PNG\r\n\x1a\n")))
		b.add("png-signature-wrong-last", icoSingle(4, 4, 32, append([]byte("\x89PNG\r\n\x1a\x0b"), base[8:]...)))
	}

	// Seeded corruptions of small bitmap files (every position).
	{
		d := newDIB(3, 2, 32)
		d.pixels = icoNoise(1, 3*2*4)
		d.mask = dibMask(3, 2, 2)
		small := icoSingle(3, 2, 32, d.bytes())
		for pos := range len(small) {
			for _, x := range []byte{0xFF, 0x80, 0x01} {
				m := append([]byte(nil), small...)
				m[pos] ^= x
				b.add(fmt.Sprintf("flip-bmp32-%03d-%02x", pos, x), m)
			}
		}
		p := newDIB(3, 2, 4)
		p.colorsUsed = 3
		p.palette = dibPalette(3, 4, 5)
		p.pixels = icoNoise(6, dibRowBytes(3, 4)*2)
		p.mask = dibMask(3, 2, 7)
		pal := icoSingle(3, 2, 4, p.bytes())
		for pos := range len(pal) {
			for _, x := range []byte{0xFF, 0x04} {
				m := append([]byte(nil), pal...)
				m[pos] ^= x
				b.add(fmt.Sprintf("flip-palette4-%03d-%02x", pos, x), m)
			}
		}
		r := newDIB(4, 2, 8)
		r.compression = 1
		r.colorsUsed = 3
		r.palette = dibPalette(3, 4, 8)
		r.pixels = []byte{4, 1, 0, 0, 0, 3, 0, 1, 2, 0, 1}
		rl := icoSingle(4, 2, 8, r.bytes())
		for pos := range len(rl) {
			for _, x := range []byte{0xFF, 0x03} {
				m := append([]byte(nil), rl...)
				m[pos] ^= x
				b.add(fmt.Sprintf("flip-rle8-%03d-%02x", pos, x), m)
			}
		}
		bf := newDIB(3, 2, 16)
		bf.headerSize = 56
		bf.compression = 3
		bf.masks = []uint32{0xF800, 0x07E0, 0x001F, 0}
		bf.pixels = icoNoise(9, dibRowBytes(3, 16)*2)
		bf.mask = dibMask(3, 2, 3)
		bfFile := icoSingle(3, 2, 16, bf.bytes())
		for pos := range len(bfFile) {
			m := append([]byte(nil), bfFile...)
			m[pos] ^= 0xFF
			b.add(fmt.Sprintf("flip-bitfields16-%03d", pos), m)
		}
	}

	// Limits: the bitmap's width * (height / 2) * 4 may not exceed 2^29 bytes; the dimensions are at most 65535.
	limit := func(name string, width, height int, lazy bool) {
		d := newDIB(width, 1, 8)
		d.height = int32(2 * height)
		d.palette = dibPalette(256, 4, 1)
		entry := []icoEntry{{planes: 1, bpp: 8}}
		if !lazy {
			d.pixels = icoNoise(1, 64)
			b.add(name, icoFile(1, entry, [][]byte{d.bytes()}))
			return
		}
		b.addLazy(name, func() []byte {
			d.pixels = make([]byte, dibRowBytes(width, 8)*height)
			for i := range d.pixels {
				d.pixels[i] = byte(i >> 3)
			}
			return icoFile(1, entry, [][]byte{d.bytes()})
		})
	}
	limit("limit-8bit-exact-16384x8192", 16384, 8192, true)
	limit("limit-8bit-over-16384x8193", 16384, 8193, false)
	limit("limit-8bit-over-16384x8193-with-data", 16384, 8193, true)
	limit("limit-8bit-65535x2049", 65535, 2049, false)
	limit("limit-8bit-65535x2048", 65535, 2048, false)
	limit("limit-8bit-65535x65535", 65535, 65535, false)
	limit("limit-8bit-8192x16384", 8192, 16384, false)
	limit("limit-8bit-32768x4096", 32768, 4096, false)
	limit("limit-8bit-2048x2048", 2048, 2048, true)
	return b.cases
}

func bits2(bits uint16) int { return int(bits) * 2 }
