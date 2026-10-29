// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"slices"
)

// A JPEG writer for the corpus of jpegdecode_corpus_test.go: sequential, extended sequential, progressive and lossless scans over
// quantized coefficients (or samples), with any sampling factors, restart intervals, scan scripts and marker segments. It is test
// code, integer-only and deterministic; it is not an encoder anyone should use (the Huffman tables are Annex K or optimized per
// scan, the quantization is a rounding division), but its streams are valid, so that every decoder path of jpeg-decoder 0.3.1 is
// reached by a stream that means something. TestJPEGWriterProgressiveEqualsSequential checks the progressive encoder against the
// sequential one, and TestJPEGWriterBaselineMatchesGo checks the whole writer against Go's image/jpeg decoder.

// jdwHuff is a Huffman table for writing: the DHT form (bits, values) and the code of each value.
type jdwHuff struct {
	bits [16]uint8
	vals []uint8
	code [256]uint16
	size [256]uint8 // 0: the value has no code
}

// jdwNewHuff derives the codes of Annex C Figures C.1 and C.2.
func jdwNewHuff(counts [16]uint8, vals []uint8) *jdwHuff {
	h := &jdwHuff{bits: counts, vals: slices.Clone(vals)}
	code, k := uint32(0), 0
	for length := 1; length <= 16; length++ {
		for range counts[length-1] {
			h.code[vals[k]] = uint16(code)
			h.size[vals[k]] = uint8(length)
			code++
			k++
		}
		code <<= 1
	}
	return h
}

// jdwOptimalHuff is Annex K.2 (the procedure of libjpeg's jpeg_gen_optimal_table) over the frequencies of 256 values; a table
// always has at least one value.
func jdwOptimalHuff(freq *[256]int64) *jdwHuff {
	var f [257]int64
	copy(f[:], freq[:])
	f[256] = 1 // reserve the all-ones code point
	used := false
	for _, v := range freq {
		used = used || v > 0
	}
	if !used {
		f[0] = 1
	}
	var codesize [257]int
	var others [257]int
	for i := range others {
		others[i] = -1
	}
	for {
		c1 := -1
		var v int64 = 1 << 60
		for i := range 257 {
			if f[i] != 0 && f[i] <= v {
				v, c1 = f[i], i
			}
		}
		c2 := -1
		v = 1 << 60
		for i := range 257 {
			if f[i] != 0 && f[i] <= v && i != c1 {
				v, c2 = f[i], i
			}
		}
		if c2 < 0 {
			break
		}
		f[c1] += f[c2]
		f[c2] = 0
		codesize[c1]++
		for others[c1] >= 0 {
			c1 = others[c1]
			codesize[c1]++
		}
		others[c1] = c2
		codesize[c2]++
		for others[c2] >= 0 {
			c2 = others[c2]
			codesize[c2]++
		}
	}
	var counts [33]int
	for i := range 257 {
		if codesize[i] > 0 {
			counts[codesize[i]]++
		}
	}
	for i := 32; i > 16; i-- {
		for counts[i] > 0 {
			j := i - 2
			for counts[j] == 0 {
				j--
			}
			counts[i] -= 2
			counts[i-1]++
			counts[j+1] += 2
			counts[j]--
		}
	}
	i := 16
	for counts[i] == 0 {
		i--
	}
	counts[i]-- // the reserved code point
	var bitsOut [16]uint8
	for l := 1; l <= 16; l++ {
		bitsOut[l-1] = uint8(counts[l])
	}
	var vals []uint8
	for l := 1; l <= 32; l++ {
		for s := range 256 {
			if codesize[s] == l {
				vals = append(vals, uint8(s))
			}
		}
	}
	return jdwNewHuff(bitsOut, vals)
}

// jdwBitWriter writes the entropy-coded bits with byte stuffing.
type jdwBitWriter struct {
	out  []byte
	acc  uint64
	nacc uint
}

func (w *jdwBitWriter) put(value uint32, n uint) {
	if n == 0 {
		return
	}
	w.acc = w.acc<<n | uint64(value)&(1<<n-1)
	w.nacc += n
	for w.nacc >= 8 {
		b := byte(w.acc >> (w.nacc - 8))
		w.out = append(w.out, b)
		if b == 0xFF {
			w.out = append(w.out, 0)
		}
		w.nacc -= 8
	}
}

// flush pads the last byte with 1 bits.
func (w *jdwBitWriter) flush() {
	if w.nacc > 0 {
		w.put(1<<(8-w.nacc)-1, 8-w.nacc)
	}
	w.acc = 0
}

