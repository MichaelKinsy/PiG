// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"fmt"
	"strings"
)

// The groups of the corpus: valid streams of every process, sampling and colour space, and the structures and failures
// jpeg-decoder 0.3.1 tells apart.

// jdcSequential: baseline and extended sequential streams of every sampling and size, interleaved or one scan per component, and
// grayscale ones.
func (b *jdcBuilder) sequential() {
	for si, sz := range jdcSizes {
		for _, f := range jdcSamplings {
			if strings.HasPrefix(f.name, "ni") {
				continue
			}
			s := jdcSamplingSpec(sz[0], sz[1], f.name, jdcYCbCrIDs)
			s.fillImage(b.image("photo", sz[0], sz[1]), "ycbcr")
			if jdcInterleavedFits(s) {
				s.scans = jdcSeqInterleaved(3)
			} else {
				s.scans = jdcSeqSeparate(3)
			}
			s.stdTables = si%2 == 0
			if si%3 == 1 {
				s.sof = 0xC1
			}
			b.add(jdcName("seq", f.name, fmt.Sprintf("%dx%d", sz[0], sz[1])), s.build())
		}
	}
	// one scan per component for the samplings that fit one interleaved scan too
	for si, sz := range jdcSizes {
		if si%3 != 0 {
			continue
		}
		for _, f := range jdcSamplings {
			if strings.HasPrefix(f.name, "ni") {
				continue
			}
			s := jdcSamplingSpec(sz[0], sz[1], f.name, jdcYCbCrIDs)
			s.fillImage(b.image("screenshot", sz[0], sz[1]), "ycbcr")
			s.scans = jdcSeqSeparate(3)
			b.add(jdcName("seqsep", f.name, fmt.Sprintf("%dx%d", sz[0], sz[1])), s.build())
		}
	}
	// the non-interleaved blocks as jpeg-decoder counts them, where they differ from the standard's
	for _, name := range []string{"h3v3", "g4", "mixed", "inv", "410"} {
		for _, sz := range [][2]int{{33, 31}, {65, 20}, {100, 75}} {
			s := jdcSamplingSpec(sz[0], sz[1], name, jdcYCbCrIDs)
			s.fillImage(b.image("photo", sz[0], sz[1]), "ycbcr")
			s.scans = jdcSeqSeparate(3)
			s.rustGeometry = true
			b.add(jdcName("seqrust", name, fmt.Sprintf("%dx%d", sz[0], sz[1])), s.build())
		}
	}
	for _, hv := range [][2]uint8{{1, 1}, {2, 2}, {1, 2}, {2, 1}, {4, 1}, {3, 3}, {4, 4}} {
		for _, sz := range jdcSizes {
			s := jdcNewSpec(sz[0], sz[1], [][2]uint8{hv}, []uint8{1}, 85)
			s.fillImage(b.image("screenshot", sz[0], sz[1]), "gray")
			s.scans = jdcSeqInterleaved(1)
			s.stdTables = true
			b.add(jdcName("gray", fmt.Sprintf("%dx%d", hv[0], hv[1]), fmt.Sprintf("%dx%d", sz[0], sz[1])), s.build())
		}
	}
}

// progressive: every script of jdcProgScript for samplings, sizes and grayscale.
func (b *jdcBuilder) progressive() {
	for script := range 6 {
		for _, name := range []string{"444", "420", "422", "440", "411", "mixed", "inv", "h3"} {
			for _, sz := range [][2]int{{9, 17}, {33, 31}, {65, 20}} {
				s := jdcSamplingSpec(sz[0], sz[1], name, jdcYCbCrIDs)
				s.fillImage(b.image("photo", sz[0], sz[1]), "ycbcr")
				s.sof = 0xC2
				s.scans = jdcProgScript(3, script, jdcInterleavedFits(s))
				s.splitDHT = script%2 == 1
				b.add(jdcName("prog", script, name, fmt.Sprintf("%dx%d", sz[0], sz[1])), s.build())
			}
		}
		for _, hv := range [][2]uint8{{1, 1}, {2, 2}} {
			for _, sz := range [][2]int{{1, 1}, {17, 9}, {100, 75}} {
				s := jdcNewSpec(sz[0], sz[1], [][2]uint8{hv}, []uint8{1}, 60)
				s.fillImage(b.image("screenshot", sz[0], sz[1]), "gray")
				s.sof = 0xC2
				s.scans = jdcProgScript(1, script, true)
				b.add(jdcName("progray", script, fmt.Sprintf("%dx%d", hv[0], hv[1]), fmt.Sprintf("%dx%d", sz[0], sz[1])), s.build())
			}
		}
	}
	// four components
	for script := range 3 {
		s := jdcNewSpec(33, 31, [][2]uint8{{1, 1}, {1, 1}, {1, 1}, {1, 1}}, []uint8{1, 2, 3, 4}, 70)
		s.fillImage(b.image("photo", 33, 31), "cmyk")
		s.sof = 0xC2
		s.adobe = 0
		s.scans = jdcProgScript(4, script, true)
		b.add(jdcName("prog4", script), s.build())
	}
}

