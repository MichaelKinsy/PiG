// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// tiffCase is a file of the corpus compared with Photon.
type tiffCase struct {
	name string
	data []byte
	// build makes the data of a file too big to keep with the others (data is nil).
	build func() []byte
}

// bytes is the file's data.
func (c tiffCase) bytes() []byte {
	if c.build != nil {
		return c.build()
	}
	return c.data
}

// tiffColorKind is a colour layout of the corpus.
type tiffColorKind struct {
	name        string
	samples     int
	bits        int
	photometric int
	extra       bool
	// jpeg is whether the 8-bit JPEG branch of the corpus covers it.
	jpeg bool
}

var tiffColorKinds = []tiffColorKind{
	{"gray8", 1, 8, tiffBlackIsZero, false, true},
	{"gray16", 1, 16, tiffBlackIsZero, false, false},
	{"white8", 1, 8, tiffWhiteIsZero, false, true},
	{"white16", 1, 16, tiffWhiteIsZero, false, false},
	{"rgb8", 3, 8, tiffRGB, false, true},
	{"rgb16", 3, 16, tiffRGB, false, false},
	{"rgba8", 4, 8, tiffRGB, true, false},
	{"rgba16", 4, 16, tiffRGB, true, false},
	{"cmyk8", 4, 8, tiffCMYK, false, false},
}

var tiffCompressions = []struct {
	name string
	id   int
}{
	{"none", tiffCompressionNone},
	{"lzw", tiffCompressionLZW},
	{"packbits", tiffCompressionPackBits},
	{"deflate", tiffCompressionDeflate},
	{"olddeflate", tiffCompressionOldDeflate},
	{"jpeg", tiffCompressionModernJPEG},
}

// tiffLayouts are the strip and tile layouts the grid covers, for an image of 37 x 23 pixels.
var tiffLayouts = []struct {
	name         string
	rowsPerStrip int
	tileW, tileH int
	planar       bool
}{
	{name: "strip"},
	{name: "rps1", rowsPerStrip: 1},
	{name: "rps5", rowsPerStrip: 5},
	{name: "tile16", tileW: 16, tileH: 16},
	{name: "tile64", tileW: 64, tileH: 64},
	{name: "tile8x32", tileW: 8, tileH: 32},
	{name: "planar", planar: true, rowsPerStrip: 7},
	{name: "planartile", planar: true, tileW: 16, tileH: 16},
}

func tiffEndName(little bool) string {
	if little {
		return "le"
	}
	return "be"
}