// jdwSink receives the symbols and bits of a scan: it counts the frequencies in the first pass and writes in the second.
type jdwSink struct {
	count   bool
	freq    [2][4][256]int64
	huff    [2][4]*jdwHuff
	w       jdwBitWriter
	rstNext int
}

func (s *jdwSink) symbol(class, table int, sym uint8) {
	if s.count {
		s.freq[class][table][sym]++
		return
	}
	h := s.huff[class][table]
	if h.size[sym] == 0 {
		panic("jpeg writer: symbol without a code")
	}
	s.w.put(uint32(h.code[sym]), uint(h.size[sym]))
}

func (s *jdwSink) bits(value uint32, n uint) {
	if !s.count {
		s.w.put(value, n)
	}
}

// restart ends the interval: pad, and write RSTn.
func (s *jdwSink) restart() {
	if s.count {
		return
	}
	s.w.flush()
	s.w.out = append(s.w.out, 0xFF, byte(0xD0+s.rstNext))
	s.rstNext = (s.rstNext + 1) & 7
}

func jdwNBits(v int) uint { return uint(bits.Len(uint(v))) }

// jdwComponent is the frame header's entry of a component and its blocks (natural order, block-row major over the padded grid of
// blockW x blockH blocks) or, for lossless, its samples.
type jdwComponent struct {
	id, h, v, tq   uint8
	tdc, tac       int
	blockW, blockH int
	blocks         [][64]int16
	samples        []uint16 // lossless: width*height
}

// jdwScan is one scan of a script: component indices, spectral selection and successive approximation (the predictor and point
// transform of a lossless scan are ss and al).
type jdwScan struct {
	comps          []int
	ss, se, ah, al int
}

// jdwSpec is a stream.
type jdwSpec struct {
	width, height int
	precision     int
	sof           byte // 0xC0, 0xC1, 0xC2 or 0xC3
	comps         []jdwComponent
	qtab          [4][64]uint16 // natural order
	q16           bool
	restart       int
	scans         []jdwScan
	stdTables     bool // Annex K tables (sequential, 8-bit, small magnitudes) instead of optimized ones
	rustGeometry  bool // non-interleaved blocks as jpeg-decoder counts them rather than as the standard does
	splitDHT      bool // one DHT segment per table
	dcAsDiff      bool // the DC coefficient of a block is the DC difference to code (the predictor then overflows int16 as in a hostile file)
	// marker segments before the frame
	jfif    bool
	adobe   int // -1: none
	extra   [][]byte
	comment bool
}

// jdwSeg is a segment of a stream in order (the marker and the data, or the entropy-coded data of a scan after its header).
type jdwSeg struct {
	name string
	data []byte
}

func jdwMarkerSeg(name string, marker byte, payload []byte) jdwSeg {
	d := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(d[2:], uint16(len(payload)+2))
	return jdwSeg{name, append(d, payload...)}
}

func (s *jdwSpec) hmax() int {
	m := 1
	for _, c := range s.comps {
		m = max(m, int(c.h))
	}
	return m
}

func (s *jdwSpec) vmax() int {
	m := 1
	for _, c := range s.comps {
		m = max(m, int(c.v))
	}
	return m
}

func jdwCeilDiv(a, b int) int { return (a + b - 1) / b }

// jdwGrid fills the block grids of the components from the frame size.
func (s *jdwSpec) jdwGrid() (mcuW, mcuH int) {
	mcuW = jdwCeilDiv(s.width, 8*s.hmax())
	mcuH = jdwCeilDiv(s.height, 8*s.vmax())
	for i := range s.comps {
		s.comps[i].blockW = mcuW * int(s.comps[i].h)
		s.comps[i].blockH = mcuH * int(s.comps[i].v)
	}
	return mcuW, mcuH
}

// jdwBlockRef is a block of an MCU: a component and its position in the padded grid.
type jdwBlockRef struct{ comp, bx, by int }

