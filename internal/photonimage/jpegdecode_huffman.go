// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"encoding/binary"
	"slices"
)

// Ports jpeg-decoder 0.3.1 src/huffman.rs (cited as huffman.rs).

const jpegLUTBits = 8 // huffman.rs:12

// jpegHuffmanDecoder is huffman.rs:14-18 HuffmanDecoder: a 64-bit bit buffer that reads ahead of what the scan consumed, and the
// marker that ended the entropy-coded data, once met.
type jpegHuffmanDecoder struct {
	bits      uint64
	numBits   uint8
	hasMarker bool
	marker    byte
}

// jpegLUTEntry is one (value, size) of huffman.rs:186 lut.
type jpegLUTEntry struct{ value, size uint8 }

// jpegACLUTEntry is one (value, run_size) of huffman.rs:187 ac_lut.
type jpegACLUTEntry struct {
	value   int16
	runSize uint8
}

// jpegHuffmanTable is huffman.rs:181-189 HuffmanTable.
type jpegHuffmanTable struct {
	values  []uint8
	delta   [16]int32
	maxcode [16]int32
	lut     [1 << jpegLUTBits]jpegLUTEntry
	acLUT   *[1 << jpegLUTBits]jpegACLUTEntry
}

// readBits is huffman.rs:123-163 read_bits. Once the marker is met the buffer is filled with zero bits. The loop of the Rust code
// appends one byte at a time while the buffer holds 56 bits or fewer, that is k = (56 - numBits) / 8 + 1 bytes; when none of
// them is 0xFF (so none is stuffing or the start of a marker) they are taken as one word, and when the marker has been met the
// k zero bytes are just counted, which are the same bits and the same positions as the loop gives; anything else, and the end of
// the input, is the loop itself (readBitsSlow).
func (h *jpegHuffmanDecoder) readBits(r *jpegReader) error {
	if h.numBits > 56 {
		return nil
	}
	k := (56-h.numBits)/8 + 1
	if h.hasMarker {
		h.numBits += 8 * k
		return nil
	}
	if r.pos+8 <= len(r.data) {
		head := binary.BigEndian.Uint64(r.data[r.pos:]) &^ (^uint64(0) >> (8 * k))
		// a zero byte of the complement is a 0xFF byte of the first k bytes (the rest of the word is 0x00, whose complement is not)
		if inverse := ^head; (inverse-0x0101010101010101)&^inverse&0x8080808080808080 == 0 {
			h.bits |= head >> h.numBits
			h.numBits += 8 * k
			r.pos += int(k)
			return nil
		}
	}
	return h.readBitsSlow(r)
}

// readBitsSlow is the loop of read_bits, byte by byte.
func (h *jpegHuffmanDecoder) readBitsSlow(r *jpegReader) error {
	for h.numBits <= 56 {
		var byt byte
		if !h.hasMarker {
			b, err := r.readU8()
			if err != nil {
				return err
			}
			byt = b
		}
		if byt == 0xFF {
			next, err := r.readU8()
			if err != nil {
				return err
			}
			// Check for byte stuffing.
			if next != 0x00 {
				// The end of the entropy-coded data: read on to identify the marker, skipping fill bytes.
				for next == 0xFF {
					next, err = r.readU8()
					if err != nil {
						return err
					}
				}
				if next == 0x00 {
					return jpegFormatError("FF 00 found where marker was expected")
				}
				h.marker, h.hasMarker = next, true
				continue
			}
		}
		h.bits |= uint64(byt) << (56 - h.numBits)
		h.numBits += 8
	}
	return nil
}

// peekBits is huffman.rs:108-114 peek_bits (count is 1 to 16).
func (h *jpegHuffmanDecoder) peekBits(count uint8) uint16 {
	return uint16((h.bits >> (64 - count)) & (1<<count - 1))
}

// consumeBits is huffman.rs:116-121 consume_bits.
func (h *jpegHuffmanDecoder) consumeBits(count uint8) {
	h.bits <<= count
	h.numBits -= count
}

