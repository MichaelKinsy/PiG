// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"math"
)

// DecodePNG decodes a PNG file as Photon does: image 0.24.9 `load_from_memory` gives the bytes to `PngDecoder::with_limits(r, Limits::default())`
// (codecs/png.rs:119-199), which reads the image with the png crate 0.17.14 under the EXPAND transformation (the version the wasm of
// @silvia-odwyer/photon and photon-node embeds), and Photon converts the result with `to_rgba8`. The port follows the crate's streaming
// decoder (decoder/stream.rs), its reader (decoder/mod.rs, transform.rs, transform/palette.rs, adam7.rs, interlace_info.rs) and fdeflate
// 0.3.6 (pngdecode_inflate.go) step by step, so that it accepts the files the crate accepts and gives their pixels: text chunks are
// skipped, an ancillary chunk with a bad checksum is skipped, a critical one is an error; only the first image of an animated PNG is
// decoded (the default image: the IDAT data, or the fdAT data of the first fcTL when there is no IDAT chunk); the byte after the image
// data is not looked at but the header of the chunk after it must be there; the checksum of the zlib stream is not verified; the limits of
// the crate (512 MiB, `Limits::bytes`) are accounted for the buffers of the chunks, the palette, the transparency, the ICC profile and the
// row, in `usize` of wasm32.
func DecodePNG(data []byte) (img *image.NRGBA, err error) {
	defer func() {
		// A panic of the Rust code (an index out of range in the palette expansion, in the inflater) is an exception of the wasm instance.
		if r := recover(); r != nil {
			img, err = nil, fmt.Errorf("photonimage: png: panic: %v", r)
		}
	}()
	r, err := pngOpen(data, imageMaxAlloc)
	if err != nil {
		return nil, err
	}
	color, depth := r.outputColorType()
	channels, bytesPerPixel := pngSamples(color), pngSamples(color)*uint64(depth/8)
	// load_inner reserves the decoder's total_bytes before it decodes, and `decoder_to_image` and `to_rgba8` create image buffers.
	if err := reserveTotalBytes(r.info.width, r.info.height, bytesPerPixel); err != nil {
		return nil, err
	}
	if err := checkRGBAConversion(r.info.width, r.info.height, channels); err != nil {
		return nil, err
	}
	// `to_rgba8` allocates the RGBA buffer with `vec!`, which fails for more than isize::MAX bytes (2 GiB on wasm32).
	if uint64(r.info.width)*uint64(r.info.height)*4 > math.MaxInt32 {
		return nil, errImageBufferOverflow
	}
	out := newImageNRGBA(r.info.width, r.info.height)
	if color == pngRGBA && depth == 8 {
		if err := r.nextFrame(out.Pix); err != nil {
			return nil, err
		}
		return out, nil
	}
	raw := make([]byte, uint64(r.info.width)*uint64(r.info.height)*bytesPerPixel)
	if err := r.nextFrame(raw); err != nil {
		return nil, err
	}
	pngToRGBA8(out.Pix, raw, color, depth)
	return out, nil
}

// pngToRGBA8 is `DynamicImage::to_rgba8` of the image the decoder made (dynimage.rs:211-214, color.rs:577-633): the samples of 16 bits are
// big endian in raw and become `(v + 128) / 257`; an opaque pixel gets alpha 255, a gray one is repeated.
func pngToRGBA8(dst, raw []byte, color, depth byte) {
	switch {
	case depth == 8 && color == pngGray:
		for i, v := range raw {
			dst[4*i], dst[4*i+1], dst[4*i+2], dst[4*i+3] = v, v, v, 0xFF
		}
	case depth == 8 && color == pngGrayAlpha:
		for i := 0; i+1 < len(raw); i += 2 {
			o := 2 * i
			dst[o], dst[o+1], dst[o+2], dst[o+3] = raw[i], raw[i], raw[i], raw[i+1]
		}
	case depth == 8 && color == pngRGB:
		for i, o := 0, 0; i+2 < len(raw); i, o = i+3, o+4 {
			dst[o], dst[o+1], dst[o+2], dst[o+3] = raw[i], raw[i+1], raw[i+2], 0xFF
		}
	case depth == 16 && color == pngGray:
		for i, o := 0, 0; i+1 < len(raw); i, o = i+2, o+4 {
			v := downscale16(binary.BigEndian.Uint16(raw[i:]))
			dst[o], dst[o+1], dst[o+2], dst[o+3] = v, v, v, 0xFF
		}
	case depth == 16 && color == pngGrayAlpha:
		for i, o := 0, 0; i+3 < len(raw); i, o = i+4, o+4 {
			v := downscale16(binary.BigEndian.Uint16(raw[i:]))
			dst[o], dst[o+1], dst[o+2], dst[o+3] = v, v, v, downscale16(binary.BigEndian.Uint16(raw[i+2:]))
		}
	case depth == 16 && color == pngRGB:
		for i, o := 0, 0; i+5 < len(raw); i, o = i+6, o+4 {
			dst[o] = downscale16(binary.BigEndian.Uint16(raw[i:]))
			dst[o+1] = downscale16(binary.BigEndian.Uint16(raw[i+2:]))
			dst[o+2] = downscale16(binary.BigEndian.Uint16(raw[i+4:]))
			dst[o+3] = 0xFF
		}
	case depth == 16 && color == pngRGBA:
		for i, o := 0, 0; i+7 < len(raw); i, o = i+8, o+4 {
			dst[o] = downscale16(binary.BigEndian.Uint16(raw[i:]))
			dst[o+1] = downscale16(binary.BigEndian.Uint16(raw[i+2:]))
			dst[o+2] = downscale16(binary.BigEndian.Uint16(raw[i+4:]))
			dst[o+3] = downscale16(binary.BigEndian.Uint16(raw[i+6:]))
		}
	}
}

// PNG colour types (`ColorType`, common.rs).
const (
	pngGray      byte = 0
	pngRGB       byte = 2
	pngIndexed   byte = 3
	pngGrayAlpha byte = 4
	pngRGBA      byte = 6
)

// pngChunkBufferSize is CHUNK_BUFFER_SIZE (stream.rs:19): the capacity of the BufReader and of the first chunk buffer.
const pngChunkBufferSize = 32 * 1024

// pngUsizeMax is `usize::MAX` of wasm32.
const pngUsizeMax = math.MaxUint32

var errPNGLimits = errors.New("photonimage: png: limits are exceeded")

// pngSamples is `ColorType::samples`.
func pngSamples(colorType byte) uint64 {
	switch colorType {
	case pngGray, pngIndexed:
		return 1
	case pngRGB:
		return 3
	case pngGrayAlpha:
		return 2
	}
	return 4
}