// restarts: restart intervals, with baseline, one scan per component, and progressive scans.
func (b *jdcBuilder) restarts() {
	for _, name := range []string{"444", "420", "411"} {
		for _, sz := range [][2]int{{33, 31}, {100, 75}} {
			for _, ri := range []int{1, 2, 3, 7, 64, 1000} {
				s := jdcSamplingSpec(sz[0], sz[1], name, jdcYCbCrIDs)
				s.fillImage(b.image("photo", sz[0], sz[1]), "ycbcr")
				s.scans = jdcSeqInterleaved(3)
				s.restart = ri
				s.stdTables = ri%2 == 1
				b.add(jdcName("rst", name, fmt.Sprintf("%dx%d", sz[0], sz[1]), ri), s.build())
			}
		}
	}
	for _, ri := range []int{1, 3, 5} {
		for _, name := range []string{"420", "h3v3"} {
			s := jdcSamplingSpec(65, 20, name, jdcYCbCrIDs)
			s.fillImage(b.image("photo", 65, 20), "ycbcr")
			s.scans = jdcSeqSeparate(3)
			s.restart = ri
			b.add(jdcName("rstsep", name, ri), s.build())
		}
	}
	for _, script := range []int{0, 3, 4} {
		for _, ri := range []int{1, 2, 5} {
			for _, name := range []string{"420", "444"} {
				s := jdcSamplingSpec(65, 20, name, jdcYCbCrIDs)
				s.fillImage(b.image("photo", 65, 20), "ycbcr")
				s.sof = 0xC2
				s.scans = jdcProgScript(3, script, true)
				s.restart = ri
				b.add(jdcName("rstprog", script, name, ri), s.build())
			}
		}
	}
}

// jdcColourSpace is one case of the colour space logic of determine_color_transform.
type jdcColourSpace struct {
	name  string
	ids   []uint8
	space string
	jfif  bool
	adobe int
}

