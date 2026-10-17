// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: Copyright (c) 2015 nwin
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package images

import (
	"bytes"
	"encoding/binary"
	"hash"
	"hash/adler32"
	"hash/crc32"
	"image"
	"math/bits"
)

// Photon's `PhotonImage.get_bytes()` writes PNG with the image crate 0.24.9 `PngEncoder::new` defaults: png 0.17.14 with
// `Compression::Fast` and adaptive filtering, which compresses with fdeflate 0.3.6 and falls back to stored blocks when that
// is not smaller. encodePhotonPNG ports that encoder, so the PNG read gives the model has Photon's bytes, and with them
// Photon's size, which decides whether a PNG fits the byte limit or the next encoding is tried.

// encodePhotonPNG is img as an RGBA 8-bit PNG: the signature, IHDR, one IDAT chunk and IEND (png encoder.rs
// write_image_data with Compression::Fast).
func encodePhotonPNG(img *image.NRGBA) []byte {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	rowLength := width * 4
	rows := make([][]byte, height)
	for y := range height {
		rows[y] = img.Pix[y*img.Stride : y*img.Stride+rowLength]
	}

	compressor := newFdeflateCompressor()
	previous := make([]byte, rowLength)
	current := make([]byte, rowLength+1)
	for _, row := range rows {
		current[0] = adaptiveFilter(previous, row, current[1:])
		compressor.writeData(current)
		previous = row
	}
	compressed := compressor.finish()
	if len(compressed) > storedCompressedSize((rowLength+1)*height) {
		// png: uncompressed data, unfiltered, since fast compression would take more space than that.
		stored := newStoredCompressor()
		for _, row := range rows {
			stored.writeData([]byte{0})
			stored.writeData(row)
		}
		compressed = stored.finish()
	}

	var out bytes.Buffer
	out.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:], uint32(width))
	binary.BigEndian.PutUint32(header[4:], uint32(height))
	header[8], header[9] = 8, 6 // bit depth 8, RGBA
	writePNGChunk(&out, "IHDR", header)
	// MAX_IDAT_CHUNK_LEN is u32::MAX >> 1, more than any image here holds.
	writePNGChunk(&out, "IDAT", compressed)
	writePNGChunk(&out, "IEND", nil)
	return out.Bytes()
}

func writePNGChunk(out *bytes.Buffer, name string, data []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	out.Write(length[:])
	out.WriteString(name)
	out.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(name))
	crc.Write(data)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	out.Write(sum[:])
}

const (
	filterSub   = 1
	filterUp    = 2
	filterAvg   = 3
	filterPaeth = 4
	// bytesPerPixel is RGBA 8-bit's.
	bytesPerPixel = 4
)

// adaptiveFilter writes the row filtered into output with the filter whose output has the smallest sum of absolute signed
// bytes, trying Sub, Up, Avg and Paeth in order and taking the later on a tie, and returns the filter's type (png filter.rs
// filter with AdaptiveFilterType::Adaptive).
func adaptiveFilter(previous, current, output []byte) byte {
	minSum := ^uint64(0)
	choice := byte(0)
	for _, filter := range []byte{filterSub, filterUp, filterAvg, filterPaeth} {
		applyFilter(filter, previous, current, output)
		if sum := sumAbsSigned(output); sum <= minSum {
			minSum, choice = sum, filter
		}
	}
	if choice != filterPaeth {
		applyFilter(choice, previous, current, output)
	}
	return choice
}

func sumAbsSigned(buffer []byte) uint64 {
	var sum uint64
	for _, b := range buffer {
		value := int8(b)
		if value < 0 {
			sum += uint64(-int16(value))
		} else {
			sum += uint64(value)
		}
	}
	return sum
}

// applyFilter is png filter.rs filter_internal for one filter type.
func applyFilter(filter byte, previous, current, output []byte) {
	switch filter {
	case filterSub:
		copy(output[:bytesPerPixel], current[:bytesPerPixel])
		for i := bytesPerPixel; i < len(current); i++ {
			output[i] = current[i] - current[i-bytesPerPixel]
		}
	case filterUp:
		for i := range current {
			output[i] = current[i] - previous[i]
		}
	case filterAvg:
		for i := range bytesPerPixel {
			output[i] = current[i] - previous[i]/2
		}
		for i := bytesPerPixel; i < len(current); i++ {
			a, b := current[i-bytesPerPixel], previous[i]
			output[i] = current[i] - ((a & b) + ((a ^ b) >> 1))
		}
	case filterPaeth:
		for i := range bytesPerPixel {
			output[i] = current[i] - paeth(0, previous[i], 0)
		}
		for i := bytesPerPixel; i < len(current); i++ {
			output[i] = current[i] - paeth(current[i-bytesPerPixel], previous[i], previous[i-bytesPerPixel])
		}
	}
}