// jdwMCUs lists the MCUs of a scan in order.
func (s *jdwSpec) jdwMCUs(scan jdwScan) [][]jdwBlockRef {
	mcuW, mcuH := s.jdwGrid()
	var mcus [][]jdwBlockRef
	if len(scan.comps) > 1 {
		for my := range mcuH {
			for mx := range mcuW {
				var mcu []jdwBlockRef
				for _, ci := range scan.comps {
					c := s.comps[ci]
					for vy := range int(c.v) {
						for hx := range int(c.h) {
							mcu = append(mcu, jdwBlockRef{ci, mx*int(c.h) + hx, my*int(c.v) + vy})
						}
					}
				}
				mcus = append(mcus, mcu)
			}
		}
		return mcus
	}
	ci := scan.comps[0]
	c := s.comps[ci]
	cols, rows := jdwCeilDiv(jdwCeilDiv(s.width*int(c.h), s.hmax()), 8), jdwCeilDiv(jdwCeilDiv(s.height*int(c.v), s.vmax()), 8)
	if s.rustGeometry {
		// the loop of decode_scan: block rows and columns of the padded grid that start inside the image
		cols, rows = 0, 0
		for x := 0; x < c.blockW && x*8 < s.width; x++ {
			cols++
		}
		for y := 0; y < c.blockH && y*8 < s.height; y++ {
			rows++
		}
	}
	for by := range rows {
		for bx := range cols {
			mcus = append(mcus, []jdwBlockRef{{ci, bx, by}})
		}
	}
	return mcus
}

// jdwProgState is the state of a progressive entropy encoder: the run of empty blocks and the correction bits that wait for it.
type jdwProgState struct {
	eobrun int
	be     []uint8
}

func (p *jdwProgState) emitEOBRun(s *jdwSink, tac int) {
	if p.eobrun > 0 {
		n := jdwNBits(p.eobrun) - 1
		s.symbol(1, tac, uint8(n<<4))
		if n > 0 {
			s.bits(uint32(p.eobrun), n)
		}
		p.eobrun = 0
		for _, b := range p.be {
			s.bits(uint32(b), 1)
		}
		p.be = p.be[:0]
	}
}

func jdwDiffBits(diff int) (nbits uint, value uint32) {
	nbits = jdwNBits(jdwAbs(diff))
	if diff < 0 {
		diff--
	}
	return nbits, uint32(diff)
}

func jdwAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// encodeBlock writes one block of a scan.
func (s *jdwSpec) encodeBlock(sink *jdwSink, p *jdwProgState, scan jdwScan, ref jdwBlockRef, pred *[256]int) {
	c := &s.comps[ref.comp]
	blk := &c.blocks[ref.by*c.blockW+ref.bx]
	progressive := s.sof == 0xC2
	if !progressive {
		diff := int(blk[0]) - pred[ref.comp]
		pred[ref.comp] = int(blk[0])
		if s.dcAsDiff {
			diff = int(blk[0])
		}
		n, v := jdwDiffBits(diff)
		sink.symbol(0, c.tdc, uint8(n))
		sink.bits(v, n)
		run := 0
		for k := 1; k < 64; k++ {
			coef := int(blk[unzigzag[k]])
			if coef == 0 {
				run++
				continue
			}
			for run > 15 {
				sink.symbol(1, c.tac, 0xF0)
				run -= 16
			}
			n, v := jdwDiffBits(coef)
			sink.symbol(1, c.tac, uint8(run<<4)|uint8(n))
			sink.bits(v, n)
			run = 0
		}
		if run > 0 {
			sink.symbol(1, c.tac, 0)
		}
		return
	}

	switch {
	case scan.ss == 0 && scan.ah == 0: // DC first
		v := int(blk[0]) >> scan.al
		diff := v - pred[ref.comp]
		pred[ref.comp] = v
		if s.dcAsDiff {
			diff = int(blk[0])
		}
		n, bitsv := jdwDiffBits(diff)
		sink.symbol(0, c.tdc, uint8(n))
		sink.bits(bitsv, n)
	case scan.ss == 0: // DC refinement
		sink.bits(uint32(int(blk[0])>>scan.al)&1, 1)
	case scan.ah == 0: // AC first
		run := 0
		for k := scan.ss; k <= scan.se; k++ {
			coef := int(blk[unzigzag[k]])
			t := jdwAbs(coef) >> scan.al
			if t == 0 {
				run++
				continue
			}
			p.emitEOBRun(sink, c.tac)
			for run > 15 {
				sink.symbol(1, c.tac, 0xF0)
				run -= 16
			}
			n := jdwNBits(t)
			sink.symbol(1, c.tac, uint8(run<<4)|uint8(n))
			if coef < 0 {
				t = ^t
			}
			sink.bits(uint32(t), n)
			run = 0
		}
		if run > 0 {
			p.eobrun++
			if p.eobrun == 0x7FFF {
				p.emitEOBRun(sink, c.tac)
			}
		}
	default: // AC refinement (jcphuff.c encode_mcu_AC_refine)
		var absv [64]int
		eob := 0
		for k := scan.ss; k <= scan.se; k++ {
			absv[k] = jdwAbs(int(blk[unzigzag[k]])) >> scan.al
			if absv[k] == 1 {
				eob = k
			}
		}
		run := 0
		var br []uint8
		flushBR := func() {
			for _, b := range br {
				sink.bits(uint32(b), 1)
			}
			br = br[:0]
		}
		for k := scan.ss; k <= scan.se; k++ {
			t := absv[k]
			if t == 0 {
				run++
				continue
			}
			for run > 15 && k <= eob {
				p.emitEOBRun(sink, c.tac)
				sink.symbol(1, c.tac, 0xF0)
				run -= 16
				flushBR()
			}
			if t > 1 {
				br = append(br, uint8(t&1))
				continue
			}
			p.emitEOBRun(sink, c.tac)
			sink.symbol(1, c.tac, uint8(run<<4)|1)
			if blk[unzigzag[k]] < 0 {
				sink.bits(0, 1)
			} else {
				sink.bits(1, 1)
			}
			flushBR()
			run = 0
		}
		if run > 0 || len(br) > 0 {
			p.eobrun++
			p.be = append(p.be, br...)
			if p.eobrun == 0x7FFF || len(p.be) > 1000-64+1 {
				p.emitEOBRun(sink, c.tac)
			}
		}
	}
}

