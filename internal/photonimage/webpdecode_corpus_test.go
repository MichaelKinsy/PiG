// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// webpCase is a file of the corpus of TestDecodeWebPMatchesPhoton.
type webpCase struct {
	name string
	data []byte
}

var webpCaseNameReplacer = strings.NewReplacer(" ", "_", "[", "", "]", "")

// webpCorpusBuilder collects the cases.
type webpCorpusBuilder struct {
	cases []webpCase
	names map[string]bool
}

func (b *webpCorpusBuilder) add(name string, data []byte) {
	name = webpCaseNameReplacer.Replace(name)
	if b.names[name] {
		panic("duplicate corpus name " + name)
	}
	b.names[name] = true
	b.cases = append(b.cases, webpCase{name, data})
}

func randomWebPLossy(rng *webpRNG, w, h int, seed uint32) *webpLossyGen {
	g := &webpLossyGen{width: w, height: h, seed: seed, scaleBits: rng.intn(4), version: rng.intn(8), show: rng.chance(80)}
	g.segments = rng.chance(55)
	g.updateMap = rng.chance(60)
	g.updateData = rng.chance(75)
	g.absolute = rng.chance(50)
	for i := range 4 {
		g.quantUpdate[i] = rng.chance(70)
		g.lfUpdate[i] = rng.chance(60)
		if g.absolute {
			g.quant[i] = rng.intn(128)
			g.lf[i] = rng.intn(64)
		} else {
			g.quant[i] = rng.intn(81) - 40
			g.lf[i] = rng.intn(41) - 20
		}
		if rng.chance(10) {
			g.quant[i] = rng.intn(255) - 127
			g.lf[i] = rng.intn(127) - 63
		}
	}
	for i := range 3 {
		g.treeProbUpdate[i] = rng.chance(70)
		g.treeProbs[i] = rng.intn(256)
	}
	g.simple = rng.chance(30)
	g.level = rng.intn(64)
	if rng.chance(15) {
		g.level = 0
	}
	g.sharpness = rng.intn(8)
	g.lfAdjust = rng.chance(50)
	g.lfDeltaUpdate = rng.chance(75)
	for i := range 4 {
		g.refUpdate[i] = rng.chance(60)
		g.modeUpdate[i] = rng.chance(60)
		g.refDelta[i] = rng.intn(127) - 63
		g.modeDelta[i] = rng.intn(127) - 63
	}
	g.partitionsLog2 = rng.intn(4)
	g.yac = rng.intn(128)
	for i := range 5 {
		g.qdeltaUpdate[i] = rng.chance(35)
		g.qdelta[i] = rng.intn(31) - 15
	}
	g.refresh = rng.intn(2)
	g.tokenUpdatePct = []int{0, 0, 2, 10, 50}[rng.intn(5)]
	g.skipProb = -1
	if rng.chance(50) {
		g.skipProb = rng.intn(256)
	}
	mbs := ((w + 15) / 16) * ((h + 15) / 16)
	g.headerExtra = mbs*(6+rng.intn(30)) + 8
	g.partitionBytes = mbs*(10+rng.intn(60))/(1<<g.partitionsLog2) + 4
	return g
}

func webpLossyFile(g *webpLossyGen) []byte {
	return webpRIFF(webpChunkBytes("VP8 ", g.build()))
}

// webpCorpus is the files Photon and DecodeWebP are compared on.
func webpCorpus() []webpCase {
	b := &webpCorpusBuilder{names: map[string]bool{}}
	b.riffAndLossy()
	b.lossless()
	b.extended()
	b.libwebp()
	b.mutations()
	return b.cases
}

// libwebp adds the files libwebp wrote (testdata/webpdecode_fixtures.py): the streams of a real encoder.
func (b *webpCorpusBuilder) libwebp() {
	files, err := filepath.Glob("testdata/webp/*.webp")
	if err != nil || len(files) == 0 {
		panic("no libwebp fixtures in testdata/webp")
	}
	slices.Sort(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			panic(err)
		}
		b.add("libwebp-"+strings.TrimSuffix(filepath.Base(f), ".webp"), data)
	}
}

var webpLossySizes = [][2]int{
	{1, 1}, {2, 2}, {3, 5}, {8, 8}, {15, 15}, {16, 16}, {17, 17}, {16, 32}, {33, 17}, {31, 63}, {64, 48}, {100, 75}, {129, 65}, {1, 40}, {40, 1}, {200, 130},
}

