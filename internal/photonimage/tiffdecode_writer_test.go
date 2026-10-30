// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"sort"
)

// A small TIFF writer for the corpus of tiffdecode_corpus_test.go: it writes the files tiff 0.9.1 reads (and refuses), in either byte
// order, with every compression the decoder knows, strips or tiles, chunky or planar, and lets the corpus change any tag or chunk.

// tiffEntrySpec is an IFD entry: the data is the value in the file's byte order (count * size bytes).
type tiffEntrySpec struct {
	tag   uint16
	typ   uint16
	count uint32
	data  []byte
	// offset, when set, places the data at that file offset instead of after the chunks (the value then lies where the corpus says).
	forceExternal bool
}

type tiffOrder struct{ little bool }

func (o tiffOrder) order() interface {
	binary.ByteOrder
	binary.AppendByteOrder
} {
	if o.little {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func (o tiffOrder) u16(v uint16) []byte { return o.order().AppendUint16(nil, v) }
func (o tiffOrder) u32(v uint32) []byte { return o.order().AppendUint32(nil, v) }

// Entry constructors.

func (o tiffOrder) shorts(tag uint16, vals ...uint64) tiffEntrySpec {
	var data []byte
	for _, v := range vals {
		data = append(data, o.u16(uint16(v))...)
	}
	return tiffEntrySpec{tag: tag, typ: tiffTypeShort, count: uint32(len(vals)), data: data}
}

func (o tiffOrder) longs(tag uint16, vals ...uint64) tiffEntrySpec {
	var data []byte
	for _, v := range vals {
		data = append(data, o.u32(uint32(v))...)
	}
	return tiffEntrySpec{tag: tag, typ: tiffTypeLong, count: uint32(len(vals)), data: data}
}

func (o tiffOrder) bytesEntry(tag uint16, typ uint16, data []byte) tiffEntrySpec {
	size := int(tiffTypeSize(typ))
	return tiffEntrySpec{tag: tag, typ: typ, count: uint32(len(data) / size), data: data}
}

// ints is an unsigned integer entry of the given type.
func (o tiffOrder) ints(tag, typ uint16, vals ...uint64) tiffEntrySpec {
	var data []byte
	for _, v := range vals {
		switch tiffTypeSize(typ) {
		case 1:
			data = append(data, byte(v))
		case 2:
			data = append(data, o.u16(uint16(v))...)
		case 4:
			data = append(data, o.u32(uint32(v))...)
		default:
			data = append(data, o.order().AppendUint64(nil, v)...)
		}
	}
	return tiffEntrySpec{tag: tag, typ: typ, count: uint32(len(vals)), data: data}
}

// tiffParts is a TIFF before it is assembled: the chunks and the entries (the strip or tile offsets and byte counts are filled in
// by assemble unless the corpus sets them itself).
type tiffParts struct {
	order   tiffOrder
	chunks  [][]byte
	entries []tiffEntrySpec
	// offsetType and countType are the entry types of the strip or tile offsets and byte counts.
	offsetType, countType uint16
	tiled                 bool
	// nextIFD is the offset of the next IFD the first one names (0: none); extraIFD appends a second, empty IFD there when true.
	nextIFD uint32
	// ifdFirst puts the IFD before the chunks.
	ifdFirst bool
	// pad is bytes written between the header and the first chunk.
	pad int
	// skipOffsets and skipCounts leave the offsets and counts entries out.
	skipOffsets, skipCounts bool
	// offsetAdd is added to every chunk offset written.
	offsetAdd int64
	// countAdd is added to every chunk byte count written.
	countAdd int64
	// signature replaces the first four bytes when not empty.
	signature []byte
	// trailer is appended after everything.
	trailer []byte
}

func (p *tiffParts) set(e tiffEntrySpec) {
	for i := range p.entries {
		if p.entries[i].tag == e.tag {
			p.entries[i] = e
			return
		}
	}
	p.entries = append(p.entries, e)
}

func (p *tiffParts) remove(tag uint16) {
	for i := range p.entries {
		if p.entries[i].tag == tag {
			p.entries = append(p.entries[:i], p.entries[i+1:]...)
			return
		}
	}
}

func (p *tiffParts) find(tag uint16) *tiffEntrySpec {
	for i := range p.entries {
		if p.entries[i].tag == tag {
			return &p.entries[i]
		}
	}
	return nil
}

// assemble writes the file: header, chunks, the values that do not fit in an entry, the IFD (or the IFD first).
func (p *tiffParts) assemble() []byte {
	o := p.order
	var buf bytes.Buffer
	if o.little {
		buf.WriteString("II*\x00")
	} else {
		buf.WriteString("MM\x00*")
	}
	buf.Write(make([]byte, 4)) // the IFD offset, patched below
	buf.Write(make([]byte, p.pad))

	entries := append([]tiffEntrySpec(nil), p.entries...)
	stripTags := [2]uint16{tiffTagStripOffsets, tiffTagStripByteCounts}
	if p.tiled {
		stripTags = [2]uint16{tiffTagTileOffsets, tiffTagTileByteCounts}
	}
	var offsets, counts []uint64

	// The IFD first needs its size before the chunks: entries at 2 + 12n + 4 bytes.
	n := len(entries)
	if !p.skipOffsets {
		n++
	}
	if !p.skipCounts {
		n++
	}
	ifdSize := 2 + 12*n + 4
	if p.ifdFirst {
		buf.Write(make([]byte, ifdSize))
	}
	for _, chunk := range p.chunks {
		offsets = append(offsets, uint64(int64(buf.Len())+p.offsetAdd))
		counts = append(counts, uint64(int64(len(chunk))+p.countAdd))
		buf.Write(chunk)
		if buf.Len()%2 == 1 {
			buf.WriteByte(0)
		}
	}
	if !p.skipOffsets {
		entries = append(entries, o.ints(stripTags[0], p.offsetType, offsets...))
	}
	if !p.skipCounts {
		entries = append(entries, o.ints(stripTags[1], p.countType, counts...))
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].tag < entries[j].tag })

	ifd := o.u16(uint16(len(entries)))
	var external []tiffEntrySpec
	externalAt := buf.Len() + ifdSizeIfAfter(p.ifdFirst, ifdSize)
	var externalData bytes.Buffer
	for _, e := range entries {
		ifd = append(ifd, o.u16(e.tag)...)
		ifd = append(ifd, o.u16(e.typ)...)
		ifd = append(ifd, o.u32(e.count)...)
		if len(e.data) <= 4 && !e.forceExternal {
			ifd = append(ifd, e.data...)
			ifd = append(ifd, make([]byte, 4-len(e.data))...)
			continue
		}
		ifd = append(ifd, o.u32(uint32(externalAt+externalData.Len()))...)
		externalData.Write(e.data)
		if externalData.Len()%2 == 1 {
			externalData.WriteByte(0)
		}
		external = append(external, e)
	}
	ifd = append(ifd, o.u32(p.nextIFD)...)

	out := buf.Bytes()
	if p.ifdFirst {
		copy(out[8+p.pad:], ifd)
		out = append(out, externalData.Bytes()...)
		copy(out[4:], o.u32(uint32(8+p.pad)))
	} else {
		ifdAt := len(out)
		out = append(out, ifd...)
		out = append(out, externalData.Bytes()...)
		copy(out[4:], o.u32(uint32(ifdAt)))
	}
	if len(p.signature) > 0 {
		copy(out, p.signature)
	}
	return append(out, p.trailer...)
}

