// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"fmt"
	"image"
)

// The inputs of the corpus of jpegdecode_corpus_test.go are built here from images (corpusImage of jpeg_oracle_test.go) or from
// random coefficients, through the writer of jpegdecode_writer_test.go.

// jdcSamplings are the sampling factors (horizontal, vertical) of the three components of the corpus' colour images.
var jdcSamplings = []struct {
	name string
	hv   [][2]uint8
}{
	{"444", [][2]uint8{{1, 1}, {1, 1}, {1, 1}}},
	{"422", [][2]uint8{{2, 1}, {1, 1}, {1, 1}}},
	{"420", [][2]uint8{{2, 2}, {1, 1}, {1, 1}}},
	{"440", [][2]uint8{{1, 2}, {1, 1}, {1, 1}}},
	{"411", [][2]uint8{{4, 1}, {1, 1}, {1, 1}}},
	{"410", [][2]uint8{{4, 2}, {1, 1}, {1, 1}}},
	{"h3", [][2]uint8{{3, 1}, {1, 1}, {1, 1}}},
	{"h3v3", [][2]uint8{{3, 3}, {1, 1}, {1, 1}}},
	{"mixed", [][2]uint8{{2, 2}, {2, 1}, {1, 2}}},
	{"inv", [][2]uint8{{1, 1}, {2, 2}, {2, 2}}},
	{"v4", [][2]uint8{{1, 4}, {1, 1}, {1, 1}}},
	{"g4", [][2]uint8{{4, 4}, {2, 2}, {1, 1}}},
	{"ni32", [][2]uint8{{3, 1}, {2, 1}, {2, 1}}},
	{"ni3", [][2]uint8{{3, 3}, {1, 2}, {1, 1}}},
}

// jdcSizes are the sizes (width, height) of the corpus' images: one pixel, one block, odd and even, and not multiples of the MCUs.
var jdcSizes = [][2]int{{1, 1}, {1, 9}, {9, 1}, {2, 3}, {7, 5}, {8, 8}, {9, 17}, {16, 16}, {33, 31}, {65, 20}, {100, 75}}

// jdcNewSpec is a frame of the sampling hv with the component ids ids (a frame with a single component is a grayscale one).
func jdcNewSpec(w, h int, hv [][2]uint8, ids []uint8, quality uint8) *jdwSpec {
	s := &jdwSpec{width: w, height: h, precision: 8, sof: 0xC0, adobe: -1}
	luma, chroma := jpegQuantTables(quality)
	for k := range 64 {
		s.qtab[0][k], s.qtab[1][k] = uint16(luma[k]), uint16(chroma[k])
		s.qtab[2][k], s.qtab[3][k] = uint16(chroma[k]), uint16(luma[k])
	}
	for i, f := range hv {
		tq := uint8(min(i, 1))
		if len(hv) == 4 && i == 3 {
			tq = 0
		}
		s.comps = append(s.comps, jdwComponent{id: ids[i], h: f[0], v: f[1], tq: tq, tdc: min(i, 1), tac: min(i, 1)})
	}
	s.jdwGrid()
	return s
}

func jdcSamplingSpec(w, h int, name string, ids []uint8) *jdwSpec {
	for _, f := range jdcSamplings {
		if f.name == name {
			return jdcNewSpec(w, h, f.hv, ids, 75)
		}
	}
	panic("unknown sampling " + name)
}

var jdcYCbCrIDs = []uint8{1, 2, 3}

func jdcClamp8(v int) uint8 { return uint8(min(max(v, 0), 255)) }

// jdcChannel is the value of component ci of the colour space for an RGB pixel.
func jdcChannel(space string, ci int, r, g, b int) int {
	switch space {
	case "gray":
		return (19595*r + 38470*g + 7471*b + 32768) >> 16
	case "rgb":
		return [3]int{r, g, b}[ci]
	case "ycbcr", "ycck":
		switch ci {
		case 0:
			return (19595*r + 38470*g + 7471*b + 32768) >> 16
		case 1:
			return int(jdcClamp8((-11056*r - 21712*g + 32768*b + 128<<16 + 32767) >> 16))
		case 2:
			return int(jdcClamp8((32768*r - 27440*g - 5328*b + 128<<16 + 32767) >> 16))
		}
	case "cmyk":
		if ci < 3 {
			return 255 - [3]int{r, g, b}[ci]
		}
	}
	return (r + g + b) / 3 // the K of YCCK
}

