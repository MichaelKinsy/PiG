// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash/adler32"
)

// pngChunks assembles a PNG from chunks (type, data): the signature, then each chunk with its length and checksum.
func pngChunks(chunks ...[2]any) []byte {
	out := []byte("\x89PNG\r\n\x1a\n")
	for _, c := range chunks {
		out = append(out, pngChunk(c[0].(string), c[1].([]byte))...)
	}
	return out
}

func pngIHDR(width, height uint32, depth, color, interlace byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, width)
	out = binary.BigEndian.AppendUint32(out, height)
	return append(out, depth, color, 0, 0, interlace)
}

// zlibOf compresses raw (compress/flate, default level) into a zlib stream with a correct header and checksum.
func zlibOf(raw []byte) []byte {
	var deflated bytes.Buffer
	w, _ := flate.NewWriter(&deflated, flate.DefaultCompression)
	_, _ = w.Write(raw)
	_ = w.Close()
	out := []byte{0x78, 0x9c}
	out = append(out, deflated.Bytes()...)
	return binary.BigEndian.AppendUint32(out, adler32.Checksum(raw))
}

// pngFilterRow filters row with the filter type against prev (zeros for the first row).
func pngFilterRow(filter byte, bpp int, prev, row []byte) []byte {
	out := make([]byte, len(row))
	for i := range row {
		var a, b, c int
		if i >= bpp {
			a, c = int(row[i-bpp]), int(prev[i-bpp])
		}
		b = int(prev[i])
		var predictor int
		switch filter {
		case 1:
			predictor = a
		case 2:
			predictor = b
		case 3:
			predictor = (a + b) / 2
		case 4:
			p := a + b - c
			pa, pb, pc := testAbs(p-a), testAbs(p-b), testAbs(p-c)
			switch {
			case pa <= pb && pa <= pc:
				predictor = a
			case pb <= pc:
				predictor = b
			default:
				predictor = c
			}
		}
		out[i] = row[i] - byte(predictor)
	}
	return out
}

// pngRawRows is the image data of a non-interlaced PNG: rows of noise filtered with the types cycling from first.
func pngRawRows(width, height, bitsPerPixel int, seed uint32, first int) []byte {
	rowBytes := (width*bitsPerPixel + 7) / 8
	bpp := max(bitsPerPixel/8, 1)
	noise := icoNoise(seed, rowBytes*height)
	prev := make([]byte, rowBytes)
	var raw []byte
	for y := range height {
		row := noise[y*rowBytes : (y+1)*rowBytes]
		filter := byte((y + first) % 5)
		raw = append(raw, filter)
		raw = append(raw, pngFilterRow(filter, bpp, prev, row)...)
		prev = row
	}
	return raw
}

// pngRawInterlaced is the image data of an Adam7 PNG with filters cycling from first in each pass.
func pngRawInterlaced(width, height, bitsPerPixel int, seed uint32, first int) []byte {
	bpp := max(bitsPerPixel/8, 1)
	var raw []byte
	for n, pass := range testAdam7(uint32(width), uint32(height)) {
		rowBytes := (int(pass[4])*bitsPerPixel + 7) / 8
		noise := icoNoise(seed+uint32(n), rowBytes*int(pass[5]))
		prev := make([]byte, rowBytes)
		for y := range int(pass[5]) {
			row := noise[y*rowBytes : (y+1)*rowBytes]
			filter := byte((y + first + n) % 5)
			raw = append(raw, filter)
			raw = append(raw, pngFilterRow(filter, bpp, prev, row)...)
			prev = row
		}
	}
	return raw
}

// icoPNGCorpus is pngStreamCorpus in ICO files: the entry names the size in the PNG's IHDR (when there is one), so that the entry check passes.
func icoPNGCorpus(b *formatCorpusBuilder) {
	pngStreamCorpus(b, func(png []byte) []byte {
		entry := icoEntry{planes: 1, bpp: 32}
		if len(png) >= 24 {
			entry.width = byte(min(binary.BigEndian.Uint32(png[16:]), 256))
			entry.height = byte(min(binary.BigEndian.Uint32(png[20:]), 256))
		}
		return icoFile(1, []icoEntry{entry}, [][]byte{png})
	})
}