// ifdSizeIfAfter is the size of the IFD placed between the chunks and the external values (0 when the IFD comes first, the values then
// follow the chunks directly).
func ifdSizeIfAfter(ifdFirst bool, size int) int {
	if ifdFirst {
		return 0
	}
	return size
}

// tiffSpec describes an image; tiffBuild writes it.
type tiffSpec struct {
	little      bool
	width       int
	height      int
	samples     int
	bits        int
	photometric int
	compression int
	predictor   int
	planar      bool
	tileW       int
	tileH       int
	// rowsPerStrip is the strip height (0: the whole image in one strip).
	rowsPerStrip int
	// sampleFormat writes the SampleFormat tag when not empty.
	sampleFormat []uint16
	// extraSamples writes ExtraSamples = unassociated alpha for four-sample RGB.
	extraSamples bool
	// gen gives the sample c of the pixel (x, y); nil picks the default pattern of the seed.
	gen  func(x, y, c int) uint32
	seed uint32
	// lzw options.
	lzw tiffLZWOptions
	// jpegTables moves the JPEG tables of every strip to a JPEGTables entry.
	jpegTables bool
	// jpegQuality is the quality of JPEG strips (default 90).
	jpegQuality int
	// shortOffsets writes the offsets and byte counts as SHORT where they fit.
	shortOffsets bool
	// tweak changes the parts before they are assembled.
	tweak func(*tiffParts)
}

