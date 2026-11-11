// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
)

// The structure group of the corpus: where the segments sit, which ones are missing or broken, and every marker jpeg-decoder 0.3.1
// handles or refuses.

// jdcSegs is the segments of a stream under edit.
type jdcSegs []jdwSeg

func (s jdcSegs) index(name string, nth int) int {
	for i, seg := range s {
		if seg.name == name {
			if nth == 0 {
				return i
			}
			nth--
		}
	}
	panic("no segment " + name)
}

func (s jdcSegs) clone() jdcSegs {
	out := make(jdcSegs, len(s))
	for i, seg := range s {
		out[i] = jdwSeg{seg.name, bytes.Clone(seg.data)}
	}
	return out
}

// without drops every segment named name.
func (s jdcSegs) without(name string) jdcSegs {
	return slices.DeleteFunc(s.clone(), func(seg jdwSeg) bool { return seg.name == name })
}

// with inserts segments before index i.
func (s jdcSegs) with(i int, ins ...jdwSeg) jdcSegs {
	out := s.clone()
	return slices.Insert(out, i, ins...)
}

// before inserts segments before the nth segment named name.
func (s jdcSegs) before(name string, nth int, ins ...jdwSeg) jdcSegs {
	return s.with(s.index(name, nth), ins...)
}

func (s jdcSegs) bytes() []byte {
	return jdwJoin(s)
}

// patch sets one byte of a segment.
func (s jdcSegs) patch(name string, nth, offset int, value byte) jdcSegs {
	out := s.clone()
	out[out.index(name, nth)].data[offset] = value
	return out
}

func jdcSeg(name string, marker byte, payload ...byte) jdwSeg {
	return jdwMarkerSeg(name, marker, payload)
}

// jdcDQT is a DQT segment of the tables given (index, table in natural order, 16-bit or not).
func jdcDQT(s *jdwSpec, tables ...int) jdwSeg {
	var payload []byte
	for _, t := range tables {
		if s.q16 {
			payload = append(payload, byte(0x10|t))
			for k := range 64 {
				payload = binary.BigEndian.AppendUint16(payload, s.qtab[t][unzigzag[k]])
			}
		} else {
			payload = append(payload, byte(t))
			for k := range 64 {
				payload = append(payload, byte(s.qtab[t][unzigzag[k]]))
			}
		}
	}
	return jdwMarkerSeg("dqt", 0xDB, payload)
}

