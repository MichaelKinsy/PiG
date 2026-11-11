// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: Copyright 2013-2014 RAD Game Tools and Valve Software
// SPDX-FileCopyrightText: Copyright 2010-2014 Rich Geldreich and Tenacious Software LLC
// SPDX-FileCopyrightText: The miniz_oxide Developers
// SPDX-License-Identifier: MIT

package photonimage

import "errors"

// A port of what tiff 0.9.1's DeflateReader does with a strip: flate2 1.0.28 `read::ZlibDecoder` over miniz_oxide 0.7.1
// (`inflate::stream::inflate` over `inflate::core::decompress`, the tinfl of miniz). Go's compress/zlib decodes the same valid streams
// but differs where it matters for error parity: miniz decodes into a 32 KiB dictionary ahead of what the reader asked for (a damaged
// stream fails a read whose bytes were fine, and the Adler-32 of a stream that ends inside the window is checked then), a match that
// reaches before the start of the data reads the zeroed dictionary instead of failing, a code with a single symbol may have any length,
// and the input comes in the 32 KiB buffers of flate2's BufReader.

const (
	tinflDictSize = 32768

	tinflStatusDone = iota
	tinflStatusNeedsMoreInput
	tinflStatusHasMoreOutput
	tinflStatusFailed
)

// tinflState is core.rs State.
type tinflState uint8

const (
	tsStart tinflState = iota
	tsReadZlibCmf
	tsReadZlibFlg
	tsReadBlockHeader
	tsBlockTypeNoCompression
	tsRawHeader
	tsRawReadFirstByte
	tsRawStoreFirstByte
	tsRawMemcpy1
	tsRawMemcpy2
	tsReadTableSizes
	tsReadHufflenTableCodeSize
	tsReadLitlenDistTablesCodeSize
	tsReadExtraBitsCodeSize
	tsDecodeLitlen
	tsWriteSymbol
	tsHuffDecodeOuterLoop1
	tsReadExtraBitsLitlen
	tsDecodeDistance
	tsReadExtraBitsDistance
	tsHuffDecodeOuterLoop2
	tsWriteLenBytesToEnd
	tsBlockDone
	tsReadAdler32
	tsDoneForever
	tsFailed
)

var (
	tinflLengthBase  = [32]uint16{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258, 512, 512, 512}
	tinflLengthExtra = [32]uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0, 0, 0, 0}
	tinflDistBase    = [32]uint16{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577, 32768, 32768}
	tinflDistExtra   = [32]uint8{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13, 13, 13}
	tinflLengthOrder = [19]uint8{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
	tinflMinSizes    = [3]uint32{257, 1, 4}
)

// tinflHuffman is core.rs HuffmanTable: the code lengths and the decoding table, here one lookup of the 15 bits a code can have
// (entries hold length<<9 | symbol, 0 for a bit pattern no code starts with).
type tinflHuffman struct {
	codeSize [288]uint8
	lookup   [1 << 15]uint16
}

// tinfl is core.rs DecompressorOxide.
type tinfl struct {
	state    tinflState
	numBits  uint32
	bitBuf   uint64
	zHeader0 uint32
	zHeader1 uint32
	zAdler   uint32
	check    uint32
	finish   uint32
	// blockType is the block type, and in init_tree the index of the table being built.
	blockType uint32
	dist      uint32
	counter   uint32
	numExtra  uint32
	sizes     [3]uint32
	tables    [3]tinflHuffman
	rawHeader [4]byte
	lenCodes  [288 + 32 + 137]uint8
}