// tiffSample is the default pattern: flat runs, gradients and noise, so every compression has something to do.
func tiffSample(seed uint32, x, y, c, bits int) uint32 {
	h := func(a uint32) uint32 {
		a = (a ^ (a >> 16)) * 0x45d9f3b
		a = (a ^ (a >> 16)) * 0x45d9f3b
		return a ^ (a >> 16)
	}
	var v uint32
	switch (x/9 + y/7 + int(seed)) % 4 {
	case 0:
		v = uint32(x*5+y*3+c*40) * 7
	case 1:
		v = 200 - uint32(c)*30
	case 2:
		v = h(uint32(x)*7919+uint32(y)*104729+uint32(c)*13+seed) >> 3
	default:
		v = uint32(x/3*17 + c*11)
	}
	if bits > 8 {
		v = v*257 + h(uint32(x+y*131)+seed)%251
	}
	if bits < 32 {
		v &= 1<<bits - 1
	}
	return v
}

func (s *tiffSpec) sample(x, y, c int) uint32 {
	if s.gen != nil {
		return s.gen(x, y, c)
	}
	return tiffSample(s.seed, x, y, c, s.bits)
}

// tiffPackRow packs samples of the given width in bits into bytes in the file's byte order, most significant bit first, padded to a byte.
func tiffPackRow(order tiffOrder, samples []uint32, bits int) []byte {
	switch {
	case bits == 8:
		out := make([]byte, len(samples))
		for i, v := range samples {
			out[i] = byte(v)
		}
		return out
	case bits == 16:
		out := make([]byte, 0, 2*len(samples))
		for _, v := range samples {
			out = append(out, order.u16(uint16(v))...)
		}
		return out
	case bits == 32:
		out := make([]byte, 0, 4*len(samples))
		for _, v := range samples {
			out = append(out, order.u32(v)...)
		}
		return out
	}
	out := make([]byte, (len(samples)*bits+7)/8)
	pos := 0
	for _, v := range samples {
		for b := bits - 1; b >= 0; b-- {
			if v>>b&1 == 1 {
				out[pos>>3] |= 0x80 >> (pos & 7)
			}
			pos++
		}
	}
	return out
}

// tiffPredictRow applies the horizontal predictor to a row of samples with the given channel stride.
func tiffPredictRow(samples []uint32, stride, bits int) {
	mask := uint32(1)<<bits - 1
	if bits == 32 {
		mask = 0xFFFFFFFF
	}
	for i := len(samples) - 1; i >= stride; i-- {
		samples[i] = (samples[i] - samples[i-stride]) & mask
	}
}

// chunkData is the uncompressed data of the chunk covering the pixels [x0, x0+w) x [y0, y0+h) (padded with zeros past the image) of the
// given plane (-1: all samples interleaved).
func (s *tiffSpec) chunkData(x0, y0, w, h, plane int) []byte {
	order := tiffOrder{s.little}
	spp := s.samples
	if plane >= 0 {
		spp = 1
	}
	var out []byte
	for y := y0; y < y0+h; y++ {
		row := make([]uint32, 0, w*spp)
		for x := x0; x < x0+w; x++ {
			for c := range spp {
				var v uint32
				if x < s.width && y < s.height {
					ch := c
					if plane >= 0 {
						ch = plane
					}
					v = s.sample(x, y, ch)
				}
				row = append(row, v)
			}
		}
		if s.predictor == tiffPredictorHorizontal {
			tiffPredictRow(row, spp, s.bits)
		}
		out = append(out, tiffPackRow(order, row, s.bits)...)
	}
	return out
}

// tiffBuild writes the file of a spec.
func tiffBuild(s tiffSpec) []byte {
	return tiffPartsOf(s).assemble()
}