func (b *jdcBuilder) structure() {
	seq := func() *jdwSpec {
		s := jdcSamplingSpec(33, 31, "420", jdcYCbCrIDs)
		s.fillImage(b.image("photo", 33, 31), "ycbcr")
		s.scans = jdcSeqInterleaved(3)
		return s
	}
	sep := func() *jdwSpec {
		s := jdcSamplingSpec(33, 31, "444", jdcYCbCrIDs)
		s.fillImage(b.image("photo", 33, 31), "ycbcr")
		s.scans = jdcSeqSeparate(3)
		return s
	}
	prog := func(script int) *jdwSpec {
		s := jdcSamplingSpec(33, 31, "420", jdcYCbCrIDs)
		s.fillImage(b.image("photo", 33, 31), "ycbcr")
		s.sof = 0xC2
		s.scans = jdcProgScript(3, script, true)
		return s
	}
	add := func(name string, segs jdcSegs) { b.add(jdcName("st", name), segs.bytes()) }

	base := seq()
	baseSegs := jdcSegs(base.segments())
	add("base", baseSegs)

	// quantization tables: after the frame, in one segment, redefined, per scan, missing, broken
	dqts := baseSegs.without("dqt")
	sof := dqts.index("sof", 0)
	add("dqt-after-sof", dqts.with(sof+1, jdcDQT(base, 0), jdcDQT(base, 1)))
	add("dqt-one-segment", dqts.with(sof, jdcDQT(base, 0, 1)))
	other := *base
	other.qtab[0] = other.qtab[1]
	add("dqt-redefined", dqts.with(sof, jdcDQT(base, 0), jdcDQT(base, 1), jdcDQT(&other, 0)))
	add("dqt-redefined-after-sof", dqts.with(sof, jdcDQT(base, 0), jdcDQT(base, 1)).with(sof+3, jdcDQT(&other, 0)))
	add("dqt-missing", dqts)
	add("dqt-table1-missing", dqts.with(sof, jdcDQT(base, 0)))
	add("dqt-table0-missing", dqts.with(sof, jdcDQT(base, 1)))
	add("dqt-table2", dqts.with(sof, jdcDQT(base, 0), jdcDQT(base, 2)))
	dqtBad := func(name string, edit func(d []byte) []byte) {
		segs := baseSegs.clone()
		i := segs.index("dqt", 0)
		segs[i].data = edit(segs[i].data)
		add(name, segs)
	}
	dqtBad("dqt-precision2", func(d []byte) []byte { d[4] |= 0x20; return d })
	dqtBad("dqt-index4", func(d []byte) []byte { d[4] = 4; return d })
	dqtBad("dqt-zero-entry", func(d []byte) []byte { d[10] = 0; return d })
	dqtBad("dqt-short-length", func(d []byte) []byte { d[3] -= 1; return d[:len(d)-1] })
	dqtBad("dqt-long-length", func(d []byte) []byte { d[3] += 1; return append(d, 7) })
	dqtBad("dqt-trailing-bytes", func(d []byte) []byte { d[3] += 3; return append(d, 1, 2, 3) })
	dqtBad("dqt-length-1", func(d []byte) []byte { d[2], d[3] = 0, 1; return d })
	dqtBad("dqt-length-2", func(d []byte) []byte { d[2], d[3] = 0, 2; return d[:4] })

	sp := sep()
	spSegs := jdcSegs(sp.segments())
	nodqt := spSegs.without("dqt")
	sof = nodqt.index("sof", 0)
	sos0 := nodqt.index("sos", 0)
	// the tables of a scan are the ones in force at its start
	q1 := *sp
	q1.qtab[1] = sp.qtab[0]
	add("dqt-per-scan", nodqt.with(sos0, jdcDQT(sp, 0), jdcDQT(sp, 1)).before("sos", 1, jdcDQT(&q1, 1)))
	add("dqt-after-first-scan", nodqt.before("sos", 0, jdcDQT(sp, 0)).before("sos", 1, jdcDQT(sp, 1)))
	add("dqt-after-last-scan", nodqt.with(sof, jdcDQT(sp, 0), jdcDQT(sp, 1)).before("eoi", 0, jdcDQT(&q1, 1)))

	// Huffman tables: split, per scan, before the frame, missing, broken
	hs := seq()
	hs.splitDHT = true
	add("dht-split", jdcSegs(hs.segments()))
	hsSegs := jdcSegs(seq().segments())
	nodht := hsSegs.without("dht")
	add("dht-missing", nodht)
	dhtI := hsSegs.index("dht", 0)
	add("dht-before-sof", jdcSegs(slices.Insert(nodht.clone(), nodht.index("sof", 0), hsSegs[dhtI])))
	add("dht-after-sof-twice", hsSegs.with(hsSegs.index("sos", 0), hsSegs[dhtI]))
	dhtBad := func(name string, edit func(d []byte) []byte) {
		segs := hsSegs.clone()
		i := segs.index("dht", 0)
		segs[i].data = edit(segs[i].data)
		add(name, segs)
	}
	dhtBad("dht-class2", func(d []byte) []byte { d[4] = 0x20; return d })
	dhtBad("dht-index4", func(d []byte) []byte { d[4] = 0x04; return d })
	dhtBad("dht-index2-baseline", func(d []byte) []byte { d[4] = 0x02; return d })
	dhtBad("dht-zero-counts", func(d []byte) []byte { clear(d[5:21]); return d })
	dhtBad("dht-oversubscribed", func(d []byte) []byte { clear(d[5:21]); d[5], d[6] = 3, 1; return d })
	dhtBad("dht-too-many-values", func(d []byte) []byte { d[5+15] = 255; d[5+14] = 255; return d })
	dhtBad("dht-length-short", func(d []byte) []byte { d[3] -= 1; return d })
	dhtBad("dht-length-long", func(d []byte) []byte { d[3] += 1; return d })
	dhtBad("dht-length-18", func(d []byte) []byte { d[2], d[3] = 0, 18; return d[:20] })
	dhtBad("dht-length-2", func(d []byte) []byte { d[2], d[3] = 0, 2; return d[:4] })

	// tables with long codes: optimized for a skewed alphabet, and with the codes of every length
	long := seq()
	long.fillRandom("huge", func() *corpusRNG { r := corpusRNG(77); return &r }())
	long.sof = 0xC1
	add("dht-long-codes", jdcSegs(long.segments()))
	wide := sep()
	wide.fillRandom("wide", func() *corpusRNG { r := corpusRNG(78); return &r }())
	add("dht-wide-codes", jdcSegs(wide.segments()))

	// the restart interval: before and after the frame, changed between scans, and the markers it promises
	rs := sep()
	rs.restart = 3
	rsSegs := jdcSegs(rs.segments())
	dri := rsSegs.index("dri", 0)
	noDRI := rsSegs.without("dri")
	add("dri-after-sof", noDRI.before("sos", 0, rsSegs[dri]))
	add("dri-repeated-between-scans", rsSegs.before("sos", 1, rsSegs[dri]).before("sos", 2, rsSegs[dri]))
	dri5 := jdwMarkerSeg("dri", 0xDD, binary.BigEndian.AppendUint16(nil, 5))
	add("dri-changed-between-scans", rsSegs.before("sos", 1, dri5))
	add("dri-zero-after-first-scan", rsSegs.before("sos", 1, jdwMarkerSeg("dri", 0xDD, []byte{0, 0})))
	add("dri-missing-but-rst", noDRI)
	add("dri-bad-length", rsSegs.patch("dri", 0, 3, 3))
	rstStripped := rsSegs.clone()
	for i := range rstStripped {
		if rstStripped[i].name == "ecs" {
			rstStripped[i].data = jdcStripRST(rstStripped[i].data, false)
		}
	}
	add("rst-missing", rstStripped)
	rstSwapped := rsSegs.clone()
	for i := range rstSwapped {
		if rstSwapped[i].name == "ecs" {
			rstSwapped[i].data = jdcStripRST(rstSwapped[i].data, true)
		}
	}
	add("rst-wrong-number", rstSwapped)
	last := rsSegs.index("eoi", 0)
	add("rst-extra-after-scan", rsSegs.with(last, jdwSeg{"rst", []byte{0xFF, 0xD0}}))
	add("rst-extra-between-scans", rsSegs.with(rsSegs.index("sos", 1), jdwSeg{"rst", []byte{0xFF, 0xD3}}))
	add("rst-before-sof", rsSegs.with(rsSegs.index("sof", 0), jdwSeg{"rst", []byte{0xFF, 0xD0}}))
	add("rst-after-eoi", append(rsSegs.clone(), jdwSeg{"rst", []byte{0xFF, 0xD0}}))
	add("rst-in-header", rsSegs.with(1, jdwSeg{"rst", []byte{0xFF, 0xD1}}))

	// the end of the data: EOI missing, repeated, followed by anything, fill bytes, junk between segments
	add("eoi-missing", baseSegs.without("eoi"))
	add("eoi-twice", append(baseSegs.clone(), baseSegs[baseSegs.index("eoi", 0)]))
	add("eoi-garbage-after", append(baseSegs.clone(), jdwSeg{"junk", []byte("not a jpeg at all, \xff\x00\xff")}))
	add("eoi-fill-bytes", baseSegs.with(baseSegs.index("eoi", 0), jdwSeg{"fill", []byte{0xFF, 0xFF, 0xFF}}))
	add("fill-before-markers", jdcSegs(slices.Insert(baseSegs.clone(), baseSegs.index("dqt", 0), jdwSeg{"fill", []byte{0xFF, 0xFF}})))
	add("junk-between-segments", baseSegs.with(baseSegs.index("sof", 0), jdwSeg{"junk", []byte{1, 2, 3, 0, 0xFF, 0x00, 5}}))
	add("junk-before-sos", baseSegs.with(baseSegs.index("sos", 0), jdwSeg{"junk", []byte{9, 9}}))
	add("junk-after-scan", baseSegs.with(baseSegs.index("eoi", 0), jdwSeg{"junk", []byte{9, 9, 0}}))
	add("ff00-after-scan", baseSegs.with(baseSegs.index("eoi", 0), jdwSeg{"ff00", []byte{0xFF, 0x00}}))
	add("junk-ff-before-sof", baseSegs.with(baseSegs.index("sof", 0), jdwSeg{"junk", []byte{0xFF, 0x00, 0xFF, 0x00}}))
	add("zero-padding-after-eoi", append(baseSegs.clone(), jdwSeg{"pad", make([]byte, 100)}))

	// application segments
	app := func(name string, marker byte, payload ...byte) {
		add(name, baseSegs.with(1, jdwMarkerSeg("app", marker, payload)))
	}
	app("app0-short", 0xE0, 'J', 'F', 'I')
	app("app0-empty", 0xE0)
	app("app0-jfif", 0xE0, []byte("JFIF\x00\x01\x02\x00\x00\x01\x00\x01\x00\x00")...)
	app("app0-avi1", 0xE0, []byte("AVI1\x00\x00\x00\x00\x00")...)
	app("app0-jfxx", 0xE0, []byte("JFXX\x00\x10\x00\x00\x00")...)
	app("app1-exif", 0xE1, []byte("Exif\x00\x00II*\x00\x08\x00\x00\x00\x00\x00")...)
	app("app1-exif-short", 0xE1, []byte("Exif\x00")...)
	app("app1-xmp", 0xE1, append([]byte("http://ns.adobe.com/xap/1.0/\x00"), []byte("<x:xmpmeta/>")...)...)
	app("app1-empty", 0xE1)
	app("app2-icc", 0xE2, append([]byte("ICC_PROFILE\x00\x01\x01"), make([]byte, 40)...)...)
	app("app2-icc-short", 0xE2, []byte("ICC_PROFILE\x00\x01")...)
	app("app2-other", 0xE2, make([]byte, 30)...)
	app("app2-14", 0xE2, make([]byte, 14)...)
	app("app13-photoshop", 0xED, append([]byte("Photoshop 3.0\x00"), make([]byte, 20)...)...)
	app("app13-short", 0xED, []byte("Photoshop 3.0")...)
	app("app14-adobe-short", 0xEE, []byte("Adobe\x00d")...)
	app("app14-other", 0xEE, make([]byte, 12)...)
	app("app14-adobe-long", 0xEE, []byte("Adobe\x00\x64\x00\x00\x00\x00\x01\x01\x02\x03")...)
	app("app15", 0xEF, make([]byte, 9)...)
	app("app5-big", 0xE5, make([]byte, 60000)...)
	add("app-length-overrun", baseSegs.with(1, jdcSeg("app", 0xE3, 1, 2, 3)).patch("app", 0, 3, 200))
	add("com", baseSegs.with(1, jdcSeg("com", 0xFE, []byte("hello")...)))
	add("com-empty", baseSegs.with(1, jdcSeg("com", 0xFE)))
	add("com-length-overrun", baseSegs.with(1, jdcSeg("com", 0xFE, 1)).patch("com", 0, 3, 60))
	adobe := func(name string, adobe int, jfif bool) {
		s := seq()
		s.adobe, s.jfif = adobe, jfif
		add(name, jdcSegs(s.segments()))
	}
	adobe("adobe-ycbcr", 1, false)
	adobe("adobe-after-jfif", 0, true)

	// markers that are not allowed, or not supported
	for _, m := range []byte{0x01, 0x02, 0x50, 0xBF, 0xC8, 0xDE, 0xDF, 0xF0, 0xF7, 0xFD, 0xCC} {
		add(fmt.Sprintf("marker-%02x-in-header", m), baseSegs.with(1, jdcSeg("m", m, 0, 0)))
	}
	for _, m := range []byte{0x01, 0xC8, 0xF0, 0xDE, 0xDF, 0xCC, 0xD8} {
		add(fmt.Sprintf("marker-%02x-after-scan", m), baseSegs.with(baseSegs.index("eoi", 0), jdcSeg("m", m, 0, 0)))
	}
	add("soi-twice", baseSegs.with(1, jdwSeg{"soi", []byte{0xFF, 0xD8}}))
	add("sof-twice", baseSegs.with(baseSegs.index("sos", 0), baseSegs[baseSegs.index("sof", 0)]))
	add("sos-before-sof", baseSegs.with(baseSegs.index("sof", 0), baseSegs[baseSegs.index("sos", 0)]))
	add("eoi-before-sof", baseSegs.with(baseSegs.index("sof", 0), jdwSeg{"eoi", []byte{0xFF, 0xD9}}))
	add("eoi-only", jdcSegs{{"soi", []byte{0xFF, 0xD8}}, {"eoi", []byte{0xFF, 0xD9}}})
	add("sof-only", jdcSegs(baseSegs[:baseSegs.index("sos", 0)]))
	add("dnl-after-first-scan", jdcDNL(spSegs, 0))
	add("dnl-after-second-scan", jdcDNL(spSegs, 1))
	add("dnl-before-sos", spSegs.with(spSegs.index("sos", 0), jdcSeg("dnl", 0xDC, 0, 31)))
	add("dnl-after-eoi", append(spSegs.clone(), jdcSeg("dnl", 0xDC, 0, 31)))

	// frame headers
	sofEdit := func(name string, edit func(d []byte) []byte) {
		segs := baseSegs.clone()
		i := segs.index("sof", 0)
		segs[i].data = edit(segs[i].data)
		add(name, segs)
	}
	for _, m := range []byte{0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF} {
		sofEdit(fmt.Sprintf("sof-marker-%02x", m), func(d []byte) []byte { d[1] = m; return d })
	}
	for _, p := range []byte{0, 1, 2, 4, 7, 9, 12, 16, 17} {
		sofEdit(fmt.Sprintf("sof-precision-%d", p), func(d []byte) []byte { d[4] = p; return d })
		sofEdit(fmt.Sprintf("sof1-precision-%d", p), func(d []byte) []byte { d[1] = 0xC1; d[4] = p; return d })
		sofEdit(fmt.Sprintf("sof2-precision-%d", p), func(d []byte) []byte { d[1] = 0xC2; d[4] = p; return d })
	}
	sofEdit("sof-height-0", func(d []byte) []byte { d[5], d[6] = 0, 0; return d })
	sofEdit("sof-width-0", func(d []byte) []byte { d[7], d[8] = 0, 0; return d })
	sofEdit("sof-components-0", func(d []byte) []byte { d[9] = 0; return d })
	sofEdit("sof-components-2", func(d []byte) []byte { d[9] = 2; return d })
	sofEdit("sof-components-4", func(d []byte) []byte { d[9] = 4; return d })
	sofEdit("sof-length-short", func(d []byte) []byte { d[3]--; return d })
	sofEdit("sof-length-long", func(d []byte) []byte { d[3]++; return d })
	sofEdit("sof-length-6", func(d []byte) []byte { d[2], d[3] = 0, 6; return d[:8] })
	sofEdit("sof-duplicate-id", func(d []byte) []byte { d[13] = d[10]; return d })
	sofEdit("sof-h0", func(d []byte) []byte { d[11] = 0x01; return d })
	sofEdit("sof-v0", func(d []byte) []byte { d[11] = 0x20; return d })
	sofEdit("sof-h5", func(d []byte) []byte { d[11] = 0x51; return d })
	sofEdit("sof-v5", func(d []byte) []byte { d[11] = 0x25; return d })
	sofEdit("sof-tq4", func(d []byte) []byte { d[12] = 4; return d })
	sofEdit("sof-tq3", func(d []byte) []byte { d[12] = 3; return d })
	sofEdit("sof-1x1-image", func(d []byte) []byte { d[5], d[6], d[7], d[8] = 0, 1, 0, 1; return d })
	sofEdit("sof-bigger-image", func(d []byte) []byte { d[5], d[6], d[7], d[8] = 0, 40, 0, 40; return d })
	sofEdit("sof-smaller-image", func(d []byte) []byte { d[5], d[6], d[7], d[8] = 0, 8, 0, 9; return d })
	sofEdit("sof-sampling-3x1", func(d []byte) []byte { d[11] = 0x31; return d })
	sofEdit("sof-sampling-2x3", func(d []byte) []byte { d[11] = 0x23; return d })

	// scan headers
	sosEdit := func(base *jdwSpec, name string, edit func(d []byte) []byte) {
		segs := jdcSegs(base.segments()).clone()
		i := segs.index("sos", 0)
		segs[i].data = edit(segs[i].data)
		add(name, segs)
	}
	sosEdit(seq(), "sos-components-0", func(d []byte) []byte { d[4] = 0; return d })
	sosEdit(seq(), "sos-components-2", func(d []byte) []byte { d[4] = 2; return d })
	sosEdit(seq(), "sos-components-5", func(d []byte) []byte { d[4] = 5; return d })
	sosEdit(seq(), "sos-length-short", func(d []byte) []byte { d[3]--; return d })
	sosEdit(seq(), "sos-length-long", func(d []byte) []byte { d[3]++; return d })
	sosEdit(seq(), "sos-length-2", func(d []byte) []byte { d[2], d[3] = 0, 2; return d[:4] })
	sosEdit(seq(), "sos-unknown-id", func(d []byte) []byte { d[7] = 99; return d })
	sosEdit(seq(), "sos-duplicate-id", func(d []byte) []byte { d[7] = d[5]; return d })
	sosEdit(seq(), "sos-order", func(d []byte) []byte { d[5], d[7] = d[7], d[5]; return d })
	sosEdit(seq(), "sos-dc-table-2", func(d []byte) []byte { d[6] = 0x21; return d })
	sosEdit(seq(), "sos-ac-table-2", func(d []byte) []byte { d[6] = 0x02; return d })
	sosEdit(seq(), "sos-table-4", func(d []byte) []byte { d[6] = 0x40; return d })
	sosEdit(seq(), "sos-se-62", func(d []byte) []byte { d[len(d)-2] = 62; return d })
	sosEdit(seq(), "sos-se-0", func(d []byte) []byte { d[len(d)-2] = 0; return d })
	sosEdit(seq(), "sos-ss-1", func(d []byte) []byte { d[len(d)-3] = 1; return d })
	sosEdit(seq(), "sos-ah-al", func(d []byte) []byte { d[len(d)-1] = 0x01; return d })
	sosEdit(seq(), "sos-ah-1", func(d []byte) []byte { d[len(d)-1] = 0x10; return d })
	sosEdit(prog(0), "sosp-se-64", func(d []byte) []byte { d[len(d)-2] = 64; return d })
	sosEdit(prog(0), "sosp-ss-gt-se", func(d []byte) []byte { d[len(d)-3], d[len(d)-2] = 5, 3; return d })
	sosEdit(prog(0), "sosp-ss0-se1", func(d []byte) []byte { d[len(d)-2] = 1; return d })
	sosEdit(prog(0), "sosp-al-14", func(d []byte) []byte { d[len(d)-1] = 0x0E; return d })
	sosEdit(prog(0), "sosp-ah-14", func(d []byte) []byte { d[len(d)-1] = 0xE0; return d })
	sosEdit(prog(0), "sosp-ah3-al0", func(d []byte) []byte { d[len(d)-1] = 0x30; return d })
	sosEdit(prog(0), "sosp-ah1-al0-first", func(d []byte) []byte { d[len(d)-1] = 0x10; return d })
	sosEdit(prog(0), "sosp-al-13", func(d []byte) []byte { d[len(d)-1] = 0x0D; return d })
	sosEdit(prog(0), "sosp-ac-first-interleaved", func(d []byte) []byte { d[len(d)-3], d[len(d)-2] = 1, 5; return d })

	// scans that repeat, are missing, come in odd orders
	sepSegs := spSegs
	add("scan-repeated", sepSegs.with(sepSegs.index("eoi", 0), sepSegs[sepSegs.index("sos", 0)], sepSegs[sepSegs.index("ecs", 0)]))
	add("scan-missing-last", jdcSegs(sepSegs[:sepSegs.index("sos", 2)]).with(sepSegs.index("sos", 2), jdwSeg{"eoi", []byte{0xFF, 0xD9}}))
	add("scan-order-reversed", func() jdcSegs {
		s := sep()
		s.scans = []jdwScan{{comps: []int{2}, se: 63}, {comps: []int{1}, se: 63}, {comps: []int{0}, se: 63}}
		return jdcSegs(s.segments())
	}())
	add("scan-comp-subset", func() jdcSegs {
		s := seq()
		s.scans = []jdwScan{{comps: []int{0, 1}, se: 63}, {comps: []int{2}, se: 63}}
		return jdcSegs(s.segments())
	}())
	add("scan-comp-1-only", func() jdcSegs {
		s := seq()
		s.scans = []jdwScan{{comps: []int{1}, se: 63}}
		return jdcSegs(s.segments())
	}())
	add("scan-interleaved-too-big", func() jdcSegs {
		s := jdcSamplingSpec(33, 31, "h3v3", jdcYCbCrIDs)
		s.fillImage(b.image("photo", 33, 31), "ycbcr")
		s.scans = jdcSeqInterleaved(3)
		return jdcSegs(s.segments())
	}())
	add("scan-interleaved-10-blocks", func() jdcSegs {
		s := jdcSamplingSpec(33, 31, "410", jdcYCbCrIDs)
		s.fillImage(b.image("photo", 33, 31), "ycbcr")
		s.scans = jdcSeqInterleaved(3)
		return jdcSegs(s.segments())
	}())
	add("scan-4x4-single", func() jdcSegs {
		s := jdcNewSpec(40, 40, [][2]uint8{{4, 4}}, []uint8{1}, 80)
		s.fillImage(b.image("photo", 40, 40), "gray")
		s.scans = jdcSeqInterleaved(1)
		return jdcSegs(s.segments())
	}())

	// progressive scripts cut short: what is rendered from the coefficients that came
	for _, script := range []int{0, 2, 4} {
		full := jdcSegs(func() *jdwSpec { s := prog(script); return s }().segments())
		nscans := 0
		for _, seg := range full {
			if seg.name == "sos" {
				nscans++
			}
		}
		for drop := 1; drop < nscans; drop++ {
			cut := full.index("sos", nscans-drop)
			add(fmt.Sprintf("prog%d-drop-%d", script, drop), jdcSegs(full[:cut]).with(cut, jdwSeg{"eoi", []byte{0xFF, 0xD9}}))
		}
		// without the quantization table of the chroma: the unfinished components cannot be rendered
		add(fmt.Sprintf("prog%d-chroma-table-missing", script), func() jdcSegs {
			s := prog(script)
			segs := jdcSegs(s.segments())
			segs = segs.without("dqt")
			return segs.with(segs.index("sof", 0), jdcDQT(s, 0))
		}())
		// the DHT of every scan dropped but the first: the refinement scans use the first scan's tables
		add(fmt.Sprintf("prog%d-dht-once", script), func() jdcSegs {
			s := prog(script)
			segs := jdcSegs(s.segments())
			out := segs.clone()
			seen := 0
			return slices.DeleteFunc(out, func(seg jdwSeg) bool {
				if seg.name == "dht" {
					seen++
					return seen > 1
				}
				return false
			})
		}())
	}

	// MJPEG: the standard tables stand in for the missing DHT of an AVI1 frame
	mj := seq()
	mj.stdTables = true
	mjSegs := jdcSegs(mj.segments())
	add("mjpeg-no-avi1", mjSegs.without("dht"))
	add("mjpeg-avi1", mjSegs.without("dht").with(1, jdcSeg("avi1", 0xE0, []byte("AVI1\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")...)))
	add("mjpeg-avi1-with-dht", mjSegs.with(1, jdcSeg("avi1", 0xE0, []byte("AVI1\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")...)))
	mjSep := sep()
	mjSep.stdTables = true
	mjSepSegs := jdcSegs(mjSep.segments())
	add("mjpeg-avi1-separate", mjSepSegs.without("dht").with(1, jdcSeg("avi1", 0xE0, []byte("AVI1\x00\x00\x00\x00\x00")...)))
	add("mjpeg-avi1-late", mjSepSegs.without("dht").with(mjSepSegs.without("dht").index("sos", 1), jdcSeg("avi1", 0xE0, []byte("AVI1\x00\x00\x00\x00\x00")...)))
	mjProg := prog(0)
	add("mjpeg-avi1-progressive", jdcSegs(mjProg.segments()).without("dht").with(1, jdcSeg("avi1", 0xE0, []byte("AVI1\x00\x00\x00\x00\x00")...)))

	// the extremes of the size
	for _, c := range []struct {
		name string
		w, h int
		hv   [][2]uint8
	}{
		{"65535x1-gray", 65535, 1, [][2]uint8{{1, 1}}},
		{"1x65535-gray", 1, 65535, [][2]uint8{{1, 1}}},
		{"65535x1-420", 65535, 1, [][2]uint8{{2, 2}, {1, 1}, {1, 1}}},
		{"1x65535-420", 1, 65535, [][2]uint8{{2, 2}, {1, 1}, {1, 1}}},
		{"1x65535-v3", 1, 65535, [][2]uint8{{1, 3}, {1, 1}, {1, 1}}},
		{"2x65535-v4-gray", 2, 65535, [][2]uint8{{1, 4}}},
		{"3x65535-v3-gray", 3, 65535, [][2]uint8{{1, 3}}},
		{"1x65528-v3", 1, 65528, [][2]uint8{{1, 3}, {1, 1}, {1, 1}}},
		{"1x65529-v3", 1, 65529, [][2]uint8{{1, 3}, {1, 1}, {1, 1}}},
		{"65535x2-h4", 65535, 2, [][2]uint8{{4, 1}, {1, 1}, {1, 1}}},
	} {
		ids := jdcYCbCrIDs[:len(c.hv)]
		s := jdcNewSpec(c.w, c.h, c.hv, ids, 50)
		s.fillRandom("sparse", func() *corpusRNG { r := corpusRNG(uint32(c.w*31 + c.h)); return &r }())
		if jdcInterleavedFits(s) {
			s.scans = jdcSeqInterleaved(len(c.hv))
		} else {
			s.scans = jdcSeqSeparate(len(c.hv))
		}
		add(jdcName("size", c.name), jdcSegs(s.segments()))
		s.scans = jdcSeqSeparate(len(c.hv))
		s.rustGeometry = true
		add(jdcName("size-rust", c.name), jdcSegs(s.segments()))
	}
	// an image over the 512 MiB of image's limits: one over the limit and one a pixel below it (the data is the one block of a flat image)
	for _, c := range [][2]int{{24000, 24000}, {23170, 23170}, {23171, 23170}, {16384, 10923}, {16384, 10922}} {
		s := jdcNewSpec(c[0], c[1], [][2]uint8{{1, 1}}, []uint8{1}, 50)
		s.scans = jdcSeqInterleaved(1)
		segs := jdcSegs(jdcHeaderOnly(s))
		add(jdcName("limit", c[0], c[1]), segs)
	}
}