// colourSpaces: the identifiers, JFIF and Adobe markers that decide how the components become RGB, for three and four
// components, with the samplings that go with them.
func (b *jdcBuilder) colourSpaces() {
	three := []jdcColourSpace{
		{"ids123", []uint8{1, 2, 3}, "ycbcr", false, -1},
		{"ids123-adobe0", []uint8{1, 2, 3}, "ycbcr", false, 0},
		{"ids123-adobe2", []uint8{1, 2, 3}, "ycbcr", false, 2},
		{"idsRGB", []uint8{82, 71, 66}, "rgb", false, -1},
		{"idsRGB-adobe1", []uint8{82, 71, 66}, "rgb", false, 1},
		{"idsrgb", []uint8{114, 103, 98}, "rgb", false, -1},
		{"idsbgycc", []uint8{1, 34, 35}, "ycbcr", false, -1},
		{"ids789", []uint8{7, 8, 9}, "ycbcr", false, -1},
		{"ids789-jfif", []uint8{7, 8, 9}, "ycbcr", true, -1},
		{"ids789-adobe0", []uint8{7, 8, 9}, "rgb", false, 0},
		{"ids789-adobe1", []uint8{7, 8, 9}, "ycbcr", false, 1},
		{"ids789-adobe2", []uint8{7, 8, 9}, "ycbcr", false, 2},
		{"ids789-adobe3", []uint8{7, 8, 9}, "ycbcr", false, 3},
		{"ids789-jfif-adobe0", []uint8{7, 8, 9}, "ycbcr", true, 0},
		{"ids021", []uint8{0, 2, 1}, "ycbcr", false, -1},
		{"ids255", []uint8{255, 254, 253}, "ycbcr", true, -1},
	}
	for _, cs := range three {
		for _, name := range []string{"444", "420"} {
			s := jdcSamplingSpec(17, 13, name, cs.ids)
			s.fillImage(b.image("photo", 17, 13), cs.space)
			s.scans = jdcSeqInterleaved(3)
			s.jfif, s.adobe = cs.jfif, cs.adobe
			b.add(jdcName("cs3", cs.name, name), s.build())
		}
		s := jdcSamplingSpec(17, 13, "444", cs.ids)
		s.fillImage(b.image("photo", 17, 13), cs.space)
		s.sof = 0xC2
		s.scans = jdcProgScript(3, 2, true)
		s.jfif, s.adobe = cs.jfif, cs.adobe
		b.add(jdcName("cs3prog", cs.name), s.build())
	}
	four := []jdcColourSpace{
		{"none", []uint8{1, 2, 3, 4}, "cmyk", false, -1},
		{"jfif", []uint8{1, 2, 3, 4}, "cmyk", true, -1},
		{"adobe0", []uint8{1, 2, 3, 4}, "cmyk", false, 0},
		{"adobe1", []uint8{1, 2, 3, 4}, "ycck", false, 1},
		{"adobe2", []uint8{1, 2, 3, 4}, "ycck", false, 2},
		{"adobe2-ids", []uint8{67, 77, 89, 75}, "ycck", false, 2},
		{"idsRGBA", []uint8{82, 71, 66, 65}, "cmyk", false, -1},
	}
	samplings := [][][2]uint8{
		{{1, 1}, {1, 1}, {1, 1}, {1, 1}},
		{{2, 2}, {1, 1}, {1, 1}, {2, 2}},
		{{2, 1}, {1, 1}, {1, 1}, {1, 1}},
		{{1, 1}, {1, 1}, {1, 1}, {2, 2}},
	}
	for _, cs := range four {
		for si, hv := range samplings {
			s := jdcNewSpec(21, 19, hv, cs.ids, 80)
			s.fillImage(b.image("photo", 21, 19), cs.space)
			if jdcInterleavedFits(s) {
				s.scans = jdcSeqInterleaved(4)
			} else {
				s.scans = jdcSeqSeparate(4)
			}
			s.jfif, s.adobe = cs.jfif, cs.adobe
			b.add(jdcName("cs4", cs.name, si), s.build())
		}
	}
	// grayscale with the markers colour images use
	for _, cs := range []jdcColourSpace{{"plain", []uint8{1}, "gray", false, -1}, {"jfif", []uint8{9}, "gray", true, -1}, {"adobe0", []uint8{1}, "gray", false, 0}, {"adobe2", []uint8{1}, "gray", false, 2}, {"adobe9", []uint8{1}, "gray", false, 9}} {
		s := jdcNewSpec(23, 11, [][2]uint8{{1, 1}}, cs.ids, 90)
		s.fillImage(b.image("screenshot", 23, 11), cs.space)
		s.scans = jdcSeqInterleaved(1)
		s.jfif, s.adobe = cs.jfif, cs.adobe
		b.add(jdcName("cs1", cs.name), s.build())
	}
	// a number of components jpeg-decoder does not take
	for _, n := range []int{2, 5, 6} {
		hv := make([][2]uint8, n)
		ids := make([]uint8, n)
		for i := range hv {
			hv[i], ids[i] = [2]uint8{1, 1}, uint8(i+1)
		}
		s := jdcNewSpec(8, 8, hv, ids, 80)
		s.fillImage(b.image("photo", 8, 8), "cmyk")
		s.scans = jdcSeqSeparate(n)
		b.add(jdcName("cs", n, "components"), s.build())
	}
}

// losslessSpec is a lossless frame of ncomp components whose samples are a picture scaled to the precision.
func (b *jdcBuilder) losslessSpec(w, h, precision, ncomp int, predictor, pt int) *jdwSpec {
	hv := make([][2]uint8, ncomp)
	ids := make([]uint8, ncomp)
	for i := range hv {
		hv[i], ids[i] = [2]uint8{1, 1}, uint8(i+1)
	}
	s := jdcNewSpec(w, h, hv, ids, 80)
	s.sof, s.precision = 0xC3, precision
	img := b.image("photo", w, h)
	maxSample := (1 << precision) - 1
	for ci := range s.comps {
		c := &s.comps[ci]
		c.tq = 0
		c.samples = make([]uint16, w*h)
		for y := range h {
			for x := range w {
				o := img.PixOffset(x, y)
				v := jdcChannel("rgb", 0, int(img.Pix[o]), int(img.Pix[o+1]), int(img.Pix[o+2]))
				if ncomp > 1 {
					v = int(img.Pix[o+ci%3])
				}
				c.samples[y*w+x] = uint16(v * maxSample / 255)
			}
		}
	}
	s.scans = []jdwScan{{comps: jdcAllComps(ncomp), ss: predictor, se: 0, al: pt}}
	return s
}