// initTree is core.rs init_tree: it builds the tables from block_type down to 0; block_type 2 stops for the code lengths of the
// dynamic tables. It returns the next state, or tsFailed (BadTotalSymbols).
func (r *tinfl) initTree() tinflState {
	for {
		table := &r.tables[r.blockType]
		size := int(r.sizes[r.blockType])
		clear(table.lookup[:])
		var totalSymbols [16]uint32
		var nextCode [17]uint32
		for _, c := range table.codeSize[:size] {
			totalSymbols[c]++
		}
		var used, total uint32
		for i := 1; i < 16; i++ {
			used += totalSymbols[i]
			total += totalSymbols[i]
			total <<= 1
			nextCode[i+1] = total
		}
		if total != 65536 && used > 1 {
			return tsFailed
		}
		for sym := range size {
			length := uint32(table.codeSize[sym])
			if length == 0 {
				continue
			}
			code := nextCode[length]
			nextCode[length]++
			var rev uint32
			for range length {
				rev = rev<<1 | code&1
				code >>= 1
			}
			entry := uint16(length<<9) | uint16(sym)
			for ; rev < 1<<15; rev += 1 << length {
				table.lookup[rev] = entry
			}
		}
		if r.blockType == 2 {
			r.counter = 0
			return tsReadLitlenDistTablesCodeSize
		}
		if r.blockType == 0 {
			break
		}
		r.blockType--
	}
	r.counter = 0
	return tsDecodeLitlen
}

func (r *tinfl) startStaticTable() {
	r.sizes[0], r.sizes[1] = 288, 32
	for i := range 144 {
		r.tables[0].codeSize[i] = 8
	}
	for i := 144; i < 256; i++ {
		r.tables[0].codeSize[i] = 9
	}
	for i := 256; i < 280; i++ {
		r.tables[0].codeSize[i] = 7
	}
	for i := 280; i < 288; i++ {
		r.tables[0].codeSize[i] = 8
	}
	for i := range 32 {
		r.tables[1].codeSize[i] = 5
	}
}

// tinflRun is one call of core.rs `decompress`: the input, the dictionary written from outPos, and whether more input will follow. It
// returns the status, the bytes of input used and the bytes written.
type tinflRun struct {
	r       *tinfl
	in      []byte
	ip      int
	dict    []byte
	pos     int
	hasMore bool
}

// bytesLeft is OutputBuffer::bytes_left.
func (c *tinflRun) bytesLeft() int { return len(c.dict) - c.pos }

// readBits is core.rs read_bits: false when the input ends first.
func (c *tinflRun) readBits(n uint32) (uint32, bool) {
	r := c.r
	for r.numBits < n {
		if c.ip >= len(c.in) {
			return 0, false
		}
		r.bitBuf |= uint64(c.in[c.ip]) << r.numBits
		c.ip++
		r.numBits += 8
	}
	bits := uint32(r.bitBuf & (1<<n - 1))
	r.bitBuf >>= n
	r.numBits -= n
	return bits, true
}

// readByte is core.rs read_byte.
func (c *tinflRun) readByte() (byte, bool) {
	if c.ip >= len(c.in) {
		return 0, false
	}
	b := c.in[c.ip]
	c.ip++
	return b, true
}

// decodeHuffman is core.rs decode_huffman_code: the symbol of the next code of a table. ok is false when the input ends before a code is
// complete; failed is InvalidCodeLen (a pattern no code starts with, with 15 bits to look at).
func (c *tinflRun) decodeHuffman(table int) (symbol uint32, ok, failed bool) {
	r := c.r
	for r.numBits < 15 && c.ip < len(c.in) {
		r.bitBuf |= uint64(c.in[c.ip]) << r.numBits
		c.ip++
		r.numBits += 8
	}
	entry := r.tables[table].lookup[r.bitBuf&(1<<15-1)]
	length := uint32(entry >> 9)
	if length == 0 {
		if r.numBits >= 15 {
			return 0, false, true
		}
		return 0, false, false
	}
	if length > r.numBits {
		return 0, false, false
	}
	r.bitBuf >>= length
	r.numBits -= length
	return uint32(entry & 511), true, false
}