func (b *webpCorpusBuilder) riffAndLossy() {
	// the container
	b.add("riff-empty", nil)
	b.add("riff-short", []byte("RIFF"))
	b.add("riff-no-webp", []byte("RIFF\x04\x00\x00\x00"))
	b.add("riff-bad-webp", []byte("RIFF\x04\x00\x00\x00WEBQ"))
	b.add("riff-bad-riff", append([]byte("RIFX"), webpRIFF()[4:]...))
	b.add("riff-no-chunk", webpRIFF())
	b.add("riff-unknown-first", webpRIFF(webpChunkBytes("JUNK", []byte{1, 2, 3})))
	b.add("riff-anim-first", webpRIFF(webpChunkBytes("ANIM", []byte{1, 2, 3, 4, 5, 6})))
	b.add("riff-alph-first", webpRIFF(webpChunkBytes("ALPH", []byte{0, 1, 2, 3})))
	b.add("riff-chunk-length-missing", append(webpRIFF(), "VP8 \x01\x00"...))

	// lossy frames with random parameters
	rng := newWebPRNG(7)
	for i := range 150 {
		size := webpLossySizes[rng.intn(len(webpLossySizes))]
		g := randomWebPLossy(rng, size[0], size[1], uint32(i)+1)
		b.add(fmt.Sprintf("vp8-%03d-%dx%d", i, size[0], size[1]), webpLossyFile(g))
	}

	// every sharpness with both filter types, and the filter levels around the thresholds of the hev threshold
	for _, simple := range []bool{false, true} {
		for sharp := range 8 {
			for _, level := range []int{1, 10, 20, 33, 40, 63} {
				g := randomWebPLossy(rng, 70, 50, uint32(1000+sharp*100+level))
				g.simple, g.sharpness, g.level = simple, sharp, level
				g.segments, g.lfAdjust = false, false
				b.add(fmt.Sprintf("vp8-filter-simple%v-sharp%d-level%d", simple, sharp, level), webpLossyFile(g))
			}
		}
	}

	// every number of partitions, with and without segments, with and without the skip flag
	for parts := range 4 {
		for variant := range 4 {
			g := randomWebPLossy(rng, 90, 75, uint32(2000+parts*10+variant))
			g.partitionsLog2 = parts
			g.partitionBytes = 6 * 5 * 8 * 50 / (1 << parts)
			g.segments = variant&1 != 0
			g.skipProb = -1
			if variant&2 != 0 {
				g.skipProb = 128
			}
			b.add(fmt.Sprintf("vp8-partitions%d-variant%d", parts, variant), webpLossyFile(g))
		}
	}

	// segments: absolute and delta values for the quantizer and the filter level
	for variant := range 12 {
		g := randomWebPLossy(rng, 65, 65, uint32(3000+variant))
		g.segments, g.updateMap, g.updateData = true, variant&1 != 0, variant&2 != 0
		g.absolute = variant&4 != 0
		g.lfAdjust = variant&8 != 0
		b.add(fmt.Sprintf("vp8-segments-%02d", variant), webpLossyFile(g))
	}

	// with and without data: the quantizer of a frame with no segment data ignores its base index in this decoder
	for yac := 0; yac < 128; yac += 9 {
		g := randomWebPLossy(rng, 48, 48, uint32(4000+yac))
		g.segments, g.yac = false, yac
		b.add(fmt.Sprintf("vp8-yac-%03d", yac), webpLossyFile(g))
	}

	// larger frames
	for i, size := range [][2]int{{320, 240}, {640, 480}, {1000, 700}, {2048, 8}, {8, 2048}} {
		g := randomWebPLossy(rng, size[0], size[1], uint32(5000+i))
		b.add(fmt.Sprintf("vp8-large-%dx%d", size[0], size[1]), webpLossyFile(g))
	}

	// degenerate frames
	g := randomWebPLossy(rng, 32, 32, 6000)
	base := g.build()
	tweak := func(name string, f func([]byte) []byte) {
		b.add("vp8-"+name, webpRIFF(webpChunkBytes("VP8 ", f(slices.Clone(base)))))
	}
	tweak("non-keyframe", func(d []byte) []byte { d[0] |= 1; return d })
	tweak("bad-magic", func(d []byte) []byte { d[3] ^= 1; return d })
	tweak("empty", func([]byte) []byte { return nil })
	tweak("two-bytes", func(d []byte) []byte { return d[:2] })
	tweak("tag-only", func(d []byte) []byte { return d[:3] })
	tweak("no-dimensions", func(d []byte) []byte { return d[:8] })
	tweak("first-partition-missing", func(d []byte) []byte { return d[:10] })
	tweak("first-partition-short", func(d []byte) []byte { return d[:20] })
	tweak("first-partition-huge", func(d []byte) []byte { d[2] = 0xff; return d })
	tweak("last-partition-missing", func(d []byte) []byte {
		first := int(d[0]>>5) | int(d[1])<<3 | int(d[2])<<11
		return d[:10+first+3]
	})
	tweak("trailing-bytes", func(d []byte) []byte { return append(d, 1, 2, 3, 4, 5) })
	for _, size := range [][2]int{{0, 0}, {0, 16}, {16, 0}, {1, 0}, {0, 1}, {16383, 1}, {1, 16383}} {
		g := randomWebPLossy(rng, size[0], size[1], uint32(6100+size[0]*3+size[1]))
		b.add(fmt.Sprintf("vp8-dims-%dx%d", size[0], size[1]), webpLossyFile(g))
	}
	// a frame of 16383 by 16383 pixels has no data but is far over the memory limit of RGB
	g = randomWebPLossy(rng, 16383, 16383, 6150)
	g.headerExtra, g.partitionBytes = 0, 0
	b.add("vp8-over-limit-16383x16383", webpLossyFile(g))
	for i := range 6 {
		g := randomWebPLossy(rng, 32, 32, uint32(6200+i))
		g.colorSpace = i & 1
		g.pixelType = i >> 1 & 1
		b.add(fmt.Sprintf("vp8-colorspace%d-pixeltype%d-%d", g.colorSpace, g.pixelType, i), webpLossyFile(g))
	}
	// a first partition and partitions of the minimum size
	for i := range 4 {
		g := randomWebPLossy(rng, 40, 40, uint32(6300+i))
		g.headerExtra, g.partitionBytes = 0, 0
		g.partitionsLog2 = i
		b.add(fmt.Sprintf("vp8-minimal-partitions%d", 1<<i), webpLossyFile(g))
	}
	// padding and odd chunk lengths
	odd := slices.Clone(base)
	odd = odd[:len(odd)-len(odd)%2-1]
	b.add("vp8-odd-length", webpRIFF(webpChunkBytes("VP8 ", odd)))
	b.add("vp8-odd-length-unpadded", webpRIFF(append(append([]byte("VP8 "), byte(len(odd)), byte(len(odd)>>8), 0, 0), odd...)))
	b.add("vp8-padding-dropped", webpRIFF(append(append([]byte("VP8 "), byte(len(base)+1), byte((len(base)+1)>>8), 0, 0), base...)))
	full := webpRIFF(webpChunkBytes("VP8 ", base))
	b.add("vp8-riff-size-lies", append([]byte("RIFF\xff\xff\xff\xff"), full[8:]...))
	b.add("vp8-chunk-length-lies", func() []byte { d := slices.Clone(full); d[16], d[17], d[18], d[19] = 0xff, 0xff, 0xff, 0x7f; return d }())
	b.add("vp8-chunk-length-short", func() []byte { d := slices.Clone(full); d[16] = 40; return d }())
	b.add("vp8-trailing-chunk", append(slices.Clone(full), webpChunkBytes("EXIF", []byte("Exif\x00\x00II*\x00"))...))
	b.add("vp8-trailing-garbage", append(slices.Clone(full), 1, 2, 3))
	b.add("vp8-then-vp8", webpRIFF(webpChunkBytes("VP8 ", base), webpChunkBytes("VP8 ", base)))
}