// decode is huffman.rs:31-58 decode (Figure F.16).
func (h *jpegHuffmanDecoder) decode(r *jpegReader, table *jpegHuffmanTable) (uint8, error) {
	if h.numBits < 16 {
		if err := h.readBits(r); err != nil {
			return 0, err
		}
	}
	entry := table.lut[h.peekBits(jpegLUTBits)]
	if entry.size > 0 {
		h.consumeBits(entry.size)
		return entry.value, nil
	}
	bits := h.peekBits(16)
	for i := uint8(jpegLUTBits); i < 16; i++ {
		code := int32(bits >> (15 - i))
		if code <= table.maxcode[i] {
			h.consumeBits(i + 1)
			index := int(code + table.delta[i])
			if index < 0 || index >= len(table.values) {
				return 0, jpegFormatError("failed to decode huffman code")
			}
			return table.values[index], nil
		}
	}
	return 0, jpegFormatError("failed to decode huffman code")
}

// decodeFastAC is huffman.rs:60-79 decode_fast_ac: the value and run of a short AC code, or ok = false when the table's lookup has
// no entry for the next 8 bits.
func (h *jpegHuffmanDecoder) decodeFastAC(r *jpegReader, table *jpegHuffmanTable) (value int16, run uint8, ok bool, err error) {
	acLUT := table.acLUT
	if acLUT == nil {
		return 0, 0, false, nil
	}
	if h.numBits < jpegLUTBits {
		if err := h.readBits(r); err != nil {
			return 0, 0, false, err
		}
	}
	entry := acLUT[h.peekBits(jpegLUTBits)]
	if entry.runSize == 0 {
		return 0, 0, false, nil
	}
	h.consumeBits(entry.runSize & 0x0f)
	return entry.value, entry.runSize >> 4, true, nil
}

// getBits is huffman.rs:81-91 get_bits.
func (h *jpegHuffmanDecoder) getBits(r *jpegReader, count uint8) (uint16, error) {
	if h.numBits < count {
		if err := h.readBits(r); err != nil {
			return 0, err
		}
	}
	bits := h.peekBits(count)
	h.consumeBits(count)
	return bits, nil
}

// receiveExtend is huffman.rs:93-96 receive_extend.
func (h *jpegHuffmanDecoder) receiveExtend(r *jpegReader, count uint8) (int16, error) {
	value, err := h.getBits(r, count)
	if err != nil {
		return 0, err
	}
	return jpegExtend(value, count), nil
}

// reset is huffman.rs:98-101 reset.
func (h *jpegHuffmanDecoder) reset() {
	h.bits = 0
	h.numBits = 0
}

// takeMarker is huffman.rs:103-106 take_marker.
func (h *jpegHuffmanDecoder) takeMarker(r *jpegReader) (marker byte, ok bool, err error) {
	if err := h.readBits(r); err != nil {
		return 0, false, err
	}
	marker, ok = h.marker, h.hasMarker
	h.hasMarker = false
	return marker, ok, nil
}

// jpegExtend is huffman.rs:165-173 extend (Figure F.12), in i16 arithmetic.
func jpegExtend(value uint16, count uint8) int16 {
	vt := uint16(1) << (uint16(count) - 1)
	if value < vt {
		return int16(value) + int16(-1)<<count + 1
	}
	return int16(value)
}