// decompress is core.rs `decompress` (without the non-wrapping mode, which flate2 uses only for a call with Finish before any other).
func (r *tinfl) decompress(in, dict []byte, outPos int, hasMore bool) (status, inUsed, outLen int) {
	c := &tinflRun{r: r, in: in, dict: dict, pos: outPos, hasMore: hasMore}
	mask := len(dict) - 1
	noInput := tinflStatusFailed
	if hasMore {
		noInput = tinflStatusNeedsMoreInput
	}
	state := r.state

loop:
	for {
		switch state {
		case tsStart:
			r.bitBuf, r.numBits, r.dist, r.counter, r.numExtra = 0, 0, 0, 0, 0
			r.zHeader0, r.zHeader1, r.zAdler, r.check = 0, 0, 1, 1
			state = tsReadZlibCmf

		case tsReadZlibCmf:
			b, ok := c.readByte()
			if !ok {
				status = noInput
				break loop
			}
			r.zHeader0 = uint32(b)
			state = tsReadZlibFlg

		case tsReadZlibFlg:
			b, ok := c.readByte()
			if !ok {
				status = noInput
				break loop
			}
			r.zHeader1 = uint32(b)
			// validate_zlib_header: a multiple of 31, no dictionary, deflate, a window of at most 32 KiB.
			if (r.zHeader0*256+r.zHeader1)%31 != 0 || r.zHeader1&0x20 != 0 || r.zHeader0&15 != 8 || 1<<((r.zHeader0>>4)+8) > 32768 {
				state = tsFailed
			} else {
				state = tsReadBlockHeader
			}

		case tsReadBlockHeader:
			bits, ok := c.readBits(3)
			if !ok {
				status = noInput
				break loop
			}
			r.finish = bits & 1
			r.blockType = bits >> 1 & 3
			switch r.blockType {
			case 0:
				state = tsBlockTypeNoCompression
			case 1:
				r.startStaticTable()
				state = r.initTree()
			case 2:
				r.counter = 0
				state = tsReadTableSizes
			default:
				state = tsFailed
			}

		case tsBlockTypeNoCompression:
			// pad_to_bytes
			if _, ok := c.readBits(r.numBits & 7); !ok {
				status = noInput
				break loop
			}
			r.counter = 0
			state = tsRawHeader

		case tsRawHeader:
			if r.counter < 4 {
				var b byte
				var ok bool
				if r.numBits != 0 {
					var bits uint32
					bits, ok = c.readBits(8)
					b = byte(bits)
				} else {
					b, ok = c.readByte()
				}
				if !ok {
					status = noInput
					break loop
				}
				r.rawHeader[r.counter] = b
				r.counter++
				continue
			}
			length := uint16(r.rawHeader[0]) | uint16(r.rawHeader[1])<<8
			check := uint16(r.rawHeader[2]) | uint16(r.rawHeader[3])<<8
			r.counter = uint32(length)
			switch {
			case length != ^check:
				state = tsFailed
			case r.counter == 0:
				state = tsBlockDone
			case r.numBits != 0:
				state = tsRawReadFirstByte
			default:
				state = tsRawMemcpy1
			}

		case tsRawReadFirstByte:
			bits, ok := c.readBits(8)
			if !ok {
				status = noInput
				break loop
			}
			r.dist = bits
			state = tsRawStoreFirstByte

		case tsRawStoreFirstByte:
			if c.bytesLeft() == 0 {
				status = tinflStatusHasMoreOutput
				break loop
			}
			c.dict[c.pos] = byte(r.dist)
			c.pos++
			r.counter--
			if r.counter == 0 || r.numBits == 0 {
				state = tsRawMemcpy1
			} else {
				state = tsRawReadFirstByte
			}

		case tsRawMemcpy1:
			switch {
			case r.counter == 0:
				state = tsBlockDone
			case c.bytesLeft() == 0:
				status = tinflStatusHasMoreOutput
				break loop
			default:
				state = tsRawMemcpy2
			}

		case tsRawMemcpy2:
			if c.ip >= len(c.in) {
				status = noInput
				break loop
			}
			n := min(c.bytesLeft(), len(c.in)-c.ip, int(r.counter))
			copy(c.dict[c.pos:], c.in[c.ip:c.ip+n])
			c.pos += n
			c.ip += n
			r.counter -= uint32(n)
			state = tsRawMemcpy1

		case tsReadTableSizes:
			if r.counter < 3 {
				bits, ok := c.readBits([3]uint32{5, 5, 4}[r.counter])
				if !ok {
					status = noInput
					break loop
				}
				r.sizes[r.counter] = bits + tinflMinSizes[r.counter]
				r.counter++
				continue
			}
			clear(r.tables[2].codeSize[:])
			r.counter = 0
			if r.sizes[0] <= 286 && r.sizes[1] <= 30 {
				state = tsReadHufflenTableCodeSize
			} else {
				state = tsFailed
			}

		case tsReadHufflenTableCodeSize:
			if r.counter < r.sizes[2] {
				bits, ok := c.readBits(3)
				if !ok {
					status = noInput
					break loop
				}
				r.tables[2].codeSize[tinflLengthOrder[r.counter]] = uint8(bits)
				r.counter++
				continue
			}
			r.sizes[2] = 19
			state = r.initTree()

		case tsReadLitlenDistTablesCodeSize:
			switch {
			case r.counter < r.sizes[0]+r.sizes[1]:
				symbol, ok, failed := c.decodeHuffman(2)
				if failed {
					state = tsFailed
					continue
				}
				if !ok {
					status = noInput
					break loop
				}
				r.dist = symbol
				switch {
				case symbol < 16:
					r.lenCodes[r.counter] = uint8(symbol)
					r.counter++
				case symbol == 16 && r.counter == 0:
					state = tsFailed
				default:
					r.numExtra = [3]uint32{2, 3, 7}[symbol-16]
					state = tsReadExtraBitsCodeSize
				}
			case r.counter != r.sizes[0]+r.sizes[1]:
				state = tsFailed
			default:
				copy(r.tables[0].codeSize[:r.sizes[0]], r.lenCodes[:r.sizes[0]])
				copy(r.tables[1].codeSize[:r.sizes[1]], r.lenCodes[r.sizes[0]:r.sizes[0]+r.sizes[1]])
				r.blockType--
				state = r.initTree()
			}

		case tsReadExtraBitsCodeSize:
			extra, ok := c.readBits(r.numExtra)
			if !ok {
				status = noInput
				break loop
			}
			extra += [4]uint32{3, 3, 11, 0}[(r.dist-16)&3]
			var val uint8
			if r.dist == 16 {
				val = r.lenCodes[r.counter-1]
			}
			for i := range extra {
				r.lenCodes[r.counter+i] = val
			}
			r.counter += extra
			state = tsReadLitlenDistTablesCodeSize

		case tsDecodeLitlen:
			symbol, ok, failed := c.decodeHuffman(0)
			if failed {
				state = tsFailed
				continue
			}
			if !ok {
				status = noInput
				break loop
			}
			r.counter = symbol
			state = tsWriteSymbol

		case tsWriteSymbol:
			switch {
			case r.counter >= 256:
				state = tsHuffDecodeOuterLoop1
			case c.bytesLeft() > 0:
				c.dict[c.pos] = byte(r.counter)
				c.pos++
				state = tsDecodeLitlen
			default:
				status = tinflStatusHasMoreOutput
				break loop
			}

		case tsHuffDecodeOuterLoop1:
			r.counter &= 511
			switch {
			case r.counter == 256:
				state = tsBlockDone
			case r.counter > 285:
				state = tsFailed
			default:
				r.numExtra = uint32(tinflLengthExtra[(r.counter-257)&31])
				r.counter = uint32(tinflLengthBase[(r.counter-257)&31])
				if r.numExtra != 0 {
					state = tsReadExtraBitsLitlen
				} else {
					state = tsDecodeDistance
				}
			}

		case tsReadExtraBitsLitlen:
			extra, ok := c.readBits(r.numExtra)
			if !ok {
				status = noInput
				break loop
			}
			r.counter += extra
			state = tsDecodeDistance

		case tsDecodeDistance:
			symbol, ok, failed := c.decodeHuffman(1)
			if failed {
				state = tsFailed
				continue
			}
			if !ok {
				status = noInput
				break loop
			}
			if symbol > 29 {
				state = tsFailed
				continue
			}
			r.numExtra = uint32(tinflDistExtra[symbol&31])
			r.dist = uint32(tinflDistBase[symbol&31])
			if r.numExtra != 0 {
				state = tsReadExtraBitsDistance
			} else {
				state = tsHuffDecodeOuterLoop2
			}

		case tsReadExtraBitsDistance:
			extra, ok := c.readBits(r.numExtra)
			if !ok {
				status = noInput
				break loop
			}
			r.dist += extra
			state = tsHuffDecodeOuterLoop2

		case tsHuffDecodeOuterLoop2:
			// The output is the wrapping dictionary: a distance past the start of the data reads what the dictionary holds (zeros at first).
			// The copy is made byte by byte in order, as miniz's `transfer` does, and as much of it as fits before the end of the dictionary.
			state = tsWriteLenBytesToEnd
			if r.counter == 0 {
				state = tsDecodeLitlen
			}

		case tsWriteLenBytesToEnd:
			if c.bytesLeft() == 0 {
				status = tinflStatusHasMoreOutput
				break loop
			}
			n := min(c.bytesLeft(), int(r.counter))
			for i := range n {
				c.dict[c.pos+i] = c.dict[(c.pos+i-int(r.dist))&mask]
			}
			c.pos += n
			r.counter -= uint32(n)
			if r.counter == 0 {
				state = tsDecodeLitlen
			}

		case tsBlockDone:
			if r.finish == 0 {
				state = tsReadBlockHeader
				continue
			}
			// pad_to_bytes, then the whole bytes of the bit buffer go back to the input.
			c.readBits(r.numBits & 7)
			undo := min(int(r.numBits>>3), c.ip)
			c.ip -= undo
			r.numBits -= uint32(undo) << 3
			r.bitBuf &= 1<<r.numBits - 1
			r.counter = 0
			state = tsReadAdler32

		case tsReadAdler32:
			if r.counter < 4 {
				var b byte
				var ok bool
				if r.numBits != 0 {
					var bits uint32
					bits, ok = c.readBits(8)
					b = byte(bits)
				} else {
					b, ok = c.readByte()
				}
				if !ok {
					status = noInput
					break loop
				}
				r.zAdler = r.zAdler<<8 | uint32(b)
				r.counter++
				continue
			}
			state = tsDoneForever

		case tsDoneForever:
			status = tinflStatusDone
			break loop

		default: // tsFailed
			status = tinflStatusFailed
			break loop
		}
	}

	if status != tinflStatusNeedsMoreInput && status != tinflStatusFailed {
		undo := min(int(r.numBits>>3), c.ip)
		c.ip -= undo
		r.numBits -= uint32(undo) << 3
	}
	// HasMoreOutput overrides NeedsMoreInput when the output is full (unless the Adler-32 is what is missing).
	if status == tinflStatusNeedsMoreInput && c.bytesLeft() == 0 && state != tsReadAdler32 {
		status = tinflStatusHasMoreOutput
	}
	r.state = state
	r.bitBuf &= 1<<r.numBits - 1

	if status != tinflStatusFailed {
		r.check = adler32Update(r.check, c.dict[outPos:c.pos])
		if status == tinflStatusDone && r.check != r.zAdler {
			status = tinflStatusFailed
		}
	}
	return status, c.ip, c.pos - outPos
}