// tiffCorpus is the files whose decoding the test compares with Photon: the format grid (colours, compressions, predictors, layouts,
// byte orders), LZW and PackBits edge streams, the formats and tags tiff refuses, tag types and values the reader converts in its own
// ways, damaged headers and directories, truncations, seeded corruptions of the compressed data, and the size limits.
func tiffCorpus() []tiffCase {
	var cases []tiffCase
	seen := map[string]bool{}
	add := func(name string, data []byte) {
		if seen[name] {
			panic("duplicate corpus name " + name)
		}
		seen[name] = true
		cases = append(cases, tiffCase{name: name, data: data})
	}
	addLazy := func(name string, build func() []byte) {
		if seen[name] {
			panic("duplicate corpus name " + name)
		}
		seen[name] = true
		cases = append(cases, tiffCase{name: name, build: build})
	}
	spec := func(little bool, kind tiffColorKind, comp int) tiffSpec {
		return tiffSpec{little: little, width: 37, height: 23, samples: kind.samples, bits: kind.bits, photometric: kind.photometric,
			compression: comp, extraSamples: kind.extra, seed: uint32(kind.samples*7 + kind.bits)}
	}

	// The grid.
	for _, little := range []bool{true, false} {
		for _, kind := range tiffColorKinds {
			for _, comp := range tiffCompressions {
				for _, predictor := range []int{0, tiffPredictorHorizontal} {
					for _, layout := range tiffLayouts {
						if comp.id == tiffCompressionModernJPEG && (!kind.jpeg || predictor != 0) {
							continue
						}
						// Strip layouts for every combination, the rest for the compressions and colours that differ in the code.
						if layout.name != "strip" && layout.name != "tile16" && (kind.samples == 4 && kind.bits == 16 || comp.id == tiffCompressionOldDeflate) {
							continue
						}
						s := spec(little, kind, comp.id)
						s.predictor = predictor
						s.rowsPerStrip, s.tileW, s.tileH, s.planar = layout.rowsPerStrip, layout.tileW, layout.tileH, layout.planar
						if comp.id == tiffCompressionModernJPEG && s.planar && kind.samples != 1 {
							s.jpegTables = true
						}
						add(fmt.Sprintf("grid-%s-%s-%s-p%d-%s", tiffEndName(little), kind.name, comp.name, predictor, layout.name), tiffBuild(s))
					}
				}
			}
		}
	}

	// JPEG: tables in JPEGTables, quality, sizes (restart intervals and subsampling are the JPEG decoder's own corpus).
	for _, little := range []bool{true, false} {
		for _, kind := range []tiffColorKind{tiffColorKinds[0], tiffColorKinds[2], tiffColorKinds[4]} {
			for _, layout := range tiffLayouts[:6] {
				s := spec(little, kind, tiffCompressionModernJPEG)
				s.rowsPerStrip, s.tileW, s.tileH = layout.rowsPerStrip, layout.tileW, layout.tileH
				s.jpegTables = true
				add(fmt.Sprintf("jpegtables-%s-%s-%s", tiffEndName(little), kind.name, layout.name), tiffBuild(s))
				s.jpegQuality = 20
				s.shortOffsets = true
				add(fmt.Sprintf("jpegq20-%s-%s-%s", tiffEndName(little), kind.name, layout.name), tiffBuild(s))
			}
		}
	}

	// Sizes.
	for _, size := range [][2]int{{1, 1}, {1, 9}, {9, 1}, {2, 2}, {3, 5}, {16, 16}, {17, 17}, {64, 3}, {3, 64}, {255, 2}, {256, 2}, {257, 2}} {
		for _, kind := range []tiffColorKind{tiffColorKinds[0], tiffColorKinds[1], tiffColorKinds[4], tiffColorKinds[6], tiffColorKinds[8]} {
			for _, comp := range tiffCompressions[:5] {
				s := spec(true, kind, comp.id)
				s.width, s.height = size[0], size[1]
				add(fmt.Sprintf("size-%dx%d-%s-%s", size[0], size[1], kind.name, comp.name), tiffBuild(s))
			}
		}
	}

	// LZW: streams that fill and clear the table, switch the code size, start without a clear code, or end without an end code.
	for _, little := range []bool{true, false} {
		for _, kind := range []tiffColorKind{tiffColorKinds[0], tiffColorKinds[4], tiffColorKinds[5]} {
			for _, opt := range []struct {
				name string
				lzw  tiffLZWOptions
			}{
				{"default", tiffLZWOptions{}},
				{"never-clear", tiffLZWOptions{clearAt: 4096}},
				{"clear3000", tiffLZWOptions{clearAt: 3000}},
				{"clear600", tiffLZWOptions{clearAt: 600}},
				{"late-switch", tiffLZWOptions{lateSwitch: true}},
				{"no-initial-clear", tiffLZWOptions{noInitialClear: true}},
				{"no-end", tiffLZWOptions{noEnd: true}},
				{"no-clear-no-end", tiffLZWOptions{noInitialClear: true, noEnd: true}},
			} {
				for _, predictor := range []int{0, tiffPredictorHorizontal} {
					s := spec(little, kind, tiffCompressionLZW)
					s.width, s.height = 150, 120
					s.predictor = predictor
					s.lzw = opt.lzw
					s.gen = func(x, y, c int) uint32 {
						v := tiffSample(7, x*3+y, y*5+x, c, kind.bits)
						if (x+y)%13 < 5 {
							return uint32(x/5+c) & 0xFF
						}
						return v
					}
					add(fmt.Sprintf("lzw-%s-%s-%s-p%d", tiffEndName(little), kind.name, opt.name, predictor), tiffBuild(s))
				}
			}
		}
	}

	// Formats and tags tiff refuses or reads in a way of its own.
	refuse := func(name string, s tiffSpec) { add("refuse-"+name, tiffBuild(s)) }
	for _, little := range []bool{true, false} {
		e := tiffEndName(little)
		base := func(kind tiffColorKind, comp int) tiffSpec { return spec(little, kind, comp) }
		for _, bits := range []int{1, 2, 3, 4, 5, 6, 7, 9, 10, 12, 15, 24, 32} {
			s := base(tiffColorKinds[0], tiffCompressionNone)
			s.bits = bits
			refuse(fmt.Sprintf("%s-gray-%dbit", e, bits), s)
			s.compression = tiffCompressionLZW
			s.photometric = tiffWhiteIsZero
			refuse(fmt.Sprintf("%s-white-%dbit-lzw", e, bits), s)
			s = base(tiffColorKinds[4], tiffCompressionNone)
			s.bits = bits
			refuse(fmt.Sprintf("%s-rgb-%dbit", e, bits), s)
			s = base(tiffColorKinds[8], tiffCompressionNone)
			s.bits = bits
			refuse(fmt.Sprintf("%s-cmyk-%dbit", e, bits), s)
		}
		for _, photometric := range []int{3, 4, 6, 8, 9, 32844, 0xFFFF} {
			for _, samples := range []int{1, 2, 3, 4} {
				s := base(tiffColorKinds[4], tiffCompressionNone)
				s.photometric, s.samples = photometric, samples
				refuse(fmt.Sprintf("%s-photometric%d-%dsamples", e, photometric, samples), s)
			}
		}
		for _, samples := range []int{2, 5, 6} {
			for _, photometric := range []int{tiffBlackIsZero, tiffWhiteIsZero, tiffRGB, tiffCMYK, tiffYCbCr} {
				for _, bits := range []int{8, 16} {
					s := base(tiffColorKinds[4], tiffCompressionNone)
					s.samples, s.photometric, s.bits = samples, photometric, bits
					refuse(fmt.Sprintf("%s-%dsamples-photometric%d-%dbit", e, samples, photometric, bits), s)
				}
			}
		}
		for _, format := range [][]uint16{{1}, {2}, {3}, {4}, {5}, {0}, {1, 1, 1}, {1, 2, 1}, {2, 2, 2}, {3, 3, 3}, {1, 1, 1, 1}, {0xFFFF}, {}} {
			for _, kind := range []tiffColorKind{tiffColorKinds[0], tiffColorKinds[4], tiffColorKinds[1]} {
				s := base(kind, tiffCompressionNone)
				s.sampleFormat = format
				if len(format) == 0 {
					s.tweak = func(p *tiffParts) { p.set(tiffOrder{little}.shorts(tiffTagSampleFormat)) }
				}
				refuse(fmt.Sprintf("%s-sampleformat-%v-%s", e, format, kind.name), s)
			}
		}
		for _, predictor := range []int{1, 2, 3, 4, 5, 0x8000} {
			for _, kind := range []tiffColorKind{tiffColorKinds[0], tiffColorKinds[1], tiffColorKinds[4]} {
				s := base(kind, tiffCompressionNone)
				s.predictor = predictor
				if predictor == 1 {
					s.tweak = func(p *tiffParts) { p.set(tiffOrder{little}.shorts(tiffTagPredictor, 1)) }
				}
				refuse(fmt.Sprintf("%s-predictor%d-%s", e, predictor, kind.name), s)
			}
		}
		for _, comp := range []int{0, 2, 3, 4, 6, 9, 10, 34712, 32771, 32773, 32946, 0xFFFF} {
			s := base(tiffColorKinds[0], comp)
			refuse(fmt.Sprintf("%s-compression%d", e, comp), s)
		}
		// Samples whose bit depths differ, a bit depth list of the wrong length, and values that do not fit a byte.
		for _, bits := range [][]uint64{{8, 8, 16}, {8, 8, 8, 8}, {8, 8}, {8}, {16}, {}, {8, 8, 8, 0}, {256, 256, 256}, {65535, 65535, 65535}, {8, 16, 8}} {
			s := base(tiffColorKinds[4], tiffCompressionNone)
			s.tweak = func(p *tiffParts) { p.set(tiffOrder{little}.shorts(tiffTagBitsPerSample, bits...)) }
			refuse(fmt.Sprintf("%s-bitslist-%v", e, bits), s)
		}
		s := base(tiffColorKinds[4], tiffCompressionNone)
		s.tweak = func(p *tiffParts) { p.remove(tiffTagBitsPerSample) }
		refuse(e+"-no-bitspersample-rgb", s)
		s = base(tiffColorKinds[0], tiffCompressionNone)
		s.tweak = func(p *tiffParts) { p.remove(tiffTagBitsPerSample) }
		refuse(e+"-no-bitspersample-gray", s)
		// A single BitsPerSample for three samples is read as the value of all of them (libtiff's way).
		s = base(tiffColorKinds[4], tiffCompressionNone)
		s.tweak = func(p *tiffParts) { p.set(tiffOrder{little}.shorts(tiffTagBitsPerSample, 8)) }
		refuse(e+"-one-bitspersample-rgb", s)
		s = base(tiffColorKinds[5], tiffCompressionLZW)
		s.tweak = func(p *tiffParts) { p.set(tiffOrder{little}.shorts(tiffTagBitsPerSample, 16)) }
		refuse(e+"-one-bitspersample-rgb16", s)
		// Planar images of the colour types: RGB panics in image's copy_from_slice, CMYK is read as four-byte pixels of the first plane.
		for _, kind := range tiffColorKinds {
			for _, comp := range tiffCompressions[:4] {
				s := base(kind, comp.id)
				s.planar = true
				s.rowsPerStrip = 4
				refuse(fmt.Sprintf("%s-planar-%s-%s", e, kind.name, comp.name), s)
				s.width, s.height = 8, 8
				refuse(fmt.Sprintf("%s-planar8x8-%s-%s", e, kind.name, comp.name), s)
				s.width, s.height = 5, 7
				s.rowsPerStrip = 0
				refuse(fmt.Sprintf("%s-planar5x7-%s-%s", e, kind.name, comp.name), s)
			}
		}
		// JPEG compression under colours and depths it cannot carry.
		for _, kind := range tiffColorKinds {
			s := base(kind, tiffCompressionModernJPEG)
			refuse(fmt.Sprintf("%s-jpeg-%s", e, kind.name), s)
			s.jpegTables = true
			refuse(fmt.Sprintf("%s-jpegtables-%s", e, kind.name), s)
		}
	}

	addTagTypeCorpus(add)
	addStructureCorpus(add)
	addCorruptionCorpus(add)
	addLimitCorpus(add)
	addStreamCorpus(add)
	addDeflateCorpus(add)
	addRealCorpus(add)
	addValueCountCorpus(addLazy)
	return cases
}