var webpLosslessSizes = [][2]int{
	{1, 1}, {2, 1}, {1, 2}, {3, 3}, {5, 7}, {8, 8}, {16, 16}, {17, 9}, {31, 33}, {64, 48}, {100, 40}, {130, 70}, {257, 3}, {3, 257},
}

func (b *webpCorpusBuilder) lossless() {
	rng := newWebPRNG(11)
	for i := range 220 {
		size := webpLosslessSizes[rng.intn(len(webpLosslessSizes))]
		g := randomWebPLossless(rng, 4)
		b.add(fmt.Sprintf("vp8l-%03d-%dx%d", i, size[0], size[1]), webpRIFF(webpChunkBytes("VP8L", g.chunk(size[0], size[1], rng.chance(50)))))
	}

	// each transform alone, with every block size
	for _, t := range []int{0, 1} {
		for bitsN := 2; bitsN <= 9; bitsN++ {
			g := randomWebPLossless(rng, 0)
			g.transforms = []int{t}
			g.predBits, g.colorBits = bitsN, bitsN
			b.add(fmt.Sprintf("vp8l-transform%d-bits%d", t, bitsN), webpRIFF(webpChunkBytes("VP8L", g.chunk(150, 90, true))))
		}
	}
	for _, size := range []int{1, 2, 3, 4, 5, 9, 16, 17, 100, 255, 256} {
		for _, w := range []int{1, 5, 8, 9, 33} {
			g := randomWebPLossless(rng, 0)
			g.transforms = []int{3}
			g.tableSize = size
			b.add(fmt.Sprintf("vp8l-palette%d-width%d", size, w), webpRIFF(webpChunkBytes("VP8L", g.chunk(w, 7, true))))
		}
	}
	// every order of the four transforms
	var permute func(prefix, rest []int)
	permute = func(prefix, rest []int) {
		if len(rest) == 0 {
			g := randomWebPLossless(rng, 0)
			g.transforms = slices.Clone(prefix)
			g.tableSize = 3 + rng.intn(30)
			b.add(fmt.Sprintf("vp8l-order-%v", prefix), webpRIFF(webpChunkBytes("VP8L", g.chunk(40, 30, true))))
			g = randomWebPLossless(rng, 0)
			g.transforms = slices.Clone(prefix)
			g.tableSize = 200
			b.add(fmt.Sprintf("vp8l-order-wide-%v", prefix), webpRIFF(webpChunkBytes("VP8L", g.chunk(41, 29, true))))
			return
		}
		for i := range rest {
			next := slices.Clone(rest)
			next = slices.Delete(next, i, i+1)
			permute(append(slices.Clone(prefix), rest[i]), next)
		}
	}
	permute(nil, []int{0, 1, 2, 3})
	for n := 1; n <= 3; n++ {
		for _, t := range [][]int{{2}, {0, 2}, {2, 0, 1}} {
			g := randomWebPLossless(rng, 0)
			g.transforms = t
			_ = n
			b.add(fmt.Sprintf("vp8l-subgreen-%v-%d", t, n), webpRIFF(webpChunkBytes("VP8L", g.chunk(20+n, 11+n, false))))
		}
	}

	// color cache bits and meta prefix codes
	for bitsN := 1; bitsN <= 11; bitsN++ {
		g := randomWebPLossless(rng, 1)
		g.cacheBits, g.cachePct = bitsN, 40
		b.add(fmt.Sprintf("vp8l-cache%d", bitsN), webpRIFF(webpChunkBytes("VP8L", g.chunk(60, 40, true))))
	}
	for bitsN := 2; bitsN <= 9; bitsN++ {
		for _, groups := range []int{1, 2, 5, 20} {
			g := randomWebPLossless(rng, 1)
			g.metaBits, g.numGroups = bitsN, groups
			b.add(fmt.Sprintf("vp8l-meta%d-groups%d", bitsN, groups), webpRIFF(webpChunkBytes("VP8L", g.chunk(120, 80, true))))
		}
	}
	// the position in the meta prefix image is a u16 expression: the product overflows from 1030x1030 with 4x4 blocks
	g := randomWebPLossless(rng, 0)
	g.transforms, g.metaBits, g.numGroups, g.cacheBits = nil, 2, 3, 0
	b.add("vp8l-meta-wrap-1030x1030", webpRIFF(webpChunkBytes("VP8L", g.chunk(1030, 1030, true))))
	g = randomWebPLossless(rng, 0)
	g.transforms, g.metaBits, g.numGroups, g.cacheBits = nil, 2, 300, 4
	b.add("vp8l-meta-wrap-groups-1100x300", webpRIFF(webpChunkBytes("VP8L", g.chunk(1100, 300, true))))

	// long copies and far distances
	for _, maxLen := range []int{1, 7, 50, 300, 4096} {
		g := randomWebPLossless(rng, 0)
		g.transforms, g.backrefPct, g.cachePct, g.maxLength = nil, 70, 0, maxLen
		b.add(fmt.Sprintf("vp8l-copies-%d", maxLen), webpRIFF(webpChunkBytes("VP8L", g.chunk(200, 100, true))))
	}

	// larger images
	for i, size := range [][2]int{{640, 480}, {1000, 700}, {2000, 3}, {3, 2000}, {16384, 1}, {1, 16384}} {
		g := randomWebPLossless(rng, 4)
		b.add(fmt.Sprintf("vp8l-large-%dx%d-%d", size[0], size[1], i), webpRIFF(webpChunkBytes("VP8L", g.chunk(size[0], size[1], true))))
	}

	// distance prefix symbols the specification does not have: the decoder reads their extra bits into a 32-bit count
	for _, s := range []int{39, 40, 41, 42, 63, 64, 65, 66, 67, 100, 255} {
		for variant := range 2 {
			g := randomWebPLossless(rng, 0)
			g.transforms, g.metaBits, g.cacheBits = nil, 0, 0
			g.backrefPct, g.cachePct, g.maxLength, g.hostileDist = 60, 0, 40, s
			size := [][2]int{{60, 40}, {17, 5}}[variant]
			b.add(fmt.Sprintf("vp8l-hostile-dist-%d-%d", s, variant), webpRIFF(webpChunkBytes("VP8L", g.chunk(size[0], size[1], true))))
		}
	}

	// images of one color: the prefix codes have one symbol each, a pixel takes no bit, the stream is a few bytes however large the image.
	// In an extended image the canvas can be smaller than the frame, then Photon returns the first pixels of the frame, whatever its size: a
	// frame of 11586 by 11586 pixels has more than the 2^27 pixels the memory limit allows for RGBA8, and 11585 by 11585 has not.
	constant := randomWebPLossless(rng, 0)
	for _, size := range [][2]int{{1, 1}, {64, 64}, {1000, 1000}} {
		argb := uint32(0x80a05010)
		b.add(fmt.Sprintf("vp8l-constant-%dx%d", size[0], size[1]), webpRIFF(webpChunkBytes("VP8L", constant.constantChunk(size[0], size[1], argb))))
		b.add(fmt.Sprintf("vp8x-vp8l-constant-%dx%d-canvas-1x1", size[0], size[1]), webpRIFF(webpVP8XChunk(0x10, 1, 1), webpChunkBytes("VP8L", constant.constantChunk(size[0], size[1], argb))))
		b.add(fmt.Sprintf("vp8x-vp8l-constant-%dx%d-canvas-2x2", size[0], size[1]), webpRIFF(webpVP8XChunk(0x10, 2, 2), webpChunkBytes("VP8L", constant.constantChunk(size[0], size[1], argb))))
	}
	b.add("vp8l-constant-11586x11586", webpRIFF(webpChunkBytes("VP8L", constant.constantChunk(11586, 11586, 0x01020304))))
	b.add("vp8l-constant-16384x16384", webpRIFF(webpChunkBytes("VP8L", constant.constantChunk(16384, 16384, 0x01020304))))
	b.add("vp8x-vp8l-constant-11586x11586-canvas-1x1", webpRIFF(webpVP8XChunk(0x10, 1, 1), webpChunkBytes("VP8L", constant.constantChunk(11586, 11586, 0x01020304))))

	// degenerate streams
	g = randomWebPLossless(rng, 2)
	base := g.chunk(40, 40, true)
	tweak := func(name string, f func([]byte) []byte) {
		b.add("vp8l-"+name, webpRIFF(webpChunkBytes("VP8L", f(slices.Clone(base)))))
	}
	tweak("bad-signature", func(d []byte) []byte { d[0] = 0x2e; return d })
	tweak("version1", func(d []byte) []byte { d[4] |= 1 << 5; return d })
	tweak("empty", func([]byte) []byte { return nil })
	tweak("signature-only", func(d []byte) []byte { return d[:1] })
	tweak("header-only", func(d []byte) []byte { return d[:5] })
	tweak("trailing-bytes", func(d []byte) []byte { return append(d, 9, 8, 7) })
	for _, cut := range []int{6, 7, 9, 12, 16, 24, 40, 60} {
		if cut < len(base) {
			tweak(fmt.Sprintf("truncated-%d", cut), func(d []byte) []byte { return d[:cut] })
		}
	}
}