// adler32Update is shared.rs update_adler32.
func adler32Update(adler uint32, data []byte) uint32 {
	a, b := adler&0xFFFF, adler>>16
	for len(data) > 0 {
		chunk := data[:min(len(data), 5552)]
		data = data[len(chunk):]
		for _, v := range chunk {
			a += uint32(v)
			b += a
		}
		a %= 65521
		b %= 65521
	}
	return b<<16 | a
}

// tiffInflater is flate2's `read::ZlibDecoder<R>` with miniz_oxide's streaming `inflate` (zio.rs:118-150 `read`, stream.rs:185-357).
type tiffInflater struct {
	// data is the file from the chunk's offset on; bufStart and bufEnd bound the 32 KiB buffer of the BufReader flate2 puts around it.
	data     []byte
	bufStart int
	bufEnd   int

	state     tinfl
	dict      []byte
	dictOfs   int
	dictAvail int
	// lastFailed is state.last_status < 0; lastDone is TINFLStatus::Done; hasFlushed is a call with Finish made.
	lastFailed, lastDone, hasFlushed bool
}

var errTIFFInflate = errors.New("photonimage: tiff: corrupt deflate stream")

func newTIFFInflater(data []byte) *tiffInflater {
	return &tiffInflater{data: data, dict: make([]byte, tinflDictSize)}
}