// tiffTypeNames are the entry types the tag-type corpus puts values in.
var tiffTypeNames = map[uint16]string{
	tiffTypeByte: "byte", tiffTypeASCII: "ascii", tiffTypeShort: "short", tiffTypeLong: "long", tiffTypeRational: "rational", tiffTypeSByte: "sbyte",
	tiffTypeUndefined: "undefined", tiffTypeSShort: "sshort", tiffTypeSLong: "slong", tiffTypeSRational: "srational", tiffTypeFloat: "float",
	tiffTypeDouble: "double", tiffTypeIFD: "ifd", tiffTypeLong8: "long8", tiffTypeSLong8: "slong8", tiffTypeIFD8: "ifd8",
}

// entryValues reads the integer values of an entry of a spec's parts (their types are the ones tiffBuild wrote: SHORT or LONG).
func entryValues(o tiffOrder, e *tiffEntrySpec) []uint64 {
	size := int(tiffTypeSize(e.typ))
	var vals []uint64
	for i := 0; i+size <= len(e.data); i += size {
		switch size {
		case 1:
			vals = append(vals, uint64(e.data[i]))
		case 2:
			vals = append(vals, uint64(o.order().Uint16(e.data[i:])))
		case 4:
			vals = append(vals, uint64(o.order().Uint32(e.data[i:])))
		default:
			vals = append(vals, o.order().Uint64(e.data[i:]))
		}
	}
	return vals
}

// addTagTypeCorpus changes the type and the count of each tag the decoder reads: the same values as every entry type, as a list of
// two or three values, and with no values at all.
func addTagTypeCorpus(add func(string, []byte)) {
	type target struct {
		name string
		spec func(little bool) tiffSpec
		tag  uint16
	}
	rgbStrips := func(comp int) func(little bool) tiffSpec {
		return func(little bool) tiffSpec {
			kind := tiffColorKinds[4]
			return tiffSpec{little: little, width: 13, height: 11, samples: 3, bits: 8, photometric: tiffRGB, compression: comp, rowsPerStrip: 4, seed: 3, predictor: tiffPredictorHorizontal, jpegTables: comp == tiffCompressionModernJPEG, extraSamples: kind.extra}
		}
	}
	tiles := func(little bool) tiffSpec {
		return tiffSpec{little: little, width: 13, height: 11, samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: tiffCompressionPackBits, tileW: 8, tileH: 8, seed: 5, sampleFormat: []uint16{1}}
	}
	targets := []target{
		{"width", rgbStrips(1), tiffTagImageWidth},
		{"height", rgbStrips(1), tiffTagImageLength},
		{"bits", rgbStrips(1), tiffTagBitsPerSample},
		{"compression", rgbStrips(tiffCompressionLZW), tiffTagCompression},
		{"photometric", rgbStrips(1), tiffTagPhotometricInterpretation},
		{"samples", rgbStrips(1), tiffTagSamplesPerPixel},
		{"predictor", rgbStrips(1), tiffTagPredictor},
		{"rowsperstrip", rgbStrips(1), tiffTagRowsPerStrip},
		{"stripoffsets", rgbStrips(1), tiffTagStripOffsets},
		{"stripcounts", rgbStrips(1), tiffTagStripByteCounts},
		{"jpegtables", rgbStrips(tiffCompressionModernJPEG), tiffTagJPEGTables},
		{"tilewidth", tiles, tiffTagTileWidth},
		{"tilelength", tiles, tiffTagTileLength},
		{"tileoffsets", tiles, tiffTagTileOffsets},
		{"tilecounts", tiles, tiffTagTileByteCounts},
		{"sampleformat", tiles, tiffTagSampleFormat},
	}
	types := []uint16{tiffTypeByte, tiffTypeASCII, tiffTypeShort, tiffTypeLong, tiffTypeRational, tiffTypeSByte, tiffTypeUndefined, tiffTypeSShort,
		tiffTypeSLong, tiffTypeSRational, tiffTypeFloat, tiffTypeDouble, tiffTypeIFD, tiffTypeLong8, tiffTypeSLong8, tiffTypeIFD8, 0, 14, 15, 19, 0xFFFF}
	for _, little := range []bool{true, false} {
		o := tiffOrder{little}
		e := tiffEndName(little)
		for _, t := range targets {
			// Each type, with the values the entry had.
			for _, typ := range types {
				s := t.spec(little)
				name := tiffTypeNames[typ]
				if name == "" {
					name = fmt.Sprintf("type%d", typ)
				}
				s.tweak = func(p *tiffParts) {
					entry := p.find(t.tag)
					if t.tag == tiffTagStripOffsets || t.tag == tiffTagStripByteCounts || t.tag == tiffTagTileOffsets || t.tag == tiffTagTileByteCounts {
						if t.tag == tiffTagStripOffsets || t.tag == tiffTagTileOffsets {
							p.offsetType = typ
						} else {
							p.countType = typ
						}
						return
					}
					if entry == nil {
						return
					}
					vals := entryValues(o, entry)
					if !tiffKnownType(typ) {
						*entry = tiffEntrySpec{tag: t.tag, typ: typ, count: uint32(len(vals)), data: o.u32(uint32(vals[0]))}
						return
					}
					*entry = o.ints(t.tag, typ, vals...)
				}
				add(fmt.Sprintf("tagtype-%s-%s-%s", e, t.name, name), tiffBuild(s))
			}
			// Counts: none, two, three values.
			for _, count := range []int{0, 2, 3} {
				for _, typ := range []uint16{tiffTypeShort, tiffTypeLong, tiffTypeUndefined, tiffTypeASCII} {
					if t.tag == tiffTagStripOffsets || t.tag == tiffTagStripByteCounts || t.tag == tiffTagTileOffsets || t.tag == tiffTagTileByteCounts {
						continue
					}
					s := t.spec(little)
					s.tweak = func(p *tiffParts) {
						entry := p.find(t.tag)
						if entry == nil {
							return
						}
						vals := entryValues(o, entry)
						var list []uint64
						for i := range count {
							list = append(list, vals[i%len(vals)])
						}
						*entry = o.ints(t.tag, typ, list...)
					}
					add(fmt.Sprintf("tagcount-%s-%s-%s-%d", e, t.name, tiffTypeNames[typ], count), tiffBuild(s))
				}
			}
			// The entry missing, or twice (the later one wins).
			s := t.spec(little)
			s.tweak = func(p *tiffParts) {
				switch t.tag {
				case tiffTagStripOffsets, tiffTagTileOffsets:
					p.skipOffsets = true
				case tiffTagStripByteCounts, tiffTagTileByteCounts:
					p.skipCounts = true
				default:
					p.remove(t.tag)
				}
			}
			add(fmt.Sprintf("tagmissing-%s-%s", e, t.name), tiffBuild(s))
			s = t.spec(little)
			s.tweak = func(p *tiffParts) {
				entry := p.find(t.tag)
				if entry == nil {
					return
				}
				// The later entry of a tag with the same value but a bad type; assemble sorts stably, so the added one is later.
				bad := *entry
				bad.typ, bad.data, bad.count = tiffTypeSByte, []byte{1}, 1
				p.entries = append(p.entries, bad)
			}
			add(fmt.Sprintf("tagtwice-%s-%s", e, t.name), tiffBuild(s))
		}
	}
}

