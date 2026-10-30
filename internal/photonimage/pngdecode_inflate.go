// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

// A port of fdeflate 0.3.6 (src/decompress.rs, src/huffman.rs, src/tables.rs), the inflater of png 0.17.14, which Photon's wasm embeds
// (`strings` of photon_rs_bg.wasm: fdeflate-0.3.6, png-0.17.14, image-0.24.9). It is a literal port of the table-driven decoder, not a
// replacement by compress/flate: which streams it accepts differs from zlib's (a block without an end-of-block code, a distance tree of one
// code, the end-of-block symbols 286 and 287 of a fixed block, an input that ends in the middle of a symbol), and the tables of a block
// are rebuilt over the ones of the block before.

var (
	errFdBadZlibHeader               = errors.New("fdeflate: the zlib header is corrupt")
	errFdInsufficientInput           = errors.New("fdeflate: all input was consumed, but the end of the stream hasn't been reached")
	errFdInvalidBlockType            = errors.New("fdeflate: invalid block type")
	errFdInvalidUncompressedLength   = errors.New("fdeflate: an uncompressed block's NLEN value is invalid")
	errFdInvalidHlit                 = errors.New("fdeflate: too many literals")
	errFdInvalidHdist                = errors.New("fdeflate: too many distance codes")
	errFdInvalidCodeLengthRepeat     = errors.New("fdeflate: invalid code length repeat")
	errFdBadCodeLengthHuffmanTree    = errors.New("fdeflate: bad huffman tree")
	errFdBadLiteralLengthHuffmanTree = errors.New("fdeflate: bad literal/length huffman tree")
	errFdBadDistanceHuffmanTree      = errors.New("fdeflate: bad distance huffman tree")
	errFdInvalidLiteralLengthCode    = errors.New("fdeflate: invalid literal/length code")
	errFdInvalidDistanceCode         = errors.New("fdeflate: invalid distance code")
	errFdDistanceTooFarBack          = errors.New("fdeflate: a back-reference is too far back")
	errFdWrongChecksum               = errors.New("fdeflate: the deflate stream checksum is incorrect")
)

const (
	fdLiteralEntry     uint32 = 0x8000
	fdExceptionalEntry uint32 = 0x4000
	fdSecondaryEntry   uint32 = 0x2000
)

// fdCLCLOrder is CLCL_ORDER (tables.rs).
var fdCLCLOrder = [19]int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

var fdLenSymToLenExtra = [29]uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}

var fdLenSymToLenBase = [29]int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}

var fdDistSymToDistExtra = [30]uint8{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}

var fdDistSymToDistBase = [30]uint16{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}

// fdLitlenTableEntries is LITLEN_TABLE_ENTRIES: the symbol 256 and the symbols after 285 are exceptional entries, which the decoder treats as
// the end of the block.
var fdLitlenTableEntries = func() (entries [288]uint32) {
	for i := range entries {
		entries[i] = fdExceptionalEntry
	}
	for i := range 256 {
		entries[i] = uint32(i)<<16 | fdLiteralEntry | 1<<8
	}
	for i := 257; i < 286; i++ {
		entries[i] = uint32(fdLenSymToLenBase[i-257])<<16 | uint32(fdLenSymToLenExtra[i-257])<<8
	}
	return entries
}()

// fdDistanceTableEntries is DISTANCE_TABLE_ENTRIES (the symbols 30 and 31 have no entry bits).
var fdDistanceTableEntries = func() (entries [32]uint32) {
	for i := range 30 {
		entries[i] = uint32(fdDistSymToDistBase[i])<<16 | uint32(fdDistSymToDistExtra[i])<<8 | fdLiteralEntry
	}
	return entries
}()

// fdFixedCodeLengths is FIXED_CODE_LENGTHS: the 288 literal/length lengths and the 32 distance lengths of a fixed block.
var fdFixedCodeLengths = func() (lengths [320]uint8) {
	for i := range lengths {
		switch {
		case i < 144:
			lengths[i] = 8
		case i < 256:
			lengths[i] = 9
		case i < 280:
			lengths[i] = 7
		case i < 288:
			lengths[i] = 8
		default:
			lengths[i] = 5
		}
	}
	return lengths
}()