// pngFrameControl is the fcTL chunk (`FrameControl`).
type pngFrameControl struct{ width, height uint32 }

// pngInfo is the parts of `Info` (common.rs) that decoding needs.
type pngInfo struct {
	width, height uint32
	depth         byte
	colorType     byte
	interlaced    bool

	palette    []byte
	hasPalette bool
	trns       []byte
	hasTRNS    bool

	// Chunks that may appear once before the image data (the checks of their parse functions).
	phys, chrm, gama, srgb bool

	frame    pngFrameControl
	hasFrame bool
}

// checkedRawRowLength is `ColorType::checked_raw_row_length` (common.rs:51-55) in the `usize` of wasm32.
func pngCheckedRawRowLength(colorType, depth byte, width uint32) (uint64, bool) {
	bits := uint64(width) * pngSamples(colorType) * uint64(depth)
	n := 1 + (bits+7)/8
	return n, n <= pngUsizeMax
}

// rawRowLengthFromWidth is `ColorType::raw_row_length_from_width` (common.rs:57-69).
func pngRawRowLengthFromWidth(colorType, depth byte, width uint32) int {
	bits := uint64(width) * pngSamples(colorType) * uint64(depth)
	return int(1 + (bits+7)/8)
}

// outputColorType is `Reader::output_color_type` (decoder/mod.rs:646-678) under EXPAND.
func (i *pngInfo) outputColorType() (colorType, depth byte) {
	depth = i.depth
	if depth < 8 {
		depth = 8
	}
	colorType = i.colorType
	switch i.colorType {
	case pngGray:
		if i.hasTRNS {
			colorType = pngGrayAlpha
		}
	case pngRGB:
		if i.hasTRNS {
			colorType = pngRGBA
		}
	case pngIndexed:
		colorType = pngRGB
		if i.hasTRNS {
			colorType = pngRGBA
		}
	}
	return colorType, depth
}

func (i *pngInfo) rawRowLengthFromWidth(width uint32) int {
	return pngRawRowLengthFromWidth(i.colorType, i.depth, width)
}

// pngInput is the `BufReader` of capacity CHUNK_BUFFER_SIZE the decoder reads the stream through: a window of the data that starts
// where the buffer was last refilled.
type pngInput struct {
	data        []byte
	pos, filled int
}

func (in *pngInput) fillBuf() []byte {
	if in.pos >= in.filled {
		n := min(pngChunkBufferSize, len(in.data)-in.filled)
		in.pos = in.filled
		in.filled += n
	}
	return in.data[in.pos:in.filled]
}

func (in *pngInput) consume(n int) { in.pos = min(in.pos+n, in.filled) }

type pngEvent uint8

const (
	pngNothing pngEvent = iota
	pngHeader
	pngChunkBegin
	pngChunkComplete
	pngFrameControlEvent
	pngImageData
	pngImageDataFlushed
	pngPartialChunk
	pngImageEnd
)

type pngDecoded struct {
	kind  pngEvent
	chunk [4]byte
}

type pngStateKind uint8

const (
	pngStateNone pngStateKind = iota
	pngStateU32
	pngStateReadChunkData
	pngStateParseChunkData
	pngStateImageData
)

type pngU32Kind uint8

const (
	pngSignature1st pngU32Kind = iota
	pngSignature2nd
	pngLength
	pngType
	pngCRC
	pngAPNGSequenceNumber
)

var (
	pngTypeIHDR = [4]byte{'I', 'H', 'D', 'R'}
	pngTypePLTE = [4]byte{'P', 'L', 'T', 'E'}
	pngTypeTRNS = [4]byte{'t', 'R', 'N', 'S'}
	pngTypePHYS = [4]byte{'p', 'H', 'Y', 's'}
	pngTypeGAMA = [4]byte{'g', 'A', 'M', 'A'}
	pngTypeACTL = [4]byte{'a', 'c', 'T', 'L'}
	pngTypeFCTL = [4]byte{'f', 'c', 'T', 'L'}
	pngTypeCHRM = [4]byte{'c', 'H', 'R', 'M'}
	pngTypeSRGB = [4]byte{'s', 'R', 'G', 'B'}
	pngTypeICCP = [4]byte{'i', 'C', 'C', 'P'}
	pngTypeIDAT = [4]byte{'I', 'D', 'A', 'T'}
	pngTypeFDAT = [4]byte{'f', 'd', 'A', 'T'}
	pngTypeIEND = [4]byte{'I', 'E', 'N', 'D'}
)

// pngStream is `StreamingDecoder` (decoder/stream.rs) with its default options (the adler32 of the zlib stream and the checksums of
// ancillary chunks ignored, the other checksums verified) and the text chunks ignored.
type pngStream struct {
	state pngStateKind

	// State::U32.
	u32Kind  pngU32Kind
	u32Bytes [4]byte
	u32Count int
	u32Len   uint32  // Type { length }
	u32Chunk [4]byte // Crc(chunk type)
	// State::ReadChunkData, ParseChunkData and ImageData.
	stateChunk [4]byte

	// ChunkState.
	chunkType [4]byte
	crc       uint32
	remaining uint32
	raw       []byte
	rawCap    int // the capacity of the chunk buffer, which the limits are accounted by

	inflater pngZlib
	info     *pngInfo

	seqSet bool
	seq    uint32

	haveIDAT, readyForIDAT, readyForFDAT, haveICCP bool

	limits uint64 // `Limits::bytes`, in the usize of wasm32
}

func newPNGStream(limitBytes uint64) *pngStream {
	s := &pngStream{state: pngStateU32, u32Kind: pngSignature1st, raw: make([]byte, 0, pngChunkBufferSize), rawCap: pngChunkBufferSize, readyForIDAT: true, limits: min(limitBytes, pngUsizeMax)}
	s.inflater.init()
	return s
}

func (s *pngStream) setU32(kind pngU32Kind) {
	s.state, s.u32Kind, s.u32Bytes, s.u32Count = pngStateU32, kind, [4]byte{}, 0
}

func (s *pngStream) reserveBytes(n uint64) error {
	if s.limits >= n {
		s.limits -= n
		return nil
	}
	return errPNGLimits
}

// update is `StreamingDecoder::update` (stream.rs:610-628).
func (s *pngStream) update(buf []byte, imageData *[]byte) (int, pngDecoded, error) {
	total := len(buf)
	for len(buf) > 0 && s.state != pngStateNone {
		n, res, err := s.nextState(buf, imageData)
		if err != nil {
			return 0, pngDecoded{}, err
		}
		buf = buf[n:]
		if res.kind != pngNothing {
			return total - len(buf), res, nil
		}
	}
	return total - len(buf), pngDecoded{}, nil
}