// lossless: predictors, point transforms, precisions, component counts and restart intervals of the lossless process.
func (b *jdcBuilder) lossless() {
	for _, sz := range [][2]int{{1, 1}, {7, 5}, {33, 31}} {
		for p := range 8 {
			b.add(jdcName("ll", 8, "p", p, fmt.Sprintf("%dx%d", sz[0], sz[1])), b.losslessSpec(sz[0], sz[1], 8, 1, p, 0).build())
		}
	}
	for _, sz := range [][2]int{{7, 5}, {33, 31}} {
		for _, pt := range []int{1, 2, 4, 7} {
			for _, p := range []int{1, 4, 5} {
				b.add(jdcName("ll", 8, "pt", pt, "p", p, fmt.Sprintf("%dx%d", sz[0], sz[1])), b.losslessSpec(sz[0], sz[1], 8, 1, p, pt).build())
			}
		}
	}
	for _, precision := range []int{16, 12, 10, 9} {
		for _, p := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
			b.add(jdcName("ll", precision, "p", p), b.losslessSpec(17, 13, precision, 1, p, 0).build())
		}
		b.add(jdcName("ll", precision, "pt", 3), b.losslessSpec(17, 13, precision, 1, 4, 3).build())
		b.add(jdcName("ll", precision, "pt", 8), b.losslessSpec(17, 13, precision, 1, 4, 8).build())
	}
	for _, precision := range []int{2, 3, 4, 5, 6, 7} {
		b.add(jdcName("ll", precision, "p", 1), b.losslessSpec(9, 7, precision, 1, 1, 0).build())
		b.add(jdcName("ll", precision, "pt", 1), b.losslessSpec(9, 7, precision, 1, 4, 1).build())
	}
	for _, precision := range []int{8, 12, 16} {
		for _, ncomp := range []int{3, 4} {
			for _, p := range []int{1, 4, 6, 7} {
				b.add(jdcName("ll", precision, "c", ncomp, "p", p), b.losslessSpec(15, 11, precision, ncomp, p, 0).build())
			}
		}
	}
	for _, ri := range []int{1, 3, 5, 16, 1000} {
		for _, p := range []int{0, 1, 4, 7} {
			s := b.losslessSpec(17, 13, 8, 1, p, 0)
			s.restart = ri
			b.add(jdcName("ll", "rst", ri, "p", p), s.build())
			s = b.losslessSpec(9, 7, 8, 3, p, 0)
			s.restart = ri
			b.add(jdcName("ll", "rst3", ri, "p", p), s.build())
		}
	}
	// the precision 1 and 17, lossless frames with a scan per component, with sampling factors, with a bad predictor
	b.add("ll-precision1", b.losslessSpec(8, 8, 1, 1, 1, 0).build())
	b.add("ll-precision17", b.losslessSpec(8, 8, 17, 1, 1, 0).build())
	s := b.losslessSpec(9, 7, 8, 3, 1, 0)
	s.scans = []jdwScan{{comps: []int{0}, ss: 1}, {comps: []int{1}, ss: 1}, {comps: []int{2}, ss: 1}}
	b.add("ll-separate-scans", s.build())
	s = b.losslessSpec(9, 7, 8, 3, 1, 0)
	s.comps[0].h, s.comps[0].v = 2, 2
	b.add("ll-sampling-2x2", s.build())
	s = b.losslessSpec(9, 7, 8, 3, 1, 0)
	s.comps[0].h, s.comps[1].h = 3, 2
	b.add("ll-sampling-nonint", s.build())
	b.add("ll-predictor8", b.losslessSpec(9, 7, 8, 1, 8, 0).build())
	s = b.losslessSpec(9, 7, 8, 1, 1, 0)
	s.scans[0].se = 1
	b.add("ll-se1", s.build())
	s = b.losslessSpec(9, 7, 8, 1, 1, 0)
	s.scans[0].ah = 1
	b.add("ll-ah1", s.build())
	s = b.losslessSpec(9, 7, 8, 1, 1, 0)
	s.comps[0].tq = 1
	b.add("ll-tq1", s.build())
	// 1x1 lossless streams of every precision (the first sample comes from the predictor of the first line)
	for _, precision := range []int{2, 8, 9, 16} {
		b.add(jdcName("ll1x1", precision), b.losslessSpec(1, 1, precision, 1, 1, 0).build())
	}
}