// fdNextCodeword is `next_codeword` (huffman.rs:4-14).
func fdNextCodeword(codeword, tableSize uint16) uint16 {
	if codeword == tableSize-1 {
		return codeword
	}
	adv := 15 - bits.LeadingZeros16(codeword^(tableSize-1))
	bit := uint16(1) << adv
	codeword &= bit - 1
	codeword |= bit
	return codeword
}

// fdBuildTable is `huffman::build_table` (huffman.rs:16-182): it fills the primary table (whose length is a power of two), the
// secondary table and the codes of the symbols, and reports whether the lengths form a valid tree.
func fdBuildTable(lengths []uint8, entries []uint32, codes []uint16, primary []uint32, secondary *[]uint16, isDistanceTable, doubleLiteral bool) bool {
	var histogram [16]int
	for _, length := range lengths {
		histogram[length]++
	}
	maxLength := 15
	for maxLength > 1 && histogram[maxLength] == 0 {
		maxLength--
	}

	entryOf := func(symbol int) uint32 {
		if symbol < len(entries) {
			return entries[symbol]
		}
		return uint32(symbol) << 16
	}

	// Zero and one symbol codes, which only a distance code may have.
	if isDistanceTable {
		if maxLength == 0 {
			clear(primary)
			*secondary = (*secondary)[:0]
			return true
		} else if maxLength == 1 && histogram[1] == 1 {
			symbol := 0
			for i, l := range lengths {
				if l == 1 {
					symbol = i
					break
				}
			}
			codes[symbol] = 0
			entry := entryOf(symbol) | 1
			for i := 0; i < len(primary); i += 2 {
				primary[i] = entry
				primary[i+1] = 0
			}
			return true
		}
	}

	var offsets [16]int
	codespaceUsed := 0
	offsets[1] = histogram[0]
	for i := 1; i < maxLength; i++ {
		offsets[i+1] = offsets[i] + histogram[i]
		codespaceUsed = codespaceUsed<<1 + histogram[i]
	}
	codespaceUsed = codespaceUsed<<1 + histogram[maxLength]
	if codespaceUsed != 1<<maxLength {
		return false
	}

	nextIndex := offsets
	var sortedSymbols [288]int
	for symbol, length := range lengths {
		sortedSymbols[nextIndex[length]] = symbol
		nextIndex[length]++
	}

	var codeword uint16
	i := histogram[0]

	primaryTableBits := bits.Len(uint(len(primary))) - 1
	primaryTableMask := uint16(1)<<primaryTableBits - 1
	for length := 1; length <= primaryTableBits; length++ {
		currentTableEnd := 1 << length

		for range histogram[length] {
			symbol := sortedSymbols[i]
			i++
			primary[codeword] = entryOf(symbol) | uint32(length)
			codes[symbol] = codeword
			codeword = fdNextCodeword(codeword, uint16(currentTableEnd))
		}

		if doubleLiteral {
			for len1 := 1; len1 < length-1; len1++ {
				len2 := length - len1
				for sym1Index := offsets[len1]; sym1Index < nextIndex[len1]; sym1Index++ {
					for sym2Index := offsets[len2]; sym2Index < nextIndex[len2]; sym2Index++ {
						sym1, sym2 := sortedSymbols[sym1Index], sortedSymbols[sym2Index]
						if sym1 < 256 && sym2 < 256 {
							pair := codes[sym1] | codes[sym2]<<len1
							entry := uint32(sym1)<<16 | uint32(sym2)<<24 | fdLiteralEntry | 2<<8
							primary[pair] = entry | uint32(length)
						}
					}
				}
			}
		}

		if length < primaryTableBits {
			copy(primary[currentTableEnd:], primary[:currentTableEnd])
		}
	}

	*secondary = (*secondary)[:0]
	if maxLength > primaryTableBits {
		subtableStart := 0
		subtablePrefix := uint16(0xFFFF)
		for length := primaryTableBits + 1; length <= maxLength; length++ {
			subtableSize := 1 << (length - primaryTableBits)
			for range histogram[length] {
				if codeword&primaryTableMask != subtablePrefix {
					subtablePrefix = codeword & primaryTableMask
					subtableStart = len(*secondary)
					primary[subtablePrefix] = uint32(subtableStart)<<16 | fdExceptionalEntry | fdSecondaryEntry | uint32(subtableSize-1)
					*secondary = append(*secondary, make([]uint16, subtableSize)...)
				}
				symbol := sortedSymbols[i]
				i++
				codes[symbol] = codeword
				(*secondary)[subtableStart+int(codeword>>primaryTableBits)] = uint16(symbol)<<4 | uint16(length)
				codeword = fdNextCodeword(codeword, uint16(1)<<length)
			}
			if length < maxLength && codeword&primaryTableMask == subtablePrefix {
				*secondary = append(*secondary, (*secondary)[subtableStart:]...)
				subtableSize := len(*secondary) - subtableStart
				primary[subtablePrefix] = uint32(subtableStart)<<16 | fdExceptionalEntry | fdSecondaryEntry | uint32(subtableSize-1)
			}
		}
	}
	return true
}