func tiffPartsOf(s tiffSpec) *tiffParts {
	o := tiffOrder{s.little}
	if s.samples == 0 {
		s.samples = 1
	}
	p := &tiffParts{order: o, offsetType: tiffTypeLong, countType: tiffTypeLong}
	if s.shortOffsets {
		p.offsetType, p.countType = tiffTypeShort, tiffTypeShort
	}

	planes := 1
	if s.planar {
		planes = s.samples
	}
	if s.tileW > 0 {
		p.tiled = true
		across, down := (s.width+s.tileW-1)/s.tileW, (s.height+s.tileH-1)/s.tileH
		for plane := range planes {
			for ty := range down {
				for tx := range across {
					pl := -1
					if s.planar {
						pl = plane
					}
					raw := s.chunkData(tx*s.tileW, ty*s.tileH, s.tileW, s.tileH, pl)
					p.chunks = append(p.chunks, s.compressChunkAt(raw, s.tileW, s.tileH, pl))
				}
			}
		}
		p.entries = append(p.entries, o.longs(tiffTagTileWidth, uint64(s.tileW)), o.longs(tiffTagTileLength, uint64(s.tileH)))
	} else {
		rps := s.rowsPerStrip
		if rps == 0 {
			rps = s.height
		}
		for plane := range planes {
			for y := 0; y < s.height; y += rps {
				h := min(rps, s.height-y)
				pl := -1
				if s.planar {
					pl = plane
				}
				raw := s.chunkData(0, y, s.width, h, pl)
				p.chunks = append(p.chunks, s.compressChunkAt(raw, s.width, h, pl))
			}
		}
		p.entries = append(p.entries, o.longs(tiffTagRowsPerStrip, uint64(rps)))
	}

	bits := make([]uint64, s.samples)
	for i := range bits {
		bits[i] = uint64(s.bits)
	}
	p.entries = append(p.entries,
		o.longs(tiffTagImageWidth, uint64(s.width)),
		o.longs(tiffTagImageLength, uint64(s.height)),
		o.shorts(tiffTagBitsPerSample, bits...),
		o.shorts(tiffTagCompression, uint64(s.compression)),
		o.shorts(tiffTagPhotometricInterpretation, uint64(s.photometric)),
		o.shorts(tiffTagSamplesPerPixel, uint64(s.samples)),
	)
	if s.predictor != 0 {
		p.entries = append(p.entries, o.shorts(tiffTagPredictor, uint64(s.predictor)))
	}
	if s.planar {
		p.entries = append(p.entries, o.shorts(tiffTagPlanarConfiguration, 2))
	}
	if len(s.sampleFormat) > 0 {
		vals := make([]uint64, len(s.sampleFormat))
		for i, f := range s.sampleFormat {
			vals[i] = uint64(f)
		}
		p.entries = append(p.entries, o.shorts(tiffTagSampleFormat, vals...))
	}
	if s.extraSamples {
		p.entries = append(p.entries, o.shorts(338, 2))
	}
	if s.compression == tiffCompressionModernJPEG && s.jpegTables {
		p.entries = append(p.entries, o.bytesEntry(tiffTagJPEGTables, tiffTypeUndefined, s.jpegTablesData()))
	}
	if s.tweak != nil {
		s.tweak(p)
	}
	return p
}

func (s *tiffSpec) compressChunkAt(raw []byte, w, h, plane int) []byte {
	switch s.compression {
	case tiffCompressionNone:
		return raw
	case tiffCompressionLZW:
		return tiffLZWEncode(raw, s.lzw)
	case tiffCompressionPackBits:
		return tiffPackBitsEncode(raw)
	case tiffCompressionDeflate, tiffCompressionOldDeflate:
		var out bytes.Buffer
		zw := zlib.NewWriter(&out)
		_, _ = zw.Write(raw)
		_ = zw.Close()
		return out.Bytes()
	case tiffCompressionModernJPEG:
		return s.jpegChunk(raw, w, h, plane)
	}
	return raw
}

// tiffPackBitsEncode is PackBits with runs of three or more equal bytes, literals of up to 128, and a no-op byte now and then.
func tiffPackBitsEncode(raw []byte) []byte {
	var out []byte
	i := 0
	for i < len(raw) {
		run := 1
		for i+run < len(raw) && raw[i+run] == raw[i] && run < 128 {
			run++
		}
		if run >= 3 {
			out = append(out, byte(int8(1-run)), raw[i])
			i += run
			continue
		}
		start := i
		for i < len(raw) && i-start < 128 {
			r := 1
			for i+r < len(raw) && raw[i+r] == raw[i] && r < 3 {
				r++
			}
			if r >= 3 {
				break
			}
			i++
		}
		if i == start {
			i++
		}
		out = append(out, byte(i-start-1))
		out = append(out, raw[start:i]...)
		if i%97 == 0 {
			out = append(out, 0x80) // -128: a no-op
		}
	}
	return out
}

// tiffLZWOptions varies the LZW stream a strip gets.
type tiffLZWOptions struct {
	// noInitialClear leaves out the clear code the stream starts with.
	noInitialClear bool
	// noEnd leaves out the end code.
	noEnd bool
	// clearAt is the table size at which the encoder sends a clear code (0: 4094; 4096 or more: never, the table stays full).
	clearAt int
	// lateSwitch uses the GIF rule for the code size (one code later than TIFF's), which a TIFF decoder reads wrongly.
	lateSwitch bool
}