// addStructureCorpus damages the file's structure: the header, the directory, the strip and tile bookkeeping.
func addStructureCorpus(add func(string, []byte)) {
	for _, little := range []bool{true, false} {
		e := tiffEndName(little)
		o := tiffOrder{little}
		base := func(comp int) tiffSpec {
			return tiffSpec{little: little, width: 11, height: 9, samples: 3, bits: 8, photometric: tiffRGB, compression: comp, rowsPerStrip: 4, seed: 9}
		}
		withTweak := func(comp int, f func(p *tiffParts)) []byte {
			s := base(comp)
			s.tweak = f
			return tiffBuild(s)
		}
		add("struct-"+e+"-plain", tiffBuild(base(tiffCompressionNone)))

		// Signature, IFD placement and the chain of directories.
		for _, sig := range []string{"II+\x00", "MM\x00+", "II*\x01", "MM\x01*", "IM*\x00", "MI\x00*", "II\x2a\x00", "II\x00\x2a", "MM*\x00", "\x00\x00\x00\x00", "II*", "MM\x00"} {
			p := tiffPartsOf(base(tiffCompressionNone))
			p.signature = []byte(sig)
			add(fmt.Sprintf("struct-%s-signature-%x", e, sig), p.assemble())
		}
		add("struct-"+e+"-ifdfirst", withTweak(tiffCompressionNone, func(p *tiffParts) { p.ifdFirst = true }))
		add("struct-"+e+"-ifdfirst-lzw", withTweak(tiffCompressionLZW, func(p *tiffParts) { p.ifdFirst = true }))
		add("struct-"+e+"-pad", withTweak(tiffCompressionNone, func(p *tiffParts) { p.pad = 13 }))
		for _, offset := range []uint32{0, 1, 2, 4, 6, 7, 8, 9, 100, 1000, 0x7FFFFFFF, 0xFFFFFFFF} {
			data := tiffBuild(base(tiffCompressionNone))
			copy(data[4:], o.u32(offset))
			add(fmt.Sprintf("struct-%s-ifdoffset-%d", e, offset), data)
		}
		for _, next := range []int{0, 1, -1, 2, 1000, -2, 0x7FFFFFFF} {
			data := tiffBuild(base(tiffCompressionNone))
			ifd := int(o.order().Uint32(data[4:]))
			count := int(o.order().Uint16(data[ifd:]))
			at := ifd + 2 + 12*count
			switch next {
			case -1: // the first IFD again: a cycle
				copy(data[at:], data[4:8])
			case -2: // an IFD that is a copy of the first one, placed after the file
				dup := slices.Clone(data[ifd : at+4])
				newOffset := len(data)
				data = append(data, dup...)
				o.order().PutUint32(data[at:], uint32(newOffset))
			default:
				o.order().PutUint32(data[at:], uint32(next))
			}
			add(fmt.Sprintf("struct-%s-nextifd-%d", e, next), data)
		}
		// A second image of another size, in the second IFD: the first is the one read.
		{
			data := tiffBuild(base(tiffCompressionNone))
			ifd := int(o.order().Uint32(data[4:]))
			count := int(o.order().Uint16(data[ifd:]))
			at := ifd + 2 + 12*count
			second := slices.Clone(data[ifd : at+4])
			o.order().PutUint32(second[len(second)-4:], 0)
			o.order().PutUint32(data[at:], uint32(len(data)))
			add("struct-"+e+"-secondifd", append(data, second...))
		}

		// Cut the entry table: an IFD that claims more entries than the file has, or fewer.
		for _, delta := range []int{-3, -1, 1, 2, 50, 0x7FFF} {
			data := tiffBuild(base(tiffCompressionNone))
			ifd := int(o.order().Uint32(data[4:]))
			count := int(o.order().Uint16(data[ifd:]))
			o.order().PutUint16(data[ifd:], uint16(count+delta))
			add(fmt.Sprintf("struct-%s-entrycount%+d", e, delta), data)
		}
		// Entries of an unknown type are skipped.
		add("struct-"+e+"-unknowntype", withTweak(tiffCompressionNone, func(p *tiffParts) {
			p.entries = append(p.entries, tiffEntrySpec{tag: 40000, typ: 99, count: 7, data: []byte{1, 2, 3, 4}}, tiffEntrySpec{tag: 40001, typ: 0, count: 1, data: []byte{1, 2, 3, 4}})
		}))
		// Tags nobody reads, with values that would not convert.
		add("struct-"+e+"-junktags", withTweak(tiffCompressionNone, func(p *tiffParts) {
			p.entries = append(p.entries,
				tiffEntrySpec{tag: 270, typ: tiffTypeASCII, count: 6, data: []byte("junk\xff\x00")},
				tiffEntrySpec{tag: 305, typ: tiffTypeASCII, count: 3, data: []byte("ab\x01")},
				tiffEntrySpec{tag: 50000, typ: tiffTypeLong, count: 100000, data: o.u32(0xFFFFFF)},
				o.ints(282, tiffTypeRational, 1<<32|1), o.ints(339+10, tiffTypeDouble, 0),
			)
		}))
		add("struct-"+e+"-extrasamples", withTweak(tiffCompressionNone, func(p *tiffParts) { p.entries = append(p.entries, o.shorts(338, 0, 1, 2, 3)) }))
		add("struct-"+e+"-extrasamples-rgba", func() []byte {
			s := base(tiffCompressionNone)
			s.samples, s.extraSamples = 4, false
			s.tweak = func(p *tiffParts) { p.entries = append(p.entries, o.shorts(338, 1)) }
			return tiffBuild(s)
		}())

		// Strip bookkeeping.
		for _, comp := range []int{tiffCompressionNone, tiffCompressionPackBits, tiffCompressionLZW, tiffCompressionDeflate} {
			cn := fmt.Sprint(comp)
			for _, add1 := range []int64{-3, -1, 1, 3, 1000, -1000} {
				add("struct-"+e+"-offsetadd"+cn+fmt.Sprintf("%+d", add1), withTweak(comp, func(p *tiffParts) { p.offsetAdd = add1 }))
				add("struct-"+e+"-countadd"+cn+fmt.Sprintf("%+d", add1), withTweak(comp, func(p *tiffParts) { p.countAdd = add1 }))
			}
			add("struct-"+e+"-dropstrip"+cn, withTweak(comp, func(p *tiffParts) { p.chunks = p.chunks[:len(p.chunks)-1] }))
			add("struct-"+e+"-extrastrip"+cn, withTweak(comp, func(p *tiffParts) { p.chunks = append(p.chunks, []byte{1, 2, 3}) }))
			add("struct-"+e+"-emptystrip"+cn, withTweak(comp, func(p *tiffParts) { p.chunks[1] = nil }))
			add("struct-"+e+"-swapstrips"+cn, withTweak(comp, func(p *tiffParts) { p.chunks[0], p.chunks[1] = p.chunks[1], p.chunks[0] }))
			add("struct-"+e+"-shortstrip"+cn, withTweak(comp, func(p *tiffParts) { p.chunks[0] = p.chunks[0][:len(p.chunks[0])/2] }))
			add("struct-"+e+"-longstrip"+cn, withTweak(comp, func(p *tiffParts) { p.chunks[0] = append(p.chunks[0], make([]byte, 40)...) }))
			for _, rps := range []uint64{0, 1, 3, 4, 5, 8, 9, 10, 100, 0xFFFF, 0xFFFFFFFF} {
				add("struct-"+e+"-rps"+cn+"-"+fmt.Sprint(rps), withTweak(comp, func(p *tiffParts) { p.set(o.longs(tiffTagRowsPerStrip, rps)) }))
			}
			add("struct-"+e+"-norps"+cn, withTweak(comp, func(p *tiffParts) { p.remove(tiffTagRowsPerStrip) }))
			add("struct-"+e+"-bothtags"+cn, withTweak(comp, func(p *tiffParts) {
				p.entries = append(p.entries, o.longs(tiffTagTileWidth, 8), o.longs(tiffTagTileLength, 8), o.longs(tiffTagTileOffsets, 8), o.longs(tiffTagTileByteCounts, 8))
			}))
			add("struct-"+e+"-stripoffsets-tilecounts"+cn, withTweak(comp, func(p *tiffParts) {
				p.entries = append(p.entries, o.longs(tiffTagTileByteCounts, 8))
			}))
			add("struct-"+e+"-onlyoffsets"+cn, withTweak(comp, func(p *tiffParts) { p.skipCounts = true }))
			add("struct-"+e+"-onlycounts"+cn, withTweak(comp, func(p *tiffParts) { p.skipOffsets = true }))
		}
		// Tile bookkeeping, tiles of various sizes against the image of 11 x 9.
		for _, comp := range []int{tiffCompressionNone, tiffCompressionPackBits, tiffCompressionLZW} {
			cn := fmt.Sprint(comp)
			for _, tile := range [][2]int{{1, 1}, {4, 4}, {5, 3}, {11, 9}, {12, 10}, {16, 16}, {11, 1}, {1, 9}, {100, 100}, {32768, 32768}, {65536, 65536}, {1 << 20, 4}, {4, 1 << 20}} {
				s := base(comp)
				s.tileW, s.tileH = tile[0], tile[1]
				s.rowsPerStrip = 0
				if tile[0]*tile[1] > 1<<16 {
					// Too big to write the padded tiles; write one tile of the image's size and say the tile size in the tags.
					s.tileW, s.tileH = 0, 0
					s.tweak = func(p *tiffParts) {
						p.tiled = true
						p.entries = append(p.entries, o.longs(tiffTagTileWidth, uint64(tile[0])), o.longs(tiffTagTileLength, uint64(tile[1])))
						p.remove(tiffTagRowsPerStrip)
					}
				}
				add(fmt.Sprintf("struct-%s-tile%d-%dx%d", e, comp, tile[0], tile[1]), tiffBuild(s))
			}
			for _, wrap := range []uint64{0xFFFFFFFF, 0xFFFFFFF0, 0x80000000, 0x7FFFFFFF} {
				for _, which := range []string{"w", "h", "both"} {
					s := base(comp)
					s.rowsPerStrip = 0
					s.tweak = func(p *tiffParts) {
						p.tiled = true
						p.remove(tiffTagRowsPerStrip)
						tw, tl := uint64(4), uint64(4)
						if which != "h" {
							tw = wrap
						}
						if which != "w" {
							tl = wrap
						}
						p.entries = append(p.entries, o.longs(tiffTagTileWidth, tw), o.longs(tiffTagTileLength, tl))
						p.offsetType, p.countType = tiffTypeLong, tiffTypeLong
						if len(p.chunks) > 1 {
							p.chunks = p.chunks[:1]
						}
					}
					add(fmt.Sprintf("struct-%s-tilewrap-%s-%d-%s", e, cn, wrap, which), tiffBuild(s))
				}
			}
			for _, count := range []int{0, 1, 2, 5, 6, 7, 9} {
				s := base(comp)
				s.tileW, s.tileH = 4, 4
				s.rowsPerStrip = 0
				s.tweak = func(p *tiffParts) {
					if count < len(p.chunks) {
						p.chunks = p.chunks[:count]
					}
					for len(p.chunks) < count {
						p.chunks = append(p.chunks, []byte{0})
					}
				}
				add(fmt.Sprintf("struct-%s-tilecount%s-%d", e, cn, count), tiffBuild(s))
			}
		}
		// An image with no strips at all and a huge claimed count.
		add("struct-"+e+"-zero-strips", withTweak(tiffCompressionNone, func(p *tiffParts) { p.chunks = nil }))
		add("struct-"+e+"-bigcount-offsets", withTweak(tiffCompressionNone, func(p *tiffParts) {
			p.skipOffsets = true
			p.entries = append(p.entries, tiffEntrySpec{tag: tiffTagStripOffsets, typ: tiffTypeLong, count: 0xFFFFFFFF, data: o.u32(8)})
		}))
		add("struct-"+e+"-bigcount-counts", withTweak(tiffCompressionNone, func(p *tiffParts) {
			p.skipCounts = true
			p.entries = append(p.entries, tiffEntrySpec{tag: tiffTagStripByteCounts, typ: tiffTypeShort, count: 0x7FFFFFFF, data: o.u32(8)})
		}))
		// A value far past the end of the file, and one that overlaps the header.
		for _, at := range []uint32{0, 4, 8, 0xFFFFFFFF, 0xFFFFFFF0, 1 << 30} {
			data := tiffBuild(base(tiffCompressionNone))
			ifd := int(o.order().Uint32(data[4:]))
			count := int(o.order().Uint16(data[ifd:]))
			for i := range count {
				entry := data[ifd+2+12*i:]
				if o.order().Uint16(entry) == tiffTagBitsPerSample {
					o.order().PutUint32(entry[8:], at)
				}
			}
			add(fmt.Sprintf("struct-%s-externalat-%d", e, at), data)
		}
	}
}