// nextState is `StreamingDecoder::next_state` (stream.rs:630-745).
func (s *pngStream) nextState(buf []byte, imageData *[]byte) (int, pngDecoded, error) {
	state := s.state
	s.state = pngStateNone
	switch state {
	case pngStateU32:
		avail := min(4-s.u32Count, len(buf))
		copy(s.u32Bytes[s.u32Count:], buf[:avail])
		s.u32Count += avail
		if s.u32Count < 4 {
			s.state = pngStateU32
			return avail, pngDecoded{}, nil
		}
		res, err := s.parseU32(s.u32Kind, s.u32Bytes, imageData)
		return avail, res, err
	case pngStateParseChunkData:
		if s.remaining == 0 {
			res, err := s.parseChunk(s.stateChunk)
			return 0, res, err
		}
		if err := s.reserveCurrentChunk(); err != nil {
			return 0, pngDecoded{}, err
		}
		s.state = pngStateReadChunkData
		return 0, pngDecoded{kind: pngPartialChunk}, nil
	case pngStateReadChunkData:
		if s.remaining == 0 {
			s.u32Chunk = s.stateChunk
			s.setU32(pngCRC)
			return 0, pngDecoded{}, nil
		}
		bufAvail := s.rawCap - len(s.raw)
		bytesAvail := min(len(buf), bufAvail)
		n := min(int(s.remaining), bytesAvail)
		if bufAvail == 0 {
			s.state = pngStateParseChunkData
			return 0, pngDecoded{}, nil
		}
		s.crc = crc32.Update(s.crc, crc32.IEEETable, buf[:n])
		s.raw = append(s.raw, buf[:n]...)
		s.remaining -= uint32(n)
		if s.remaining == 0 {
			s.state = pngStateParseChunkData
		} else {
			s.state = pngStateReadChunkData
		}
		return n, pngDecoded{}, nil
	case pngStateImageData:
		n := min(len(buf), int(s.remaining))
		part := buf[:n]
		consumed, err := s.inflater.decompress(part, imageData)
		if err != nil {
			return 0, pngDecoded{}, err
		}
		s.crc = crc32.Update(s.crc, crc32.IEEETable, part[:consumed])
		s.remaining -= uint32(consumed)
		if s.remaining == 0 {
			s.u32Chunk = s.stateChunk
			s.setU32(pngCRC)
		} else {
			s.state = pngStateImageData
		}
		return consumed, pngDecoded{kind: pngImageData}, nil
	}
	return 0, pngDecoded{}, errors.New("photonimage: png: no state")
}

var errPNGFormat = errors.New("photonimage: png: invalid PNG")

func pngFormatError(format string, args ...any) error {
	return fmt.Errorf("photonimage: png: "+format, args...)
}

// parseU32 is `StreamingDecoder::parse_u32` (stream.rs:760-905).
func (s *pngStream) parseU32(kind pngU32Kind, b [4]byte, imageData *[]byte) (pngDecoded, error) {
	val := binary.BigEndian.Uint32(b[:])
	switch kind {
	case pngSignature1st:
		if b != [4]byte{137, 80, 78, 71} {
			return pngDecoded{}, pngFormatError("invalid signature")
		}
		s.setU32(pngSignature2nd)
		return pngDecoded{}, nil
	case pngSignature2nd:
		if b != [4]byte{13, 10, 26, 10} {
			return pngDecoded{}, pngFormatError("invalid signature")
		}
		s.setU32(pngLength)
		return pngDecoded{}, nil
	case pngLength:
		s.setU32(pngType)
		s.u32Len = val
		return pngDecoded{}, nil
	case pngType:
		length := s.u32Len
		if s.info == nil && b != pngTypeIHDR {
			return pngDecoded{}, pngFormatError("chunk %q before IHDR", b[:])
		}
		if b != s.chunkType && (s.chunkType == pngTypeIDAT || s.chunkType == pngTypeFDAT) {
			s.chunkType = b
			if err := s.inflater.finishCompressedChunks(imageData); err != nil {
				return pngDecoded{}, err
			}
			s.inflater.reset()
			s.readyForIDAT, s.readyForFDAT = false, false
			s.state, s.u32Kind, s.u32Bytes, s.u32Count, s.u32Len = pngStateU32, pngType, b, 4, length
			return pngDecoded{kind: pngImageDataFlushed}, nil
		}
		switch b {
		case pngTypeFDAT:
			if !s.readyForFDAT {
				return pngDecoded{}, pngFormatError("unexpected restart of fdAT chunk sequence")
			}
			if length < 4 {
				return pngDecoded{}, pngFormatError("fdAT chunk shorter than 4 bytes")
			}
			s.setU32(pngAPNGSequenceNumber)
		case pngTypeIDAT:
			if !s.readyForIDAT {
				return pngDecoded{}, pngFormatError("unexpected restart of IDAT chunk sequence")
			}
			s.haveIDAT = true
			s.state, s.stateChunk = pngStateImageData, b
		default:
			s.state, s.stateChunk = pngStateReadChunkData, b
		}
		s.chunkType = b
		s.crc = crc32.Update(0, crc32.IEEETable, b[:])
		s.remaining = length
		s.raw = s.raw[:0]
		return pngDecoded{kind: pngChunkBegin, chunk: b}, nil
	case pngCRC:
		sum := s.crc
		if val == sum {
			s.setU32(pngLength)
			if s.u32Chunk == pngTypeIEND {
				return pngDecoded{kind: pngImageEnd}, nil
			}
			return pngDecoded{kind: pngChunkComplete}, nil
		}
		if s.u32Chunk[0]&32 != 0 {
			// An ancillary chunk with an invalid checksum is skipped.
			s.setU32(pngLength)
			return pngDecoded{}, nil
		}
		return pngDecoded{}, pngFormatError("CRC error: expected 0x%x have 0x%x while decoding %q chunk", val, sum, s.u32Chunk[:])
	case pngAPNGSequenceNumber:
		s.remaining -= 4
		if !s.seqSet {
			return pngDecoded{}, pngFormatError("fcTL chunk missing before fdAT chunk")
		}
		if val != s.seq+1 {
			return pngDecoded{}, pngFormatError("sequence is not in order, expected #%d got #%d", s.seq+1, val)
		}
		s.seq = val
		var seq [4]byte
		binary.BigEndian.PutUint32(seq[:], val)
		s.crc = crc32.Update(s.crc, crc32.IEEETable, seq[:])
		s.state, s.stateChunk = pngStateImageData, pngTypeFDAT
		return pngDecoded{kind: pngPartialChunk, chunk: pngTypeFDAT}, nil
	}
	return pngDecoded{}, errPNGFormat
}