// newJPEGHuffmanTable is huffman.rs:191-254 HuffmanTable::new.
func newJPEGHuffmanTable(bits *[16]uint8, values []uint8, isAC bool) (*jpegHuffmanTable, error) {
	huffcode, huffsize, err := jpegDeriveHuffmanCodes(bits)
	if err != nil {
		return nil, err
	}

	// Section F.2.2.3, Figure F.15: delta[i] is VALPTR(I) - MINCODE(I).
	t := &jpegHuffmanTable{values: append([]uint8(nil), values...)}
	for i := range t.maxcode {
		t.maxcode[i] = -1
	}
	j := 0
	for i := range 16 {
		if bits[i] != 0 {
			t.delta[i] = int32(j) - int32(huffcode[j])
			j += int(bits[i])
			t.maxcode[i] = int32(huffcode[j-1])
		}
	}

	// A lookup table for faster decoding.
	for i, size := range huffsize {
		if size > jpegLUTBits {
			continue
		}
		bitsRemaining := jpegLUTBits - size
		start := int(huffcode[i] << bitsRemaining)
		entry := jpegLUTEntry{values[i], size}
		for k := range 1 << bitsRemaining {
			t.lut[start+k] = entry
		}
	}

	// A lookup table for small AC coefficients which both decodes the value and does the equivalent of receive_extend.
	if isAC {
		t.acLUT = new([1 << jpegLUTBits]jpegACLUTEntry)
		for i, entry := range &t.lut {
			runLength := entry.value >> 4
			magnitudeCategory := entry.value & 0x0f
			if magnitudeCategory > 0 && entry.size+magnitudeCategory <= jpegLUTBits {
				unextended := uint16(((i << entry.size) & (1<<jpegLUTBits - 1)) >> (jpegLUTBits - magnitudeCategory))
				t.acLUT[i] = jpegACLUTEntry{jpegExtend(unextended, magnitudeCategory), runLength<<4 | (entry.size + magnitudeCategory)}
			}
		}
	}
	return t, nil
}

// jpegDeriveHuffmanCodes is huffman.rs:256-292 derive_huffman_codes (Section C.2).
func jpegDeriveHuffmanCodes(bits *[16]uint8) (huffcode []uint16, huffsize []uint8, err error) {
	// Figure C.1
	for i, count := range bits {
		for range count {
			huffsize = append(huffsize, uint8(i+1))
		}
	}
	// Figure C.2
	huffcode = make([]uint16, len(huffsize))
	codeSize := huffsize[0]
	code := uint32(0)
	for i, size := range huffsize {
		for codeSize < size {
			code <<= 1
			codeSize++
		}
		if code >= uint32(1)<<size {
			return nil, nil, jpegFormatError("bad huffman code length")
		}
		huffcode[i] = uint16(code)
		code++
	}
	return huffcode, huffsize, nil
}

// fillDefaultMJPEGTables is huffman.rs:295-346 fill_default_mjpeg_tables (Tables K.3 to K.6): the standard tables stand in for the
// ones an AVI1 (MJPEG) frame leaves out.
func fillDefaultMJPEGTables(scan *jpegScan, dc, ac *[4]*jpegHuffmanTable) {
	uses := func(indices *[jpegMaxComponents]int, index int) bool {
		return slices.Contains(indices[:scan.count], index)
	}
	if dc[0] == nil && uses(&scan.dcTableIndices, 0) {
		dc[0] = mustJPEGHuffmanTable(&stdLumaDCCodeLengths, stdDCValues[:], false) // Table K.3
	}
	if dc[1] == nil && uses(&scan.dcTableIndices, 1) {
		dc[1] = mustJPEGHuffmanTable(&stdChromaDCCodeLengths, stdDCValues[:], false) // Table K.4
	}
	if ac[0] == nil && uses(&scan.acTableIndices, 0) {
		ac[0] = mustJPEGHuffmanTable(&stdLumaACCodeLengths, stdLumaACValues[:], true) // Table K.5
	}
	if ac[1] == nil && uses(&scan.acTableIndices, 1) {
		ac[1] = mustJPEGHuffmanTable(&stdChromaACCodeLengths, stdChromaACValues[:], true) // Table K.6
	}
}

// mustJPEGHuffmanTable is `HuffmanTable::new(..).unwrap()` of a table that is valid.
func mustJPEGHuffmanTable(bits *[16]uint8, values []uint8, isAC bool) *jpegHuffmanTable {
	t, err := newJPEGHuffmanTable(bits, values, isAC)
	if err != nil {
		panic(err)
	}
	return t
}