// fillImage sets the blocks of the components to the DCT of the planes of img in the colour space: samples subsampled by picking,
// edges replicated, the forward DCT of jpeg.go and a rounding division by the quantization table.
func (s *jdwSpec) fillImage(img *image.NRGBA, space string) {
	hmax, vmax := s.hmax(), s.vmax()
	for ci := range s.comps {
		c := &s.comps[ci]
		cw, ch := jdwCeilDiv(s.width*int(c.h), hmax), jdwCeilDiv(s.height*int(c.v), vmax)
		plane := make([]uint8, cw*ch)
		for cy := range ch {
			for cx := range cw {
				px, py := min(cx*hmax/int(c.h), s.width-1), min(cy*vmax/int(c.v), s.height-1)
				o := img.PixOffset(px, py)
				plane[cy*cw+cx] = jdcClamp8(jdcChannel(space, ci, int(img.Pix[o]), int(img.Pix[o+1]), int(img.Pix[o+2])))
			}
		}
		c.blocks = make([][64]int16, c.blockW*c.blockH)
		q := &s.qtab[c.tq]
		for by := range c.blockH {
			for bx := range c.blockW {
				var samples [64]uint8
				for j := range 8 {
					for i := range 8 {
						samples[j*8+i] = plane[min(by*8+j, ch-1)*cw+min(bx*8+i, cw-1)]
					}
				}
				var dct [64]int32
				fdct(&samples, &dct)
				for k := range 64 {
					d := int(q[k]) * 8
					v := (jdwAbs(int(dct[k])) + d/2) / d
					if dct[k] < 0 {
						v = -v
					}
					c.blocks[by*c.blockW+bx][k] = int16(v)
				}
			}
		}
	}
}

// fillRandom sets the blocks to random coefficients: "sparse" (few small AC), "dense" (every coefficient), "wide" (up to 10 bits,
// the largest an Annex K table codes), "huge" (up to 15 bits, which needs optimized tables), "dcjump" (the DC differences are the
// largest the decoder takes, so the predictor wraps), or "zero".
func (s *jdwSpec) fillRandom(kind string, rnd *corpusRNG) {
	for ci := range s.comps {
		c := &s.comps[ci]
		c.blocks = make([][64]int16, c.blockW*c.blockH)
		dc := 0
		for b := range c.blocks {
			blk := &c.blocks[b]
			switch kind {
			case "zero":
			case "dcjump":
				blk[0] = int16(2047 - int(rnd.next()>>8)%5)
				if b%7 == 6 {
					blk[0] = -2047
				}
				for k := 1; k < 64; k++ {
					if rnd.next()%9 == 0 {
						blk[unzigzag[k]] = int16(int(rnd.next()>>8)%255 - 127)
					}
				}
			default:
				step := int(rnd.next()>>8)%81 - 40
				if kind == "wide" || kind == "huge" {
					step = int(rnd.next()>>8)%1601 - 800
				}
				dc = min(max(dc+step, -1000), 1000)
				blk[0] = int16(dc)
				for k := 1; k < 64; k++ {
					r := rnd.next()
					switch kind {
					case "sparse":
						if r%(3+uint32(k)/2) == 0 {
							blk[unzigzag[k]] = int16(int(r>>8)%(41-k/2) - (40-k/2)/2)
						}
					case "dense":
						blk[unzigzag[k]] = int16(int(r>>8)%(512>>(k/8)) - (256 >> (k / 8)))
					case "wide":
						if r%3 != 0 {
							blk[unzigzag[k]] = int16(int(r>>8)%2047 - 1023)
						}
					case "huge":
						if r%4 != 0 {
							blk[unzigzag[k]] = int16(int(r>>8)%65535 - 32767)
						}
					}
				}
			}
		}
	}
	s.dcAsDiff = kind == "dcjump"
	if kind == "huge" {
		for ci := range s.comps {
			for b := range s.comps[ci].blocks {
				// DC differences must stay in the 11 bits of a DC category
				d := int(s.comps[ci].blocks[b][0])
				s.comps[ci].blocks[b][0] = int16(min(max(d, -1000), 1000))
			}
		}
	}
}

// Scan scripts.

func jdcAllComps(n int) []int {
	c := make([]int, n)
	for i := range c {
		c[i] = i
	}
	return c
}

// jdcSeqInterleaved is the one scan of a sequential frame.
func jdcSeqInterleaved(n int) []jdwScan { return []jdwScan{{comps: jdcAllComps(n), se: 63}} }

// jdcSeqSeparate is a scan per component.
func jdcSeqSeparate(n int) []jdwScan {
	var scans []jdwScan
	for i := range n {
		scans = append(scans, jdwScan{comps: []int{i}, se: 63})
	}
	return scans
}