// jdcQuant sets the quantization tables of a spec to a family.
func jdcQuant(s *jdwSpec, kind string) {
	for t := range 4 {
		for k := range 64 {
			switch kind {
			case "ones":
				s.qtab[t][k] = 1
			case "max8":
				s.qtab[t][k] = 255
			case "max16":
				s.qtab[t][k] = 65535
				s.q16 = true
			case "ramp16":
				s.qtab[t][k] = uint16(1 + (k*1021+t*4093)%65535)
				s.q16 = true
			case "q16small":
				s.qtab[t][k] *= 3
				s.q16 = true
			}
		}
	}
}

// extremes: random coefficients of every magnitude, with quantization tables that saturate the IDCT and overflow its integers.
func (b *jdcBuilder) extremes() {
	rnd := corpusRNG(0x9e3779b9)
	for _, kind := range []string{"zero", "sparse", "dense", "wide", "huge", "dcjump"} {
		for _, quant := range []string{"std", "ones", "max8", "max16", "ramp16"} {
			for _, mode := range []string{"seq", "prog"} {
				for _, name := range []string{"444", "420"} {
					s := jdcSamplingSpec(25, 19, name, jdcYCbCrIDs)
					s.fillRandom(kind, &rnd)
					if quant != "std" {
						jdcQuant(s, quant)
					}
					s.stdTables = mode == "seq" && kind != "huge"
					if mode == "seq" {
						s.scans = jdcSeqInterleaved(3)
						s.sof = 0xC1
					} else {
						s.sof = 0xC2
						s.scans = jdcProgScript(3, 0, true)
						if kind == "dcjump" {
							s.scans = jdcProgScript(3, 1, true)
						}
					}
					b.add(jdcName("ext", kind, quant, mode, name), s.build())
				}
			}
		}
	}
	// gray, one component: the IDCT's DC-only shortcuts
	for _, kind := range []string{"zero", "sparse", "dense", "wide"} {
		for _, quant := range []string{"std", "max8", "max16"} {
			s := jdcNewSpec(33, 17, [][2]uint8{{1, 1}}, []uint8{1}, 75)
			s.fillRandom(kind, &rnd)
			if quant != "std" {
				jdcQuant(s, quant)
			}
			s.scans = jdcSeqInterleaved(1)
			b.add(jdcName("extgray", kind, quant), s.build())
		}
	}
}

// truncated: streams cut short at positions through the data, with and without an EOI after the cut.
func (b *jdcBuilder) truncated() {
	bases := map[string][]byte{}
	s := jdcSamplingSpec(33, 31, "420", jdcYCbCrIDs)
	s.fillImage(b.image("photo", 33, 31), "ycbcr")
	s.scans = jdcSeqInterleaved(3)
	bases["seq420"] = s.build()
	s = jdcSamplingSpec(33, 31, "444", jdcYCbCrIDs)
	s.fillImage(b.image("photo", 33, 31), "ycbcr")
	s.sof = 0xC2
	s.scans = jdcProgScript(3, 0, true)
	bases["prog444"] = s.build()
	s = jdcNewSpec(17, 9, [][2]uint8{{1, 1}}, []uint8{1}, 80)
	s.fillImage(b.image("screenshot", 17, 9), "gray")
	s.scans = jdcSeqInterleaved(1)
	bases["gray"] = s.build()
	s = jdcSamplingSpec(33, 31, "422", jdcYCbCrIDs)
	s.fillImage(b.image("photo", 33, 31), "ycbcr")
	s.scans = jdcSeqSeparate(3)
	s.restart = 4
	bases["sep422rst"] = s.build()
	for _, name := range []string{"seq420", "prog444", "gray", "sep422rst"} {
		data := bases[name]
		for i := 1; i < 24; i++ {
			cut := len(data) * i / 24
			b.add(jdcName("trunc", name, cut), data[:cut])
			if i%2 == 0 {
				b.add(jdcName("trunceoi", name, cut), append(bytes.Clone(data[:cut]), 0xFF, 0xD9))
			}
		}
		for _, drop := range []int{1, 2, 3, 5, 9, 17} {
			b.add(jdcName("truncend", name, drop), data[:len(data)-drop])
		}
	}
}

func (b *jdcBuilder) all() []jdcCase {
	b.sequential()
	b.progressive()
	b.restarts()
	b.colourSpaces()
	b.lossless()
	b.extremes()
	b.truncated()
	b.structure()
	b.fixtures()
	b.mutants()
	return b.cases
}