// reserveCurrentChunk is `reserve_current_chunk` (stream.rs:907-921): the chunk buffer grows by what is allowed, up to its length.
func (s *pngStream) reserveCurrentChunk() error {
	max := s.limits
	reserveSize := min(satSub(max, uint64(s.rawCap)), uint64(len(s.raw)))
	if err := s.reserveBytes(reserveSize); err != nil {
		return err
	}
	if uint64(s.rawCap-len(s.raw)) < reserveSize {
		bigger := make([]byte, len(s.raw), len(s.raw)+int(reserveSize))
		copy(bigger, s.raw)
		s.raw = bigger
		s.rawCap = len(s.raw) + int(reserveSize)
	}
	if s.rawCap == len(s.raw) {
		return errPNGLimits
	}
	return nil
}

func satSub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

// parseChunk is `StreamingDecoder::parse_chunk` (stream.rs:923-960). The text chunks are ignored.
func (s *pngStream) parseChunk(kind [4]byte) (pngDecoded, error) {
	s.setU32(pngCRC)
	s.u32Chunk = kind
	var res pngDecoded
	var err error
	switch kind {
	case pngTypeIHDR:
		res, err = s.parseIHDR()
	case pngTypePLTE:
		err = s.parsePLTE()
	case pngTypeTRNS:
		err = s.parseTRNS()
	case pngTypePHYS:
		err = s.parsePHYS()
	case pngTypeGAMA:
		err = s.parseGAMA()
	case pngTypeACTL:
		if len(s.raw) < 8 {
			err = pngFormatError("chunk is too short: acTL")
		}
	case pngTypeFCTL:
		res, err = s.parseFCTL()
	case pngTypeCHRM:
		err = s.parseCHRM()
	case pngTypeSRGB:
		err = s.parseSRGB()
	case pngTypeICCP:
		s.parseICCP()
	}
	if err != nil {
		s.state = pngStateNone
		return pngDecoded{}, err
	}
	if res.kind == pngNothing {
		res.kind = pngPartialChunk
		switch kind {
		case pngTypePLTE, pngTypeTRNS, pngTypeGAMA, pngTypeCHRM, pngTypeSRGB, pngTypeICCP:
			res.kind = pngNothing
		}
	}
	return res, nil
}

func pngChunkTooShort(kind string) error { return pngFormatError("chunk is too short: %s", kind) }

func (s *pngStream) parseIHDR() (pngDecoded, error) {
	if s.info != nil {
		return pngDecoded{}, pngFormatError("chunk IHDR must appear at most once")
	}
	c := s.raw
	if len(c) < 13 {
		return pngDecoded{}, pngChunkTooShort("IHDR")
	}
	width, height := binary.BigEndian.Uint32(c), binary.BigEndian.Uint32(c[4:])
	if width == 0 || height == 0 {
		return pngDecoded{}, pngFormatError("invalid image dimensions")
	}
	depth, colorType := c[8], c[9]
	switch depth {
	case 1, 2, 4, 8, 16:
	default:
		return pngDecoded{}, pngFormatError("invalid bit depth %d", depth)
	}
	switch colorType {
	case pngGray, pngRGB, pngIndexed, pngGrayAlpha, pngRGBA:
	default:
		return pngDecoded{}, pngFormatError("invalid color type %d", colorType)
	}
	// is_combination_invalid (common.rs:71-79).
	if depth < 8 && (colorType == pngRGB || colorType == pngGrayAlpha || colorType == pngRGBA) || depth == 16 && colorType == pngIndexed {
		return pngDecoded{}, pngFormatError("invalid color/depth combination in header")
	}
	if c[10] != 0 {
		return pngDecoded{}, pngFormatError("unknown compression method %d", c[10])
	}
	if c[11] != 0 {
		return pngDecoded{}, pngFormatError("unknown filter method %d", c[11])
	}
	if c[12] > 1 {
		return pngDecoded{}, pngFormatError("unknown interlace method %d", c[12])
	}
	interlaced := c[12] == 1
	if rawRowLen, ok := pngCheckedRawRowLength(colorType, depth, width); ok {
		if interlaced {
			// This overshoots, but overestimating should be fine.
			rawRowLen = min(rawRowLen*2, pngUsizeMax)
		}
		s.inflater.maxTotalOutput = min(uint64(height)*rawRowLen, pngUsizeMax)
	}
	s.info = &pngInfo{width: width, height: height, depth: depth, colorType: colorType, interlaced: interlaced}
	return pngDecoded{kind: pngHeader}, nil
}

func (s *pngStream) parsePLTE() error {
	if s.info.hasPalette {
		return pngFormatError("chunk PLTE must appear at most once")
	}
	if err := s.reserveBytes(uint64(len(s.raw))); err != nil {
		return err
	}
	s.info.palette, s.info.hasPalette = bytes.Clone(s.raw), true
	return nil
}

// parseTRNS is `parse_trns` (stream.rs:1077-1138).
func (s *pngStream) parseTRNS() error {
	info := s.info
	if info.hasTRNS {
		return pngFormatError("chunk PLTE must appear at most once")
	}
	if err := s.reserveBytes(uint64(len(s.raw))); err != nil {
		return err
	}
	v := bytes.Clone(s.raw)
	switch info.colorType {
	case pngGray:
		if len(v) < 2 {
			return pngFormatError("not enough palette entries, expect 2 got %d", len(v))
		}
		if info.depth < 16 {
			v = v[1:2]
		}
	case pngRGB:
		if len(v) < 6 {
			return pngFormatError("not enough palette entries, expect 6 got %d", len(v))
		}
		if info.depth < 16 {
			v = []byte{v[1], v[3], v[5]}
		}
	case pngIndexed:
		if !info.hasPalette {
			return pngFormatError("chunk tRNS is invalid after PLTE chunk")
		} else if s.haveIDAT {
			return pngFormatError("chunk tRNS must appear between PLTE and IDAT chunks")
		}
	default:
		return pngFormatError("transparency chunk found for color type %d", info.colorType)
	}
	info.trns, info.hasTRNS = v, true
	return nil
}

func (s *pngStream) parsePHYS() error {
	info := s.info
	if s.haveIDAT {
		return pngFormatError("chunk pHYs is invalid after IDAT chunk")
	} else if info.phys {
		return pngFormatError("chunk pHYs must appear at most once")
	}
	if len(s.raw) < 9 {
		return pngChunkTooShort("pHYs")
	}
	if s.raw[8] > 1 {
		return pngFormatError("invalid physical pixel size unit %d", s.raw[8])
	}
	info.phys = true
	return nil
}

func (s *pngStream) parseCHRM() error {
	if s.haveIDAT {
		return pngFormatError("chunk cHRM is invalid after IDAT chunk")
	} else if s.info.chrm {
		return pngFormatError("chunk cHRM must appear at most once")
	}
	if len(s.raw) < 32 {
		return pngChunkTooShort("cHRM")
	}
	s.info.chrm = true
	return nil
}