// tiffLZWEncode is TIFF LZW, most significant bit first, with the early change.
func tiffLZWEncode(raw []byte, opt tiffLZWOptions) []byte {
	var out []byte
	var acc uint64
	var nbits uint
	emit := func(code, size int) {
		acc = acc<<uint(size) | uint64(code)
		nbits += uint(size)
		for nbits >= 8 {
			out = append(out, byte(acc>>(nbits-8)))
			nbits -= 8
		}
	}
	clearAt := opt.clearAt
	if clearAt == 0 {
		clearAt = 4094
	}
	size, next := 9, 258
	table := map[uint32]int{}
	reset := func() {
		table = map[uint32]int{}
		size, next = 9, 258
	}
	if !opt.noInitialClear {
		emit(256, 9)
	}
	if len(raw) > 0 {
		cur := int(raw[0])
		for _, b := range raw[1:] {
			key := uint32(cur)<<8 | uint32(b)
			if code, ok := table[key]; ok {
				cur = code
				continue
			}
			emit(cur, size)
			if next < 4096 {
				table[key] = next
				next++
				switch {
				case opt.lateSwitch && next == 1<<size+1 && size < 12:
					size++
				case !opt.lateSwitch && next == 1<<size && size < 12:
					size++
				}
			}
			if next >= clearAt && clearAt < 4096 {
				emit(256, size)
				reset()
			}
			cur = int(b)
		}
		emit(cur, size)
		// libtiff's LZWPostEncode counts the last code as an entry too: the width of the end code follows.
		if !opt.lateSwitch && next < 4096 {
			next++
			if next == 4095 {
				emit(256, size)
				size = 9
			} else if next == 1<<size && size < 12 {
				size++
			}
		}
	}
	if !opt.noEnd {
		emit(257, size)
	}
	if nbits > 0 {
		out = append(out, byte(acc<<(8-nbits)))
	}
	return out
}

// jpegChunk encodes a chunk as a JPEG with Go's encoder: grayscale for one sample, YCbCr (three components) for three; CMYK has no Go
// encoder, so a four-sample chunk is built from a hand-made baseline frame.
func (s *tiffSpec) jpegChunk(raw []byte, w, h, plane int) []byte {
	quality := s.jpegQuality
	if quality == 0 {
		quality = 90
	}
	spp := s.samples
	if plane >= 0 {
		spp = 1
	}
	var img image.Image
	switch spp {
	case 1:
		g := image.NewGray(image.Rect(0, 0, w, h))
		copy(g.Pix, raw)
		img = g
	case 3:
		n := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i := range w * h {
			n.SetNRGBA(i%w, i/w, color.NRGBA{R: raw[3*i], G: raw[3*i+1], B: raw[3*i+2], A: 255})
		}
		img = n
	default:
		// Four components are not something Go's encoder writes: use the gray plane of the first sample (a one-component JPEG) so
		// that the data under a CMYK or RGBA image is a valid JPEG with the wrong component count.
		g := image.NewGray(image.Rect(0, 0, w, h))
		for i := range w * h {
			g.Pix[i] = raw[spp*i]
		}
		img = g
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		panic(err)
	}
	data := out.Bytes()
	if !s.jpegTables {
		return data
	}
	_, strip := splitJPEGTables(data)
	return strip
}

// jpegTablesData is the JPEGTables stream of the spec: the tables (DQT and DHT segments) of a JPEG of the spec's kind between SOI and EOI.
func (s *tiffSpec) jpegTablesData() []byte {
	probe := *s
	probe.jpegTables = false
	spp := s.samples
	if s.planar {
		spp = 1
	}
	probe.samples = spp
	raw := make([]byte, 8*8*spp)
	tables, _ := splitJPEGTables(probe.jpegChunk(raw, 8, 8, -1))
	return tables
}

// splitJPEGTables splits a JPEG into a tables-only stream (SOI, the DQT and DHT segments, EOI) and an abbreviated one (SOI, the rest).
func splitJPEGTables(data []byte) (tables, strip []byte) {
	tables = []byte{0xFF, 0xD8}
	strip = []byte{0xFF, 0xD8}
	pos := 2
	for pos+4 <= len(data) {
		marker := data[pos+1]
		length := int(data[pos+2])<<8 | int(data[pos+3])
		segment := data[pos : pos+2+length]
		if marker == 0xDB || marker == 0xC4 {
			tables = append(tables, segment...)
		} else {
			strip = append(strip, segment...)
		}
		pos += 2 + length
		if marker == 0xDA { // SOS: the entropy-coded data follows to the end
			strip = append(strip, data[pos:]...)
			break
		}
	}
	return append(tables, 0xFF, 0xD9), strip
}