// encodeScan writes (or counts) one scan's entropy-coded segment.
func (s *jdwSpec) encodeScan(sink *jdwSink, scan jdwScan) {
	if s.sof == 0xC3 {
		s.encodeLossless(sink, scan)
		return
	}
	mcus := s.jdwMCUs(scan)
	var pred [256]int
	var p jdwProgState
	tac := s.comps[scan.comps[0]].tac
	for n, mcu := range mcus {
		if s.restart > 0 && n > 0 && n%s.restart == 0 {
			if s.sof == 0xC2 {
				p.emitEOBRun(sink, tac)
			}
			sink.restart()
			pred = [256]int{}
		}
		for _, ref := range mcu {
			s.encodeBlock(sink, &p, scan, ref, &pred)
			if s.sof == 0xC2 {
				tac = s.comps[ref.comp].tac
			}
		}
	}
	if s.sof == 0xC2 {
		p.emitEOBRun(sink, tac)
	}
}

// encodeLossless writes the DPCM differences of a lossless scan (Section H).
func (s *jdwSpec) encodeLossless(sink *jdwSink, scan jdwScan) {
	predictor, pt := scan.ss, scan.al
	w, h := s.width, s.height
	n := 0
	for y := range h {
		for x := range w {
			if s.restart > 0 && n > 0 && n%s.restart == 0 {
				sink.restart()
			}
			n++
			for _, ci := range scan.comps {
				c := s.comps[ci]
				sample := func(x, y int) int { return int(c.samples[y*w+x] >> pt) }
				var pred int
				switch {
				case x == 0 && y == 0, s.restart > 0 && (n-1)%s.restart == 0:
					pred = 1 << (s.precision - pt - 1)
				case y == 0:
					pred = sample(x-1, y)
				case x == 0:
					pred = sample(x, y-1)
				default:
					ra, rb, rc := sample(x-1, y), sample(x, y-1), sample(x-1, y-1)
					switch predictor {
					case 0:
						pred = 0
					case 1:
						pred = ra
					case 2:
						pred = rb
					case 3:
						pred = rc
					case 4:
						pred = ra + rb - rc
					case 5:
						pred = ra + (rb-rc)>>1
					case 6:
						pred = rb + (ra-rc)>>1
					default:
						pred = (ra + rb) / 2
					}
				}
				diff := (sample(x, y) - pred) & 0xFFFF
				if diff >= 32768 {
					diff -= 65536
				}
				if diff == -32768 {
					sink.symbol(0, c.tdc, 16)
					continue
				}
				nb, v := jdwDiffBits(diff)
				sink.symbol(0, c.tdc, uint8(nb))
				sink.bits(v, nb)
			}
		}
	}
}

// jdwStdTables are the Annex K tables, indexed [class][table].
var jdwStdTables = [2][2]*jdwHuff{
	{jdwNewHuff(stdLumaDCCodeLengths, stdDCValues[:]), jdwNewHuff(stdChromaDCCodeLengths, stdDCValues[:])},
	{jdwNewHuff(stdLumaACCodeLengths, stdLumaACValues[:]), jdwNewHuff(stdChromaACCodeLengths, stdChromaACValues[:])},
}

func jdwDHT(class, index int, h *jdwHuff) []byte {
	b := []byte{byte(class<<4 | index)}
	b = append(b, h.bits[:]...)
	return append(b, h.vals...)
}