type fdState uint8

const (
	fdZlibHeader fdState = iota
	fdBlockHeader
	fdCodeLengthCodes
	fdCodeLengths
	fdCompressedData
	fdUncompressedData
	fdChecksum
	fdDone
)

// fdDecompressor is `Decompressor`.
type fdDecompressor struct {
	litlenTable   [4096]uint32
	secondary     []uint16
	distTable     [512]uint32
	distSecondary []uint16
	eofCode       uint16
	eofMask       uint16
	eofBits       uint8

	hlit, hdist, hclen, numLengthsRead int
	headerTable                        [128]uint32
	codeLengths                        [320]uint8

	uncompressedBytesLeft uint16

	buffer uint64
	nbits  uint8

	queuedRLE     bool
	queuedRLEByte byte
	queuedRLELen  int
	queuedBack    bool
	queuedDist    int
	queuedBackLen int

	lastBlock      bool
	state          fdState
	adlerA, adlerB uint32 // simd_adler32's `Adler32`, which starts from a = 1, b = 0
	ignoreAdler    bool
}

func newFdDecompressor() *fdDecompressor { return &fdDecompressor{state: fdZlibHeader, adlerA: 1} }

func (d *fdDecompressor) done() bool { return d.state == fdDone }

func (d *fdDecompressor) ignoreAdler32() { d.ignoreAdler = true }

// checksumWrite adds data to the Adler-32.
func (d *fdDecompressor) checksumWrite(data []byte) {
	a, b := d.adlerA, d.adlerB
	for _, v := range data {
		a = (a + uint32(v)) % 65521
		b = (b + a) % 65521
	}
	d.adlerA, d.adlerB = a, b
}

func (d *fdDecompressor) checksumFinish() uint32 { return d.adlerB<<16 | d.adlerA }

func (d *fdDecompressor) fillBuffer(input *[]byte) {
	in := *input
	if len(in) >= 8 {
		d.buffer |= binary.LittleEndian.Uint64(in) << d.nbits
		*input = in[(63-int(d.nbits))/8:]
		d.nbits |= 56
	} else {
		nbytes := min(len(in), (63-int(d.nbits))/8)
		var data [8]byte
		copy(data[:], in[:nbytes])
		d.buffer |= binary.LittleEndian.Uint64(data[:]) << d.nbits
		d.nbits += uint8(nbytes) * 8
		*input = in[nbytes:]
	}
}

func (d *fdDecompressor) peekBits(n uint8) uint64 { return d.buffer & (1<<n - 1) }

func (d *fdDecompressor) consumeBits(n uint8) {
	d.buffer >>= n
	d.nbits -= n
}

func (d *fdDecompressor) readBlockHeader(input *[]byte) error {
	d.fillBuffer(input)
	if d.nbits < 3 {
		return nil
	}
	start := d.peekBits(3)
	d.lastBlock = start&1 != 0
	switch start >> 1 {
	case 0b00:
		alignBits := (d.nbits - 3) % 8
		headerBits := 3 + 32 + alignBits
		if d.nbits < headerBits {
			return nil
		}
		length := uint16(d.peekBits(alignBits+19) >> (alignBits + 3))
		nlen := uint16(d.peekBits(headerBits) >> (alignBits + 19))
		if nlen != ^length {
			return errFdInvalidUncompressedLength
		}
		d.state = fdUncompressedData
		d.uncompressedBytesLeft = length
		d.consumeBits(headerBits)
		return nil
	case 0b01:
		d.consumeBits(3)
		if err := d.buildTables(288, fdFixedCodeLengths[:]); err != nil {
			return err
		}
		d.state = fdCompressedData
		return nil
	case 0b10:
		if d.nbits < 17 {
			return nil
		}
		d.hlit = int(d.peekBits(8)>>3) + 257
		d.hdist = int(d.peekBits(13)>>8) + 1
		d.hclen = int(d.peekBits(17)>>13) + 4
		if d.hlit > 286 {
			return errFdInvalidHlit
		}
		if d.hdist > 30 {
			return errFdInvalidHdist
		}
		d.consumeBits(17)
		d.state = fdCodeLengthCodes
		return nil
	}
	return errFdInvalidBlockType
}