// addCorruptionCorpus truncates files at many lengths and flips bytes of the compressed data and the directory.
func addCorruptionCorpus(add func(string, []byte)) {
	rng := rand.New(rand.NewPCG(20261029, 6))
	type base struct {
		name string
		s    tiffSpec
	}
	var bases []base
	for _, little := range []bool{true, false} {
		for _, comp := range tiffCompressions {
			kind := tiffColorKinds[4]
			s := tiffSpec{little: little, width: 14, height: 11, samples: 3, bits: 8, photometric: tiffRGB, compression: comp.id, rowsPerStrip: 4, seed: 21,
				predictor: tiffPredictorHorizontal, jpegTables: comp.id == tiffCompressionModernJPEG && little, extraSamples: kind.extra}
			if comp.id == tiffCompressionModernJPEG {
				s.predictor = 0
			}
			bases = append(bases, base{fmt.Sprintf("%s-%s", tiffEndName(little), comp.name), s})
			g := s
			g.samples, g.photometric, g.bits = 1, tiffBlackIsZero, 16
			if comp.id == tiffCompressionModernJPEG {
				g.bits = 8
			}
			g.tileW, g.tileH, g.rowsPerStrip = 8, 8, 0
			bases = append(bases, base{fmt.Sprintf("%s-%s-gray-tiles", tiffEndName(little), comp.name), g})
		}
	}
	for _, b := range bases {
		data := tiffBuild(b.s)
		// Every eleventh length (and every length of the last bytes), so the cut falls in the header, the data and the directory.
		for n := range len(data) {
			if n%11 != 0 && n < len(data)-12 {
				continue
			}
			add(fmt.Sprintf("cut-%s-%d", b.name, n), slices.Clone(data[:n]))
		}
		// Changes of a few bytes anywhere in the file.
		for i := range 24 {
			d := slices.Clone(data)
			for range 1 + rng.IntN(3) {
				pos := rng.IntN(len(d))
				switch rng.IntN(4) {
				case 0:
					d[pos] ^= 1 << rng.IntN(8)
				case 1:
					d[pos] = byte(rng.IntN(256))
				case 2:
					d[pos] = 0
				default:
					d[pos] = 0xFF
				}
			}
			add(fmt.Sprintf("flip-%s-%d", b.name, i), d)
		}
		// Changes confined to the compressed data.
		for i := range 20 {
			q := tiffPartsOf(b.s)
			chunk := rng.IntN(len(q.chunks))
			c := slices.Clone(q.chunks[chunk])
			if len(c) == 0 {
				continue
			}
			for range 1 + rng.IntN(2) {
				pos := rng.IntN(len(c))
				if rng.IntN(2) == 0 {
					c[pos] ^= 1 << rng.IntN(8)
				} else {
					c[pos] = byte(rng.IntN(256))
				}
			}
			switch rng.IntN(5) {
			case 0:
				c = c[:rng.IntN(len(c))]
			case 1:
				c = append(c, byte(rng.IntN(256)), byte(rng.IntN(256)))
			}
			q.chunks[chunk] = c
			add(fmt.Sprintf("chunk-%s-%d", b.name, i), q.assemble())
		}
	}
	// Streams whose end is the question: data after the end code or end of stream, and the data ending right at the needed byte.
	for _, little := range []bool{true, false} {
		for _, comp := range []int{tiffCompressionLZW, tiffCompressionPackBits, tiffCompressionDeflate} {
			s := tiffSpec{little: little, width: 12, height: 4, samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: comp, seed: 2}
			good := tiffPartsOf(s)
			for _, tail := range [][]byte{{0}, {0xFF}, {0, 0, 0, 0}, {1, 2, 3, 4, 5, 6, 7, 8}, {0xFF, 0xFF, 0xFF}, bytesRepeat(0xAA, 64)} {
				q := tiffPartsOf(s)
				q.chunks[0] = append(slices.Clone(good.chunks[0]), tail...)
				add(fmt.Sprintf("tail-%s-%d-%x", tiffEndName(little), comp, tail[:min(4, len(tail))]), q.assemble())
			}
			for cut := 1; cut <= 6; cut++ {
				q := tiffPartsOf(s)
				q.chunks[0] = slices.Clone(good.chunks[0][:max(0, len(good.chunks[0])-cut)])
				add(fmt.Sprintf("tailcut-%s-%d-%d", tiffEndName(little), comp, cut), q.assemble())
			}
		}
	}
}