// pngStreamCorpus adds the PNG streams that Go's encoder never writes: every filter, interlacing, split and empty IDAT chunks, damaged zlib
// streams and headers with valid checksums, chunk order and chunk contents the png crate checks, and animated PNGs. wrap puts each PNG in
// the file the corpus is of (an ICO file; for the PNG corpus itself it returns the PNG).
func pngStreamCorpus(b *formatCorpusBuilder, ico func([]byte) []byte) {
	idat := func(z []byte) [2]any { return [2]any{"IDAT", z} }
	iend := [2]any{"IEND", []byte{}}
	rgba := func(w, h int) [2]any { return [2]any{"IHDR", pngIHDR(uint32(w), uint32(h), 8, 6, 0)} }

	// Filters: every type on every row position, both image sizes below and above one filter's reach.
	for _, size := range [][2]int{{1, 1}, {1, 5}, {5, 1}, {4, 4}, {9, 7}, {33, 17}} {
		for first := range 5 {
			w, h := size[0], size[1]
			b.add(fmt.Sprintf("png-filters-rgba8-%dx%d-from-%d", w, h, first), ico(pngChunks(rgba(w, h), idat(zlibOf(pngRawRows(w, h, 32, uint32(w*h+first), first))), iend)))
			b.add(fmt.Sprintf("png-filters-adam7-rgba8-%dx%d-from-%d", w, h, first),
				ico(pngChunks([2]any{"IHDR", pngIHDR(uint32(w), uint32(h), 8, 6, 1)}, idat(zlibOf(pngRawInterlaced(w, h, 32, uint32(w*h+first), first))), iend)))
		}
	}
	for _, depth := range []byte{1, 2, 4, 8} {
		for _, interlace := range []byte{0, 1} {
			for _, w := range []int{1, 3, 8, 9, 17} {
				h := 5
				var raw []byte
				if interlace == 0 {
					raw = pngRawRows(w, h, int(depth), 7, 1)
				} else {
					raw = pngRawInterlaced(w, h, int(depth), 7, 1)
				}
				palette := icoNoise(3, 3*(1<<depth))
				trns := []byte{0, 128, 255, 17}
				b.add(fmt.Sprintf("png-palette-d%d-adam%d-w%d", depth, interlace, w),
					ico(pngChunks([2]any{"IHDR", pngIHDR(uint32(w), uint32(h), depth, 3, interlace)}, [2]any{"PLTE", palette}, [2]any{"tRNS", trns}, idat(zlibOf(raw)), iend)))
			}
		}
	}
	for _, depth := range []byte{8} {
		for _, interlace := range []byte{0, 1} {
			w, h := 6, 5
			var raw []byte
			if interlace == 0 {
				raw = pngRawRows(w, h, 24, 9, 2)
			} else {
				raw = pngRawInterlaced(w, h, 24, 9, 2)
			}
			for name, trns := range map[string][]byte{"hit": {0, 0, 0, 0, 0, 0}, "noise-key": {1, 2, 3, 4, 5, 6}, "high-bytes": {0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}} {
				b.add(fmt.Sprintf("png-rgb8-trns-%s-adam%d", name, interlace), ico(pngChunks([2]any{"IHDR", pngIHDR(uint32(w), uint32(h), depth, 2, interlace)}, [2]any{"tRNS", trns}, idat(zlibOf(raw)), iend)))
			}
		}
	}
	// A transparent colour that occurs in the image: pixels (r, g, b) all zero.
	{
		raw := []byte{0}
		raw = append(raw, 0, 0, 0, 10, 20, 30, 0, 0, 0, 5, 5, 5)
		b.add("png-rgb8-trns-key-pixels", ico(pngChunks([2]any{"IHDR", pngIHDR(4, 1, 8, 2, 0)}, [2]any{"tRNS", []byte{0, 0, 0, 0, 0, 0}}, idat(zlibOf(raw)), iend)))
		b.add("png-rgb8-trns-key-high-byte-ignored", ico(pngChunks([2]any{"IHDR", pngIHDR(4, 1, 8, 2, 0)}, [2]any{"tRNS", []byte{1, 0, 1, 0, 1, 0}}, idat(zlibOf(raw)), iend)))
	}

	// The zlib stream.
	good := pngRawRows(4, 4, 32, 5, 0)
	z := zlibOf(good)
	withZ := func(name string, stream []byte) {
		b.add("png-zlib-"+name, ico(pngChunks(rgba(4, 4), idat(stream), iend)))
	}
	withZ("good", z)
	withZ("bad-adler", append(append([]byte(nil), z[:len(z)-4]...), 1, 2, 3, 4))
	withZ("no-adler", z[:len(z)-4])
	withZ("adler-3-bytes", z[:len(z)-1])
	withZ("adler-1-byte", z[:len(z)-3])
	withZ("trailing-bytes", append(append([]byte(nil), z...), 9, 9, 9, 9, 9))
	withZ("cut-in-the-middle", z[:len(z)/2])
	withZ("cut-before-last-byte-of-deflate", z[:len(z)-5])
	withZ("header-only", z[:2])
	withZ("one-byte", z[:1])
	withZ("empty", nil)
	for _, h := range [][2]byte{{0x78, 0x9d}, {0x78, 0x00}, {0x88, 0x1c}, {0x78, 0xbb}, {0x08, 0x1d}, {0x18, 0x19}, {0x28, 0x1f}, {0x38, 0x1a}, {0x48, 0x17}, {0x58, 0x13}, {0x68, 0x1e}, {0x77, 0x00}, {0x78, 0x5e}, {0x78, 0xda}, {0x78, 0x01}, {0x79, 0x00}, {0x70, 0x17}} {
		withZ(fmt.Sprintf("header-%02x%02x", h[0], h[1]), append([]byte{h[0], h[1]}, z[2:]...))
	}
	{
		// A stored (uncompressed) deflate block, and a fixed-Huffman one, with the checksum correct or not.
		stored := []byte{0x78, 0x01, 0x01, byte(len(good)), byte(len(good) >> 8), ^byte(len(good)), ^byte(len(good) >> 8)}
		stored = append(stored, good...)
		stored = binary.BigEndian.AppendUint32(stored, adler32.Checksum(good))
		withZ("stored", stored)
		withZ("stored-short-nlen", append(append([]byte(nil), stored[:5]...), append([]byte{0xff, 0xff}, stored[7:]...)...))
		withZ("stored-not-final", append([]byte{0x78, 0x01, 0x00}, stored[3:]...))
		withZ("block-type-3", append([]byte{0x78, 0x9c, 0x07}, z[3:]...))
	}
	// Row counts and filter bytes.
	for _, rows := range []int{0, 1, 3, 5, 8} {
		raw := pngRawRows(4, rows, 32, 6, 0)
		b.add(fmt.Sprintf("png-rows-%d", rows), ico(pngChunks(rgba(4, 4), idat(zlibOf(raw)), iend)))
	}
	for _, filter := range []byte{5, 6, 17, 128, 255} {
		raw := append([]byte(nil), good...)
		raw[17*2] = filter
		b.add(fmt.Sprintf("png-bad-filter-%d-row2", filter), ico(pngChunks(rgba(4, 4), idat(zlibOf(raw)), iend)))
		raw = append([]byte(nil), good...)
		raw[17*3] = filter
		b.add(fmt.Sprintf("png-bad-filter-%d-last-row", filter), ico(pngChunks(rgba(4, 4), idat(zlibOf(raw)), iend)))
	}
	b.add("png-extra-raw-after-rows", ico(pngChunks(rgba(4, 4), idat(zlibOf(append(append([]byte(nil), good...), 1, 2, 3))), iend)))
	b.add("png-partial-last-row", ico(pngChunks(rgba(4, 4), idat(zlibOf(good[:len(good)-1])), iend)))

	// IDAT chunks: split anywhere, empty ones between, a chunk of another type between, a checksum error in a later one.
	for _, split := range []int{1, 2, 3, 7, 17, 40} {
		var chunks [][2]any
		chunks = append(chunks, rgba(4, 4))
		for i := 0; i < len(z); i += split {
			chunks = append(chunks, idat(z[i:min(i+split, len(z))]))
		}
		chunks = append(chunks, iend)
		b.add(fmt.Sprintf("png-idat-split-%d", split), ico(pngChunks(chunks...)))
	}
	b.add("png-idat-empty-between", ico(pngChunks(rgba(4, 4), idat(z[:5]), idat(nil), idat(z[5:]), iend)))
	b.add("png-idat-empty-first", ico(pngChunks(rgba(4, 4), idat(nil), idat(z), iend)))
	b.add("png-idat-empty-last", ico(pngChunks(rgba(4, 4), idat(z), idat(nil), iend)))
	b.add("png-idat-only-empty", ico(pngChunks(rgba(4, 4), idat(nil), iend)))
	b.add("png-idat-text-between", ico(pngChunks(rgba(4, 4), idat(z[:5]), [2]any{"tEXt", []byte("a\x00b")}, idat(z[5:]), iend)))
	b.add("png-idat-after-text-complete", ico(pngChunks(rgba(4, 4), idat(z), [2]any{"tEXt", []byte("a\x00b")}, idat(z[:3]), iend)))
	b.add("png-idat-then-garbage-chunk", ico(pngChunks(rgba(4, 4), idat(z), [2]any{"XXXX", bytes.Repeat([]byte{7}, 50)})))
	b.add("png-idat-then-eight-bytes", ico(append(pngChunks(rgba(4, 4), idat(z)), 0, 0, 0, 0, 'I', 'E', 'N', 'D')))
	b.add("png-idat-then-seven-bytes", ico(append(pngChunks(rgba(4, 4), idat(z)), 0, 0, 0, 0, 'I', 'E', 'N')))
	b.add("png-idat-then-nothing", ico(pngChunks(rgba(4, 4), idat(z))))
	b.add("png-idat-then-big-length", ico(append(pngChunks(rgba(4, 4), idat(z)), 0xFF, 0xFF, 0xFF, 0xFF, 'X', 'X', 'X', 'X')))
	b.add("png-idat-then-idat-header", ico(append(pngChunks(rgba(4, 4), idat(z)), 0, 0, 0, 5, 'I', 'D', 'A', 'T')))
	{
		bad := pngChunks(rgba(4, 4), idat(z[:10]), idat(z[10:]), iend)
		// Damage the second chunk's checksum (the data of the first is enough only for a few rows).
		bad[len(bad)-12-4-1] ^= 0xFF
		b.add("png-idat-second-crc-bad", ico(bad))
		whole := pngChunks(rgba(4, 4), idat(z), iend)
		whole[len(whole)-12-4-1] ^= 0xFF
		b.add("png-idat-crc-bad-after-all-rows", ico(whole))
	}

	// Chunk order and contents before the image data.
	pal4 := icoNoise(1, 12)
	trns := []byte{1, 2}
	ct3 := func(chunks ...[2]any) []byte {
		all := [][2]any{{"IHDR", pngIHDR(4, 4, 8, 3, 0)}}
		all = append(all, chunks...)
		all = append(all, idat(zlibOf(pngRawRowsPalette(4, 4, 3))), iend)
		return ico(pngChunks(all...))
	}
	pal := [2]any{"PLTE", pal4}
	tr := [2]any{"tRNS", trns}
	b.add("png-order-plte-trns", ct3(pal, tr))
	b.add("png-order-trns-plte", ct3(tr, pal))
	b.add("png-order-plte-plte-trns", ct3(pal, pal, tr))
	b.add("png-order-plte-trns-trns", ct3(pal, tr, tr))
	b.add("png-order-plte-trns-text", ct3(pal, tr, [2]any{"tEXt", []byte("k\x00v")}))
	b.add("png-order-text-plte-trns", ct3([2]any{"tEXt", []byte("k\x00v")}, pal, tr))
	b.add("png-order-plte-trns-empty-plte", ct3(pal, tr, [2]any{"PLTE", []byte{}}))
	b.add("png-order-empty-plte-then-plte-trns", ct3([2]any{"PLTE", []byte{}}, pal, tr))
	b.add("png-order-empty-trns-then-trns", ct3(pal, [2]any{"tRNS", []byte{}}, tr))
	b.add("png-order-empty-trns-only", ct3(pal, [2]any{"tRNS", []byte{}}))
	b.add("png-order-trns-longer-than-palette", ct3(pal, [2]any{"tRNS", []byte{1, 2, 3, 4, 5}}))
	b.add("png-order-trns-as-long-as-palette", ct3(pal, [2]any{"tRNS", []byte{1, 2, 3, 4}}))
	b.add("png-order-trns-one-entry", ct3(pal, [2]any{"tRNS", []byte{0}}))
	b.add("png-order-plte-12-bytes-trns-256", ct3(pal, [2]any{"tRNS", make([]byte, 256)}))
	b.add("png-order-palette-256-entries", ct3([2]any{"PLTE", icoNoise(2, 768)}, [2]any{"tRNS", make([]byte, 256)}))
	b.add("png-order-palette-256-entries-short-trns", ct3([2]any{"PLTE", icoNoise(2, 768)}, tr))
	b.add("png-order-palette-255-entries", ct3([2]any{"PLTE", icoNoise(2, 765)}, tr))
	b.add("png-order-palette-257-entries", ct3([2]any{"PLTE", icoNoise(2, 771)}, tr))
	for _, n := range []int{1, 2, 3, 4, 5, 6, 7, 9, 11, 13, 14, 15, 30, 767, 769} {
		b.add(fmt.Sprintf("png-palette-bytes-%d", n), ct3([2]any{"PLTE", icoNoise(4, n)}, tr))
	}
	// Chunks the crate parses before the data.
	common := func(name string, chunk [2]any) {
		rows := zlibOf(pngRawRows(4, 4, 32, 8, 0))
		b.add("png-chunk-"+name, ico(pngChunks(rgba(4, 4), chunk, idat(rows), iend)))
	}
	common("gama-4", [2]any{"gAMA", []byte{0, 0, 0xB1, 0x8F}})
	common("gama-2", [2]any{"gAMA", []byte{0, 0}})
	common("gama-0", [2]any{"gAMA", []byte{}})
	common("gama-8", [2]any{"gAMA", make([]byte, 8)})
	common("chrm-32", [2]any{"cHRM", make([]byte, 32)})
	common("chrm-31", [2]any{"cHRM", make([]byte, 31)})
	common("chrm-36", [2]any{"cHRM", make([]byte, 36)})
	for _, v := range []byte{0, 1, 2, 3, 4, 255} {
		common(fmt.Sprintf("srgb-%d", v), [2]any{"sRGB", []byte{v}})
	}
	common("srgb-2-bytes", [2]any{"sRGB", []byte{0, 0}})
	common("srgb-empty", [2]any{"sRGB", []byte{}})
	for _, unit := range []byte{0, 1, 2, 255} {
		common(fmt.Sprintf("phys-unit-%d", unit), [2]any{"pHYs", []byte{0, 0, 0x0B, 0x13, 0, 0, 0x0B, 0x13, unit}})
	}
	common("phys-8", [2]any{"pHYs", make([]byte, 8)})
	common("phys-12", [2]any{"pHYs", make([]byte, 12)})
	common("iccp-valid", [2]any{"iCCP", append([]byte("sRGB\x00\x00"), zlibOf([]byte("profile"))...)})
	common("iccp-garbage", [2]any{"iCCP", []byte("not a profile")})
	common("iccp-empty", [2]any{"iCCP", []byte{}})
	common("iccp-no-terminator", [2]any{"iCCP", bytes.Repeat([]byte("a"), 90)})
	common("iccp-bad-method", [2]any{"iCCP", []byte("x\x00\x07zzzz")})
	common("text", [2]any{"tEXt", []byte("Comment\x00hi")})
	common("text-no-separator", [2]any{"tEXt", []byte("Comment")})
	common("ztxt-garbage", [2]any{"zTXt", []byte("k\x00\x00garbage")})
	common("itxt-garbage", [2]any{"iTXt", []byte("k\x00garbage")})
	common("time", [2]any{"tIME", make([]byte, 7)})
	common("time-short", [2]any{"tIME", make([]byte, 3)})
	common("bkgd", [2]any{"bKGD", []byte{0, 1, 2, 3, 4, 5}})
	common("sbit", [2]any{"sBIT", []byte{8, 8, 8, 8}})
	common("hist", [2]any{"hIST", []byte{0, 1}})
	common("unknown-critical", [2]any{"CRIT", []byte("x")})
	common("unknown-ancillary", [2]any{"prVt", []byte("x")})
	common("lowercase-ihdr", [2]any{"ihdr", pngIHDR(4, 4, 8, 6, 0)})
	common("second-ihdr", [2]any{"IHDR", pngIHDR(4, 4, 8, 6, 0)})
	common("second-ihdr-other", [2]any{"IHDR", pngIHDR(8, 8, 8, 2, 0)})
	common("empty-ihdr", [2]any{"IHDR", []byte{}})
	common("plte-on-rgba", [2]any{"PLTE", icoNoise(1, 12)})
	common("trns-on-rgba", [2]any{"tRNS", []byte{1, 2, 3, 4, 5, 6}})
	common("trns-empty-on-rgba", [2]any{"tRNS", []byte{}})
	common("iend-first", [2]any{"IEND", []byte{}})
	common("idat-empty-first", idat(nil))
	for _, n := range []uint32{0, 1, 2, 3, 4} {
		// An IHDR with its data cut or extended.
		data := append(pngIHDR(4, 4, 8, 6, 0), make([]byte, n)...)
		b.add(fmt.Sprintf("png-ihdr-length-13-plus-%d", n), ico(pngChunks([2]any{"IHDR", data}, idat(z), iend)))
	}
	for n := range 13 {
		b.add(fmt.Sprintf("png-ihdr-length-%d", n), ico(pngChunks([2]any{"IHDR", pngIHDR(4, 4, 8, 6, 0)[:n]}, idat(z), iend)))
	}
	b.add("png-no-ihdr", ico(pngChunks(idat(z), iend)))
	b.add("png-text-before-ihdr", ico(pngChunks([2]any{"tEXt", []byte("a\x00b")}, rgba(4, 4), idat(z), iend)))
	b.add("png-iend-only", ico(pngChunks(rgba(4, 4), iend)))
	b.add("png-ihdr-only", ico(pngChunks(rgba(4, 4))))
	b.add("png-ancillary-crc-bad", func() []byte {
		p := pngChunks(rgba(4, 4), [2]any{"tEXt", []byte("a\x00b")}, idat(z), iend)
		p[8+25+8+1] ^= 0xFF // a byte of the tEXt data
		return ico(p)
	}())
	b.add("png-gama-crc-bad", func() []byte {
		p := pngChunks(rgba(4, 4), [2]any{"gAMA", []byte{0, 0, 0xB1, 0x8F}}, idat(z), iend)
		p[8+25+8+1] ^= 0xFF
		return ico(p)
	}())
	b.add("png-chunk-length-past-end", ico(append(pngChunks(rgba(4, 4)), 0x7F, 0xFF, 0xFF, 0xFF, 't', 'E', 'X', 't', 1, 2, 3)))

	// Animated PNG: the default image is the IDAT data.
	actl := func(frames uint32) [2]any {
		return [2]any{"acTL", binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, frames), 0)}
	}
	fctl := func(seq, w, h, x, y uint32, dispose, blend byte) [2]any {
		d := binary.BigEndian.AppendUint32(nil, seq)
		for _, v := range []uint32{w, h, x, y} {
			d = binary.BigEndian.AppendUint32(d, v)
		}
		d = append(d, 0, 1, 0, 10, dispose, blend)
		return [2]any{"fcTL", d}
	}
	fdat := func(seq uint32, z []byte) [2]any {
		return [2]any{"fdAT", append(binary.BigEndian.AppendUint32(nil, seq), z...)}
	}
	b.add("apng-actl-only", ico(pngChunks(rgba(4, 4), actl(2), idat(z), iend)))
	b.add("apng-default-image", ico(pngChunks(rgba(4, 4), actl(2), fctl(0, 4, 4, 0, 0, 0, 0), idat(z), fctl(1, 4, 4, 0, 0, 0, 0), fdat(2, z), iend)))
	b.add("apng-default-image-not-in-animation", ico(pngChunks(rgba(4, 4), actl(1), idat(z), fctl(0, 4, 4, 0, 0, 0, 0), fdat(1, z), iend)))
	b.add("apng-no-actl-fctl", ico(pngChunks(rgba(4, 4), fctl(0, 4, 4, 0, 0, 0, 0), idat(z), iend)))
	for name, c := range map[string][2]any{
		"fctl-smaller-frame":     fctl(0, 2, 2, 1, 1, 0, 0),
		"fctl-smaller-height":    fctl(0, 4, 2, 0, 0, 0, 0),
		"fctl-smaller-width":     fctl(0, 2, 4, 0, 0, 0, 0),
		"fctl-width-zero":        fctl(0, 0, 4, 0, 0, 0, 0),
		"fctl-height-zero":       fctl(0, 4, 0, 0, 0, 0, 0),
		"fctl-too-wide":          fctl(0, 5, 4, 0, 0, 0, 0),
		"fctl-too-tall":          fctl(0, 4, 5, 0, 0, 0, 0),
		"fctl-offset-overflow":   fctl(0, 4, 4, 1, 0, 0, 0),
		"fctl-offset-huge":       fctl(0, 1, 1, 0xFFFFFFFF, 0, 0, 0),
		"fctl-seq-1":             fctl(1, 4, 4, 0, 0, 0, 0),
		"fctl-dispose-3":         fctl(0, 4, 4, 0, 0, 3, 0),
		"fctl-blend-2":           fctl(0, 4, 4, 0, 0, 0, 2),
		"fctl-dispose-2-blend-1": fctl(0, 4, 4, 0, 0, 2, 1),
		"fctl-short":             {"fcTL", make([]byte, 25)},
	} {
		b.add("apng-"+name, ico(pngChunks(rgba(4, 4), actl(1), c, idat(z), iend)))
	}
	b.add("apng-fctl-twice-seq", ico(pngChunks(rgba(4, 4), actl(2), fctl(0, 4, 4, 0, 0, 0, 0), fctl(1, 4, 4, 0, 0, 0, 0), idat(z), iend)))
	b.add("apng-fctl-twice-bad-seq", ico(pngChunks(rgba(4, 4), actl(2), fctl(0, 4, 4, 0, 0, 0, 0), fctl(2, 4, 4, 0, 0, 0, 0), idat(z), iend)))
	b.add("apng-fdat-first-no-fctl", ico(pngChunks(rgba(4, 4), fdat(0, z), iend)))
	b.add("apng-fdat-before-idat", ico(pngChunks(rgba(4, 4), actl(2), fctl(0, 4, 4, 0, 0, 0, 0), fdat(1, z), idat(z), iend)))
	b.add("apng-fdat-only-after-fctl", ico(pngChunks(rgba(4, 4), actl(1), fctl(0, 4, 4, 0, 0, 0, 0), fdat(1, z), iend)))
	b.add("apng-fdat-short", ico(pngChunks(rgba(4, 4), actl(1), fctl(0, 4, 4, 0, 0, 0, 0), [2]any{"fdAT", []byte{0, 0, 0}}, idat(z), iend)))
	b.add("apng-actl-short", ico(pngChunks(rgba(4, 4), [2]any{"acTL", []byte{0, 0, 0, 1}}, idat(z), iend)))
	b.add("apng-actl-zero-frames", ico(pngChunks(rgba(4, 4), actl(0), idat(z), iend)))
	b.add("apng-subframe-adam7", ico(pngChunks([2]any{"IHDR", pngIHDR(9, 7, 8, 6, 1)}, actl(1), fctl(0, 5, 4, 1, 1, 0, 0), idat(zlibOf(pngRawInterlaced(5, 4, 32, 3, 0))), iend)))
	b.add("apng-subframe-adam7-full-data", ico(pngChunks([2]any{"IHDR", pngIHDR(9, 7, 8, 6, 1)}, actl(1), fctl(0, 5, 4, 1, 1, 0, 0), idat(zlibOf(pngRawInterlaced(9, 7, 32, 3, 0))), iend)))
}

// pngRawRowsPalette is rows of palette indices below `entries`, 8 bits each, filtered with the types cycling.
func pngRawRowsPalette(width, height, entries int) []byte {
	noise := icoNoise(11, width*height)
	prev := make([]byte, width)
	var raw []byte
	for y := range height {
		row := make([]byte, width)
		for x := range row {
			row[x] = noise[y*width+x] % byte(entries)
		}
		filter := byte(y % 5)
		raw = append(raw, filter)
		raw = append(raw, pngFilterRow(filter, 1, prev, row)...)
		prev = row
	}
	return raw
}

// testAbs and testAdam7 are the corpus's own copies of the helpers of pngdecode.go, so that the corpus builds without the decoder: for each pass
// the first column and row, the steps, the number of pixels in a line and the number of lines.
func testAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func testAdam7(width, height uint32) [][6]uint32 {
	var passes [][6]uint32
	for _, p := range [7][4]uint32{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}} {
		if width <= p[0] || height <= p[1] {
			continue
		}
		passes = append(passes, [6]uint32{p[0], p[1], p[2], p[3], (width - p[0] + p[2] - 1) / p[2], (height - p[1] + p[3] - 1) / p[3]})
	}
	return passes
}