func (d *fdDecompressor) readCodeLengthCodes(input *[]byte) error {
	d.fillBuffer(input)
	if int(d.nbits)+len(*input)*8 < 3*d.hclen {
		return nil
	}
	var codeLengthLengths [19]uint8
	for i := range d.hclen {
		codeLengthLengths[fdCLCLOrder[i]] = uint8(d.peekBits(3))
		d.consumeBits(3)
		if i == 17 {
			d.fillBuffer(input)
		}
	}
	var codes [19]uint16
	var scratch []uint16
	if !fdBuildTable(codeLengthLengths[:], nil, codes[:], d.headerTable[:], &scratch, false, false) {
		return errFdBadCodeLengthHuffmanTree
	}
	d.state = fdCodeLengths
	d.numLengthsRead = 0
	return nil
}

func (d *fdDecompressor) readCodeLengths(input *[]byte) error {
	totalLengths := d.hlit + d.hdist
	for d.numLengthsRead < totalLengths {
		d.fillBuffer(input)
		if d.nbits < 7 {
			return nil
		}
		code := d.peekBits(7)
		entry := d.headerTable[code]
		length := uint8(entry & 0x7)
		symbol := uint8(entry >> 16)

		if symbol <= 15 {
			d.codeLengths[d.numLengthsRead] = symbol
			d.numLengthsRead++
			d.consumeBits(length)
			continue
		}
		var baseRepeat int
		var extraBits uint8
		switch symbol {
		case 16:
			baseRepeat, extraBits = 3, 2
		case 17:
			baseRepeat, extraBits = 3, 3
		default:
			baseRepeat, extraBits = 11, 7
		}
		if d.nbits < length+extraBits {
			return nil
		}
		var value uint8
		if symbol == 16 {
			if d.numLengthsRead == 0 {
				return errFdInvalidCodeLengthRepeat
			}
			value = d.codeLengths[d.numLengthsRead-1]
		}
		repeat := int(d.peekBits(length+extraBits)>>length) + baseRepeat
		if d.numLengthsRead+repeat > totalLengths {
			return errFdInvalidCodeLengthRepeat
		}
		for i := range repeat {
			d.codeLengths[d.numLengthsRead+i] = value
		}
		d.numLengthsRead += repeat
		d.consumeBits(length + extraBits)
	}

	copy(d.codeLengths[288:], d.codeLengths[d.hlit:totalLengths])
	for i := d.hlit; i < 288; i++ {
		d.codeLengths[i] = 0
	}
	for i := 288 + d.hdist; i < 320; i++ {
		d.codeLengths[i] = 0
	}
	if err := d.buildTables(d.hlit, d.codeLengths[:]); err != nil {
		return err
	}
	d.state = fdCompressedData
	return nil
}

// buildTables is `build_tables` (decompress.rs:362-412).
func (d *fdDecompressor) buildTables(hlit int, codeLengths []uint8) error {
	if codeLengths[256] == 0 {
		return errFdBadLiteralLengthHuffmanTree
	}
	var codes [288]uint16
	d.secondary = d.secondary[:0]
	if !fdBuildTable(codeLengths[:hlit], fdLitlenTableEntries[:], codes[:hlit], d.litlenTable[:], &d.secondary, false, true) {
		return errFdBadCodeLengthHuffmanTree
	}
	d.eofCode = codes[256]
	d.eofMask = uint16(1)<<codeLengths[256] - 1
	d.eofBits = codeLengths[256]

	lengths := codeLengths[288:320]
	allZero := true
	for _, l := range lengths {
		if l != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		clear(d.distTable[:])
		return nil
	}
	var distCodes [32]uint16
	if !fdBuildTable(lengths, fdDistanceTableEntries[:], distCodes[:], d.distTable[:], &d.distSecondary, true, false) {
		return errFdBadDistanceHuffmanTree
	}
	return nil
}

