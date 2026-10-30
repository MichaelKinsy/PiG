// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"embed"
	"fmt"
	"io/fs"
	"math/bits"
	"slices"
	"strings"
)

//go:embed testdata/gif-real/*.gif testdata/gif-real/go/*.gif
var gifRealFiles embed.FS

// gifPalette is n colours of three bytes.
func gifPalette(n int, seed uint32) []byte { return icoNoise(seed, 3*n) }

// gifPackCodes writes the LZW codes of a GIF image data stream, least significant bit first, with the code widths that weezl's decoder
// expects (it reads the first code after a clear code with the width the clear code set, and adds one table entry for each code after that,
// widening the codes when the entry that ends a width is added).
func gifPackCodes(minCode int, codes []int) []byte {
	clear, end := 1<<minCode, 1<<minCode+1
	size, next, first := minCode+1, end+1, true
	var out []byte
	var acc uint64
	var have int
	for _, c := range codes {
		acc |= uint64(c&(1<<size-1)) << have
		have += size
		for have >= 8 {
			out = append(out, byte(acc))
			acc >>= 8
			have -= 8
		}
		switch {
		case c == clear:
			size, next, first = minCode+1, end+1, true
		case c == end:
		case first:
			first = false
		case next < 4096:
			if next == 1<<size-1 && size < 12 {
				size++
			}
			next++
		}
	}
	if have > 0 {
		out = append(out, byte(acc))
	}
	return out
}

// gifEncode is an LZW encoder: the codes of the indices, with a clear code first and an end code last, and a clear code when the table is
// full if clearOnFull (without it the table stays full).
func gifEncode(minCode int, indices []byte, clearOnFull bool) []int {
	clear, end := 1<<minCode, 1<<minCode+1
	codes := []int{clear}
	dict := map[[2]int]int{}
	next := end + 1
	if len(indices) == 0 {
		return append(codes, end)
	}
	prefix := int(indices[0])
	for _, b := range indices[1:] {
		key := [2]int{prefix, int(b)}
		if code, ok := dict[key]; ok {
			prefix = code
			continue
		}
		codes = append(codes, prefix)
		if next < 4096 {
			dict[key] = next
			next++
		} else if clearOnFull {
			codes = append(codes, clear)
			dict = map[[2]int]int{}
			next = end + 1
		}
		prefix = int(b)
	}
	return append(codes, prefix, end)
}

// gifSubBlocks cuts data into sub-blocks of at most size bytes and ends them with the terminator.
func gifSubBlocks(data []byte, size int) []byte {
	var out []byte
	for len(data) > 0 {
		n := min(size, len(data))
		out = append(out, byte(n))
		out = append(out, data[:n]...)
		data = data[n:]
	}
	return append(out, 0)
}

// gifImage is an image descriptor block with its data (everything after the minimum code size byte).
type gifImage struct {
	left, top, width, height uint16
	interlaced               bool
	local                    []byte // the local colour table: 2, 4, ..., 256 entries
	minCode                  byte
	data                     []byte
}

func gifTableFlags(table []byte) byte {
	n := len(table) / 3
	if n < 2 || n&(n-1) != 0 || n > 256 {
		return 0x80
	}
	return 0x80 | byte(bits.TrailingZeros(uint(n))-1)
}

func (g gifImage) bytes() []byte {
	out := []byte{0x2C}
	out = append(out, le16(g.left)...)
	out = append(out, le16(g.top)...)
	out = append(out, le16(g.width)...)
	out = append(out, le16(g.height)...)
	flags := byte(0)
	if g.interlaced {
		flags |= 0x40
	}
	if g.local != nil {
		flags |= gifTableFlags(g.local)
	}
	out = append(out, flags)
	out = append(out, g.local...)
	out = append(out, g.minCode)
	return append(out, g.data...)
}

// gifSpec is a GIF file.
type gifSpec struct {
	sig           string // "GIF89a" unless set
	width, height uint16
	global        []byte
	flags         *byte // the screen descriptor's flags byte, from the global table unless set
	background    byte
	aspect        byte
	blocks        [][]byte
	noTrailer     bool
	trailing      []byte
}