func (s *pngStream) parseGAMA() error {
	if s.haveIDAT {
		return pngFormatError("chunk gAMA is invalid after IDAT chunk")
	} else if s.info.gama {
		return pngFormatError("chunk gAMA must appear at most once")
	}
	if len(s.raw) < 4 {
		return pngChunkTooShort("gAMA")
	}
	s.info.gama = true
	return nil
}

func (s *pngStream) parseSRGB() error {
	if s.haveIDAT {
		return pngFormatError("chunk acTL is invalid after IDAT chunk")
	} else if s.info.srgb {
		return pngFormatError("chunk sRGB must appear at most once")
	}
	if len(s.raw) < 1 {
		return pngChunkTooShort("sRGB")
	}
	if s.raw[0] > 3 {
		return pngFormatError("invalid sRGB rendering intent %d", s.raw[0])
	}
	s.info.srgb = true
	return nil
}

// parseICCP is `parse_iccp` (stream.rs:1306-1334): the errors of the profile are ignored, but a profile that is read takes its size from the
// limits.
func (s *pngStream) parseICCP() {
	if s.haveIDAT || s.haveICCP {
		// Before the image data only: after it the crate returns an error that no parse of this decoder reaches.
		return
	}
	s.haveICCP = true
	buf := s.raw
	// The profile name: one byte and up to 79 more until a zero byte.
	if len(buf) < 1 {
		return
	}
	buf = buf[1:]
	for i := 1; i < 80; i++ {
		if len(buf) < 1 {
			return
		}
		raw := buf[0]
		buf = buf[1:]
		if raw == 0 {
			break
		}
	}
	if len(buf) < 1 || buf[0] != 0 {
		return
	}
	buf = buf[1:]
	profile, tooLarge, err := fdDecompressBounded(buf, int(min(s.limits, pngUsizeMax)))
	if err != nil || tooLarge {
		return
	}
	_ = s.reserveBytes(uint64(len(profile)))
}

// parseFCTL is `parse_fctl` (stream.rs:980-1044) and `Info::validate` (:1529-1550).
func (s *pngStream) parseFCTL() (pngDecoded, error) {
	c := s.raw
	if len(c) < 4 {
		return pngDecoded{}, pngChunkTooShort("fcTL")
	}
	seq := binary.BigEndian.Uint32(c)
	if s.seqSet {
		if seq != s.seq+1 {
			return pngDecoded{}, pngFormatError("sequence is not in order, expected #%d got #%d", s.seq+1, seq)
		}
	} else if seq != 0 {
		return pngDecoded{}, pngFormatError("sequence is not in order, expected #0 got #%d", seq)
	}
	s.seq, s.seqSet = seq, true
	s.inflater.reset()
	s.readyForFDAT = true
	if len(c) < 26 {
		return pngDecoded{}, pngChunkTooShort("fcTL")
	}
	width, height := binary.BigEndian.Uint32(c[4:]), binary.BigEndian.Uint32(c[8:])
	xOffset, yOffset := binary.BigEndian.Uint32(c[12:]), binary.BigEndian.Uint32(c[16:])
	dispose, blend := c[24], c[25]
	if dispose > 2 {
		return pngDecoded{}, pngFormatError("invalid dispose op %d", dispose)
	}
	if blend > 1 {
		return pngDecoded{}, pngFormatError("invalid blend op %d", blend)
	}
	if width == 0 || height == 0 {
		return pngDecoded{}, pngFormatError("invalid image dimensions")
	}
	info := s.info
	if xOffset > info.width || width > info.width-xOffset || yOffset > info.height || height > info.height-yOffset {
		return pngDecoded{}, pngFormatError("sub frame is out-of-bounds")
	}
	info.frame, info.hasFrame = pngFrameControl{width, height}, true
	return pngDecoded{kind: pngFrameControlEvent}, nil
}

// pngZlib is `ZlibStream` (decoder/zlib.rs).
type pngZlib struct {
	state           *fdDecompressor
	started         bool
	outBuffer       []byte
	outPos, readPos int
	maxTotalOutput  uint64
}

func (z *pngZlib) init() {
	z.state = newFdDecompressor()
	z.maxTotalOutput = pngUsizeMax
}

func (z *pngZlib) reset() {
	z.started = false
	z.outBuffer = z.outBuffer[:0]
	z.outPos, z.readPos = 0, 0
	z.maxTotalOutput = pngUsizeMax
	z.state = newFdDecompressor()
}

// decompress is `ZlibStream::decompress` (zlib.rs:80-113): it returns the number of bytes consumed.
func (z *pngZlib) decompress(data []byte, imageData *[]byte) (int, error) {
	// Data after the end of the stream is ignored, as libpng does.
	if z.state.done() {
		return len(data), nil
	}
	z.prepareVecForAppending()
	if !z.started {
		z.state.ignoreAdler32()
	}
	inConsumed, outConsumed, err := z.state.read(data, z.outBuffer, z.outPos, false)
	if err != nil {
		return 0, fmt.Errorf("photonimage: png: corrupt deflate stream: %w", err)
	}
	z.started = true
	z.outPos += outConsumed
	z.transferFinishedData(imageData)
	z.compactOutBufferIfNeeded()
	return inConsumed, nil
}

// finishCompressedChunks is `ZlibStream::finish_compressed_chunks` (zlib.rs:120-152), called when the chunks of the image data end.
func (z *pngZlib) finishCompressedChunks(imageData *[]byte) error {
	if !z.started {
		return nil
	}
	for !z.state.done() {
		z.prepareVecForAppending()
		_, outConsumed, err := z.state.read(nil, z.outBuffer, z.outPos, true)
		if err != nil {
			return fmt.Errorf("photonimage: png: corrupt deflate stream: %w", err)
		}
		z.outPos += outConsumed
		if !z.state.done() {
			transferred := z.transferFinishedData(imageData)
			if transferred == 0 && outConsumed == 0 {
				return errors.New("photonimage: png: no more forward progress made in stream decoding")
			}
			z.compactOutBufferIfNeeded()
		}
	}
	z.transferFinishedData(imageData)
	z.outBuffer = z.outBuffer[:0]
	return nil
}

func (z *pngZlib) prepareVecForAppending() {
	if z.outPos >= 0 && uint64(z.outPos) >= z.maxTotalOutput {
		z.maxTotalOutput = pngUsizeMax
	}
	currentLen := len(z.outBuffer)
	desiredLen := min(uint64(z.outPos)+pngChunkBufferSize, pngUsizeMax, z.maxTotalOutput)
	if uint64(currentLen) >= desiredLen {
		return
	}
	// decoding_size: one more chunk or double, capped by the address space and by max_total_output.
	bufferedLen := min(uint64(currentLen)+uint64(max(pngChunkBufferSize, currentLen)), pngUsizeMax, math.MaxInt32, z.maxTotalOutput)
	z.outBuffer = append(z.outBuffer, make([]byte, int(bufferedLen)-currentLen)...)
}