func bytesRepeat(b byte, n int) []byte { return []byte(strings.Repeat(string([]byte{b}), n)) }

// addLimitCorpus is the size limits: images whose decoded size is at, just under and just over the 512 MiB of image's limits and the
// buffer limits that follow from it, dimensions that overflow, and files that claim much more than they hold.
func addLimitCorpus(add func(string, []byte)) {
	for _, little := range []bool{true, false} {
		o := tiffOrder{little}
		e := tiffEndName(little)
		header := func(width, height uint64, samples, bits, photometric int) *tiffParts {
			s := tiffSpec{little: little, width: 1, height: 1, samples: samples, bits: bits, photometric: photometric, compression: tiffCompressionNone}
			p := tiffPartsOf(s)
			p.set(o.longs(tiffTagImageWidth, width))
			p.set(o.longs(tiffTagImageLength, height))
			p.set(o.longs(tiffTagRowsPerStrip, height))
			return p
		}
		for _, kind := range tiffColorKinds {
			for _, dims := range [][2]uint64{{4096, 4096}, {16384, 16384}, {16385, 16384}, {32768, 16384}, {65536, 65536}, {0xFFFFFFFF, 1}, {1, 0xFFFFFFFF}, {0xFFFFFFFF, 0xFFFFFFFF},
				{46341, 46341}, {0x80000000, 2}, {1 << 20, 1 << 10}, {1 << 22, 1 << 6}} {
				p := header(dims[0], dims[1], kind.samples, kind.bits, kind.photometric)
				add(fmt.Sprintf("limit-%s-%s-%dx%d", e, kind.name, dims[0], dims[1]), p.assemble())
			}
		}
		// Whole images made of a few highly compressible bytes: Deflate and PackBits and LZW streams that expand to the limits.
		type big struct {
			name          string
			width, height int
			kind          tiffColorKind
		}
		for _, b := range []big{
			{"rgb16-at", 44739242, 1, tiffColorKinds[5]},
			{"rgb16-over", 44739243, 1, tiffColorKinds[5]},
			{"gray8-at", 16384, 16384, tiffColorKinds[0]},
			{"gray8-over", 16385, 16384, tiffColorKinds[0]},
			{"rgb8-at", 9459, 9459, tiffColorKinds[4]},
			{"rgb8-over", 9460, 9459, tiffColorKinds[4]},
		} {
			for _, comp := range []int{tiffCompressionDeflate, tiffCompressionPackBits} {
				bytesPerPixel := b.kind.samples * b.kind.bits / 8
				total := b.width * b.height * bytesPerPixel
				var data []byte
				switch comp {
				case tiffCompressionDeflate:
					data = zeroDeflate(total)
				default:
					data = zeroPackBits(total)
				}
				s := tiffSpec{little: little, width: 1, height: 1, samples: b.kind.samples, bits: b.kind.bits, photometric: b.kind.photometric, compression: comp}
				p := tiffPartsOf(s)
				p.set(o.longs(tiffTagImageWidth, uint64(b.width)))
				p.set(o.longs(tiffTagImageLength, uint64(b.height)))
				p.set(o.longs(tiffTagRowsPerStrip, uint64(b.height)))
				p.chunks = [][]byte{data}
				add(fmt.Sprintf("limit-%s-%s-%d", e, b.name, comp), p.assemble())
			}
		}
		// The compressed chunk against the intermediate buffer limit: a strip of almost 512 MiB of stored bytes cannot be had in a
		// test, but a byte count that claims it can (the limit is on the claim).
		for _, claim := range []uint64{1 << 20, 100 << 20, 256 << 20, 400 << 20, 511 << 20, 512 << 20, 0xFFFFFFFF} {
			s := tiffSpec{little: little, width: 4, height: 4, samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: tiffCompressionNone}
			p := tiffPartsOf(s)
			p.countAdd = int64(claim)
			add(fmt.Sprintf("limit-%s-claim-%d", e, claim), p.assemble())
			s.compression = tiffCompressionPackBits
			p = tiffPartsOf(s)
			p.countAdd = int64(claim)
			add(fmt.Sprintf("limit-%s-claim-packbits-%d", e, claim), p.assemble())
		}
	}
}

// zeroDeflate is the zlib stream of n zero bytes.
func zeroDeflate(n int) []byte {
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	zeros := make([]byte, 1<<16)
	for n > 0 {
		c := min(n, len(zeros))
		_, _ = zw.Write(zeros[:c])
		n -= c
	}
	_ = zw.Close()
	return out.Bytes()
}

// zeroPackBits is the PackBits stream of n zero bytes: runs of 128.
func zeroPackBits(n int) []byte {
	out := make([]byte, 0, 2*(n/128+2))
	for n >= 128 {
		out = append(out, 0x81, 0)
		n -= 128
	}
	if n >= 2 {
		out = append(out, byte(int8(1-n)), 0)
	} else if n == 1 {
		out = append(out, 0, 0)
	}
	return out
}

