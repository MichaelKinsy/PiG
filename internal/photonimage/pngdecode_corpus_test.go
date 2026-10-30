// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"compress/flate"
	"embed"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io/fs"
	"math/bits"
	"math/rand/v2"
	"slices"
	"strings"
)

//go:embed testdata/png-real/pngsuite/*.png testdata/png-real/image-rs/*.png testdata/png-real/go/*.png testdata/scene-photon-300x200.png
var pngRealFiles embed.FS

// pngCorpus is the files whose decoding the test compares with Photon: every colour type and bit depth with and without Adam7 and with every
// filter, the transparency chunk of each colour type in every shape, the chunk order and chunk contents the png crate checks, animated PNGs,
// zlib and deflate streams of every kind (the ones the standard library writes, hand-written blocks that fdeflate treats in its own way, and
// seeded damage to them), every prefix of small files, a flipped byte at every position of small files, seeded damage to larger ones, the size
// and memory limits, and the real files of PngSuite, image-rs, the Go repository and the Photon scene.
func pngCorpus() []formatCase {
	var b formatCorpusBuilder
	pngStreamCorpus(&b, func(png []byte) []byte { return png })
	pngMatrixCorpus(&b)
	pngTransparencyCorpus(&b)
	pngDeflateCorpus(&b)
	pngDamageCorpus(&b)
	pngLimitCorpus(&b)
	pngRealCorpus(&b)
	return b.cases
}

// pngMatrixCorpus is every colour type with every valid bit depth, interlaced or not, in sizes below and above a pass, with every filter
// type first in the cycle. Palette images get a palette of every entry the depth can address, and one that is shorter.
func pngMatrixCorpus(b *formatCorpusBuilder) {
	idat := func(z []byte) [2]any { return [2]any{"IDAT", z} }
	iend := [2]any{"IEND", []byte{}}
	combos := []struct {
		color  byte
		depths []byte
	}{{0, []byte{1, 2, 4, 8, 16}}, {2, []byte{8, 16}}, {3, []byte{1, 2, 4, 8}}, {4, []byte{8, 16}}, {6, []byte{8, 16}}}
	for _, combo := range combos {
		for _, depth := range combo.depths {
			bits := int(depth) * pngChannels(combo.color)
			for _, interlace := range []byte{0, 1} {
				for _, size := range [][2]int{{1, 1}, {3, 2}, {8, 8}, {9, 7}, {17, 5}} {
					for first := range 5 {
						w, h := size[0], size[1]
						seed := uint32(w*131 + h*17 + int(depth)*7 + int(combo.color) + first)
						var raw []byte
						if interlace == 0 {
							raw = pngRawRows(w, h, bits, seed, first)
						} else {
							raw = pngRawInterlaced(w, h, bits, seed, first)
						}
						chunks := [][2]any{{"IHDR", pngIHDR(uint32(w), uint32(h), depth, combo.color, interlace)}}
						if combo.color == 3 {
							chunks = append(chunks, [2]any{"PLTE", icoNoise(seed+5, 3*(1<<min(depth, 8)))})
						}
						chunks = append(chunks, idat(zlibOf(raw)), iend)
						b.add(fmt.Sprintf("png-matrix-c%d-d%d-adam%d-%dx%d-f%d", combo.color, depth, interlace, w, h, first), pngChunks(chunks...))
					}
				}
			}
		}
	}
	// A palette with fewer entries than the indices address, and one with a tRNS chunk shorter, as long as and longer.
	for _, depth := range []byte{1, 2, 4, 8} {
		for _, entries := range []int{1, 2, 3, 5, 16, 100, 255} {
			if entries > 1<<depth && depth < 8 {
				continue
			}
			w, h := 13, 4
			raw := pngRawRows(w, h, int(depth), uint32(entries), 0)
			b.add(fmt.Sprintf("png-palette-d%d-%d-entries", depth, entries),
				pngChunks([2]any{"IHDR", pngIHDR(uint32(w), uint32(h), depth, 3, 0)}, [2]any{"PLTE", icoNoise(uint32(entries), 3*entries)}, idat(zlibOf(raw)), iend))
			for _, n := range uniqueInts(0, 1, entries-1, entries, entries+1, 256, 300) {
				b.add(fmt.Sprintf("png-palette-d%d-%d-entries-trns-%d", depth, entries, n),
					pngChunks([2]any{"IHDR", pngIHDR(uint32(w), uint32(h), depth, 3, 0)}, [2]any{"PLTE", icoNoise(uint32(entries), 3*entries)},
						[2]any{"tRNS", icoNoise(uint32(n)+9, n)}, idat(zlibOf(raw)), iend))
			}
		}
	}
}