func (z *pngZlib) transferFinishedData(imageData *[]byte) int {
	transferred := z.outBuffer[z.readPos:z.outPos]
	*imageData = append(*imageData, transferred...)
	z.readPos = z.outPos
	return len(transferred)
}

// compactOutBufferIfNeeded keeps the 32 KiB of lookback that fdeflate needs once the buffer holds more than four times as much.
func (z *pngZlib) compactOutBufferIfNeeded() {
	const lookback = 32768
	if z.outPos > lookback*4 {
		copy(z.outBuffer, z.outBuffer[z.outPos-lookback:z.outPos])
		z.readPos, z.outPos = lookback, lookback
	}
}

// pngInterlace is `InterlaceInfo`: the line of the image (or of the pass) a row belongs to, and for Adam7 the pass and its width.
type pngInterlace struct {
	adam7 bool
	pass  uint8
	line  uint32
	width uint32
}

// pngInterlaceIter is `InterlaceInfoIter` over the rows of a (sub)frame: its height lines, or the rows of the seven passes.
type pngInterlaceIter struct {
	adam7  bool
	line   uint32 // Null: the next line of height
	height uint32
	// Adam7Iterator (adam7.rs:36-100).
	lines, lineWidth uint32
	currentPass      uint8
	width            uint32
}

func newPNGInterlaceIter(width, height uint32, interlaced bool) *pngInterlaceIter {
	if !interlaced {
		return &pngInterlaceIter{height: height}
	}
	it := &pngInterlaceIter{adam7: true, currentPass: 1, width: width, height: height}
	it.initPass()
	return it
}

func pngCeilToU32(v float64) uint32 {
	v = math.Ceil(v)
	if !(v > 0) {
		return 0
	}
	return uint32(min(v, math.MaxUint32))
}

func (it *pngInterlaceIter) initPass() {
	w, h := float64(it.width), float64(it.height)
	var lineWidth, lines float64
	switch it.currentPass {
	case 1:
		lineWidth, lines = w/8, h/8
	case 2:
		lineWidth, lines = (w-4)/8, h/8
	case 3:
		lineWidth, lines = w/4, (h-4)/8
	case 4:
		lineWidth, lines = (w-2)/4, h/4
	case 5:
		lineWidth, lines = w/2, (h-2)/4
	case 6:
		lineWidth, lines = (w-1)/2, h/2
	default:
		lineWidth, lines = w, (h-1)/2
	}
	it.lineWidth, it.lines, it.line = pngCeilToU32(lineWidth), pngCeilToU32(lines), 0
}

func (it *pngInterlaceIter) next() (pngInterlace, bool) {
	if !it.adam7 {
		if it.line >= it.height {
			return pngInterlace{}, false
		}
		it.line++
		return pngInterlace{line: it.line - 1}, true
	}
	for {
		if it.line < it.lines && it.lineWidth > 0 {
			it.line++
			return pngInterlace{adam7: true, pass: it.currentPass, line: it.line - 1, width: it.lineWidth}, true
		}
		if it.currentPass >= 7 {
			return pngInterlace{}, false
		}
		it.currentPass++
		it.initPass()
	}
}

// pngReader is `Reader` (decoder/mod.rs) over a `Decoder` with the EXPAND transformation.
type pngReader struct {
	stream *pngStream
	in     pngInput
	atEOF  bool
	info   *pngInfo

	bpp int // `bpp_in_prediction`

	// SubframeInfo.
	subWidth, subHeight uint32
	subRowLen           int
	current             *pngInterlace
	iter                *pngInterlaceIter
	consumedAndFlushed  bool

	dataStream              []byte
	prevStart, currentStart int
	transform               func(row, out []byte)
	scratch                 []byte
	zeroRow                 []byte
}

// decodeNext is `ReadDecoder::decode_next` (decoder/mod.rs:299-316); ok is false at the end of the image.
func (r *pngReader) decodeNext(imageData *[]byte) (res pngDecoded, ok bool, err error) {
	for !r.atEOF {
		buf := r.in.fillBuf()
		if len(buf) == 0 {
			return pngDecoded{}, false, errors.New("photonimage: png: unexpected end of file")
		}
		consumed, res, err := r.stream.update(buf, imageData)
		if err != nil {
			return pngDecoded{}, false, err
		}
		r.in.consume(consumed)
		switch res.kind {
		case pngNothing:
		case pngImageEnd:
			r.atEOF = true
		default:
			return res, true, nil
		}
	}
	return pngDecoded{}, false, nil
}

// finishDecoding is `ReadDecoder::finish_decoding` (decoder/mod.rs:318-338).
func (r *pngReader) finishDecoding() error {
	for !r.atEOF {
		buf := r.in.fillBuf()
		if len(buf) == 0 {
			return errors.New("photonimage: png: unexpected end of file")
		}
		var scratch []byte
		consumed, res, err := r.stream.update(buf, &scratch)
		if err != nil {
			return err
		}
		r.in.consume(consumed)
		switch res.kind {
		case pngImageEnd:
			r.atEOF = true
		case pngImageDataFlushed:
			return nil
		}
	}
	return errors.New("photonimage: png: unexpected end of file")
}

// pngOpen is `Decoder::new_with_limits`, `read_header_info`, `set_transformations(EXPAND)` and `read_info` (decoder/mod.rs:129-238, 405-453):
// the chunks up to the first image data, with the crate's limit of limitBytes.
func pngOpen(data []byte, limitBytes uint64) (*pngReader, error) {
	r := &pngReader{stream: newPNGStream(limitBytes), in: pngInput{data: data}}
	var scratch []byte
	for r.stream.info == nil {
		_, ok, err := r.decodeNext(&scratch)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("photonimage: png: unexpected end of file")
		}
	}
	r.info = r.stream.info

	// The size of a raw line and of the output buffer, before the chunks that change the output (the transparency) are read.
	if _, ok := pngCheckedRawRowLength(r.info.colorType, r.info.depth, r.info.width); !ok {
		return nil, errPNGLimits
	}
	color, depth := r.info.outputColorType()
	rowLen, ok := pngCheckedRawRowLength(color, depth, r.info.width)
	if !ok {
		return nil, errPNGLimits
	}
	if (rowLen-1)*uint64(r.info.height) > pngUsizeMax {
		return nil, errPNGLimits
	}

	if err := r.readUntilImageData(); err != nil {
		return nil, err
	}
	return r, nil
}