// alphaChunk is an ALPH chunk of a width by height frame.
func (b *webpCorpusBuilder) alphaChunk(rng *webpRNG, filter, compression, w, h int) []byte {
	info := byte(filter<<2 | compression)
	var data []byte
	if compression == 0 {
		data = rng.bytes(w * h)
	} else {
		g := randomWebPLossless(rng, 3)
		data = g.stream(w, h)
	}
	return webpChunkBytes("ALPH", append([]byte{info}, data...))
}

func (b *webpCorpusBuilder) extended() {
	rng := newWebPRNG(23)
	lossy := func(w, h int) []byte {
		return webpChunkBytes("VP8 ", randomWebPLossy(rng, w, h, rng.next()).build())
	}
	lossless := func(w, h int) []byte {
		return webpChunkBytes("VP8L", randomWebPLossless(rng, 3).chunk(w, h, true))
	}

	// static images
	b.add("vp8x-lossy", webpRIFF(webpVP8XChunk(0, 40, 30), lossy(40, 30)))
	b.add("vp8x-lossless", webpRIFF(webpVP8XChunk(0x10, 40, 30), lossless(40, 30)))
	b.add("vp8x-lossy-icc-exif-xmp", webpRIFF(webpVP8XChunk(0x2c, 20, 20), webpChunkBytes("ICCP", rng.bytes(33)), lossy(20, 20), webpChunkBytes("EXIF", rng.bytes(10)), webpChunkBytes("XMP ", rng.bytes(11))))
	b.add("vp8x-canvas-larger-lossy", webpRIFF(webpVP8XChunk(0, 41, 30), lossy(40, 30)))
	b.add("vp8x-canvas-transposed-lossy", webpRIFF(webpVP8XChunk(0, 30, 40), lossy(40, 30)))
	b.add("vp8x-canvas-transposed-lossless", webpRIFF(webpVP8XChunk(0x10, 30, 40), lossless(40, 30)))
	b.add("vp8x-canvas-larger-lossless", webpRIFF(webpVP8XChunk(0x10, 50, 40), lossless(40, 30)))
	b.add("vp8x-canvas-smaller-lossless", webpRIFF(webpVP8XChunk(0x10, 20, 40), lossless(40, 30)))
	b.add("vp8x-canvas-tiny-lossless", webpRIFF(webpVP8XChunk(0x10, 1, 1), lossless(40, 30)))
	b.add("vp8x-unknown-before", webpRIFF(webpVP8XChunk(0, 40, 30), webpChunkBytes("JUNK", []byte{1}), lossy(40, 30)))
	b.add("vp8x-unknown-after", webpRIFF(webpVP8XChunk(0, 40, 30), lossy(40, 30), webpChunkBytes("JUNK", []byte{1, 2})))
	b.add("vp8x-unknown-between", webpRIFF(webpVP8XChunk(0, 40, 30), webpChunkBytes("JUNK", nil), webpChunkBytes("EXIF", nil), lossy(40, 30)))
	b.add("vp8x-two-images", webpRIFF(webpVP8XChunk(0, 40, 30), lossy(40, 30), lossless(40, 30)))
	b.add("vp8x-two-images-lossless-first", webpRIFF(webpVP8XChunk(0x10, 40, 30), lossless(40, 30), lossy(40, 30)))
	b.add("vp8x-no-image", webpRIFF(webpVP8XChunk(0, 40, 30), webpChunkBytes("EXIF", nil)))
	b.add("vp8x-only", webpRIFF(webpVP8XChunk(0, 40, 30)))
	b.add("vp8x-nested-riff", webpRIFF(webpVP8XChunk(0, 40, 30), webpChunkBytes("RIFF", nil), lossy(40, 30)))
	b.add("vp8x-nested-vp8x", webpRIFF(webpVP8XChunk(0, 40, 30), webpVP8XChunk(0, 40, 30), lossy(40, 30)))
	b.add("vp8x-short-header", webpRIFF(webpChunkBytes("VP8X", []byte{0, 0, 0, 0, 1, 0, 0, 1})))
	for i, flags := range []byte{0x80, 0x40, 0x01} {
		b.add(fmt.Sprintf("vp8x-reserved-flags-%d", i), webpRIFF(webpVP8XChunk(flags, 40, 30), lossy(40, 30)))
	}
	b.add("vp8x-reserved-bytes", webpRIFF(webpChunkBytes("VP8X", []byte{0, 1, 0, 0, 39, 0, 0, 29, 0, 0}), lossy(40, 30)))
	b.add("vp8x-canvas-product-overflow", webpRIFF(webpVP8XChunk(0, 1<<24, 1<<24), lossy(40, 30)))
	b.add("vp8x-canvas-over-limit-lossless", webpRIFF(webpVP8XChunk(0x10, 12000, 12000), lossless(40, 30)))
	b.add("vp8x-canvas-max-lossless", webpRIFF(webpVP8XChunk(0x10, 1<<14, 1<<14), lossless(40, 30)))
	b.add("vp8x-canvas-over-limit-lossy", webpRIFF(webpVP8XChunk(0, 1<<13, 1<<14), lossy(40, 30)))
	b.add("vp8x-vp8-chunk-before-alph", webpRIFF(webpVP8XChunk(0x10, 20, 20), lossy(20, 20), b.alphaChunk(rng, 0, 0, 20, 20), lossy(20, 20)))

	// lossy with alpha: every filter, raw and lossless compression
	for filter := range 4 {
		for compression := range 2 {
			for i, size := range [][2]int{{1, 1}, {13, 9}, {40, 33}} {
				name := fmt.Sprintf("vp8x-alph-filter%d-compression%d-%d", filter, compression, i)
				b.add(name, webpRIFF(webpVP8XChunk(0x10, size[0], size[1]), b.alphaChunk(rng, filter, compression, size[0], size[1]), lossy(size[0], size[1])))
			}
		}
	}
	b.add("vp8x-alph-bad-size", webpRIFF(webpVP8XChunk(0x10, 20, 20), b.alphaChunk(rng, 0, 0, 20, 19), lossy(20, 20)))
	b.add("vp8x-alph-canvas-larger", webpRIFF(webpVP8XChunk(0x10, 21, 20), b.alphaChunk(rng, 1, 0, 21, 20), lossy(20, 20)))
	b.add("vp8x-alph-canvas-transposed", webpRIFF(webpVP8XChunk(0x10, 20, 30), b.alphaChunk(rng, 2, 0, 20, 30), lossy(30, 20)))
	b.add("vp8x-alph-compressed-canvas-transposed", webpRIFF(webpVP8XChunk(0x10, 20, 30), b.alphaChunk(rng, 3, 1, 20, 30), lossy(30, 20)))
	b.add("vp8x-alph-no-vp8", webpRIFF(webpVP8XChunk(0x10, 20, 20), b.alphaChunk(rng, 0, 0, 20, 20)))
	b.add("vp8x-alph-then-vp8l", webpRIFF(webpVP8XChunk(0x10, 20, 20), b.alphaChunk(rng, 0, 0, 20, 20), lossless(20, 20)))
	b.add("vp8x-alph-then-unknown", webpRIFF(webpVP8XChunk(0x10, 20, 20), b.alphaChunk(rng, 0, 0, 20, 20), webpChunkBytes("JUNK", nil), lossy(20, 20)))
	for _, info := range []byte{0x40, 0x10, 0x20, 0x02, 0x03, 0x80} {
		alph := webpChunkBytes("ALPH", append([]byte{info}, rng.bytes(400)...))
		b.add(fmt.Sprintf("vp8x-alph-info-%02x", info), webpRIFF(webpVP8XChunk(0x10, 20, 20), alph, lossy(20, 20)))
	}
	b.add("vp8x-alph-empty", webpRIFF(webpVP8XChunk(0x10, 20, 20), webpChunkBytes("ALPH", nil), lossy(20, 20)))
	b.add("vp8x-alph-too-large-compressed", webpRIFF(webpVP8XChunk(0x10, 70000, 2), b.alphaChunk(rng, 0, 1, 20, 20), lossy(20, 20)))

	// animations
	anim := func(bg [4]byte, loop int) []byte {
		return webpChunkBytes("ANIM", []byte{bg[0], bg[1], bg[2], bg[3], byte(loop), byte(loop >> 8)})
	}
	bg := [4]byte{10, 20, 30, 200}
	b.add("anim-full-lossy", webpRIFF(webpVP8XChunk(0x02, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, lossy(40, 30))))
	b.add("anim-full-lossless", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(40, 30))))
	b.add("anim-full-alph", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, b.alphaChunk(rng, 1, 1, 40, 30), lossy(40, 30))))
	for _, blend := range []byte{0, 1, 2, 3} {
		for _, flags := range []byte{0x02, 0x12} {
			name := fmt.Sprintf("anim-offset-lossless-flags%02x-blend%d", flags, blend)
			b.add(name, webpRIFF(webpVP8XChunk(flags, 40, 30), anim(bg, 3), webpANMFChunk(2, 1, 20, 15, 50, blend, lossless(20, 15))))
			name = fmt.Sprintf("anim-offset-alph-flags%02x-blend%d", flags, blend)
			b.add(name, webpRIFF(webpVP8XChunk(flags, 40, 30), anim(bg, 3), webpANMFChunk(1, 3, 20, 15, 50, blend, b.alphaChunk(rng, 2, 0, 20, 15), lossy(20, 15))))
			name = fmt.Sprintf("anim-offset-lossy-flags%02x-blend%d", flags, blend)
			b.add(name, webpRIFF(webpVP8XChunk(flags, 40, 30), anim(bg, 3), webpANMFChunk(1, 3, 20, 15, 50, blend, lossy(20, 15))))
		}
	}
	// the frame flags: bit 0 disposes of the frame to the background color, bit 1 turns the blending off
	b.add("anim-dispose-full", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 1, lossless(40, 30))))
	b.add("anim-dispose-no-alpha-flag", webpRIFF(webpVP8XChunk(0x02, 40, 30), anim(bg, 0), webpANMFChunk(2, 2, 20, 10, 100, 1, lossless(20, 10))))
	b.add("anim-two-frames", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 1, lossless(40, 30)), webpANMFChunk(5, 5, 10, 10, 100, 0, lossless(10, 10))))
	b.add("anim-second-frame-broken", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 1, lossless(40, 30)), webpANMFChunk(5, 5, 10, 10, 100, 0, []byte("junk"))))
	b.add("anim-second-frame-outside", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 1, lossless(40, 30)), webpANMFChunk(30, 5, 20, 10, 100, 0, lossless(20, 10))))
	b.add("anim-no-anim-chunk", webpRIFF(webpVP8XChunk(0x12, 40, 30), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(40, 30))))
	b.add("anim-no-anim-chunk-static-after", webpRIFF(webpVP8XChunk(0x12, 40, 30), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(40, 30)), lossy(40, 30)))
	b.add("anim-anim-after-frame", webpRIFF(webpVP8XChunk(0x12, 40, 30), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(40, 30)), anim(bg, 0)))
	b.add("anim-no-frame", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0)))
	b.add("anim-no-frame-static", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), lossy(40, 30)))
	b.add("anim-two-anim", webpRIFF(webpVP8XChunk(0x02, 40, 30), anim(bg, 0), anim([4]byte{1, 2, 3, 4}, 1), webpANMFChunk(0, 0, 20, 30, 100, 0, lossless(20, 30))))
	b.add("anim-short-anim", webpRIFF(webpVP8XChunk(0x02, 40, 30), webpChunkBytes("ANIM", []byte{1, 2, 3, 4, 5}), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(40, 30))))
	b.add("anim-frame-outside", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(10, 0, 40, 30, 100, 0, lossless(40, 30))))
	b.add("anim-frame-reserved-flags", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 4, lossless(40, 30))))
	b.add("anim-frame-header-short", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpChunkBytes("ANMF", rng.bytes(12))))
	b.add("anim-frame-no-image", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0)))
	b.add("anim-frame-nested-unknown", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, webpChunkBytes("JUNK", nil), lossless(40, 30))))
	b.add("anim-frame-nested-vp8x", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, webpVP8XChunk(0, 40, 30), lossless(40, 30))))
	b.add("anim-frame-image-smaller", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(2, 2, 20, 15, 100, 0, lossless(10, 10))))
	b.add("anim-frame-image-larger", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(2, 2, 20, 15, 100, 0, lossless(30, 20))))
	b.add("anim-frame-image-smaller-same-canvas", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(30, 20))))
	b.add("anim-frame-image-larger-same-canvas", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(50, 40))))
	b.add("anim-frame-lossy-image-other-size-same-canvas", webpRIFF(webpVP8XChunk(0x02, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, lossy(30, 40))))
	b.add("anim-frame-lossy-image-other-count-same-canvas", webpRIFF(webpVP8XChunk(0x02, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, lossy(30, 30))))
	b.add("anim-unknown-chunks", webpRIFF(webpVP8XChunk(0x12, 40, 30), webpChunkBytes("JUNK", nil), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(40, 30))))
	b.add("anim-trailing-unknown", webpRIFF(webpVP8XChunk(0x12, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(40, 30)), webpChunkBytes("JUNK", nil)))
	b.add("anim-flag-without-frames-static", webpRIFF(webpVP8XChunk(0x12, 40, 30), lossless(40, 30)))
	b.add("anim-frames-without-flag", webpRIFF(webpVP8XChunk(0x10, 40, 30), anim(bg, 0), webpANMFChunk(0, 0, 40, 30, 100, 0, lossless(40, 30))))
	// frames of every alpha in front of every background: the blending of do_alpha_blending
	for i := range 12 {
		frame := randomWebPLossless(rng, 0)
		frame.transforms = []int{3}
		frame.tableSize = 8 + rng.intn(200)
		canvasBG := [4]byte{byte(rng.next()), byte(rng.next()), byte(rng.next()), byte(rng.next())}
		if i%4 == 0 {
			canvasBG[3] = 255
		}
		b.add(fmt.Sprintf("anim-blend-%02d", i), webpRIFF(webpVP8XChunk(byte(0x02|0x10*(i%2)), 33, 21), anim(canvasBG, 0),
			webpANMFChunk(1, 1, 17, 9, 10, 0, webpChunkBytes("VP8L", frame.chunk(17, 9, true)))))
	}
}