// tiffLZWCodes writes codes as 9-bit LZW codes, most significant bit first (the width of the first 254 codes after a clear code).
func tiffLZWCodes(codes []int) []byte {
	var out []byte
	var acc uint32
	var nbits uint
	for _, code := range codes {
		acc = acc<<9 | uint32(code)
		nbits += 9
		for nbits >= 8 {
			out = append(out, byte(acc>>(nbits-8)))
			nbits -= 8
		}
	}
	if nbits > 0 {
		out = append(out, byte(acc<<(8-nbits)))
	}
	return out
}

// addStreamCorpus is streams written by hand: random LZW code sequences (invalid codes, codes of the next entry, clear and end codes
// anywhere, a stream that ends where the strip needs its last byte), random PackBits headers, and Deflate streams of more than one
// window of 32 KiB with damage in the second window.
func addStreamCorpus(add func(string, []byte)) {
	rng := rand.New(rand.NewPCG(20261029, 7))
	for _, little := range []bool{true, false} {
		e := tiffEndName(little)
		for i := range 700 {
			n := 3 + rng.IntN(10)
			codes := make([]int, n)
			for j := range codes {
				switch rng.IntN(10) {
				case 0:
					codes[j] = 256
				case 1:
					codes[j] = 257
				case 2, 3:
					codes[j] = 258 + rng.IntN(4)
				default:
					codes[j] = 65 + rng.IntN(4)
				}
			}
			if rng.IntN(3) > 0 {
				codes[0] = 256
			}
			width := 1 + rng.IntN(12)
			s := tiffSpec{little: little, width: width, height: 1 + rng.IntN(3), samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: tiffCompressionLZW}
			p := tiffPartsOf(s)
			p.chunks = [][]byte{tiffLZWCodes(codes)}
			add(fmt.Sprintf("lzwcodes-%s-%d", e, i), p.assemble())
		}
		for i := range 300 {
			n := 1 + rng.IntN(40)
			stream := make([]byte, n)
			for j := range stream {
				switch rng.IntN(4) {
				case 0:
					stream[j] = byte(rng.IntN(256))
				case 1:
					stream[j] = byte(256 - 1 - rng.IntN(5))
				default:
					stream[j] = byte(rng.IntN(6))
				}
			}
			s := tiffSpec{little: little, width: 1 + rng.IntN(9), height: 1 + rng.IntN(5), samples: 1 + 2*rng.IntN(2), bits: 8, photometric: tiffBlackIsZero, compression: tiffCompressionPackBits}
			if s.samples == 3 {
				s.photometric = tiffRGB
			}
			p := tiffPartsOf(s)
			p.chunks = [][]byte{stream}
			if rng.IntN(3) == 0 {
				p.countAdd = int64(rng.IntN(9)) - 4
			}
			add(fmt.Sprintf("packbitscodes-%s-%d", e, i), p.assemble())
		}
		// Deflate: more than one window, damaged late.
		for _, size := range [][2]int{{256, 128}, {256, 129}, {200, 200}, {300, 300}} {
			for _, rows := range []int{0, 1, 128, 7} {
				s := tiffSpec{little: little, width: size[0], height: size[1], samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: tiffCompressionDeflate, rowsPerStrip: rows, seed: 3}
				s.gen = func(x, y, c int) uint32 { return tiffSample(1, x*7+y, y*3+x*x, c, 8) }
				base := tiffPartsOf(s)
				add(fmt.Sprintf("bigdeflate-%s-%dx%d-rps%d", e, size[0], size[1], rows), base.assemble())
				for i := range 8 {
					q := tiffPartsOf(s)
					chunk := rng.IntN(len(q.chunks))
					c := slices.Clone(q.chunks[chunk])
					pos := len(c)/2 + rng.IntN(len(c)/2)
					switch {
					case i%2 == 0:
						c[pos] ^= 1 << rng.IntN(8)
					case i%4 == 1:
						c = c[:pos]
					default:
						c[len(c)-1-rng.IntN(4)] ^= 0x55
					}
					q.chunks[chunk] = c
					add(fmt.Sprintf("bigdeflate-%s-%dx%d-rps%d-damage%d", e, size[0], size[1], rows, i), q.assemble())
				}
			}
		}
		// LZW and PackBits over several table cycles with damage late in the stream.
		for _, comp := range []int{tiffCompressionLZW, tiffCompressionPackBits} {
			s := tiffSpec{little: little, width: 220, height: 90, samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: comp, rowsPerStrip: 30}
			s.gen = func(x, y, c int) uint32 { return tiffSample(2, x*5+y, y*3+x*x, c, 8) }
			for i := range 12 {
				q := tiffPartsOf(s)
				chunk := rng.IntN(len(q.chunks))
				c := slices.Clone(q.chunks[chunk])
				pos := len(c)/2 + rng.IntN(len(c)/2)
				if i%3 == 0 {
					c = c[:pos]
				} else {
					c[pos] ^= 1 << rng.IntN(8)
				}
				q.chunks[chunk] = c
				add(fmt.Sprintf("longstream-%s-%d-%d", e, comp, i), q.assemble())
			}
		}
	}
}

// tiffBitWriter writes a deflate stream: bits least significant first, Huffman codes most significant bit first.
type tiffBitWriter struct {
	out  []byte
	acc  uint32
	bits uint
}

func (w *tiffBitWriter) write(value uint32, n uint) {
	w.acc |= value << w.bits
	w.bits += n
	for w.bits >= 8 {
		w.out = append(w.out, byte(w.acc))
		w.acc >>= 8
		w.bits -= 8
	}
}

func (w *tiffBitWriter) code(code uint32, n uint) {
	var rev uint32
	for i := range n {
		rev |= (code >> i & 1) << (n - 1 - i)
	}
	w.write(rev, n)
}

func (w *tiffBitWriter) flush() []byte {
	if w.bits > 0 {
		w.out = append(w.out, byte(w.acc))
		w.acc, w.bits = 0, 0
	}
	return w.out
}

// fixedLiteral and fixedMatch write symbols of the fixed Huffman code of RFC 1951.
func (w *tiffBitWriter) fixedLiteral(b byte) {
	if b < 144 {
		w.code(0x30+uint32(b), 8)
	} else {
		w.code(0x190+uint32(b)-144, 9)
	}
}

// fixedMatch writes a match of length 3 to 10 at a distance of dist (the distance codes below 4 have no extra bits, 4 and 5 have one).
func (w *tiffBitWriter) fixedMatch(length int, distCode, distExtra uint32, extraBits uint) {
	w.code(uint32(256+length-2), 7)
	w.code(distCode, 5)
	if extraBits > 0 {
		w.write(distExtra, extraBits)
	}
}

func (w *tiffBitWriter) endOfBlock() { w.code(0, 7) }

// zlibWrap wraps a deflate stream with a zlib header and the Adler-32 of data.
func zlibWrap(deflate, data []byte) []byte {
	out := append([]byte{0x78, 0x9C}, deflate...)
	return append(out, byte(adler32Update(1, data)>>24), byte(adler32Update(1, data)>>16), byte(adler32Update(1, data)>>8), byte(adler32Update(1, data)))
}