// readCompressed is `read_compressed` (decompress.rs:414-889): the fast loop for at least 8 bytes of input and of output, then the careful one.
func (d *fdDecompressor) readCompressed(input *[]byte, output []byte, outputIndex int) (int, error) {
	d.fillBuffer(input)
	litlenEntry := d.litlenTable[d.buffer&0xfff]
	for d.state == fdCompressedData && outputIndex+8 <= len(output) && len(*input) >= 8 {
		var bitsv uint64
		litlenCodeBits := uint8(litlenEntry)
		if litlenEntry&fdLiteralEntry != 0 {
			litlenEntry2 := d.litlenTable[d.buffer>>litlenCodeBits&0xfff]
			litlenCodeBits2 := uint8(litlenEntry2)
			litlenEntry3 := d.litlenTable[d.buffer>>(litlenCodeBits+litlenCodeBits2)&0xfff]
			litlenCodeBits3 := uint8(litlenEntry3)
			litlenEntry4 := d.litlenTable[d.buffer>>(litlenCodeBits+litlenCodeBits2+litlenCodeBits3)&0xfff]

			advance := int(litlenEntry&0xf00) >> 8
			output[outputIndex] = byte(litlenEntry >> 16)
			output[outputIndex+1] = byte(litlenEntry >> 24)
			outputIndex += advance

			if litlenEntry2&fdLiteralEntry != 0 {
				advance2 := int(litlenEntry2&0xf00) >> 8
				output[outputIndex] = byte(litlenEntry2 >> 16)
				output[outputIndex+1] = byte(litlenEntry2 >> 24)
				outputIndex += advance2

				if litlenEntry3&fdLiteralEntry != 0 {
					advance3 := int(litlenEntry3&0xf00) >> 8
					output[outputIndex] = byte(litlenEntry3 >> 16)
					output[outputIndex+1] = byte(litlenEntry3 >> 24)
					outputIndex += advance3

					litlenEntry = litlenEntry4
					d.consumeBits(litlenCodeBits + litlenCodeBits2 + litlenCodeBits3)
					d.fillBuffer(input)
					continue
				}
				d.consumeBits(litlenCodeBits + litlenCodeBits2)
				litlenEntry = litlenEntry3
				litlenCodeBits = litlenCodeBits3
				d.fillBuffer(input)
				bitsv = d.buffer
			} else {
				d.consumeBits(litlenCodeBits)
				bitsv = d.buffer
				litlenEntry = litlenEntry2
				litlenCodeBits = litlenCodeBits2
				if d.nbits < 48 {
					d.fillBuffer(input)
				}
			}
		} else {
			bitsv = d.buffer
		}

		var lengthBase uint32
		var lengthExtraBits uint8
		switch {
		case litlenEntry&fdExceptionalEntry == 0:
			lengthBase, lengthExtraBits = litlenEntry>>16, uint8(litlenEntry>>8)
		case litlenEntry&fdSecondaryEntry != 0:
			index := litlenEntry>>16 + (uint32(bitsv>>12) & (litlenEntry & 0xff))
			secondaryEntry := d.secondary[index]
			symbol := secondaryEntry >> 4
			litlenCodeBits = uint8(secondaryEntry & 0xf)
			switch {
			case symbol <= 255:
				d.consumeBits(litlenCodeBits)
				litlenEntry = d.litlenTable[d.buffer&0xfff]
				d.fillBuffer(input)
				output[outputIndex] = byte(symbol)
				outputIndex++
				continue
			case symbol == 256:
				d.consumeBits(litlenCodeBits)
				d.state = d.afterBlock()
			default:
				lengthBase, lengthExtraBits = uint32(fdLenSymToLenBase[symbol-257]), fdLenSymToLenExtra[symbol-257]
			}
			if symbol == 256 {
				goto fastLoopDone
			}
		case litlenCodeBits == 0:
			return outputIndex, errFdInvalidLiteralLengthCode
		default:
			d.consumeBits(litlenCodeBits)
			d.state = d.afterBlock()
			goto fastLoopDone
		}
		bitsv >>= litlenCodeBits

		{
			lengthExtraMask := uint64(1)<<lengthExtraBits - 1
			length := int(lengthBase) + int(bitsv&lengthExtraMask)
			bitsv >>= lengthExtraBits

			distEntry := d.distTable[bitsv&0x1ff]
			var distBase uint16
			var distExtraBits, distCodeBits uint8
			if distEntry&fdLiteralEntry != 0 {
				distBase, distExtraBits, distCodeBits = uint16(distEntry>>16), uint8(distEntry>>8)&0xf, uint8(distEntry)
			} else if distEntry>>8 == 0 {
				return outputIndex, errFdInvalidDistanceCode
			} else {
				index := distEntry>>16 + (uint32(bitsv>>9) & (distEntry & 0xff))
				secondaryEntry := d.distSecondary[index]
				distSymbol := int(secondaryEntry >> 4)
				if distSymbol >= 30 {
					return outputIndex, errFdInvalidDistanceCode
				}
				distBase, distExtraBits, distCodeBits = fdDistSymToDistBase[distSymbol], fdDistSymToDistExtra[distSymbol], uint8(secondaryEntry&0xf)
			}
			bitsv >>= distCodeBits

			dist := int(distBase) + int(bitsv&(1<<distExtraBits-1))
			if dist > outputIndex {
				return outputIndex, errFdDistanceTooFarBack
			}

			d.consumeBits(litlenCodeBits + lengthExtraBits + distCodeBits + distExtraBits)
			d.fillBuffer(input)
			litlenEntry = d.litlenTable[d.buffer&0xfff]

			var stop bool
			outputIndex, stop = d.copyBackReference(output, outputIndex, length, dist)
			if stop {
				goto fastLoopDone
			}
		}
	}
fastLoopDone:

	for d.state == fdCompressedData {
		d.fillBuffer(input)
		if outputIndex == len(output) {
			break
		}

		bitsv := d.buffer
		litlenEntry := d.litlenTable[bitsv&0xfff]
		litlenCodeBits := uint8(litlenEntry)

		if litlenEntry&fdLiteralEntry != 0 {
			advance := int(litlenEntry&0xf00) >> 8
			if d.nbits < litlenCodeBits {
				break
			} else if outputIndex+1 < len(output) {
				output[outputIndex] = byte(litlenEntry >> 16)
				output[outputIndex+1] = byte(litlenEntry >> 24)
				outputIndex += advance
				d.consumeBits(litlenCodeBits)
				continue
			} else if outputIndex+advance == len(output) {
				output[outputIndex] = byte(litlenEntry >> 16)
				outputIndex++
				d.consumeBits(litlenCodeBits)
				break
			}
			output[outputIndex] = byte(litlenEntry >> 16)
			d.queuedRLE, d.queuedRLEByte, d.queuedRLELen = true, byte(litlenEntry>>24), 1
			outputIndex++
			d.consumeBits(litlenCodeBits)
			break
		}

		var lengthBase uint32
		var lengthExtraBits uint8
		switch {
		case litlenEntry&fdExceptionalEntry == 0:
			lengthBase, lengthExtraBits = litlenEntry>>16, uint8(litlenEntry>>8)
		case litlenEntry&fdSecondaryEntry != 0:
			index := litlenEntry>>16 + (uint32(bitsv>>12) & (litlenEntry & 0xff))
			secondaryEntry := d.secondary[index]
			symbol := secondaryEntry >> 4
			litlenCodeBits = uint8(secondaryEntry & 0xf)
			if d.nbits < litlenCodeBits {
				goto careful
			} else if symbol < 256 {
				d.consumeBits(litlenCodeBits)
				output[outputIndex] = byte(symbol)
				outputIndex++
				continue
			} else if symbol == 256 {
				d.consumeBits(litlenCodeBits)
				d.state = d.afterBlock()
				goto careful
			}
			lengthBase, lengthExtraBits = uint32(fdLenSymToLenBase[symbol-257]), fdLenSymToLenExtra[symbol-257]
		case litlenCodeBits == 0:
			return outputIndex, errFdInvalidLiteralLengthCode
		default:
			if d.nbits < litlenCodeBits {
				goto careful
			}
			d.consumeBits(litlenCodeBits)
			d.state = d.afterBlock()
			goto careful
		}
		bitsv >>= litlenCodeBits

		{
			lengthExtraMask := uint64(1)<<lengthExtraBits - 1
			length := int(lengthBase) + int(bitsv&lengthExtraMask)
			bitsv >>= lengthExtraBits

			distEntry := d.distTable[bitsv&0x1ff]
			var distBase uint16
			var distExtraBits, distCodeBits uint8
			if distEntry&fdLiteralEntry != 0 {
				distBase, distExtraBits, distCodeBits = uint16(distEntry>>16), uint8(distEntry>>8)&0xf, uint8(distEntry)
			} else if int(d.nbits) > int(litlenCodeBits)+int(lengthExtraBits)+9 {
				if distEntry>>8 == 0 {
					return outputIndex, errFdInvalidDistanceCode
				}
				index := distEntry>>16 + (uint32(bitsv>>9) & (distEntry & 0xff))
				secondaryEntry := d.distSecondary[index]
				distSymbol := int(secondaryEntry >> 4)
				if distSymbol >= 30 {
					return outputIndex, errFdInvalidDistanceCode
				}
				distBase, distExtraBits, distCodeBits = fdDistSymToDistBase[distSymbol], fdDistSymToDistExtra[distSymbol], uint8(secondaryEntry&0xf)
			} else {
				break
			}
			bitsv >>= distCodeBits

			dist := int(distBase) + int(bitsv&(1<<distExtraBits-1))
			totalBits := litlenCodeBits + lengthExtraBits + distCodeBits + distExtraBits
			if d.nbits < totalBits {
				break
			} else if dist > outputIndex {
				return outputIndex, errFdDistanceTooFarBack
			}
			d.consumeBits(totalBits)

			var stop bool
			outputIndex, stop = d.copyBackReference(output, outputIndex, length, dist)
			if stop {
				break
			}
		}
		continue
	careful:
		break
	}

	if d.state == fdCompressedData && !d.queuedBack && !d.queuedRLE && d.nbits >= 15 && uint16(d.peekBits(15))&d.eofMask == d.eofCode {
		d.consumeBits(d.eofBits)
		d.state = d.afterBlock()
	}
	return outputIndex, nil
}