// segments lays the stream out, segment by segment.
func (s *jdwSpec) segments() []jdwSeg {
	s.jdwGrid()
	var segs []jdwSeg
	segs = append(segs, jdwSeg{"soi", []byte{0xFF, 0xD8}})
	if s.jfif {
		segs = append(segs, jdwMarkerSeg("jfif", 0xE0, []byte("JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")))
	}
	if s.adobe >= 0 {
		segs = append(segs, jdwMarkerSeg("adobe", 0xEE, []byte{'A', 'd', 'o', 'b', 'e', 0, 100, 0, 0, 0, 0, byte(s.adobe)}))
	}
	for _, e := range s.extra {
		segs = append(segs, jdwSeg{"extra", e})
	}
	if s.comment {
		segs = append(segs, jdwMarkerSeg("com", 0xFE, []byte("generated by jpegdecode_writer_test.go")))
	}
	used := map[uint8]bool{}
	for _, c := range s.comps {
		used[c.tq] = true
	}
	for t := range 4 {
		if !used[uint8(t)] {
			continue
		}
		var payload []byte
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
		segs = append(segs, jdwMarkerSeg("dqt", 0xDB, payload))
	}
	if s.restart > 0 {
		segs = append(segs, jdwMarkerSeg("dri", 0xDD, binary.BigEndian.AppendUint16(nil, uint16(s.restart))))
	}
	sof := []byte{byte(s.precision), byte(s.height >> 8), byte(s.height), byte(s.width >> 8), byte(s.width), byte(len(s.comps))}
	for _, c := range s.comps {
		sof = append(sof, c.id, c.h<<4|c.v, c.tq)
	}
	segs = append(segs, jdwMarkerSeg("sof", s.sof, sof))

	if s.stdTables {
		var payload []byte
		for class := range 2 {
			for t := range 2 {
				payload = append(payload, jdwDHT(class, t, jdwStdTables[class][t])...)
			}
		}
		segs = append(segs, jdwMarkerSeg("dht", 0xC4, payload))
	}
	for _, scan := range s.scans {
		// pass 1: the frequencies of the symbols; pass 2: the entropy-coded data
		sink := &jdwSink{count: true}
		s.encodeScan(sink, scan)
		sink.count = false
		var dht []jdwSeg
		var payload []byte
		for class := range 2 {
			for t := range 4 {
				if !s.scanUses(scan, class, t) {
					continue
				}
				if s.stdTables {
					sink.huff[class][t] = jdwStdTables[class][min(t, 1)]
					continue
				}
				h := jdwOptimalHuff(&sink.freq[class][t])
				sink.huff[class][t] = h
				if s.splitDHT {
					dht = append(dht, jdwMarkerSeg("dht", 0xC4, jdwDHT(class, t, h)))
				} else {
					payload = append(payload, jdwDHT(class, t, h)...)
				}
			}
		}
		if len(payload) > 0 {
			dht = append(dht, jdwMarkerSeg("dht", 0xC4, payload))
		}
		segs = append(segs, dht...)
		sos := []byte{byte(len(scan.comps))}
		for _, ci := range scan.comps {
			c := s.comps[ci]
			sos = append(sos, c.id, byte(c.tdc<<4|c.tac))
		}
		sos = append(sos, byte(scan.ss), byte(scan.se), byte(scan.ah<<4|scan.al))
		segs = append(segs, jdwMarkerSeg("sos", 0xDA, sos))
		s.encodeScan(sink, scan)
		sink.w.flush()
		segs = append(segs, jdwSeg{"ecs", sink.w.out})
	}
	segs = append(segs, jdwSeg{"eoi", []byte{0xFF, 0xD9}})
	return segs
}

// scanUses reports whether the scan codes symbols with the table (class, index).
func (s *jdwSpec) scanUses(scan jdwScan, class, index int) bool {
	for _, ci := range scan.comps {
		c := s.comps[ci]
		if class == 0 && c.tdc == index && (s.sof == 0xC3 || scan.ss == 0 && scan.ah == 0) {
			return true
		}
		if class == 1 && c.tac == index && s.sof != 0xC3 && scan.se > 0 && (s.sof != 0xC2 || scan.ss > 0) {
			return true
		}
	}
	return false
}

// build is the stream.
func (s *jdwSpec) build() []byte { return jdwJoin(s.segments()) }

func jdwJoin(segs []jdwSeg) []byte {
	var out bytes.Buffer
	for _, seg := range segs {
		out.Write(seg.data)
	}
	return out.Bytes()
}