// addDeflateCorpus is zlib streams of every block type and compression level, and streams written by hand: matches that reach
// before the start of the data (miniz reads its zeroed dictionary), invalid distance codes, codes with a single symbol, and
// streams with a wrong header or checksum.
func addDeflateCorpus(add func(string, []byte)) {
	rng := rand.New(rand.NewPCG(20261029, 8))
	for _, little := range []bool{true, false} {
		e := tiffEndName(little)
		// Levels: stored blocks (0), fastest, default, best, Huffman only (-2).
		for _, level := range []int{0, 1, 6, 9, -2} {
			for _, size := range [][2]int{{40, 30}, {200, 190}, {256, 130}} {
				s := tiffSpec{little: little, width: size[0], height: size[1], samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: tiffCompressionDeflate, rowsPerStrip: 0}
				s.gen = func(x, y, c int) uint32 { return tiffSample(4, x*3+y, y*5+x*x, c, 8) }
				p := tiffPartsOf(s)
				raw := s.chunkData(0, 0, s.width, s.height, -1)
				var out bytes.Buffer
				zw, _ := zlib.NewWriterLevel(&out, level)
				_, _ = zw.Write(raw)
				_ = zw.Close()
				p.chunks = [][]byte{out.Bytes()}
				add(fmt.Sprintf("deflate-%s-level%d-%dx%d", e, level, size[0], size[1]), p.assemble())
				for i := range 6 {
					q := tiffPartsOf(s)
					c := slices.Clone(out.Bytes())
					pos := rng.IntN(len(c))
					if i < 3 {
						c[pos] ^= 1 << rng.IntN(8)
					} else {
						c = c[:pos]
					}
					q.chunks = [][]byte{c}
					add(fmt.Sprintf("deflate-%s-level%d-%dx%d-damage%d", e, level, size[0], size[1], i), q.assemble())
				}
			}
		}

		// Hand-made fixed-Huffman streams of one strip of 4x1 gray pixels.
		type hand struct {
			name  string
			build func(w *tiffBitWriter)
			data  []byte // the output, for the Adler-32
		}
		hands := []hand{
			{"run", func(w *tiffBitWriter) { w.fixedLiteral('A'); w.fixedMatch(3, 0, 0, 0); w.endOfBlock() }, []byte("AAAA")},
			{"before-start", func(w *tiffBitWriter) { w.fixedLiteral('A'); w.fixedMatch(3, 4, 0, 1); w.endOfBlock() }, []byte("A\x00\x00\x00")},
			{"before-start-far", func(w *tiffBitWriter) { w.fixedLiteral('A'); w.fixedMatch(3, 29, 0, 13); w.endOfBlock() }, []byte("A\x00\x00\x00")},
			{"dist30", func(w *tiffBitWriter) { w.fixedLiteral('A'); w.fixedMatch(3, 30, 0, 0); w.endOfBlock() }, []byte("AAAA")},
			{"dist31", func(w *tiffBitWriter) { w.fixedLiteral('A'); w.fixedMatch(3, 31, 0, 0); w.endOfBlock() }, []byte("AAAA")},
			{"length-too-long", func(w *tiffBitWriter) { w.fixedLiteral('A'); w.code(0xC0+6, 8); w.endOfBlock() }, []byte("AAAA")},
			{"literals", func(w *tiffBitWriter) {
				for _, b := range []byte("ABCD") {
					w.fixedLiteral(b)
				}
				w.endOfBlock()
			}, []byte("ABCD")},
			{"no-end-of-block", func(w *tiffBitWriter) {
				for _, b := range []byte("ABCD") {
					w.fixedLiteral(b)
				}
			}, []byte("ABCD")},
			{"more-literals", func(w *tiffBitWriter) {
				for _, b := range []byte("ABCDEFG") {
					w.fixedLiteral(b)
				}
				w.endOfBlock()
			}, []byte("ABCDEFG")},
		}
		for _, h := range hands {
			for _, adler := range []string{"good", "bad", "none"} {
				for _, final := range []uint32{1, 0} {
					w := &tiffBitWriter{}
					w.write(final, 1)
					w.write(1, 2) // fixed Huffman
					h.build(w)
					deflate := w.flush()
					var stream []byte
					switch adler {
					case "good":
						stream = zlibWrap(deflate, h.data)
					case "bad":
						stream = zlibWrap(deflate, append([]byte("x"), h.data...))
					default:
						stream = append([]byte{0x78, 0x9C}, deflate...)
					}
					s := tiffSpec{little: little, width: 4, height: 1, samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: tiffCompressionDeflate}
					p := tiffPartsOf(s)
					p.chunks = [][]byte{stream}
					add(fmt.Sprintf("hand-%s-%s-%s-final%d", e, h.name, adler, final), p.assemble())
				}
			}
		}
		// Headers: every first byte pair of a zlib stream of a stored block.
		for _, header := range [][2]byte{{0x78, 0x9C}, {0x78, 0x01}, {0x78, 0xDA}, {0x08, 0x1D}, {0x18, 0x95}, {0x28, 0x15}, {0x38, 0x8D}, {0x48, 0x0D}, {0x58, 0x85}, {0x68, 0xDE},
			{0x78, 0xBB}, {0x88, 0x1C}, {0x89, 0x1C}, {0x78, 0x00}, {0x00, 0x00}, {0xFF, 0xFF}, {0x78, 0x3D}, {0x78, 0x7D}, {0x77, 0x9C}} {
			w := &tiffBitWriter{}
			w.write(1, 1)
			w.write(0, 2)
			stored := append(w.flush(), 4, 0, 0xFB, 0xFF, 'A', 'B', 'C', 'D')
			s := tiffSpec{little: little, width: 4, height: 1, samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: tiffCompressionDeflate}
			p := tiffPartsOf(s)
			stream := append([]byte{header[0], header[1]}, stored...)
			p.chunks = [][]byte{append(stream, byte(adler32Update(1, []byte("ABCD"))>>24), byte(adler32Update(1, []byte("ABCD"))>>16), byte(adler32Update(1, []byte("ABCD"))>>8), byte(adler32Update(1, []byte("ABCD"))))}
			add(fmt.Sprintf("header-%s-%02x%02x", e, header[0], header[1]), p.assemble())
		}
	}
}

// addValueCountCorpus is a SampleFormat entry with a count at the limit the tag reader puts on the values of one entry
// (the 256 MiB of the decoding buffer divided by the size of a `Value`): files of 17 to 22 MB.
func addValueCountCorpus(add func(string, func() []byte)) {
	for _, count := range []int{8388608, 8388609, 11184810, 11184811} {
		add(fmt.Sprintf("valuecount-%d", count), func() []byte {
			s := tiffSpec{little: true, width: 2, height: 2, samples: 1, bits: 8, photometric: tiffBlackIsZero, compression: tiffCompressionNone, seed: 1}
			p := tiffPartsOf(s)
			o := tiffOrder{true}
			p.set(tiffEntrySpec{tag: tiffTagSampleFormat, typ: tiffTypeShort, count: uint32(count), data: bytes.Repeat(o.u16(1), count)})
			return p.assemble()
		})
	}
}

// addRealCorpus is files other software wrote, in testdata/tiff-real: macOS `sips` (the TIFF of an NSBitmapImageRep: RGBA, LZW with the
// predictor, gray with alpha) and libtiff 4.7 `tiffcp` over them (every compression it writes, with the predictors, strips of a few
// rows, tiles, planar and both byte orders, and a BigTIFF).
func addRealCorpus(add func(string, []byte)) {
	files, err := filepath.Glob("testdata/tiff-real/*.tif")
	if err != nil || len(files) == 0 {
		panic(fmt.Sprintf("no files in testdata/tiff-real: %v", err))
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			panic(err)
		}
		add("real-"+strings.TrimSuffix(filepath.Base(file), ".tif"), data)
	}
}