func (d *fdDecompressor) afterBlock() fdState {
	if d.lastBlock {
		return fdChecksum
	}
	return fdBlockHeader
}

// copyBackReference copies a match as the three cases of `read_compressed` do (run of one byte, copy of 16-byte blocks, byte by byte)
// and queues what does not fit into the output; stop is whether the output is full.
func (d *fdDecompressor) copyBackReference(output []byte, outputIndex, length, dist int) (int, bool) {
	copyLength := min(length, len(output)-outputIndex)
	if dist == 1 {
		last := output[outputIndex-1]
		for i := range copyLength {
			output[outputIndex+i] = last
		}
		if copyLength < length {
			d.queuedRLE, d.queuedRLEByte, d.queuedRLELen = true, last, length-copyLength
			return len(output), true
		}
	} else if outputIndex+length+15 <= len(output) {
		start := outputIndex - dist
		copy(output[outputIndex:outputIndex+16], output[start:start+16])
		if length > 16 || dist < 16 {
			step := min(dist, 16)
			for i := step; i < length; i += step {
				copy(output[outputIndex+i:outputIndex+i+16], output[start+i:start+i+16])
			}
		}
	} else {
		if dist < copyLength {
			for i := range copyLength {
				output[outputIndex+i] = output[outputIndex+i-dist]
			}
		} else {
			copy(output[outputIndex:outputIndex+copyLength], output[outputIndex-dist:outputIndex+copyLength-dist])
		}
		if copyLength < length {
			d.queuedBack, d.queuedDist, d.queuedBackLen = true, dist, length-copyLength
			return len(output), true
		}
	}
	return outputIndex + copyLength, false
}