// outputColorType is the colour type and bit depth of the rows `Reader::output_color_type` gives.
func (r *pngReader) outputColorType() (colorType, depth byte) { return r.info.outputColorType() }

func (r *pngReader) outputLineSize(width uint32) int {
	color, depth := r.info.outputColorType()
	return pngRawRowLengthFromWidth(color, depth, width) - 1
}

// readUntilImageData is `Reader::read_until_image_data` (decoder/mod.rs:405-453).
func (r *pngReader) readUntilImageData() error {
	for {
		var buf []byte
		res, ok, err := r.decodeNext(&buf)
		if err != nil {
			return err
		}
		if !ok {
			return pngFormatError("IDAT or fdAT chunk is missing")
		}
		if res.kind == pngChunkBegin && (res.chunk == pngTypeIDAT || res.chunk == pngTypeFDAT) {
			break
		}
	}
	info := r.info
	switch info.colorType {
	case pngGray, pngIndexed:
		r.bpp = 1 * int((info.depth+7)>>3)
	case pngRGB:
		r.bpp = 3 * int((info.depth+7)>>3)
	case pngGrayAlpha:
		r.bpp = 2 * int((info.depth+7)>>3)
	default:
		r.bpp = 4 * int((info.depth+7)>>3)
	}

	r.subWidth, r.subHeight = info.width, info.height
	if info.hasFrame {
		r.subWidth, r.subHeight = info.frame.width, info.frame.height
	}
	r.iter = newPNGInterlaceIter(r.subWidth, r.subHeight, info.interlaced)
	r.setCurrent(r.iter.next())
	r.subRowLen = info.rawRowLengthFromWidth(r.subWidth)
	r.consumedAndFlushed = false
	r.dataStream = r.dataStream[:0]
	r.currentStart, r.prevStart = 0, 0

	if err := r.stream.reserveBytes(uint64(r.outputLineSize(r.subWidth))); err != nil {
		return err
	}
	r.prevStart = r.currentStart
	return nil
}

func (r *pngReader) setCurrent(i pngInterlace, ok bool) {
	if !ok {
		r.current = nil
		return
	}
	r.current = &i
}

// nextFrame is `Reader::next_frame` (decoder/mod.rs:475-559) for the first (default) image: the rows of the subframe the image data
// holds, into buf of width * height * bytes per output pixel bytes (rows of the subframe's width, one after the other, where the subframe
// is smaller than the image).
func (r *pngReader) nextFrame(buf []byte) error {
	info := r.info
	lineSize := r.outputLineSize(r.subWidth)
	if len(buf) < r.outputLineSize(info.width)*int(info.height) {
		return errors.New("photonimage: png: the buffer is too small")
	}
	color, depth := info.outputColorType()
	if info.interlaced {
		stride := r.outputLineSize(info.width)
		bitsPP := int(pngSamples(color)) * int(depth)
		for {
			row, interlace, ok, err := r.nextInterlacedRow()
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			pngExpandPass(buf, stride, row, interlace, bitsPP)
		}
	} else {
		already := r.subHeight
		if r.current != nil {
			already = r.current.line
		}
		for i := int(already); i < int(r.subHeight) && (i+1)*lineSize <= len(buf); i++ {
			if err := r.nextInterlacedRowImpl(r.subRowLen, buf[i*lineSize:(i+1)*lineSize]); err != nil {
				return err
			}
		}
	}
	if !r.consumedAndFlushed {
		if err := r.finishDecoding(); err != nil {
			return err
		}
		r.consumedAndFlushed = true
	}
	return nil
}

// nextInterlacedRow is `Reader::next_interlaced_row` (decoder/mod.rs:568-600).
func (r *pngReader) nextInterlacedRow() ([]byte, pngInterlace, bool, error) {
	if r.current == nil {
		return nil, pngInterlace{}, false, nil
	}
	interlace := *r.current
	if interlace.line == 0 {
		r.prevStart = r.currentStart
	}
	rowLen, width := r.subRowLen, r.subWidth
	if interlace.adam7 {
		rowLen, width = r.info.rawRowLengthFromWidth(interlace.width), interlace.width
	}
	outputLineSize := r.outputLineSize(width)
	if cap(r.scratch) < outputLineSize {
		r.scratch = make([]byte, outputLineSize)
	}
	r.scratch = r.scratch[:outputLineSize]
	if err := r.nextInterlacedRowImpl(rowLen, r.scratch); err != nil {
		return nil, pngInterlace{}, false, err
	}
	return r.scratch, interlace, true, nil
}

// nextInterlacedRowImpl is `Reader::next_interlaced_row_impl` (decoder/mod.rs:622-642).
func (r *pngReader) nextInterlacedRowImpl(rowLen int, out []byte) error {
	if err := r.nextRawInterlacedRow(rowLen); err != nil {
		return err
	}
	row := r.dataStream[r.prevStart:r.currentStart]
	if r.transform == nil {
		t, err := pngCreateTransform(r.info)
		if err != nil {
			return err
		}
		r.transform = t
	}
	r.transform(row, out)
	r.setCurrent(r.iter.next())
	return nil
}

// nextRawInterlacedRow is `Reader::next_raw_interlaced_row` (decoder/mod.rs:697-740): the next unfiltered row of the zlib stream.
func (r *pngReader) nextRawInterlacedRow(rowLen int) error {
	for len(r.dataStream)-r.currentStart < rowLen {
		if r.consumedAndFlushed {
			return pngFormatError("IDAT or fDAT chunk does not have enough data for image")
		}
		if r.prevStart > 0 {
			copy(r.dataStream, r.dataStream[r.prevStart:])
			r.dataStream = r.dataStream[:len(r.dataStream)-r.prevStart]
			r.currentStart -= r.prevStart
			r.prevStart = 0
		}
		res, ok, err := r.decodeNext(&r.dataStream)
		if err != nil {
			return err
		}
		if ok && res.kind == pngImageDataFlushed {
			r.consumedAndFlushed = true
		}
	}

	row := r.dataStream[r.currentStart:]
	filter := row[0]
	if filter > 4 {
		return pngFormatError("unknown filter method %d", filter)
	}
	prev := r.dataStream[r.prevStart:r.currentStart]
	if len(prev) == 0 {
		// The first row of the image or of a pass: the row above is zeros.
		if cap(r.zeroRow) < rowLen-1 {
			r.zeroRow = make([]byte, rowLen-1)
		}
		prev = r.zeroRow[:rowLen-1]
		clear(prev)
	}
	pngUnfilter(filter, r.bpp, prev, row[1:rowLen])
	r.prevStart = r.currentStart + 1
	r.currentStart += rowLen
	return nil
}