// uniqueInts is the values without repeats, in order.
func uniqueInts(values ...int) []int {
	var out []int
	for _, v := range values {
		if v >= 0 && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// packSamples writes samples of depth bits, most significant first, padded to a byte (16 bits big endian).
func packSamples(samples []int, depth int) []byte {
	var out []byte
	switch depth {
	case 16:
		for _, s := range samples {
			out = append(out, byte(s>>8), byte(s))
		}
	case 8:
		for _, s := range samples {
			out = append(out, byte(s))
		}
	default:
		var acc, n int
		for _, s := range samples {
			acc = acc<<depth | s&(1<<depth-1)
			n += depth
			if n == 8 {
				out = append(out, byte(acc))
				acc, n = 0, 0
			}
		}
		if n > 0 {
			out = append(out, byte(acc<<(8-n)))
		}
	}
	return out
}

// pngRowsOf is the image data of rows of samples, each filtered with type 0.
func pngRowsOf(rows [][]int, depth int) []byte {
	var raw []byte
	for _, r := range rows {
		raw = append(raw, 0)
		raw = append(raw, packSamples(r, depth)...)
	}
	return raw
}

// pngTransparencyCorpus is the tRNS chunk of grayscale and RGB images with pixels that do and do not match its colour, in every length and with
// garbage in the bytes that are ignored, before and after the data, and on colour types that may not have one.
func pngTransparencyCorpus(b *formatCorpusBuilder) {
	idat := func(z []byte) [2]any { return [2]any{"IDAT", z} }
	iend := [2]any{"IEND", []byte{}}
	for _, depth := range []int{1, 2, 4, 8, 16} {
		maxv := 1<<depth - 1
		var row []int
		for x := range 12 {
			row = append(row, (x*5+1)&maxv)
		}
		rows := [][]int{row, row[3:], append([]int{maxv, 0}, row...)}
		w := len(rows[2])
		// Rows of different lengths are not allowed: pad to the width.
		for i := range rows {
			for len(rows[i]) < w {
				rows[i] = append(rows[i], 0)
			}
		}
		raw := pngRowsOf(rows, depth)
		for _, key := range uniqueInts(0, 1, 6, maxv, maxv-1) {
			for _, shape := range []string{"2", "2-garbage-high", "3", "4", "8", "1", "0"} {
				var trns []byte
				switch shape {
				case "2":
					trns = []byte{byte(key >> 8), byte(key)}
				case "2-garbage-high":
					trns = []byte{0xAB, byte(key)}
				case "3":
					trns = []byte{byte(key >> 8), byte(key), 7}
				case "4":
					trns = []byte{byte(key >> 8), byte(key), 0, 0}
				case "8":
					trns = []byte{byte(key >> 8), byte(key), 1, 2, 3, 4, 5, 6}
				case "1":
					trns = []byte{byte(key)}
				case "0":
					trns = []byte{}
				}
				for _, interlace := range []byte{0, 1} {
					data := raw
					if interlace == 1 {
						data = pngRawInterlaced(w, 3, depth, uint32(depth+key), 0)
					}
					name := fmt.Sprintf("png-trns-gray-d%d-key%d-len%s-adam%d", depth, key, shape, interlace)
					b.add(name, pngChunks([2]any{"IHDR", pngIHDR(uint32(w), 3, byte(depth), 0, interlace)}, [2]any{"tRNS", trns}, idat(zlibOf(data)), iend))
				}
			}
		}
		b.add(fmt.Sprintf("png-trns-gray-d%d-after-idat", depth), pngChunks([2]any{"IHDR", pngIHDR(uint32(w), 3, byte(depth), 0, 0)}, idat(zlibOf(raw)), [2]any{"tRNS", []byte{0, 1}}, iend))
		b.add(fmt.Sprintf("png-trns-gray-d%d-twice", depth), pngChunks([2]any{"IHDR", pngIHDR(uint32(w), 3, byte(depth), 0, 0)}, [2]any{"tRNS", []byte{0, 1}}, [2]any{"tRNS", []byte{0, 2}}, idat(zlibOf(raw)), iend))
	}
	for _, depth := range []int{8, 16} {
		maxv := 1<<depth - 1
		var row []int
		for x := range 6 {
			row = append(row, x*3+1, x*5+2, x*7+3)
		}
		row = append(row, 0, 0, 0, maxv, maxv, maxv, 1, 2, 3)
		raw := pngRowsOf([][]int{row, row}, depth)
		w := len(row) / 3
		for _, key := range [][3]int{{0, 0, 0}, {1, 2, 3}, {maxv, maxv, maxv}, {4, 7, 10}, {0x0101, 0x0202, 0x0303}} {
			for _, shape := range []string{"6", "6-garbage-high", "7", "12", "5", "3", "0", "9-high-only"} {
				var trns []byte
				switch shape {
				case "6":
					trns = packSamples(key[:], 16)
				case "6-garbage-high":
					trns = []byte{0xCC, byte(key[0]), 0xDD, byte(key[1]), 0xEE, byte(key[2])}
				case "7":
					trns = append(packSamples(key[:], 16), 9)
				case "12":
					trns = append(packSamples(key[:], 16), 1, 2, 3, 4, 5, 6)
				case "5":
					trns = packSamples(key[:], 16)[:5]
				case "3":
					trns = packSamples(key[:], 16)[:3]
				case "0":
					trns = []byte{}
				case "9-high-only":
					trns = []byte{byte(key[0] >> 8), 0, byte(key[1] >> 8), 0, byte(key[2] >> 8), 0, 0, 0, 0}
				}
				for _, interlace := range []byte{0, 1} {
					data := raw
					if interlace == 1 {
						data = pngRawInterlaced(w, 2, depth*3, uint32(depth+key[0]), 1)
					}
					name := fmt.Sprintf("png-trns-rgb-d%d-key%d-%d-%d-len%s-adam%d", depth, key[0], key[1], key[2], shape, interlace)
					b.add(name, pngChunks([2]any{"IHDR", pngIHDR(uint32(w), 2, byte(depth), 2, interlace)}, [2]any{"tRNS", trns}, idat(zlibOf(data)), iend))
				}
			}
		}
	}
	// A tRNS chunk where the colour type has alpha, or before the palette that it needs, or on a palette image after the data.
	for _, c := range []struct {
		color byte
		bits  int
	}{{4, 16}, {6, 32}} {
		for _, depth := range []byte{8, 16} {
			raw := pngRawRows(3, 3, c.bits*int(depth)/8, 4, 0)
			b.add(fmt.Sprintf("png-trns-on-color%d-d%d", c.color, depth),
				pngChunks([2]any{"IHDR", pngIHDR(3, 3, depth, c.color, 0)}, [2]any{"tRNS", []byte{0, 1, 0, 1, 0, 1, 0, 1}}, idat(zlibOf(raw)), iend))
		}
	}
	pal := icoNoise(2, 12)
	raw := pngRawRows(4, 4, 8, 4, 0)
	for i := range raw {
		raw[i] &= 3
	}
	b.add("png-trns-palette-before-plte", pngChunks([2]any{"IHDR", pngIHDR(4, 4, 8, 3, 0)}, [2]any{"tRNS", []byte{1, 2}}, [2]any{"PLTE", pal}, idat(zlibOf(raw)), iend))
	b.add("png-trns-palette-after-idat", pngChunks([2]any{"IHDR", pngIHDR(4, 4, 8, 3, 0)}, [2]any{"PLTE", pal}, idat(zlibOf(raw)), [2]any{"tRNS", []byte{1, 2}}, iend))
	b.add("png-trns-palette-between-idats", func() []byte {
		z := zlibOf(raw)
		return pngChunks([2]any{"IHDR", pngIHDR(4, 4, 8, 3, 0)}, [2]any{"PLTE", pal}, idat(z[:6]), [2]any{"tRNS", []byte{1, 2}}, idat(z[6:]), iend)
	}())
	b.add("png-palette-missing", pngChunks([2]any{"IHDR", pngIHDR(4, 4, 8, 3, 0)}, idat(zlibOf(raw)), iend))
	b.add("png-palette-missing-with-trns", pngChunks([2]any{"IHDR", pngIHDR(4, 4, 8, 3, 0)}, [2]any{"tRNS", []byte{1, 2}}, idat(zlibOf(raw)), iend))
	b.add("png-palette-after-idat", pngChunks([2]any{"IHDR", pngIHDR(4, 4, 8, 3, 0)}, idat(zlibOf(raw)), [2]any{"PLTE", pal}, iend))
}

// bitWriter writes the bits of a deflate stream, the least significant first.
type bitWriter struct {
	out  []byte
	acc  uint64
	have int
}

func (w *bitWriter) bits(v uint32, n int) {
	w.acc |= uint64(v) << w.have
	w.have += n
	for w.have >= 8 {
		w.out = append(w.out, byte(w.acc))
		w.acc >>= 8
		w.have -= 8
	}
}

// code writes a Huffman code of n bits, its first bit first.
func (w *bitWriter) code(c uint32, n int) {
	var r uint32
	for i := range n {
		r |= (c >> i & 1) << (n - 1 - i)
	}
	w.bits(r, n)
}

func (w *bitWriter) finish() []byte {
	if w.have > 0 {
		w.out = append(w.out, byte(w.acc))
		w.acc, w.have = 0, 0
	}
	return w.out
}

// canonicalCodes is the codes of a deflate Huffman code with the given lengths.
func canonicalCodes(lengths []int) []uint32 {
	var count [16]int
	for _, l := range lengths {
		count[l]++
	}
	count[0] = 0
	var next [16]uint32
	code := uint32(0)
	for l := 1; l < 16; l++ {
		code = (code + uint32(count[l-1])) << 1
		next[l] = code
	}
	codes := make([]uint32, len(lengths))
	for i, l := range lengths {
		if l != 0 {
			codes[i] = next[l]
			next[l]++
		}
	}
	return codes
}

// deflateItem is a symbol of a hand-written block: a literal, a match, the end of the block, or any symbol by number.
type deflateItem struct {
	sym         int
	extra       uint32
	extraBits   int
	dist        int
	distExtra   uint32
	distExtraBs int
}

var lengthBases = [29]int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
var lengthExtraBits = [29]int{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
var distBases = [30]int{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
var distExtraBitsTable = [30]int{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}

func dfLit(b byte) deflateItem { return deflateItem{sym: int(b), dist: -1} }
func dfEnd() deflateItem       { return deflateItem{sym: 256, dist: -1} }

// dfMatch is a match of length (3 to 258) at distance (1 to 32768).
func dfMatch(length, dist int) deflateItem {
	ls := 28
	for i := 28; i >= 0; i-- {
		if lengthBases[i] <= length {
			ls = i
			break
		}
	}
	ds := 29
	for i := 29; i >= 0; i-- {
		if distBases[i] <= dist {
			ds = i
			break
		}
	}
	return deflateItem{sym: 257 + ls, extra: uint32(length - lengthBases[ls]), extraBits: lengthExtraBits[ls], dist: ds,
		distExtra: uint32(dist - distBases[ds]), distExtraBs: distExtraBitsTable[ds]}
}

// dfSymbol is a literal/length symbol by number with no distance (the end-of-block symbols 286 and 287 of a fixed block, say).
func dfSymbol(sym int) deflateItem { return deflateItem{sym: sym, dist: -1} }

func fixedLengths() []int {
	l := make([]int, 288)
	for i := range l {
		switch {
		case i < 144:
			l[i] = 8
		case i < 256:
			l[i] = 9
		case i < 280:
			l[i] = 7
		default:
			l[i] = 8
		}
	}
	return l
}

func (w *bitWriter) items(items []deflateItem, litCodes []uint32, litLens []int, distCodes []uint32, distLens []int) {
	for _, it := range items {
		w.code(litCodes[it.sym], litLens[it.sym])
		if it.sym > 256 && it.sym < 286 {
			w.bits(it.extra, it.extraBits)
		}
		if it.dist >= 0 {
			w.code(distCodes[it.dist], distLens[it.dist])
			w.bits(it.distExtra, it.distExtraBs)
		}
	}
}

// dfFixed is a block of the fixed code.
func dfFixed(w *bitWriter, final bool, items ...deflateItem) {
	f := uint32(0)
	if final {
		f = 1
	}
	w.bits(f, 1)
	w.bits(1, 2)
	lens := fixedLengths()
	dl := make([]int, 32)
	for i := range dl {
		dl[i] = 5
	}
	w.items(items, canonicalCodes(lens), lens, canonicalCodes(dl), dl)
}

// dfStored is a stored block.
func dfStored(w *bitWriter, final bool, data []byte) {
	f := uint32(0)
	if final {
		f = 1
	}
	w.bits(f, 1)
	w.bits(0, 2)
	w.finish()
	w.out = append(w.out, byte(len(data)), byte(len(data)>>8), ^byte(len(data)), ^byte(len(data)>>8))
	w.out = append(w.out, data...)
}

// dfDynamic is a block with the given code lengths of the literal/length symbols (257 to 286 of them) and the distance symbols (1 to 30);
// hclenLens are the lengths of the code length code, which the caller keeps valid unless the case is about them (nil for a default one).
func dfDynamic(w *bitWriter, final bool, litLens, distLens, clLens []int, items ...deflateItem) {
	f := uint32(0)
	if final {
		f = 1
	}
	w.bits(f, 1)
	w.bits(2, 2)
	w.bits(uint32(len(litLens)-257), 5)
	w.bits(uint32(len(distLens)-1), 5)
	if clLens == nil {
		// 13 symbols of 4 bits and 6 of 5 bits fill the code space.
		clLens = make([]int, 19)
		for i := range clLens {
			clLens[i] = 4
			if i == 16 || i == 17 || i == 18 || i == 0 || i == 1 || i == 2 {
				clLens[i] = 5
			}
		}
	}
	order := []int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
	hclen := 19
	for hclen > 4 && clLens[order[hclen-1]] == 0 {
		hclen--
	}
	w.bits(uint32(hclen-4), 4)
	for i := range hclen {
		w.bits(uint32(clLens[order[i]]), 3)
	}
	clCodes := canonicalCodes(clLens)
	all := append(slices.Clone(litLens), distLens...)
	for _, l := range all {
		w.code(clCodes[l], clLens[l])
	}
	w.items(items, canonicalCodes(litLens), litLens, canonicalCodes(distLens), distLens)
}

// goDeflate is the standard library's deflate of raw at level, with a flush after every `flushEvery` bytes (0: none).
func goDeflate(raw []byte, level, flushEvery int) []byte {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, level)
	if err != nil {
		panic(err)
	}
	if flushEvery == 0 {
		_, _ = w.Write(raw)
	} else {
		for i := 0; i < len(raw); i += flushEvery {
			_, _ = w.Write(raw[i:min(i+flushEvery, len(raw))])
			_ = w.Flush()
		}
	}
	_ = w.Close()
	return buf.Bytes()
}

// pngGradientRows is rows with structure (gradients and repeats) that compress with long matches, for a w x h image of bits per pixel.
func pngGradientRows(w, h, bits int) []byte {
	rowBytes := (w*bits + 7) / 8
	bpp := max(bits/8, 1)
	var raw []byte
	prev := make([]byte, rowBytes)
	for y := range h {
		row := make([]byte, rowBytes)
		for i := range row {
			row[i] = byte((i/bpp)*3 + y*5 + i%bpp*40)
			if (i/bpp/16+y/8)%3 == 0 {
				row[i] = 200
			}
		}
		filter := byte((y / 3) % 5)
		raw = append(raw, filter)
		raw = append(raw, pngFilterRow(filter, bpp, prev, row)...)
		prev = row
	}
	return raw
}

// pngDeflateCorpus is the zlib stream of PNGs in every form: Go's deflate at every level and with flushes, long streams (past the 32 KiB
// window and the 128 KiB at which the crate compacts its buffer) split into IDAT chunks of sizes around the 32 KiB of its buffered reader,
// hand-written blocks (fixed, stored, dynamic with trees of every shape), and seeded damage to the streams.
func pngDeflateCorpus(b *formatCorpusBuilder) {
	idat := func(z []byte) [2]any { return [2]any{"IDAT", z} }
	iend := [2]any{"IEND", []byte{}}
	gray := func(w, h int) [2]any { return [2]any{"IHDR", pngIHDR(uint32(w), uint32(h), 8, 0, 0)} }

	// Go's deflate.
	small := pngRawRows(8, 8, 8, 12, 0)
	for _, level := range []int{flate.HuffmanOnly, flate.NoCompression, flate.BestSpeed, 2, 3, 4, flate.DefaultCompression, 7, flate.BestCompression} {
		for _, flush := range []int{0, 1, 7, 20} {
			b.add(fmt.Sprintf("png-deflate-small-level%d-flush%d", level, flush), pngChunks(gray(8, 8), idat(zlibWrap(goDeflate(small, level, flush), small)), iend))
		}
	}
	type bigImage struct {
		name string
		hdr  [2]any
		raw  []byte
	}
	big := []bigImage{
		{"gray8-gradient-300x400", gray(300, 400), pngGradientRows(300, 400, 8)},
		{"gray8-noise-200x300", gray(200, 300), pngRawRows(200, 300, 8, 3, 0)},
		{"rgb8-gradient-257x129", [2]any{"IHDR", pngIHDR(257, 129, 8, 2, 0)}, pngGradientRows(257, 129, 24)},
		{"rgba8-gradient-adam7-130x97", [2]any{"IHDR", pngIHDR(130, 97, 8, 6, 1)}, pngRawInterlacedGradient(130, 97, 32)},
		{"rgba16-noise-100x80", [2]any{"IHDR", pngIHDR(100, 80, 16, 6, 0)}, pngRawRows(100, 80, 64, 11, 2)},
		{"gray1-gradient-1000x500", [2]any{"IHDR", pngIHDR(1000, 500, 1, 0, 0)}, pngGradientRows(1000, 500, 1)},
	}
	for _, img := range big {
		for _, level := range []int{flate.HuffmanOnly, flate.NoCompression, flate.BestSpeed, flate.DefaultCompression, flate.BestCompression} {
			z := zlibWrap(goDeflate(img.raw, level, 0), img.raw)
			b.add(fmt.Sprintf("png-deflate-%s-level%d", img.name, level), pngChunks(img.hdr, idat(z), iend))
		}
		z := zlibWrap(goDeflate(img.raw, flate.DefaultCompression, 0), img.raw)
		for _, split := range []int{1000, 4096, 32767, 32768, 32769, 65536} {
			var chunks [][2]any
			chunks = append(chunks, img.hdr)
			for i := 0; i < len(z); i += split {
				chunks = append(chunks, idat(z[i:min(i+split, len(z))]))
			}
			chunks = append(chunks, iend)
			b.add(fmt.Sprintf("png-deflate-%s-split%d", img.name, split), pngChunks(chunks...))
		}
		// An image with text chunks of lengths around the window of the buffered reader before the data.
		for _, pad := range []int{32760, 32768, 32769, 40000} {
			b.add(fmt.Sprintf("png-deflate-%s-pad%d", img.name, pad), pngChunks(img.hdr, [2]any{"tEXt", append([]byte("k\x00"), bytes.Repeat([]byte("v"), pad)...)}, idat(z), iend))
		}
		// The data of the stream cut off before the end, and more data than the image has.
		b.add(fmt.Sprintf("png-deflate-%s-cut-end", img.name), pngChunks(img.hdr, idat(z[:len(z)-6]), iend))
		more := append(slices.Clone(img.raw), icoNoise(5, 70000)...)
		b.add(fmt.Sprintf("png-deflate-%s-more-data", img.name), pngChunks(img.hdr, idat(zlibWrap(goDeflate(more, flate.DefaultCompression, 0), more)), iend))
		b.add(fmt.Sprintf("png-deflate-%s-trailing-after-stream", img.name), pngChunks(img.hdr, idat(append(slices.Clone(z), icoNoise(6, 9)...)), iend))
	}

	// Hand-written blocks over an 8x4 gray image: rows of 9 bytes (filter 0 and eight samples).
	rows := make([]byte, 0, 36)
	for y := range 4 {
		rows = append(rows, 0)
		for x := range 8 {
			rows = append(rows, byte(x*30+y))
		}
	}
	lits := func(data []byte) []deflateItem {
		var out []deflateItem
		for _, v := range data {
			out = append(out, dfLit(v))
		}
		return out
	}
	hand := func(name string, deflate []byte, raw []byte) {
		b.add("png-hand-"+name, pngChunks(gray(8, 4), idat(zlibWrap(deflate, raw)), iend))
	}
	{
		var w bitWriter
		dfFixed(&w, true, append(lits(rows), dfEnd())...)
		hand("fixed-literals", w.finish(), rows)
	}
	{
		var w bitWriter
		dfFixed(&w, true, append(append(lits(rows[:9]), dfMatch(9, 9), dfMatch(18, 9)), dfEnd())...)
		hand("fixed-matches", w.finish(), rows)
	}
	for _, dist := range []int{1, 2, 3, 4, 8, 9, 10, 17} {
		var w bitWriter
		items := append(lits(rows[:min(dist, 9)]), dfMatch(min(258, 36-min(dist, 9)), dist))
		dfFixed(&w, true, append(items, dfEnd())...)
		hand(fmt.Sprintf("fixed-dist-%d", dist), w.finish(), rows)
	}
	for _, sym := range []int{286, 287} {
		var w bitWriter
		dfFixed(&w, true, append(lits(rows), dfSymbol(sym))...)
		hand(fmt.Sprintf("fixed-symbol-%d-for-end", sym), w.finish(), rows)
		var w2 bitWriter
		dfFixed(&w2, false, append(lits(rows[:20]), dfSymbol(sym))...)
		dfFixed(&w2, true, append(lits(rows[20:]), dfEnd())...)
		hand(fmt.Sprintf("fixed-symbol-%d-mid", sym), w2.finish(), rows)
	}
	{
		var w bitWriter
		dfFixed(&w, true, append(lits(rows[:5]), dfMatch(10, 6))...)
		hand("fixed-dist-too-far", w.finish(), rows)
		var w2 bitWriter
		dfFixed(&w2, true, dfMatch(5, 1))
		hand("fixed-match-first", w2.finish(), rows)
		var w3 bitWriter
		dfFixed(&w3, true, append(lits(rows), deflateItem{sym: 257, dist: 30})...)
		hand("fixed-dist-symbol-30", w3.finish(), rows)
		var w4 bitWriter
		dfFixed(&w4, true, append(lits(rows), deflateItem{sym: 257, dist: 31})...)
		hand("fixed-dist-symbol-31", w4.finish(), rows)
	}
	{
		var w bitWriter
		dfStored(&w, false, rows[:10])
		dfFixed(&w, false, lits(rows[10:20])...)
		dfStored(&w, false, nil)
		dfFixed(&w, true, append(lits(rows[20:]), dfEnd())...)
		hand("mixed-blocks-fixed-without-end", w.finish(), rows)
		var w2 bitWriter
		dfStored(&w2, false, rows[:10])
		dfFixed(&w2, false, append(lits(rows[10:20]), dfEnd())...)
		dfStored(&w2, false, nil)
		dfFixed(&w2, true, append(lits(rows[20:]), dfEnd())...)
		hand("mixed-blocks", w2.finish(), rows)
		var w3 bitWriter
		dfStored(&w3, true, rows)
		hand("stored-final", w3.finish(), rows)
		var w4 bitWriter
		dfStored(&w4, false, rows)
		dfStored(&w4, true, nil)
		hand("stored-then-empty-final", w4.finish(), rows)
		var w5 bitWriter
		dfStored(&w5, false, rows[:30])
		hand("stored-not-final-ends", w5.finish(), rows)
		var w6 bitWriter
		dfFixed(&w6, false, append(lits(rows), dfEnd())...)
		hand("fixed-not-final-ends", w6.finish(), rows)
	}

	// Dynamic blocks: trees of every shape.
	balanced := balancedLengths
	symbolItems := func(data []byte) []deflateItem { return append(lits(data), dfEnd()) }
	// withMatch is the 36 bytes as the first dist literals and a match at distance dist for the rest.
	withMatch := func(dist int) []deflateItem {
		return append(append(lits(rows[:dist]), dfMatch(36-dist, dist)), dfEnd())
	}
	only := func(used ...int) []int {
		l := make([]int, 286)
		for i := 0; i+1 < len(used); i += 2 {
			l[used[i]] = used[i+1]
		}
		return l
	}
	withEnd := func(l []int, length int) []int { l[256] = length; return l }
	trees := []struct {
		name      string
		lit, dist []int
		items     []deflateItem
	}{
		{"balanced-286-dist-1x1", balanced(286), []int{1}, symbolItems(rows)},
		{"balanced-257-dist-1x1", balanced(257), []int{1}, symbolItems(rows)},
		{"balanced-286-dist-balanced-30", balanced(286), balanced(30), withMatch(9)},
		{"balanced-286-dist-balanced-2", balanced(286), balanced(2), withMatch(2)},
		{"balanced-286-dist-1x1-match", balanced(286), []int{1}, withMatch(1)},
		{"balanced-286-dist-zero-no-matches", balanced(286), []int{0}, symbolItems(rows)},
		{"balanced-286-dist-zero-30-no-matches", balanced(286), make([]int, 30), symbolItems(rows)},
		{"balanced-286-dist-zero-then-match", balanced(286), make([]int, 30), withMatch(9)},
		{"balanced-286-dist-one-code-length-2", balanced(286), []int{2}, withMatch(1)},
		{"balanced-286-dist-two-codes-length-1-1", balanced(286), []int{1, 1}, withMatch(2)},
		{"balanced-286-dist-incomplete-three-codes", balanced(286), []int{2, 2, 2}, withMatch(2)},
		{"balanced-286-dist-oversubscribed", balanced(286), []int{1, 1, 1}, withMatch(2)},
		{"balanced-286-dist-single-code-symbol-3", balanced(286), []int{0, 0, 0, 1}, withMatch(4)},
		{"balanced-286-dist-single-code-symbol-29", balanced(286), append(make([]int, 29), 1), withMatch(9)},
		{"balanced-286-no-eob", withEnd(balanced(286), 0), []int{1}, symbolItems(rows)},
		{"incomplete-literals", only(0, 1, 256, 2, 7, 3), []int{1}, symbolItems(rows)},
		{"oversubscribed-literals", only(0, 1, 256, 1, 7, 1, 9, 2), []int{1}, symbolItems(rows)},
		{"single-literal-code", only(256, 1), []int{1}, []deflateItem{dfEnd()}},
		{"two-literal-codes", only(65, 1, 256, 1), []int{1}, []deflateItem{dfLit(65), dfEnd()}},
		{"literals-with-15-bit-codes", deepTree(286, 15), []int{1}, symbolItems(rows)},
		{"literals-with-14-bit-codes", deepTree(286, 14), []int{1}, symbolItems(rows)},
		{"literals-with-13-bit-codes", deepTree(286, 13), []int{1}, symbolItems(rows)},
		{"literals-with-12-bit-codes", deepTree(286, 12), []int{1}, symbolItems(rows)},
		{"literals-with-11-bit-codes", deepTree(286, 11), []int{1}, symbolItems(rows)},
		{"literals-14-bit-dist-11-bit", deepTree(286, 14), deepTree(30, 11), withMatch(33)},
		{"dist-10-bit-codes", balanced(286), deepTree(30, 10), withMatch(33)},
		{"dist-9-bit-codes", balanced(286), deepTree(30, 9), withMatch(33)},
		{"dist-15-bit-codes", balanced(286), deepTree(30, 15), withMatch(33)},
	}
	for _, t := range trees {
		var w bitWriter
		dfDynamic(&w, true, t.lit, t.dist, nil, t.items...)
		hand("dynamic-"+t.name, w.finish(), rows)
	}
	// Dynamic headers: hlit and hdist out of range, repeat codes, a code length code that is not valid.
	for _, hlit := range []int{257, 258, 285, 286} {
		var w bitWriter
		dfDynamic(&w, true, balanced(hlit), []int{1}, nil, symbolItems(rows)...)
		hand(fmt.Sprintf("dynamic-hlit-%d", hlit), w.finish(), rows)
	}
	for _, hdist := range []int{1, 2, 29, 30} {
		var w bitWriter
		d := balanced(hdist)
		dfDynamic(&w, true, balanced(286), d, nil, symbolItems(rows)...)
		hand(fmt.Sprintf("dynamic-hdist-%d", hdist), w.finish(), rows)
	}
	for _, raw := range []struct {
		name       string
		hlit, hdst uint32
	}{{"hlit-287", 30, 0}, {"hlit-288", 31, 0}, {"hdist-31", 0, 30}, {"hdist-32", 0, 31}} {
		var w bitWriter
		w.bits(1, 1)
		w.bits(2, 2)
		w.bits(raw.hlit, 5)
		w.bits(raw.hdst, 5)
		w.bits(15, 4)
		for range 19 {
			w.bits(4, 3)
		}
		hand("dynamic-"+raw.name, w.finish(), rows)
	}
	for _, cl := range []struct {
		name string
		lens []int
	}{
		{"cl-all-zero", make([]int, 19)},
		{"cl-single", func() []int { l := make([]int, 19); l[0] = 1; return l }()},
		{"cl-incomplete", func() []int { l := make([]int, 19); l[0], l[1], l[2] = 2, 2, 2; return l }()},
		{"cl-oversubscribed", func() []int { l := make([]int, 19); l[0], l[1], l[2], l[3] = 1, 1, 1, 2; return l }()},
		{"cl-two-codes", func() []int { l := make([]int, 19); l[8], l[9] = 1, 1; return l }()},
	} {
		var w bitWriter
		w.bits(1, 1)
		w.bits(2, 2)
		w.bits(0, 5)
		w.bits(0, 5)
		w.bits(15, 4)
		order := []int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
		for _, o := range order {
			w.bits(uint32(cl.lens[o]), 3)
		}
		w.bits(0, 32)
		hand("dynamic-"+cl.name, w.finish(), rows)
	}
	// Repeat codes: 16 first (nothing to repeat), 17 and 18 for zeros, a repeat that runs past the lengths.
	for _, rep := range []struct {
		name string
		seq  []int // code length alphabet symbols with their extra bits as pairs: symbol, extra
	}{
		{"repeat-16-first", []int{16, 0}},
		{"repeat-17-first", []int{17, 0}},
		{"repeat-18-first", []int{18, 0}},
		{"repeat-past-end", []int{18, 127, 18, 127, 18, 127}},
		{"repeat-exactly-to-end", []int{18, 127, 18, 109}},
	} {
		var w bitWriter
		w.bits(1, 1)
		w.bits(2, 2)
		w.bits(0, 5)
		w.bits(0, 5)
		w.bits(15, 4)
		clLens := make([]int, 19)
		for i := range clLens {
			clLens[i] = 4
			if i == 16 || i == 17 || i == 18 || i == 0 || i == 1 || i == 2 {
				clLens[i] = 5
			}
		}
		order := []int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
		for _, o := range order {
			w.bits(uint32(clLens[o]), 3)
		}
		codes := canonicalCodes(clLens)
		for i := 0; i+1 < len(rep.seq); i += 2 {
			w.code(codes[rep.seq[i]], clLens[rep.seq[i]])
			switch rep.seq[i] {
			case 16:
				w.bits(uint32(rep.seq[i+1]), 2)
			case 17:
				w.bits(uint32(rep.seq[i+1]), 3)
			case 18:
				w.bits(uint32(rep.seq[i+1]), 7)
			}
		}
		w.bits(0, 40)
		hand("dynamic-"+rep.name, w.finish(), rows)
	}
	// The block type 3, a stored block with a wrong complement, a stream that stops inside each part.
	{
		var w bitWriter
		w.bits(1, 1)
		w.bits(3, 2)
		w.bits(0, 40)
		hand("block-type-3", w.finish(), rows)
		var w2 bitWriter
		dfStored(&w2, true, rows)
		bad := w2.finish()
		bad[3] ^= 1
		hand("stored-bad-nlen", bad, rows)
		var w3 bitWriter
		dfFixed(&w3, true, append(lits(rows), dfEnd())...)
		whole := w3.finish()
		for cut := 0; cut < len(whole); cut += 3 {
			hand(fmt.Sprintf("fixed-cut-%d", cut), whole[:cut], rows)
		}
	}

	// Seeded damage to the deflate data of valid streams, with a valid chunk checksum so that the stream is what the decoder reads.
	rng := rand.New(rand.NewPCG(1, 2))
	damageBases := []struct {
		name string
		hdr  [2]any
		raw  []byte
	}{
		{"gray", gray(30, 30), pngRawRows(30, 30, 8, 5, 0)},
		{"gradient", gray(60, 40), pngGradientRows(60, 40, 8)},
		{"rgb16", [2]any{"IHDR", pngIHDR(12, 12, 16, 2, 0)}, pngRawRows(12, 12, 48, 6, 1)},
	}
	for i := range 2400 {
		base := damageBases[i%len(damageBases)]
		level := []int{flate.HuffmanOnly, flate.BestSpeed, flate.DefaultCompression, flate.BestCompression}[i/3%4]
		z := slices.Clone(zlibWrap(goDeflate(base.raw, level, 0), base.raw))
		for range 1 + rng.IntN(3) {
			pos := 2 + rng.IntN(len(z)-2)
			switch rng.IntN(3) {
			case 0:
				z[pos] ^= 1 << rng.IntN(8)
			case 1:
				z[pos] = byte(rng.IntN(256))
			default:
				z = slices.Delete(z, pos, pos+1)
			}
			if len(z) < 8 {
				break
			}
		}
		b.add(fmt.Sprintf("png-zdamage-%s-%04d", base.name, i), pngChunks(base.hdr, idat(z), iend))
	}
}

// balancedLengths is the code lengths of a complete code over n symbols, nearly flat.
func balancedLengths(n int) []int {
	l := make([]int, n)
	depth := 1
	for 1<<depth < n {
		depth++
	}
	// 2^depth leaves, n symbols: 2^depth - n of them at depth-1, the rest at depth.
	shallow := 1<<depth - n
	for i := range l {
		l[i] = depth
		if i < shallow {
			l[i] = depth - 1
		}
	}
	if n == 1 {
		l[0] = 1
	}
	return l
}

// deepTree is the code lengths of a complete code over n symbols whose longest code has depth bits: a chain of k symbols of 1 to k bits and the
// rest flat below it, so that some codes are longer than the 12 bits of the decoder's table.
func deepTree(n, depth int) []int {
	ceilLog2 := func(v int) int { return bits.Len(uint(v - 1)) }
	k := 1
	for k+ceilLog2(n-k) < depth {
		k++
	}
	l := make([]int, n)
	for i := range k {
		l[i] = i + 1
	}
	for i, v := range balancedLengths(n - k) {
		l[k+i] = k + v
	}
	return l
}

// pngRawInterlacedGradient is pngRawInterlaced with structured rows.
func pngRawInterlacedGradient(w, h, bits int) []byte {
	var raw []byte
	for _, pass := range testAdam7(uint32(w), uint32(h)) {
		raw = append(raw, pngGradientRows(int(pass[4]), int(pass[5]), bits)...)
	}
	return raw
}

// pngDamageCorpus is small files cut at every length, with a byte flipped at every position, with every checksum wrong, and larger ones with
// seeded damage anywhere.
func pngDamageCorpus(b *formatCorpusBuilder) {
	iend := [2]any{"IEND", []byte{}}
	idat := func(z []byte) [2]any { return [2]any{"IDAT", z} }
	text := [2]any{"tEXt", []byte("Comment\x00hello")}
	bases := map[string][]byte{
		"gray8": pngChunks([2]any{"IHDR", pngIHDR(5, 3, 8, 0, 0)}, text, idat(zlibOf(pngRawRows(5, 3, 8, 1, 0))), iend),
		"rgba8": pngChunks([2]any{"IHDR", pngIHDR(3, 3, 8, 6, 0)}, [2]any{"gAMA", []byte{0, 1, 0x86, 0xA0}}, idat(zlibOf(pngRawRows(3, 3, 32, 2, 1))), iend),
		"rgb16": pngChunks([2]any{"IHDR", pngIHDR(2, 3, 16, 2, 0)}, [2]any{"tRNS", []byte{0, 1, 0, 2, 0, 3}}, idat(zlibOf(pngRawRows(2, 3, 48, 3, 2))), iend),
		"pal4":  pngChunks([2]any{"IHDR", pngIHDR(6, 3, 4, 3, 0)}, [2]any{"PLTE", icoNoise(4, 12)}, [2]any{"tRNS", []byte{9, 8}}, idat(zlibOf(pngRawRows(6, 3, 4, 4, 3))), iend),
		"adam7": pngChunks([2]any{"IHDR", pngIHDR(5, 5, 8, 4, 1)}, idat(zlibOf(pngRawInterlaced(5, 5, 16, 5, 0))), iend),
		"gray1": pngChunks([2]any{"IHDR", pngIHDR(9, 2, 1, 0, 0)}, [2]any{"tRNS", []byte{0, 1}}, idat(zlibOf(pngRawRows(9, 2, 1, 6, 0))), iend),
		"apng":  pngChunks([2]any{"IHDR", pngIHDR(3, 3, 8, 2, 0)}, [2]any{"acTL", []byte{0, 0, 0, 1, 0, 0, 0, 0}}, [2]any{"fcTL", pngFCTLData(0, 3, 3)}, idat(zlibOf(pngRawRows(3, 3, 24, 7, 0))), iend),
		"2-idats": func() []byte {
			z := zlibOf(pngRawRows(4, 3, 8, 8, 0))
			return pngChunks([2]any{"IHDR", pngIHDR(4, 3, 8, 0, 0)}, idat(z[:7]), idat(z[7:]), iend)
		}(),
	}
	names := make([]string, 0, len(bases))
	for name := range bases {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		data := bases[name]
		for n := range len(data) + 1 {
			b.add(fmt.Sprintf("png-cut-%s-%03d", name, n), data[:n])
		}
		for pos := range data {
			flipped := slices.Clone(data)
			flipped[pos] ^= 0xFF
			b.add(fmt.Sprintf("png-flip-%s-%03d", name, pos), flipped)
		}
		// Every checksum wrong in turn (a bit of the checksum flipped).
		off := 8
		for off+12 <= len(data) {
			length := int(binary.BigEndian.Uint32(data[off:]))
			crc := off + 8 + length
			bad := slices.Clone(data)
			bad[crc+3] ^= 1
			b.add(fmt.Sprintf("png-badcrc-%s-%s", name, strings.ToLower(string(data[off+4:off+8]))+fmt.Sprint(off)), bad)
			off = crc + 4
		}
	}
	// Seeded damage to larger files: several bytes changed, inserted and removed anywhere, the chunk checksums kept valid or not.
	rng := rand.New(rand.NewPCG(3, 4))
	larger := [][]byte{
		pngChunks([2]any{"IHDR", pngIHDR(40, 30, 8, 2, 0)}, text, idat(zlibOf(pngRawRows(40, 30, 24, 9, 0))), iend),
		pngChunks([2]any{"IHDR", pngIHDR(33, 17, 8, 3, 1)}, [2]any{"PLTE", icoNoise(5, 90)}, [2]any{"tRNS", []byte{1, 2, 3}}, idat(zlibOf(pngRawInterlaced(33, 17, 8, 5, 1))), iend),
		pngChunks([2]any{"IHDR", pngIHDR(20, 20, 16, 6, 0)}, [2]any{"sRGB", []byte{0}}, [2]any{"pHYs", []byte{0, 0, 0, 1, 0, 0, 0, 1, 1}}, idat(zlibOf(pngRawRows(20, 20, 64, 4, 2))), iend),
		pngChunks([2]any{"IHDR", pngIHDR(16, 16, 2, 0, 1)}, [2]any{"tRNS", []byte{0, 2}}, idat(zlibOf(pngRawInterlaced(16, 16, 2, 3, 0))), iend),
	}
	for i := range 2000 {
		data := slices.Clone(larger[i%len(larger)])
		for range 1 + rng.IntN(4) {
			pos := rng.IntN(len(data))
			switch rng.IntN(5) {
			case 0:
				data[pos] ^= 1 << rng.IntN(8)
			case 1:
				data[pos] = byte(rng.IntN(256))
			case 2:
				data = slices.Insert(data, pos, byte(rng.IntN(256)))
			case 3:
				data = slices.Delete(data, pos, min(pos+1+rng.IntN(4), len(data)))
			default:
				data = data[:max(pos, 8)]
			}
		}
		if i%2 == 1 {
			data = pngFixCRCs(data)
		}
		b.add(fmt.Sprintf("png-fuzz-%04d", i), data)
	}
}

// pngFCTLData is the data of an fcTL chunk for a w x h frame at the origin.
func pngFCTLData(seq, w, h uint32) []byte {
	d := binary.BigEndian.AppendUint32(nil, seq)
	for _, v := range []uint32{w, h, 0, 0} {
		d = binary.BigEndian.AppendUint32(d, v)
	}
	return append(d, 0, 1, 0, 10, 0, 0)
}

// pngFixCRCs rewrites the checksum of every chunk that fits whole in data, so that damage to chunk data reaches the decoder behind the check.
func pngFixCRCs(data []byte) []byte {
	out := slices.Clone(data)
	off := 8
	for off+12 <= len(out) {
		length := int(binary.BigEndian.Uint32(out[off:]))
		if length < 0 || off+12+length > len(out) {
			break
		}
		chunk := pngChunk(string(out[off+4:off+8]), out[off+8:off+8+length])
		copy(out[off+8+length:], chunk[len(chunk)-4:])
		off += 12 + length
	}
	return out
}

// pngLimitCorpus is the sizes and memory the decoder accounts for: dimensions whose rows or buffers do not fit the 32-bit usize of the wasm
// module or the 512 MiB of image's Limits, chunks that fill the buffer of the crate's limits, and a profile that uses the limit up.
func pngLimitCorpus(b *formatCorpusBuilder) {
	iend := [2]any{"IEND", []byte{}}
	idat := func(z []byte) [2]any { return [2]any{"IDAT", z} }
	oneRow := func(w uint32, bitsPerPixel uint64) []byte { return zlibZeros(1 + int((uint64(w)*bitsPerPixel+7)/8)) }
	zeros := func(n int) []byte { return make([]byte, n) }
	// Dimensions: the file has the first row of the image only, so every case that gets past the limits fails on the missing data unless the
	// image is one row high.
	for _, c := range []struct {
		name          string
		w, h          uint32
		depth, color  byte
		interlace     byte
		withData      bool
		extraChunkBeg [][2]any
	}{
		{name: "gray8-65536x65536", w: 65536, h: 65536, depth: 8, color: 0},
		{name: "gray8-65537x65537", w: 65537, h: 65537, depth: 8, color: 0},
		{name: "gray1-65536x65536", w: 65536, h: 65536, depth: 1, color: 0},
		{name: "gray1-2^31x1", w: 1 << 31, h: 1, depth: 1, color: 0},
		{name: "gray1-2^31-1x1", w: 1<<31 - 1, h: 1, depth: 1, color: 0},
		{name: "gray16-2^28+1x1", w: 1<<28 + 1, h: 1, depth: 16, color: 0},
		{name: "gray16-1x2^28+1", w: 1, h: 1<<28 + 1, depth: 16, color: 0},
		{name: "rgba16-2^26+1x1", w: 1<<26 + 1, h: 1, depth: 16, color: 6},
		{name: "rgba16-2^27x1", w: 1 << 27, h: 1, depth: 16, color: 6},
		{name: "rgba16-0x7fffffffx1", w: 0x7fffffff, h: 1, depth: 16, color: 6},
		{name: "rgba16-0xffffffffx1", w: 0xffffffff, h: 1, depth: 16, color: 6},
		{name: "rgb8-0xffffffffx0xffffffff", w: 0xffffffff, h: 0xffffffff, depth: 8, color: 2},
		{name: "gray8-1x0xffffffff", w: 1, h: 0xffffffff, depth: 8, color: 0},
		{name: "gray8-0xffffffffx1", w: 0xffffffff, h: 1, depth: 8, color: 0},
		{name: "gray8-2^29x1", w: 1 << 29, h: 1, depth: 8, color: 0, withData: true},
		{name: "gray8-2^29+1x1", w: 1<<29 + 1, h: 1, depth: 8, color: 0},
		{name: "gray8-2^28x2", w: 1 << 28, h: 2, depth: 8, color: 0},
		{name: "gray8-2^28x2-adam7", w: 1 << 28, h: 2, depth: 8, color: 0, interlace: 1},
		{name: "rgb8-2^30x1", w: 1 << 30, h: 1, depth: 8, color: 2},
		{name: "rgb16-2^29x1", w: 1 << 29, h: 1, depth: 16, color: 2},
		{name: "gray8-trns-2^28x1", w: 1 << 28, h: 1, depth: 8, color: 0, extraChunkBeg: [][2]any{{"tRNS", []byte{0, 1}}}},
		{name: "rgb8-trns-2^27x1", w: 1 << 27, h: 1, depth: 8, color: 2, extraChunkBeg: [][2]any{{"tRNS", []byte{0, 1, 0, 2, 0, 3}}}},
		{name: "gray16-trns-2^27x1", w: 1 << 27, h: 1, depth: 16, color: 0, extraChunkBeg: [][2]any{{"tRNS", []byte{0, 1}}}},
		{name: "palette8-2^29x1", w: 1 << 29, h: 1, depth: 8, color: 3, extraChunkBeg: [][2]any{{"PLTE", zeros(3)}}},
		{name: "palette8-178956715x1", w: 178956715, h: 1, depth: 8, color: 3, extraChunkBeg: [][2]any{{"PLTE", zeros(768)}}},
		{name: "palette8-178956714x1-no-data", w: 178956714, h: 1, depth: 8, color: 3, extraChunkBeg: [][2]any{{"PLTE", zeros(768)}}},
	} {
		chunks := [][2]any{{"IHDR", pngIHDR(c.w, c.h, c.depth, c.color, c.interlace)}}
		chunks = append(chunks, c.extraChunkBeg...)
		z := []byte{0x78, 0x9c, 0x03, 0x00, 0, 0, 0, 1}
		if c.withData {
			z = oneRow(c.w, uint64(c.depth)*uint64(pngChannels(c.color)))
		}
		chunks = append(chunks, idat(z), iend)
		b.add("png-limit-"+c.name, pngChunks(chunks...))
	}
	// Chunks that fill the buffer the crate reads a chunk into (and the accounting of the doubling of that buffer): one of 128 MiB and one
	// of 260 MiB, a pair of 100 MiB, and the same ahead of the image data after a profile that took most of the limit.
	rgba := [2]any{"IHDR", pngIHDR(4, 4, 8, 6, 0)}
	data := zlibOf(pngRawRows(4, 4, 32, 1, 0))
	// chunkOf is a PNG whose chunk before the data is of type typ and zeros of n bytes.
	chunkOf := func(typs ...string) func(n int) func() []byte {
		return func(n int) func() []byte {
			return func() []byte {
				head := append([]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", pngIHDR(4, 4, 8, 6, 0))...)
				tail := pngChunks(idat(data), iend)[8:]
				out := make([]byte, 0, len(head)+len(typs)*(n+12)+len(tail))
				out = append(out, head...)
				for _, typ := range typs {
					out = binary.BigEndian.AppendUint32(out, uint32(n))
					out = append(out, typ...)
					out = append(out, make([]byte, n)...)
					crc := crc32.Update(crc32.ChecksumIEEE([]byte(typ)), crc32.IEEETable, make([]byte, n))
					out = binary.BigEndian.AppendUint32(out, crc)
				}
				return append(out, tail...)
			}
		}
	}
	prVt, twoPrivate, tEXt := chunkOf("prVt"), chunkOf("prVt", "prVu"), chunkOf("tEXt")
	for _, mib := range []int{1, 32, 33, 64, 127, 128, 129, 200, 255, 256, 257, 260} {
		b.addLazy(fmt.Sprintf("png-limit-chunk-%dMiB", mib), prVt(mib<<20))
	}
	b.addLazy("png-limit-chunk-256MiB-plus-32KiB", prVt(256<<20+32<<10))
	b.addLazy("png-limit-chunk-256MiB-plus-32KiB-minus-1", prVt(256<<20+32<<10-1))
	b.addLazy("png-limit-chunk-256MiB-plus-33KiB", prVt(256<<20+33<<10))
	b.addLazy("png-limit-chunk-two-100MiB", twoPrivate(100<<20))
	b.addLazy("png-limit-chunk-text-130MiB", tEXt(130<<20))
	// An ICC profile of P bytes (compressed zeros) takes P of the limit; the row of an image after it must then fit what is left.
	iccp := func(p int) [2]any {
		return [2]any{"iCCP", append([]byte("p\x00\x00"), zlibZeros(p)...)}
	}
	for _, c := range []struct {
		name          string
		profile       int
		w             uint32
		color, depth  byte
		trns          []byte
		interlaceMode byte
	}{
		{"fits", 500 << 20, 12582912, 0, 8, nil, 0},
		{"one-too-many", 500 << 20, 12582913, 0, 8, nil, 0},
		{"fits-by-8", 500<<20 + 8, 12582904, 0, 8, nil, 0},
		{"one-too-many-by-8", 500<<20 + 8, 12582905, 0, 8, nil, 0},
		{"trns-fits", 500<<20 - 2, 6291457, 0, 8, []byte{0, 1}, 0},
		{"trns-one-too-many", 500 << 20, 6291457, 0, 8, []byte{0, 1}, 0},
		{"rgb-fits", 500 << 20, 4194304, 2, 8, nil, 0},
		{"rgb-one-too-many", 500 << 20, 4194305, 2, 8, nil, 0},
		{"profile-whole-limit", 512 << 20, 1, 0, 8, nil, 0},
		{"profile-whole-limit-minus-row", 512<<20 - 2, 1, 0, 8, nil, 0},
		{"profile-whole-limit-minus-row-minus-one", 512<<20 - 1, 1, 0, 8, nil, 0},
		{"profile-bigger-than-limit", 513 << 20, 1, 0, 8, nil, 0},
	} {
		b.addLazy("png-limit-iccp-"+c.name, func() []byte {
			chunks := [][2]any{{"IHDR", pngIHDR(c.w, 1, c.depth, c.color, c.interlaceMode)}, iccp(c.profile)}
			if c.trns != nil {
				chunks = append(chunks, [2]any{"tRNS", c.trns})
			}
			chunks = append(chunks, idat(oneRow(c.w, uint64(c.depth)*uint64(pngChannels(c.color)))), iend)
			return pngChunks(chunks...)
		})
	}
	// A profile that is not a valid stream (damaged, wrong checksum, no checksum, too long a name) leaves the limit alone.
	for name, chunk := range map[string][]byte{
		"bad-adler":       append([]byte("p\x00\x00\x78\x9c\x03\x00"), 1, 2, 3, 4),
		"truncated":       []byte("p\x00\x00\x78\x9c\x4b"),
		"name-80-bytes":   append(bytes.Repeat([]byte("n"), 80), 0, 0, 0x78, 0x9c, 3, 0, 0, 0, 0, 1),
		"name-79-bytes":   append(append(bytes.Repeat([]byte("n"), 79), 0, 0), []byte{0x78, 0x9c, 3, 0, 0, 0, 0, 1}...),
		"empty-profile":   append([]byte("p\x00\x00\x78\x9c\x03\x00"), 0, 0, 0, 1),
		"method-1":        append([]byte("p\x00\x01\x78\x9c\x03\x00"), 0, 0, 0, 1),
		"no-method":       []byte("p\x00"),
		"one-byte-name":   append([]byte("p\x00"), []byte{0x78, 0x9c, 3, 0, 0, 0, 0, 1}...),
		"name-no-null-79": append(bytes.Repeat([]byte("n"), 79), 0),
	} {
		b.add("png-iccp-"+name, pngChunks(rgba, [2]any{"iCCP", chunk}, idat(data), iend))
	}
	b.add("png-iccp-twice", pngChunks(rgba, iccp(100), iccp(200), idat(data), iend))
	b.add("png-iccp-after-plte", pngChunks([2]any{"IHDR", pngIHDR(4, 4, 8, 3, 0)}, [2]any{"PLTE", zeros(12)}, iccp(100), idat(zlibOf(zeros(20))), iend))
}

// zlibZeros is the zlib stream of n zero bytes (n at least 1) as one fixed block: a literal and matches at distance 1.
func zlibZeros(n int) []byte {
	var w bitWriter
	lens := fixedLengths()
	codes := canonicalCodes(lens)
	match := func(length int) {
		it := dfMatch(length, 1)
		w.code(codes[it.sym], lens[it.sym])
		w.bits(it.extra, it.extraBits)
		w.code(0, 5)
	}
	w.bits(1, 1)
	w.bits(1, 2)
	w.code(codes[0], lens[0])
	left := n - 1
	for left > 260 {
		match(258)
		left -= 258
	}
	if left > 258 {
		match(left - 3)
		left = 3
	}
	if left >= 3 {
		match(left)
	} else {
		for range left {
			w.code(codes[0], lens[0])
		}
	}
	w.code(codes[256], lens[256])
	// The checksum of n zero bytes: a stays 1 and b is n modulo 65521.
	return binary.BigEndian.AppendUint32(append([]byte{0x78, 0x9c}, w.finish()...), uint32(n%65521)<<16|1)
}

// pngRealCorpus is the real files: PngSuite, image-rs's PNG tests, the Go repository's and the scene of the Photon tests.
func pngRealCorpus(b *formatCorpusBuilder) {
	for _, dir := range []string{"pngsuite", "image-rs", "go"} {
		entries, err := fs.ReadDir(pngRealFiles, "testdata/png-real/"+dir)
		if err != nil {
			panic(err)
		}
		for _, e := range entries {
			data, err := pngRealFiles.ReadFile("testdata/png-real/" + dir + "/" + e.Name())
			if err != nil {
				panic(err)
			}
			b.add("real-"+dir+"-"+strings.TrimSuffix(e.Name(), ".png"), data)
		}
	}
	scene, err := pngRealFiles.ReadFile("testdata/scene-photon-300x200.png")
	if err != nil {
		panic(err)
	}
	b.add("real-scene-photon-300x200", scene)
}