// jdcHeaderOnly is the header and an empty scan of a frame whose blocks are not written: for frames of sizes only the limits see.
func jdcHeaderOnly(s *jdwSpec) []jdwSeg {
	s.jdwGrid()
	saved := s.scans
	s.scans = nil
	segs := s.segments()
	s.scans = saved
	return segs
}

// jdcDNL is a DNL marker after the n-th scan.
func jdcDNL(segs jdcSegs, n int) jdcSegs {
	return segs.with(segs.index("ecs", n)+1, jdcSeg("dnl", 0xDC, 0, 31))
}

// jdcStripRST removes the restart markers of an entropy-coded segment, or (swap) renumbers the first one.
func jdcStripRST(data []byte, swap bool) []byte {
	out := bytes.Clone(data)
	if swap {
		for i := 0; i+1 < len(out); i++ {
			if out[i] == 0xFF && out[i+1] >= 0xD0 && out[i+1] <= 0xD7 {
				out[i+1] = 0xD0 + (out[i+1]-0xD0+1)&7
				return out
			}
		}
		return out
	}
	var stripped []byte
	for i := 0; i < len(out); i++ {
		if out[i] == 0xFF && i+1 < len(out) && out[i+1] >= 0xD0 && out[i+1] <= 0xD7 {
			i++
			continue
		}
		stripped = append(stripped, out[i])
	}
	return stripped
}