// pngExpandPass is `adam7::expand_pass` (adam7.rs:112-139) for pixels of at least a byte.
func pngExpandPass(img []byte, imgRowStride int, row []byte, info pngInterlace, bitsPerPixel int) {
	var lineMul, lineOff, sampMul, sampOff int
	switch info.pass {
	case 1:
		lineMul, lineOff, sampMul, sampOff = 8, 0, 8, 0
	case 2:
		lineMul, lineOff, sampMul, sampOff = 8, 0, 8, 4
	case 3:
		lineMul, lineOff, sampMul, sampOff = 8, 4, 4, 0
	case 4:
		lineMul, lineOff, sampMul, sampOff = 4, 0, 4, 2
	case 5:
		lineMul, lineOff, sampMul, sampOff = 4, 2, 2, 0
	case 6:
		lineMul, lineOff, sampMul, sampOff = 2, 0, 2, 1
	default:
		lineMul, lineOff, sampMul, sampOff = 2, 1, 1, 0
	}
	bytesPP := bitsPerPixel / 8
	progLine := lineMul*int(info.line) + lineOff
	lineStart := progLine * imgRowStride
	for i := range int(info.width) {
		pos := lineStart + (i*sampMul+sampOff)*bytesPP
		copy(img[pos:pos+bytesPP], row[i*bytesPP:(i+1)*bytesPP])
	}
}

// pngCreateTransform is `create_transform_fn` (decoder/transform.rs:20-68) with EXPAND.
func pngCreateTransform(info *pngInfo) (func(row, out []byte), error) {
	depth := info.depth
	trns := info.hasTRNS
	switch {
	case info.colorType == pngIndexed:
		if !info.hasPalette {
			return nil, pngFormatError("missing palette of indexed image")
		}
		palette, err := pngRGBAPalette(info)
		if err != nil {
			return nil, err
		}
		channels := 3
		if trns {
			channels = 4
		}
		return func(row, out []byte) {
			if depth == 8 {
				for x := 0; x*channels < len(out); x++ {
					copy(out[x*channels:(x+1)*channels], palette[row[x]][:channels])
				}
				return
			}
			pngUnpackBits(row, out, channels, depth, func(v byte, px []byte) { copy(px, palette[v][:channels]) })
		}, nil
	case (info.colorType == pngGray || info.colorType == pngGrayAlpha) && depth < 8:
		scale := byte(255 / (1<<depth - 1))
		if trns {
			t := info.trns[0]
			return func(row, out []byte) {
				pngUnpackBits(row, out, 2, depth, func(v byte, px []byte) {
					px[1] = 0xFF
					if v == t {
						px[1] = 0
					}
					px[0] = v * scale
				})
			}, nil
		}
		return func(row, out []byte) {
			pngUnpackBits(row, out, 1, depth, func(v byte, px []byte) { px[0] = v * scale })
		}, nil
	case (info.colorType == pngGray || info.colorType == pngRGB) && trns:
		channels := int(pngSamples(info.colorType))
		if depth == 8 {
			return func(row, out []byte) {
				for x := 0; (x+1)*channels <= len(row) && (x+1)*(channels+1) <= len(out); x++ {
					in := row[x*channels : (x+1)*channels]
					px := out[x*(channels+1) : (x+1)*(channels+1)]
					copy(px, in)
					px[channels] = 0xFF
					if bytes.Equal(in, info.trns) {
						px[channels] = 0
					}
				}
			}, nil
		}
		return func(row, out []byte) {
			step := channels * 2
			for x := 0; (x+1)*step <= len(row) && (x+1)*(step+2) <= len(out); x++ {
				in := row[x*step : (x+1)*step]
				px := out[x*(step+2) : (x+1)*(step+2)]
				copy(px, in)
				px[step], px[step+1] = 0xFF, 0xFF
				if bytes.Equal(in, info.trns) {
					px[step], px[step+1] = 0, 0
				}
			}
		}, nil
	}
	return func(row, out []byte) { copy(out, row) }, nil
}

// pngUnpackBits is `unpack_bits` (transform.rs:99-146): the samples of 1, 2, 4 or 8 bits of row, one pixel of channels bytes each.
func pngUnpackBits(row, out []byte, channels int, depth byte, f func(v byte, px []byte)) {
	if depth == 8 {
		for i := 0; i < len(row) && (i+1)*channels <= len(out); i++ {
			f(row[i], out[i*channels:(i+1)*channels])
		}
		return
	}
	mask := byte(1<<depth - 1)
	for x := 0; (x+1)*channels <= len(out); x++ {
		bit := x * int(depth)
		f(row[bit/8]>>(8-int(depth)-bit%8)&mask, out[x*channels:(x+1)*channels])
	}
}

// pngRGBAPalette is `create_rgba_palette` (decoder/transform/palette.rs:39-91): a palette that is not a whole number of entries or has more
// than 256 makes the crate panic; a tRNS with more entries than the palette is ignored, the entries of the palette without a tRNS value are
// opaque, and the entries after the palette are opaque black.
func pngRGBAPalette(info *pngInfo) ([256][4]byte, error) {
	var table [256][4]byte
	for i := range table {
		table[i] = [4]byte{0, 0, 0, 0xFF}
	}
	if len(info.palette)%3 != 0 || len(info.palette)/3 > 256 {
		return table, errors.New("photonimage: png: the palette does not fit the table (the png crate panics)")
	}
	entries := len(info.palette) / 3
	trns := info.trns
	if !info.hasTRNS || len(trns) > entries {
		trns = nil
	}
	for i := range entries {
		table[i] = [4]byte{info.palette[3*i], info.palette[3*i+1], info.palette[3*i+2], 0xFF}
		if i < len(trns) {
			table[i][3] = trns[i]
		}
	}
	return table, nil
}

// pngUnfilter reverses the PNG filter of a row in place; prev is the unfiltered row above (zeros for the first row of a pass).
func pngUnfilter(filter byte, bpp int, prev, row []byte) {
	switch filter {
	case 1: // Sub
		for i := bpp; i < len(row); i++ {
			row[i] += row[i-bpp]
		}
	case 2: // Up
		for i := range row {
			row[i] += prev[i]
		}
	case 3: // Average
		for i := range row {
			var left int
			if i >= bpp {
				left = int(row[i-bpp])
			}
			row[i] += byte((left + int(prev[i])) / 2)
		}
	case 4: // Paeth
		for i := range row {
			var a, c int
			if i >= bpp {
				a, c = int(row[i-bpp]), int(prev[i-bpp])
			}
			b := int(prev[i])
			p := a + b - c
			pa, pb, pc := abs(p-a), abs(p-b), abs(p-c)
			switch {
			case pa <= pb && pa <= pc:
				row[i] += byte(a)
			case pb <= pc:
				row[i] += byte(b)
			default:
				row[i] += byte(c)
			}
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