func (s gifSpec) bytes() []byte {
	sig := s.sig
	if sig == "" {
		sig = "GIF89a"
	}
	out := []byte(sig)
	out = append(out, le16(s.width)...)
	out = append(out, le16(s.height)...)
	flags := byte(0)
	if s.global != nil {
		flags = gifTableFlags(s.global)
	}
	if s.flags != nil {
		flags = *s.flags
	}
	out = append(out, flags, s.background, s.aspect)
	out = append(out, s.global...)
	for _, b := range s.blocks {
		out = append(out, b...)
	}
	if !s.noTrailer {
		out = append(out, 0x3B)
	}
	return append(out, s.trailing...)
}

// gifControl is a graphic control extension.
func gifControl(flags byte, delay uint16, transparent byte) []byte {
	return append([]byte{0x21, 0xF9, 4, flags}, append(le16(delay), transparent, 0)...)
}

// gifExtension is an extension block with its data cut into sub-blocks of at most 255 bytes.
func gifExtension(label byte, data []byte) []byte {
	return append([]byte{0x21, label}, gifSubBlocks(data, 255)...)
}

// gifPattern is width * height palette indices below limit.
func gifPattern(kind string, width, height, limit int, seed uint32) []byte {
	out := make([]byte, width*height)
	noise := icoNoise(seed, len(out)+1)
	for i := range out {
		x, y := i%width, i/width
		switch kind {
		case "noise":
			out[i] = byte(int(noise[i]) % limit)
		case "flat":
			out[i] = byte(int(seed) % limit)
		case "stripes":
			out[i] = byte((x / 2) % 2 % limit)
		case "gradient":
			out[i] = byte((x + y) % limit)
		case "runs":
			out[i] = byte((i / 7) % limit)
		case "checker":
			out[i] = byte((x + y) % 2 % limit)
		}
	}
	return out
}

// gifValid is a one-frame GIF of the pattern with a global table of 1<<bits entries.
func gifValid(width, height, bitCount int, kind string, seed uint32) gifSpec {
	minCode := max(bitCount, 2)
	indices := gifPattern(kind, width, height, 1<<bitCount, seed)
	img := gifImage{width: uint16(width), height: uint16(height), minCode: byte(minCode), data: gifSubBlocks(gifPackCodes(minCode, gifEncode(minCode, indices, true)), 255)}
	return gifSpec{width: uint16(width), height: uint16(height), global: gifPalette(1<<bitCount, seed), blocks: [][]byte{img.bytes()}}
}

// gifInterlaced reorders the rows of indices in the order of an interlaced image.
func gifInterlaced(indices []byte, width, height int) []byte {
	var out []byte
	for _, pass := range [4][2]int{{0, 8}, {4, 8}, {2, 4}, {1, 2}} {
		for y := pass[0]; y < height; y += pass[1] {
			out = append(out, indices[y*width:(y+1)*width]...)
		}
	}
	return out
}