// jdcProgScript is a progressive scan script for n components: 0 is libjpeg's jpeg_simple_progression, 1 spectral selection only,
// 2 successive approximation of the whole AC range, 3 bands of AC coefficients with a DC scan per component, 4 a long chain of
// refinements, 5 each component's DC and AC in turn.
func jdcProgScript(n int, kind int, interleavedDC bool) []jdwScan {
	all := jdcAllComps(n)
	dc := func(ah, al int) []jdwScan {
		if interleavedDC {
			return []jdwScan{{comps: all, ss: 0, se: 0, ah: ah, al: al}}
		}
		var s []jdwScan
		for i := range n {
			s = append(s, jdwScan{comps: []int{i}, ss: 0, se: 0, ah: ah, al: al})
		}
		return s
	}
	ac := func(comp, ss, se, ah, al int) jdwScan {
		return jdwScan{comps: []int{comp}, ss: ss, se: se, ah: ah, al: al}
	}
	var scans []jdwScan
	switch kind {
	case 0:
		scans = append(scans, dc(0, 1)...)
		scans = append(scans, ac(0, 1, 5, 0, 2))
		for i := n - 1; i >= 1; i-- {
			scans = append(scans, ac(i, 1, 63, 0, 1))
		}
		scans = append(scans, ac(0, 6, 63, 0, 2), ac(0, 1, 63, 2, 1))
		scans = append(scans, dc(1, 0)...)
		for i := n - 1; i >= 1; i-- {
			scans = append(scans, ac(i, 1, 63, 1, 0))
		}
		scans = append(scans, ac(0, 1, 63, 1, 0))
	case 1:
		scans = append(scans, dc(0, 0)...)
		for i := range n {
			scans = append(scans, ac(i, 1, 5, 0, 0), ac(i, 6, 63, 0, 0))
		}
	case 2:
		scans = append(scans, dc(0, 1)...)
		for i := range n {
			scans = append(scans, ac(i, 1, 63, 0, 1))
		}
		scans = append(scans, dc(1, 0)...)
		for i := range n {
			scans = append(scans, ac(i, 1, 63, 1, 0))
		}
	case 3:
		for i := range n {
			scans = append(scans, jdwScan{comps: []int{i}, ss: 0, se: 0, ah: 0, al: 0})
		}
		for i := range n {
			scans = append(scans, ac(i, 1, 2, 0, 1), ac(i, 3, 9, 0, 1), ac(i, 10, 63, 0, 1))
		}
		for i := range n {
			scans = append(scans, ac(i, 1, 2, 1, 0), ac(i, 3, 9, 1, 0), ac(i, 10, 63, 1, 0))
		}
	case 4:
		scans = append(scans, dc(0, 2)...)
		for i := range n {
			scans = append(scans, ac(i, 1, 63, 0, 3))
		}
		scans = append(scans, dc(2, 1)...)
		for i := range n {
			scans = append(scans, ac(i, 1, 63, 3, 2))
		}
		scans = append(scans, dc(1, 0)...)
		for i := range n {
			scans = append(scans, ac(i, 1, 63, 2, 1), ac(i, 1, 63, 1, 0))
		}
	default:
		for i := range n {
			scans = append(scans, jdwScan{comps: []int{i}, ss: 0, se: 0, ah: 0, al: 0}, ac(i, 1, 63, 0, 0))
		}
	}
	return scans
}

// jdcBuilder collects the cases of the corpus.
type jdcBuilder struct {
	cases   []jdcCase
	ordinal int
}

// jdcCase is one input of the corpus.
type jdcCase struct {
	name string
	data []byte
}

func (b *jdcBuilder) add(name string, data []byte) {
	b.cases = append(b.cases, jdcCase{name, data})
}

// image is the next image of the corpus' pictures.
func (b *jdcBuilder) image(kind string, w, h int) *image.NRGBA {
	b.ordinal++
	return corpusImage(kind, b.ordinal, w, h)
}

// colour is a colour JPEG of the sampling named.
func (b *jdcBuilder) colour(name string, sampling string, w, h int, scans func(n int) []jdwScan, tweak func(*jdwSpec)) {
	s := jdcSamplingSpec(w, h, sampling, jdcYCbCrIDs)
	s.fillImage(b.image("photo", w, h), "ycbcr")
	s.scans = scans(3)
	if tweak != nil {
		tweak(s)
	}
	b.add(name, s.build())
}

// jdcInterleavedFits reports whether one interleaved scan of the frame is allowed (at most 10 blocks per MCU).
func jdcInterleavedFits(s *jdwSpec) bool {
	n := 0
	for _, c := range s.comps {
		n += int(c.h) * int(c.v)
	}
	return n <= 10
}

func jdcName(parts ...any) string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += "-"
		}
		s += fmt.Sprint(p)
	}
	return s
}