// paeth is png filter.rs filter_paeth, the Paeth predictor on unsigned bytes.
func paeth(a, b, c byte) byte {
	pa := max(b, c) - min(b, c)
	pb := max(a, c) - min(a, c)
	pc := byte(255)
	if (a < c) == (c < b) {
		pc = max(pa, pb) - min(pa, pb)
	}
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	default:
		return c
	}
}

// fdeflateHuffmanLengths is fdeflate tables.rs HUFFMAN_LENGTHS: the code lengths of every literal and length symbol, fixed
// whatever the input, tuned for filtered PNG rows.
var fdeflateHuffmanLengths = [286]uint8{
	2, 3, 4, 5, 5, 6, 6, 7, 7, 7, 8, 8, 8, 8, 8, 9, 9, 9, 9, 9, 9, 9, 10, 10, 10, 10, 10, 10, 10,
	10, 10, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 11, 11, 11, 11, 11, 11, 11,
	11, 11, 11, 10, 11, 10, 10, 10, 10, 10, 10, 10, 10, 10, 9, 9, 9, 9, 9, 8, 9, 8, 8, 8, 8, 8, 7,
	7, 7, 6, 6, 6, 5, 4, 3, 12, 12, 12, 9, 9, 11, 10, 11, 11, 10, 11, 11, 11, 11, 11, 11, 12, 11,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 9,
}

// fdeflateHuffmanCodes is fdeflate tables.rs HUFFMAN_CODES: the canonical codes of fdeflateHuffmanLengths, bit-reversed for
// deflate's least-significant-bit-first order (lib.rs compute_codes).
var fdeflateHuffmanCodes = func() (codes [286]uint16) {
	code := uint32(0)
	for length := uint8(1); length <= 16; length++ {
		for i, l := range fdeflateHuffmanLengths {
			if l == length {
				codes[i] = bits.Reverse16(uint16(code)) >> (16 - length)
				code++
			}
		}
		code <<= 1
	}
	return codes
}()

// fdeflateLengthToSymbol is the deflate length symbol of a match of length+3 (tables.rs LENGTH_TO_SYMBOL), and
// fdeflateLengthExtra its number of extra bits (LENGTH_TO_LEN_EXTRA).
var fdeflateLengthToSymbol, fdeflateLengthExtra = func() (symbols [256]uint16, extra [256]uint8) {
	bases := [29]int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	extras := [29]uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	for length := 3; length <= 258; length++ {
		symbol := 0
		for symbol+1 < len(bases) && bases[symbol+1] <= length {
			symbol++
		}
		symbols[length-3], extra[length-3] = uint16(257+symbol), extras[symbol]
	}
	return symbols, extra
}()

// fdeflateHeader is the zlib header and the dynamic Huffman block header of fdeflate's fixed code (compress.rs
// write_headers): 53 bytes and then 5 bits of the last.
var fdeflateHeader = [54]byte{
	120, 1, 237, 192, 3, 160, 36, 89, 150, 198, 241, 255, 119, 238, 141, 200, 204, 167,
	114, 75, 99, 174, 109, 219, 182, 109, 219, 182, 109, 219, 182, 109, 105, 140, 158, 150,
	74, 175, 158, 50, 51, 34, 238, 249, 118, 183, 106, 122, 166, 135, 59, 107, 213, 15,
}

// fdeflateCompressor is fdeflate compress.rs Compressor: one dynamic Huffman block with a fixed code, where runs of zero
// bytes are matches at distance 1 and every other byte a literal.
type fdeflateCompressor struct {
	out      bytes.Buffer
	checksum hash.Hash32
	buffer   uint64
	nbits    uint8
}

func newFdeflateCompressor() *fdeflateCompressor {
	c := &fdeflateCompressor{checksum: adler32.New()}
	c.out.Write(fdeflateHeader[:53])
	c.writeBits(uint64(fdeflateHeader[53]), 5)
	return c
}

func (c *fdeflateCompressor) writeBits(value uint64, nbits uint8) {
	c.buffer |= value << c.nbits
	c.nbits += nbits
	if c.nbits >= 64 {
		var word [8]byte
		binary.LittleEndian.PutUint64(word[:], c.buffer)
		c.out.Write(word[:])
		c.nbits -= 64
		c.buffer = 0
		if shift := nbits - c.nbits; shift < 64 {
			c.buffer = value >> shift
		}
	}
}

func (c *fdeflateCompressor) writeLiteral(b byte) {
	c.writeBits(uint64(fdeflateHuffmanCodes[b]), fdeflateHuffmanLengths[b])
}