// fillBuf is BufReader::fill_buf: the bytes left of the buffer, refilled from the data when it is empty.
func (z *tiffInflater) fillBuf() []byte {
	if z.bufStart >= z.bufEnd {
		z.bufEnd = min(z.bufEnd+tinflDictSize, len(z.data))
		z.bufStart = min(z.bufStart, z.bufEnd)
	}
	return z.data[z.bufStart:z.bufEnd]
}

// read is zio::read.
func (z *tiffInflater) read(p []byte) (int, error) {
	for {
		input := z.fillBuf()
		eof := len(input) == 0
		consumed, written, status := z.inflate(input, p, eof)
		z.bufStart += consumed
		switch status {
		case inflateData:
			return 0, errTIFFInflate
		case inflateOK, inflateBuf:
			if written == 0 && !eof && len(p) > 0 {
				continue
			}
		}
		return written, nil
	}
}

type inflateResult uint8

const (
	inflateOK inflateResult = iota
	inflateStreamEnd
	inflateBuf
	inflateData
)

// pushDictOut is stream.rs push_dict_out.
func (z *tiffInflater) pushDictOut(out []byte) int {
	n := min(z.dictAvail, len(out))
	copy(out, z.dict[z.dictOfs:z.dictOfs+n])
	z.dictAvail -= n
	z.dictOfs = (z.dictOfs + n) & (tinflDictSize - 1)
	return n
}