// gifCorpus is the files of TestDecodeGIFMatchesPhoton: image 0.24.9's GIF decoder over the gif crate 0.13.1 and weezl 0.1.8.
func gifCorpus() []formatCase {
	var b formatCorpusBuilder
	add := func(name string, s gifSpec) { b.add(name, s.bytes()) }

	err := fs.WalkDir(gifRealFiles, "testdata/gif-real", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		data, err := gifRealFiles.ReadFile(path)
		if err != nil {
			return err
		}
		name := "real-" + strings.TrimSuffix(strings.TrimPrefix(path, "testdata/gif-real/"), ".gif")
		name = strings.ReplaceAll(name, "/", "-")
		b.add(name, data)
		for cut := 6; cut < len(data) && cut < 60; cut++ {
			b.add(fmt.Sprintf("%s-cut%d", name, cut), data[:cut])
		}
		cuts := []int{len(data) / 4, len(data) / 2, len(data) * 3 / 4, len(data) - 2, len(data) - 1}
		slices.Sort(cuts)
		for _, cut := range slices.Compact(cuts) {
			b.add(fmt.Sprintf("%s-cut-at-%d", name, cut), data[:cut])
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	// Valid images: sizes, bits per pixel (the table has 1 << bits entries) and patterns (stripes and runs give the code that is the next code).
	for _, size := range [][2]int{{1, 1}, {2, 1}, {1, 2}, {3, 3}, {7, 5}, {16, 16}, {17, 9}, {64, 5}, {100, 30}, {255, 3}, {256, 2}, {300, 20}, {1, 300}} {
		w, h := size[0], size[1]
		for bitCount := 1; bitCount <= 8; bitCount++ {
			for _, kind := range []string{"noise", "flat", "stripes", "gradient", "runs", "checker"} {
				if (w*h > 2000 && bitCount%3 != 0) || (w*h > 200 && kind == "checker") {
					continue
				}
				add(fmt.Sprintf("valid-%dx%d-%dbit-%s", w, h, bitCount, kind), gifValid(w, h, bitCount, kind, uint32(w*h+bitCount)))
			}
		}
	}
	// 4096 codes and more: the table fills and is cleared, or stays full.
	for _, clearOnFull := range []bool{true, false} {
		for _, bitCount := range []int{2, 5, 8} {
			for _, size := range [][2]int{{64, 64}, {120, 100}} {
				w, h := size[0], size[1]
				indices := gifPattern("noise", w, h, 1<<bitCount, uint32(bitCount))
				minCode := max(bitCount, 2)
				img := gifImage{width: uint16(w), height: uint16(h), minCode: byte(minCode), data: gifSubBlocks(gifPackCodes(minCode, gifEncode(minCode, indices, clearOnFull)), 255)}
				add(fmt.Sprintf("table-full-clear%v-%dbit-%dx%d", clearOnFull, bitCount, w, h), gifSpec{width: uint16(w), height: uint16(h), global: gifPalette(1<<bitCount, 1), blocks: [][]byte{img.bytes()}})
			}
		}
	}
	// Minimum code sizes of every value, over tables of other sizes.
	for minCode := 0; minCode <= 14; minCode++ {
		for _, table := range []int{2, 4, 16, 256} {
			indices := gifPattern("noise", 20, 10, min(table, 1<<min(minCode, 8)), uint32(minCode))
			img := gifImage{width: 20, height: 10, minCode: byte(minCode), data: gifSubBlocks(gifPackCodes(max(minCode, 1), gifEncode(max(minCode, 1), indices, true)), 255)}
			add(fmt.Sprintf("mincode-%d-table-%d", minCode, table), gifSpec{width: 20, height: 10, global: gifPalette(table, 3), blocks: [][]byte{img.bytes()}})
		}
	}
	for minCode := 9; minCode <= 11; minCode++ {
		indices := gifPattern("noise", 30, 30, 256, uint32(minCode))
		img := gifImage{width: 30, height: 30, minCode: byte(minCode), data: gifSubBlocks(gifPackCodes(minCode, gifEncode(minCode, indices, true)), 255)}
		add(fmt.Sprintf("mincode-%d-wide-table", minCode), gifSpec{width: 30, height: 30, global: gifPalette(256, 3), blocks: [][]byte{img.bytes()}})
	}

	// Palettes: global and local tables of every size, none, both, indices beyond the table, a local table over a global one.
	for _, n := range []int{2, 4, 8, 16, 32, 64, 128, 256} {
		img := gifImage{width: 9, height: 7, minCode: 8, data: gifSubBlocks(gifPackCodes(8, gifEncode(8, gifPattern("noise", 9, 7, 256, 5), true)), 255)}
		add(fmt.Sprintf("global-table-%d-indices-beyond", n), gifSpec{width: 9, height: 7, global: gifPalette(n, 1), blocks: [][]byte{img.bytes()}})
		img.local = gifPalette(n, 2)
		add(fmt.Sprintf("local-table-%d-no-global", n), gifSpec{width: 9, height: 7, blocks: [][]byte{img.bytes()}})
		add(fmt.Sprintf("local-table-%d-over-global", n), gifSpec{width: 9, height: 7, global: gifPalette(256, 3), blocks: [][]byte{img.bytes()}})
	}
	{
		img := gifImage{width: 5, height: 5, minCode: 2, data: gifSubBlocks(gifPackCodes(2, gifEncode(2, gifPattern("noise", 5, 5, 4, 1), true)), 255)}
		add("no-table", gifSpec{width: 5, height: 5, blocks: [][]byte{img.bytes()}})
		flags := byte(0x80 | 3) // the flags promise a global table that the file does not have
		add("global-table-missing", gifSpec{width: 5, height: 5, flags: &flags, blocks: [][]byte{img.bytes()}})
		img.local = gifPalette(4, 1)[:7] // a cut local table
		add("local-table-cut", gifSpec{width: 5, height: 5, blocks: [][]byte{img.bytes()}})
		add("global-table-flag-without-table-bits", gifSpec{width: 5, height: 5, global: gifPalette(3, 1), blocks: [][]byte{img.bytes()}})
	}

	// Transparency: the index of the control extension, with and without the flag, twice, and after other extensions.
	for _, flags := range []byte{0, 1, 4, 5, 8, 9, 0x1D, 0xFF} {
		for _, index := range []byte{0, 1, 2, 3, 7, 255} {
			s := gifValid(8, 6, 2, "noise", 4)
			s.blocks = append([][]byte{gifControl(flags, 5, index)}, s.blocks...)
			add(fmt.Sprintf("transparent-flags%02x-index%d", flags, index), s)
		}
	}
	{
		s := gifValid(8, 6, 2, "noise", 4)
		image := s.blocks[0]
		add("transparent-two-controls", gifSpec{width: 8, height: 6, global: s.global, blocks: [][]byte{gifControl(1, 0, 1), gifControl(0, 0, 2), image}})
		add("transparent-two-controls-b", gifSpec{width: 8, height: 6, global: s.global, blocks: [][]byte{gifControl(0, 0, 1), gifControl(1, 0, 2), image}})
		add("transparent-two-controls-c", gifSpec{width: 8, height: 6, global: s.global, blocks: [][]byte{gifControl(1, 0, 1), gifControl(1, 0, 3), image}})
	}

	// Interlaced frames of every height up to 20 and some wider ones; interlaced with a data stream in file order.
	for h := 1; h <= 20; h++ {
		for _, w := range []int{1, 3, 8} {
			indices := gifInterlaced(gifPattern("noise", w, h, 4, uint32(h*w)), w, h)
			img := gifImage{width: uint16(w), height: uint16(h), interlaced: true, minCode: 2, data: gifSubBlocks(gifPackCodes(2, gifEncode(2, indices, true)), 255)}
			add(fmt.Sprintf("interlaced-%dx%d", w, h), gifSpec{width: uint16(w), height: uint16(h), global: gifPalette(4, 6), blocks: [][]byte{img.bytes()}})
			img.interlaced = false
			add(fmt.Sprintf("interlaced-data-not-flagged-%dx%d", w, h), gifSpec{width: uint16(w), height: uint16(h), global: gifPalette(4, 6), blocks: [][]byte{img.bytes()}})
		}
	}
	for _, h := range []int{0, 1} {
		img := gifImage{width: 5, height: uint16(h), interlaced: true, minCode: 2, data: gifSubBlocks(gifPackCodes(2, gifEncode(2, nil, true)), 255)}
		add(fmt.Sprintf("interlaced-empty-height-%d", h), gifSpec{width: 5, height: uint16(h), global: gifPalette(4, 6), blocks: [][]byte{img.bytes()}})
	}

	// Frame placement: the frame against the screen: offsets, smaller and larger frames, empty frames, frames outside, in every flavour of order.
	for _, screen := range [][2]int{{8, 6}, {1, 1}, {0, 0}, {0, 4}, {4, 0}, {3, 9}} {
		for _, frame := range [][4]int{{0, 0, 8, 6}, {0, 0, 4, 3}, {2, 1, 4, 3}, {0, 2, 8, 2}, {0, 4, 8, 4}, {1, 0, 8, 6}, {4, 3, 8, 6}, {0, 0, 9, 7}, {7, 5, 1, 1},
			{8, 6, 2, 2}, {9, 7, 2, 2}, {0xFFFF, 0xFFFF, 2, 2}, {0xFFFE, 0, 3, 1}, {0, 0, 0, 0}, {0, 0, 0, 3}, {0, 0, 3, 0}, {2, 2, 0, 3}, {0, 0, 1, 1}, {0, 5, 8, 1}, {0, 5, 8, 2}, {1, 1, 1, 1}, {0, 0, 3, 9}, {0, 0, 1, 20}} {
			for _, interlaced := range []bool{false, true} {
				w, h := frame[2], frame[3]
				indices := gifPattern("noise", max(w, 1), max(h, 1), 4, uint32(w*7+h))[:w*h]
				if interlaced {
					indices = gifInterlaced(indices, w, h)
				}
				img := gifImage{left: uint16(frame[0]), top: uint16(frame[1]), width: uint16(w), height: uint16(h), interlaced: interlaced, minCode: 2, data: gifSubBlocks(gifPackCodes(2, gifEncode(2, indices, true)), 255)}
				add(fmt.Sprintf("frame-screen%dx%d-at%d,%d-%dx%d-interlaced%v", screen[0], screen[1], frame[0], frame[1], w, h, interlaced),
					gifSpec{width: uint16(screen[0]), height: uint16(screen[1]), global: gifPalette(4, 9), blocks: [][]byte{gifControl(1, 0, 1), img.bytes()}})
			}
		}
	}

	// LZW defects.
	{
		const w, h, bitCount = 12, 7, 3
		indices := gifPattern("noise", w, h, 1<<bitCount, 21)
		const minCode = 3
		codes := gifEncode(minCode, indices, true)
		img := func(data []byte) gifImage {
			return gifImage{width: w, height: h, minCode: minCode, data: data}
		}
		spec := func(blocks ...[]byte) gifSpec {
			return gifSpec{width: w, height: h, global: gifPalette(8, 5), blocks: blocks}
		}
		clear, end := 1<<minCode, 1<<minCode+1
		variants := map[string][]int{
			"valid":              codes,
			"no-clear":           codes[1:],
			"no-end":             codes[:len(codes)-1],
			"no-end-no-clear":    codes[1 : len(codes)-1],
			"two-clears":         append([]int{clear}, codes...),
			"clear-in-middle":    append(append(append([]int{}, codes[:len(codes)/2]...), clear), codes[len(codes)/2:]...),
			"end-first":          {end},
			"clear-end":          {clear, end},
			"clear-only":         {clear},
			"empty":              {},
			"code-next-first":    {clear, end + 1},
			"code-above-first":   {clear, end + 2},
			"code-max-first":     {clear, 4095},
			"end-early":          append(append([]int{}, codes[:len(codes)/2]...), end),
			"extra-codes":        append(append([]int{}, codes[:len(codes)-1]...), 1, 2, 3, 4, 5, 6, 7, end),
			"invalid-after":      append(append([]int{}, codes[:len(codes)-1]...), 4000, end),
			"invalid-middle":     append(append(append([]int{}, codes[:len(codes)/2]...), 4000), codes[len(codes)/2:]...),
			"invalid-after-end":  append(append([]int{}, codes...), 4000),
			"after-end-garbage":  append(append([]int{}, codes...), 1, 2, 3),
			"first-not-clear":    {0, 1, 2},
			"literal-then-clear": {1, clear, 2, end},
		}
		for name, c := range variants {
			add("lzw-"+name, spec(img(gifSubBlocks(gifPackCodes(minCode, c), 255)).bytes()))
			add("lzw-"+name+"-sub1", spec(img(gifSubBlocks(gifPackCodes(minCode, c), 1)).bytes()))
			add("lzw-"+name+"-sub3", spec(img(gifSubBlocks(gifPackCodes(minCode, c), 3)).bytes()))
		}
		// The data cut at every byte, with and without the terminator.
		packed := gifPackCodes(minCode, codes)
		for cut := 0; cut <= len(packed); cut++ {
			add(fmt.Sprintf("lzw-bytes-cut-%d", cut), spec(img(gifSubBlocks(packed[:cut], 255)).bytes()))
			add(fmt.Sprintf("lzw-bytes-cut-%d-no-terminator", cut), spec(img(append([]byte{byte(cut)}, packed[:cut]...)).bytes()))
		}
		add("lzw-sub-block-0-first", spec(img([]byte{0}).bytes()))
		add("lzw-sub-block-0-twice", spec(img([]byte{0, 0}).bytes()))
		add("lzw-no-sub-block", spec(img(nil).bytes()))
		add("lzw-sub-block-over-file", spec(img(append([]byte{255}, packed...)).bytes()))
		add("lzw-trailing-bytes-in-sub-block", spec(img(gifSubBlocks(append(append([]byte{}, packed...), 0xFF, 0xFF, 0xFF), 255)).bytes()))
		// Sub-blocks of every size.
		for size := 1; size <= 40; size++ {
			add(fmt.Sprintf("lzw-sub-size-%d", size), spec(img(gifSubBlocks(packed, size)).bytes()))
		}
		// An invalid code, or the end of the data, right after the last pixel, with sub-blocks that put it in the same block as the last pixel or not.
		last := append(append([]int{}, codes[:len(codes)-1]...), 4000)
		for size := 1; size <= 60; size++ {
			add(fmt.Sprintf("lzw-invalid-after-last-sub-%d", size), spec(img(gifSubBlocks(gifPackCodes(minCode, last), size)).bytes()))
			add(fmt.Sprintf("lzw-invalid-after-last-end-sub-%d", size), spec(img(gifSubBlocks(gifPackCodes(minCode, append(append([]int{}, codes...), 4000)), size)).bytes()))
		}
	}
	// The same at the edge of the BufReader's 8 KiB window: the stream of an image of noise whose last sub-block crosses a multiple of 8192
	// bytes in the file, found by moving the data with a comment of every length.
	{
		const minCode = 8
		w := 100
		var codes []int
		var indices []byte
		for h := 60; ; h++ {
			indices = gifPattern("noise", w, h, 256, 77)
			codes = gifEncode(minCode, indices, true)
			if len(gifPackCodes(minCode, codes)) > 7250 {
				break
			}
		}
		h := len(indices) / w
		invalid := append(append([]int{}, codes[:len(codes)-1]...), 4000)
		for pad := 0; pad < 300; pad++ {
			for name, c := range map[string][]int{"valid": codes, "invalid-after-last": invalid} {
				img := gifImage{width: uint16(w), height: uint16(h), minCode: minCode, data: gifSubBlocks(gifPackCodes(minCode, c), 255)}
				add(fmt.Sprintf("window-%s-pad%d", name, pad), gifSpec{width: uint16(w), height: uint16(h), global: gifPalette(256, 2), blocks: [][]byte{gifExtension(0xFE, make([]byte, 17+pad)), img.bytes()}})
			}
		}
	}

	// The container: extensions, unknown blocks, frames after the first, trailers, the quirk of an empty extension.
	{
		s := gifValid(6, 4, 2, "noise", 1)
		image := s.blocks[0]
		withBlocks := func(blocks ...[]byte) gifSpec {
			return gifSpec{width: 6, height: 4, global: s.global, blocks: blocks}
		}
		app := gifExtension(0xFF, append([]byte("NETSCAPE2.0"), 3, 1, 0, 0))
		comment := gifExtension(0xFE, []byte("hello"))
		text := gifExtension(0x01, make([]byte, 12))
		add("ext-application", withBlocks(app, image))
		add("ext-comment", withBlocks(comment, image))
		add("ext-text", withBlocks(text, image))
		add("ext-all", withBlocks(app, comment, text, gifControl(1, 3, 2), image))
		add("ext-unknown-label-0", withBlocks([]byte{0x21, 0x00, 0}, image))
		add("ext-unknown-label-2a", withBlocks(gifExtension(0x2A, []byte{1, 2, 3}), image))
		add("ext-unknown-label-f0", withBlocks(gifExtension(0xF0, []byte{1}), image))
		add("ext-control-length-3", withBlocks([]byte{0x21, 0xF9, 3, 1, 0, 0, 0}, image))
		add("ext-control-length-5", withBlocks([]byte{0x21, 0xF9, 5, 1, 0, 0, 0, 0, 0}, image))
		add("ext-control-extra-sub-block", withBlocks([]byte{0x21, 0xF9, 4, 1, 0, 0, 1, 2, 0xAA, 0xBB, 0}, image))
		add("ext-control-no-terminator", withBlocks([]byte{0x21, 0xF9, 4, 1, 0, 0, 1}, image))
		add("ext-comment-empty", withBlocks([]byte{0x21, 0xFE, 0}, image))
		add("ext-comment-empty-twice", withBlocks([]byte{0x21, 0xFE, 0, 0}, image))
		add("ext-comment-empty-then-trailer", gifSpec{width: 6, height: 4, global: s.global, blocks: [][]byte{{0x21, 0xFE, 0}}})
		add("ext-app-empty", withBlocks([]byte{0x21, 0xFF, 0}, image))
		add("ext-app-short-netscape", withBlocks(gifExtension(0xFF, []byte("NETSCAPE2.0\x01")), image))
		add("ext-comment-long", withBlocks(gifExtension(0xFE, make([]byte, 5000)), image))
		add("ext-only", withBlocks(comment))
		add("ext-only-control", withBlocks(gifControl(1, 0, 0)))
		add("no-blocks", withBlocks())
		add("trailer-first", gifSpec{width: 6, height: 4, global: s.global})
		add("unknown-block-type", withBlocks([]byte{0x40, 1, 2, 3}, image))
		add("zero-block-type", withBlocks([]byte{0x00}, image))
		add("no-trailer", gifSpec{width: 6, height: 4, global: s.global, blocks: [][]byte{image}, noTrailer: true})
		add("trailing-garbage", gifSpec{width: 6, height: 4, global: s.global, blocks: [][]byte{image}, trailing: icoNoise(3, 50)})
		add("two-frames", withBlocks(image, image))
		add("second-frame-broken", withBlocks(image, []byte{0x2C, 1, 2, 3}))
		add("first-frame-broken-second-ok", withBlocks(image[:len(image)-8], image))
		add("frames-different-tables", withBlocks(image, gifImage{width: 6, height: 4, minCode: 2, local: gifPalette(4, 3), data: gifSubBlocks(gifPackCodes(2, gifEncode(2, gifPattern("noise", 6, 4, 4, 2), true)), 255)}.bytes()))
		for _, sig := range []string{"GIF87a", "GIF89a", "GIF88a", "GIF89b", "GIF90a", "GIF89", "GIF", "gif89a", "GIF89A"} {
			s2 := s
			s2.sig = sig
			s2.blocks = [][]byte{image}
			add("signature-"+sig, s2)
		}
		add("background-index", gifSpec{width: 6, height: 4, global: s.global, background: 200, aspect: 49, blocks: [][]byte{image}})
		b.add("empty", nil)
		b.add("signature-only", []byte("GIF89a"))
		b.add("header-only", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00"))
	}
	// Truncations of a small file at every byte, and mutations of every byte of its headers.
	{
		s := gifValid(5, 3, 2, "noise", 8)
		s.blocks = append([][]byte{gifControl(1, 5, 2)}, s.blocks...)
		full := s.bytes()
		for cut := 0; cut < len(full); cut++ {
			b.add(fmt.Sprintf("cut-%d", cut), full[:cut])
		}
	}
	mutate := func(name string, data []byte) {
		for off := range data {
			for _, v := range []byte{0x00, 0x01, 0x02, 0x03, 0x20, 0x80, 0xFF} {
				if data[off] == v {
					continue
				}
				m := append([]byte(nil), data...)
				m[off] = v
				b.add(fmt.Sprintf("mut-%s-%d-%02x", name, off, v), m)
			}
		}
	}
	{
		for _, c := range []struct {
			name        string
			bitCount    int
			interlaced  bool
			local       bool
			transparent bool
		}{{"plain", 2, false, false, false}, {"interlaced", 2, true, false, false}, {"local", 2, false, true, false}, {"transparent", 1, false, false, true}, {"bits3", 3, false, false, false}} {
			w, h := 5, 6
			minCode := max(c.bitCount, 2)
			indices := gifPattern("noise", w, h, 1<<c.bitCount, uint32(c.bitCount))
			if c.interlaced {
				indices = gifInterlaced(indices, w, h)
			}
			img := gifImage{width: uint16(w), height: uint16(h), interlaced: c.interlaced, minCode: byte(minCode), data: gifSubBlocks(gifPackCodes(minCode, gifEncode(minCode, indices, true)), 255)}
			spec := gifSpec{width: uint16(w), height: uint16(h), global: gifPalette(1<<c.bitCount, 4)}
			if c.local {
				img.local = gifPalette(4, 5)
				spec.global = nil
			}
			if c.transparent {
				spec.blocks = append(spec.blocks, gifControl(1, 1, 1))
			}
			spec.blocks = append(spec.blocks, img.bytes())
			mutate(c.name, spec.bytes())
		}
	}

	// The limits: the screen against 512 MiB of RGBA, a frame that is not the screen against what is left of it, and the huge sizes the header can claim.
	{
		frame := func(left, top, w, h uint16) []byte {
			n := int(w) * int(h)
			if n > 200000 {
				n = 2500 // a frame that claims more than the data has
			}
			indices := gifPattern("noise", n, 1, 4, 3)
			return gifImage{left: left, top: top, width: w, height: h, minCode: 2, data: gifSubBlocks(gifPackCodes(2, gifEncode(2, indices, true)), 255)}.bytes()
		}
		for _, size := range [][2]uint16{{65535, 2048}, {65535, 2049}, {32768, 4096}, {32768, 4097}, {16384, 8192}, {16384, 8193}, {65535, 65535}, {23170, 5792}, {23170, 5793}, {0, 65535}, {65535, 0}} {
			s := gifSpec{width: size[0], height: size[1], global: gifPalette(4, 7), blocks: [][]byte{frame(0, 0, 4, 4)}}
			add(fmt.Sprintf("limit-screen-%dx%d", size[0], size[1]), s)
			// The first frame fills the screen's width and is as high as the file has data for: only a few rows.
			add(fmt.Sprintf("limit-screen-%dx%d-full-width", size[0], size[1]), gifSpec{width: size[0], height: size[1], global: gifPalette(4, 7), blocks: [][]byte{frame(0, 0, size[0], min(size[1], 1))}})
		}
		// A frame that is not the screen is read into a buffer of its own: what is left after the screen's RGBA bytes is its limit.
		for _, size := range [][4]uint16{{32768, 2048, 32768, 1}, {32768, 2048, 1, 1}, {1, 1, 4, 4}, {1, 1, 65535, 8192}, {8, 8, 65535, 1}, {2, 2, 200, 200}} {
			s := gifSpec{width: size[0], height: size[1], global: gifPalette(4, 7), blocks: [][]byte{frame(1, 0, size[2], size[3])}}
			add(fmt.Sprintf("limit-frame-%dx%d-in-%dx%d", size[2], size[3], size[0], size[1]), s)
		}
		// Frames that claim more than the data has, of the sizes at which the buffer lengths overflow.
		for _, size := range [][2]uint16{{65535, 65535}, {65535, 16384}, {65535, 8192}, {46341, 46341}, {16384, 16384}, {32768, 32768}} {
			img := gifImage{left: 1, width: size[0], height: size[1], minCode: 2, data: gifSubBlocks(gifPackCodes(2, gifEncode(2, gifPattern("noise", 50, 50, 4, 1), true)), 255)}
			add(fmt.Sprintf("limit-frame-claims-%dx%d", size[0], size[1]), gifSpec{width: 2, height: 2, global: gifPalette(4, 1), blocks: [][]byte{img.bytes()}})
			img.left = 0
			add(fmt.Sprintf("limit-frame-claims-%dx%d-from-left", size[0], size[1]), gifSpec{width: size[0], height: 2, global: gifPalette(4, 1), blocks: [][]byte{img.bytes()}})
		}
	}
	return b.cases
}