// read is `Decompressor::read` (decompress.rs:896-1038): it returns the bytes consumed from input and written to output.
func (d *fdDecompressor) read(input, output []byte, outputPosition int, endOfInput bool) (int, int, error) {
	if d.state == fdDone {
		return 0, 0, nil
	}
	remaining := input
	outputIndex := outputPosition

	if d.queuedRLE {
		d.queuedRLE = false
		n := min(d.queuedRLELen, len(output)-outputIndex)
		for i := range n {
			output[outputIndex+i] = d.queuedRLEByte
		}
		outputIndex += n
		if n < d.queuedRLELen {
			d.queuedRLE, d.queuedRLELen = true, d.queuedRLELen-n
			return 0, n, nil
		}
	}
	if d.queuedBack {
		d.queuedBack = false
		n := min(d.queuedBackLen, len(output)-outputIndex)
		for i := range n {
			output[outputIndex+i] = output[outputIndex+i-d.queuedDist]
		}
		outputIndex += n
		if n < d.queuedBackLen {
			d.queuedBack, d.queuedBackLen = true, d.queuedBackLen-n
			return 0, n, nil
		}
	}

	lastState, haveLast := fdState(0), false
	for !haveLast || lastState != d.state {
		lastState, haveLast = d.state, true
		switch d.state {
		case fdZlibHeader:
			d.fillBuffer(&remaining)
			if d.nbits < 16 {
				goto mainDone
			}
			input0 := d.peekBits(8)
			input1 := d.peekBits(16) >> 8 & 0xff
			if input0&0x0f != 0x08 || input0&0xf0 > 0x70 || input1&0x20 != 0 || (input0<<8|input1)%31 != 0 {
				return 0, 0, errFdBadZlibHeader
			}
			d.consumeBits(16)
			d.state = fdBlockHeader
		case fdBlockHeader:
			if err := d.readBlockHeader(&remaining); err != nil {
				return 0, 0, err
			}
		case fdCodeLengthCodes:
			if err := d.readCodeLengthCodes(&remaining); err != nil {
				return 0, 0, err
			}
		case fdCodeLengths:
			if err := d.readCodeLengths(&remaining); err != nil {
				return 0, 0, err
			}
		case fdCompressedData:
			var err error
			outputIndex, err = d.readCompressed(&remaining, output, outputIndex)
			if err != nil {
				return 0, 0, err
			}
		case fdUncompressedData:
			for d.nbits > 0 && d.uncompressedBytesLeft > 0 && outputIndex < len(output) {
				output[outputIndex] = byte(d.peekBits(8))
				d.consumeBits(8)
				outputIndex++
				d.uncompressedBytesLeft--
			}
			if d.nbits == 0 {
				d.buffer = 0
			}
			copyBytes := min(int(d.uncompressedBytesLeft), len(remaining), len(output)-outputIndex)
			copy(output[outputIndex:outputIndex+copyBytes], remaining[:copyBytes])
			remaining = remaining[copyBytes:]
			outputIndex += copyBytes
			d.uncompressedBytesLeft -= uint16(copyBytes)
			if d.uncompressedBytesLeft == 0 {
				d.state = d.afterBlock()
			}
		case fdChecksum:
			d.fillBuffer(&remaining)
			alignBits := d.nbits % 8
			if d.nbits >= 32+alignBits {
				d.checksumWrite(output[outputPosition:outputIndex])
				if alignBits != 0 {
					d.consumeBits(alignBits)
				}
				if !d.ignoreAdler && uint32(bits.ReverseBytes32(uint32(d.peekBits(32)))) != d.checksumFinish() {
					return 0, 0, errFdWrongChecksum
				}
				d.state = fdDone
				d.consumeBits(32)
				goto mainDone
			}
		}
	}
mainDone:
	if !d.ignoreAdler && d.state != fdDone {
		d.checksumWrite(output[outputPosition:outputIndex])
	}
	if d.state == fdDone || !endOfInput || outputIndex == len(output) {
		return len(input) - len(remaining), outputIndex - outputPosition, nil
	}
	return 0, 0, errFdInsufficientInput
}

// fdDecompressBounded is `decompress_to_vec_bounded` (decompress.rs:1086-1118): the checksum is verified (a decompressor of its own, not
// png's ZlibStream). tooLarge is the output reaching maxlen before the end of the stream.
func fdDecompressBounded(input []byte, maxlen int) (output []byte, tooLarge bool, err error) {
	d := newFdDecompressor()
	output = make([]byte, min(1024, maxlen))
	inputIndex, outputIndex := 0, 0
	for {
		consumed, produced, err := d.read(input[inputIndex:], output, outputIndex, true)
		if err != nil {
			return nil, false, err
		}
		inputIndex += consumed
		outputIndex += produced
		if d.done() || outputIndex == maxlen {
			break
		}
		if newLen := min(outputIndex+32*1024, maxlen); newLen > len(output) {
			output = append(output, make([]byte, newLen-len(output))...)
		} else {
			output = output[:newLen]
		}
	}
	output = output[:outputIndex]
	if d.done() {
		return output, false, nil
	}
	return output, true, nil
}