// mutations are damaged copies of the files above: Photon decides which of them still decode, and to what.
func (b *webpCorpusBuilder) mutations() {
	rng := newWebPRNG(99)
	var bases []webpCase
	for _, c := range b.cases {
		if len(c.data) > 40 && len(c.data) < 6000 && !strings.Contains(c.name, "constant") && (len(bases) == 0 || rng.chance(20) || strings.HasPrefix(c.name, "anim-") || strings.HasPrefix(c.name, "vp8x-")) {
			bases = append(bases, c)
		}
	}
	for _, c := range bases {
		count := 4
		if strings.HasPrefix(c.name, "anim-") || strings.HasPrefix(c.name, "vp8x-") {
			count = 10
		}
		for k := range count {
			d := slices.Clone(c.data)
			var kind string
			switch rng.intn(6) {
			case 0:
				d = d[:rng.intn(len(d))]
				kind = "cut"
			case 1:
				d = d[:20+rng.intn(min(len(d)-20, 60))]
				kind = "cuthead"
			case 2:
				for range 1 + rng.intn(3) {
					d[12+rng.intn(min(len(d)-12, 48))] ^= byte(1 << rng.intn(8))
				}
				kind = "flipheader"
			case 3:
				for range 1 + rng.intn(6) {
					d[rng.intn(len(d))] ^= byte(1 << rng.intn(8))
				}
				kind = "flip"
			case 4:
				start := rng.intn(len(d))
				for i := start; i < min(len(d), start+1+rng.intn(8)); i++ {
					d[i] = byte(rng.next())
				}
				kind = "scramble"
			default:
				d = append(d, rng.bytes(1+rng.intn(20))...)
				kind = "append"
			}
			b.add(fmt.Sprintf("mut-%s-%d-%s", c.name, k, kind), d)
		}
	}
}