// writeRun is a literal zero and then the run of zeros after it as matches at distance 1, whose one-bit distance code is 0.
func (c *fdeflateCompressor) writeRun(run uint32) {
	c.writeBits(uint64(fdeflateHuffmanCodes[0]), fdeflateHuffmanLengths[0])
	run--
	for run >= 258 {
		c.writeBits(uint64(fdeflateHuffmanCodes[285]), fdeflateHuffmanLengths[285]+1)
		run -= 258
	}
	if run > 4 {
		symbol := fdeflateLengthToSymbol[run-3]
		c.writeBits(uint64(fdeflateHuffmanCodes[symbol]), fdeflateHuffmanLengths[symbol])
		extra := fdeflateLengthExtra[run-3]
		c.writeBits(uint64((run-3)&(1<<extra-1)), extra+1)
	} else {
		// The literal zero's code is all zero bits.
		c.writeBits(0, uint8(run)*fdeflateHuffmanLengths[0])
	}
}

// writeData is compress.rs write_data: eight bytes at a time, an all-zero word extends the run, and a run ends at the word's
// first nonzero byte.
func (c *fdeflateCompressor) writeData(data []byte) {
	c.checksum.Write(data)
	run := uint32(0)
	whole := len(data) / 8 * 8
	for offset := 0; offset < whole; offset += 8 {
		chunk := data[offset : offset+8]
		word := binary.LittleEndian.Uint64(chunk)
		if word == 0 {
			run += 8
			continue
		} else if run > 0 {
			runExtra := uint32(bits.TrailingZeros64(word) / 8)
			c.writeRun(run + runExtra)
			run = 0
			if runExtra > 0 {
				run = uint32(bits.LeadingZeros64(word) / 8)
				for _, b := range chunk[runExtra : 8-run] {
					c.writeLiteral(b)
				}
				continue
			}
		}
		if runStart := uint32(bits.LeadingZeros64(word) / 8); runStart > 0 {
			for _, b := range chunk[:8-runStart] {
				c.writeLiteral(b)
			}
			run = runStart
			continue
		}
		for _, b := range chunk {
			c.writeLiteral(b)
		}
	}
	if run > 0 {
		c.writeRun(run)
	}
	for _, b := range data[whole:] {
		c.writeLiteral(b)
	}
}

// finish writes the end of block, pads to a byte, and appends the Adler-32 checksum.
func (c *fdeflateCompressor) finish() []byte {
	c.writeBits(uint64(fdeflateHuffmanCodes[256]), fdeflateHuffmanLengths[256])
	if c.nbits%8 != 0 {
		c.writeBits(0, 8-c.nbits%8)
	}
	if c.nbits > 0 {
		var word [8]byte
		binary.LittleEndian.PutUint64(word[:], c.buffer)
		c.out.Write(word[:c.nbits/8])
		c.buffer, c.nbits = 0, 0
	}
	c.out.Write(c.checksum.Sum(nil))
	return c.out.Bytes()
}

// storedCompressor is fdeflate compress.rs StoredOnlyCompressor: a zlib stream of stored blocks of at most 65535 bytes.
type storedCompressor struct {
	out         []byte
	checksum    hash.Hash32
	blockStart  int
	blockLength int
}

func newStoredCompressor() *storedCompressor {
	return &storedCompressor{out: []byte{0x78, 0x01, 0, 0, 0, 0, 0}, checksum: adler32.New(), blockStart: 2}
}

func (s *storedCompressor) setBlockHeader(last bool) {
	size := uint16(s.blockLength)
	header := s.out[s.blockStart : s.blockStart+5]
	header[0] = 0
	if last {
		header[0] = 1
	}
	binary.LittleEndian.PutUint16(header[1:], size)
	binary.LittleEndian.PutUint16(header[3:], ^size)
}

func (s *storedCompressor) writeData(data []byte) {
	s.checksum.Write(data)
	for len(data) > 0 {
		if s.blockLength == 0xffff {
			s.setBlockHeader(false)
			s.blockStart = len(s.out)
			s.out = append(s.out, 0, 0, 0, 0, 0)
			s.blockLength = 0
		}
		n := min(len(data), 0xffff-s.blockLength)
		s.out = append(s.out, data[:n]...)
		s.blockLength += n
		data = data[n:]
	}
}

func (s *storedCompressor) finish() []byte {
	s.setBlockHeader(true)
	return s.checksum.Sum(s.out)
}

// storedCompressedSize is StoredOnlyCompressor::compressed_size: the stream's length for rawSize bytes.
func storedCompressedSize(rawSize int) int {
	return max(rawSize-1, 0)/0xffff*(0xffff+5) + rawSize%0xffff + 5 + 6
}