// inflate is stream.rs `inflate` for the zlib format with Adler-32 checking, flush None (or Finish at the end of the input).
func (z *tiffInflater) inflate(input, out []byte, finish bool) (consumed, written int, result inflateResult) {
	if z.lastFailed {
		return 0, 0, inflateData
	}
	if z.hasFlushed && !finish {
		return 0, 0, inflateData
	}
	z.hasFlushed = z.hasFlushed || finish

	if z.dictAvail != 0 {
		written = z.pushDictOut(out)
		if z.lastDone && z.dictAvail == 0 {
			return 0, written, inflateStreamEnd
		}
		return 0, written, inflateOK
	}

	origIn := len(input)
	for {
		status, used, produced := z.state.decompress(input, z.dict, z.dictOfs, !finish)
		z.lastFailed = status == tinflStatusFailed
		z.lastDone = status == tinflStatusDone
		input = input[used:]
		consumed += used
		z.dictAvail = produced
		written += z.pushDictOut(out[written:])

		if status == tinflStatusFailed {
			return consumed, written, inflateData
		}
		if status == tinflStatusNeedsMoreInput && origIn == 0 {
			return consumed, written, inflateBuf
		}
		if finish {
			if status == tinflStatusDone {
				if z.dictAvail != 0 {
					return consumed, written, inflateBuf
				}
				return consumed, written, inflateStreamEnd
			} else if written == len(out) {
				return consumed, written, inflateBuf
			}
			continue
		}
		emptyBuf := len(input) == 0 || written == len(out)
		if status == tinflStatusDone || emptyBuf || z.dictAvail != 0 {
			if status == tinflStatusDone && z.dictAvail == 0 {
				return consumed, written, inflateStreamEnd
			}
			return consumed, written, inflateOK
		}
	}
}
