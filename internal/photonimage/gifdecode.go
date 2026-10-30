// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-FileCopyrightText: 2015 nwin
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"errors"
	"image"
	"math"
)

// The GIF decoder Photon runs: image 0.24.9 `GifDecoder::new` (codecs/gif.rs) over the gif crate 0.13.1 (`ColorOutput::RGBA`, the default
// 50 MB memory limit and no check for the LZW end code), as `load_from_memory` drives it: the first frame only, placed on the logical
// screen (no disposal, no background colour, no composition with other frames) and converted with `to_rgba8`. The crate decodes the LZW
// stream with weezl 0.1.8 (`Decoder::new(Lsb, min_code_size)`), which gifLZW follows.
//
// The crate is a byte state machine over a `BufReader` of 8 KiB: what it does with a byte does not depend on how the bytes arrive,
// but for the LZW stream, whose decoder is called with the bytes of the current sub-block that are in the BufReader's window. That decides whether
// the code that follows the last pixel of the frame is read, which makes a bad one an error (gifStream.fill).

// gifSignatures are image 0.24.9's guess_format signatures for GIF (io/free_functions.rs:268-269).
var gifSignatures = [][]byte{[]byte("GIF89a"), []byte("GIF87a")}

// isGIF is image::guess_format's test for a GIF file.
func isGIF(data []byte) bool {
	for _, signature := range gifSignatures {
		if bytes.HasPrefix(data, signature) {
			return true
		}
	}
	return false
}

var (
	errGIF          = errors.New("photonimage: gif: invalid or truncated file")
	errGIFTruncated = errors.New("photonimage: gif: image truncated")
	errGIFLZW       = errors.New("photonimage: gif: invalid LZW code")
)

const (
	// gifBlockSize is the size of the buffer of the `BufReader` that feeds the decoder (std's default).
	gifBufReaderSize = 8192
	// gifMemoryLimit is `DecodeOptions::new()`'s memory limit, which bounds the data of an extension (decoder.rs:405).
	gifMemoryLimit = 50_000_000

	gifBlockExtension = 0x21
	gifBlockImage     = 0x2C
	gifBlockTrailer   = 0x3B

	gifExtText        = 0x01
	gifExtControl     = 0xF9
	gifExtComment     = 0xFE
	gifExtApplication = 0xFF
)

// gifFrame is the parts of `Frame` that decoding needs.
type gifFrame struct {
	left, top, width, height uint16
	interlaced               bool
	palette                  []byte // the local colour table
	hasPalette               bool
	transparent              int // the transparent index, or -1
}

// gifStream is the `StreamingDecoder` over the whole file.
type gifStream struct {
	data []byte
	pos  int

	width, height uint16
	global        []byte // the global colour table, nil when it is empty

	// current is the frame the control extensions and the image descriptor fill (decoder.rs `current`).
	current    *gifFrame
	extLen     int
	frame      gifFrame
	minCode    byte
	left       int // the bytes left of the current sub-block of image data
	lzw        *gifLZW
	pixelsDone bool
}

func (s *gifStream) byte() (byte, error) {
	if s.pos >= len(s.data) {
		return 0, errGIF // io::ErrorKind::UnexpectedEof
	}
	b := s.data[s.pos]
	s.pos++
	return b, nil
}

func (s *gifStream) u16() (uint16, error) {
	lo, err := s.byte()
	if err != nil {
		return 0, err
	}
	hi, err := s.byte()
	if err != nil {
		return 0, err
	}
	return uint16(hi)<<8 | uint16(lo), nil
}

func (s *gifStream) skip(n int) error {
	if len(s.data)-s.pos < n {
		s.pos = len(s.data)
		return errGIF
	}
	s.pos += n
	return nil
}

// addFrame is `add_frame`.
func (s *gifStream) addFrame() {
	if s.current == nil {
		s.current = &gifFrame{transparent: -1}
	}
}

// readInfo is `Decoder::init` (reader/mod.rs:280-311) over the header and the extensions before the first block that is not one: the logical
// screen descriptor, the global colour table and the extensions of the header, up to the block type byte of the first other block. That byte
// is returned; the state machine holds it as `BlockStart(type)`.
func (s *gifStream) readInfo() (byte, error) {
	// The magic: six bytes, then the width starts with the seventh (decoder.rs:545-557).
	magic, ok := s.take(6)
	if !ok {
		return 0, errGIF
	}
	if !bytes.HasPrefix(magic, []byte("GIF")) || (string(magic[3:]) != "87a" && string(magic[3:]) != "89a") {
		return 0, errors.New("photonimage: gif: malformed GIF header")
	}
	var err error
	if s.width, err = s.u16(); err != nil {
		return 0, err
	}
	if s.height, err = s.u16(); err != nil {
		return 0, err
	}
	flags, err := s.byte()
	if err != nil {
		return 0, err
	}
	if _, err = s.byte(); err != nil { // the background colour
		return 0, err
	}
	if _, err = s.byte(); err != nil { // the aspect ratio
		return 0, err
	}
	if flags&0x80 != 0 {
		size := 3 * (1 << ((flags & 0b111) + 1))
		table, ok := s.take(size)
		if !ok {
			return 0, errGIF
		}
		s.global = table
	}
	return s.blockType()
}

func (s *gifStream) take(n int) ([]byte, bool) {
	if len(s.data)-s.pos < n {
		s.pos = len(s.data)
		return nil, false
	}
	b := s.data[s.pos : s.pos+n]
	s.pos += n
	return b, true
}

// blockType reads the byte that starts a block. The state machine reads one byte more before it acts on it (BlockStart needs the next
// byte), so a trailer or a block type that is the last byte of the file is an error in any case.
func (s *gifStream) blockType() (byte, error) {
	t, err := s.byte()
	if err != nil {
		return 0, err
	}
	return t, nil
}

// nextFrame is `Decoder::next_frame_info` (reader/mod.rs:314-332) with the states it passes through (decoder.rs:522-821), from the block type
// byte of the first block that follows the header on: extensions are read until the first image descriptor, which gives the frame and
// leaves the stream at the first sub-block of its image data.
func (s *gifStream) nextFrame(blockType byte) error {
	for {
		switch blockType {
		case gifBlockTrailer:
			return errors.New("photonimage: gif: no more data")
		case gifBlockExtension:
			label, err := s.byte()
			if err != nil {
				return err
			}
			if err := s.extension(label); err != nil {
				return err
			}
			// BlockEnd: the next byte is the type of the next block.
			if blockType, err = s.blockType(); err != nil {
				return err
			}
			if blockType != gifBlockTrailer && s.pos >= len(s.data) {
				return errGIF
			}
		case gifBlockImage:
			return s.imageDescriptor()
		default:
			return errors.New("photonimage: gif: unknown block type encountered")
		}
	}
}

// extension is the extension block states (decoder.rs:691-738, :581-605). A sub-block length of zero right after the label is read as the
// length, not as the end: the terminator is the next zero byte (ExtensionDataBlock(0) wants one more byte), which this crate version does.
func (s *gifStream) extension(label byte) error {
	first, err := s.byte()
	if err != nil {
		return err
	}
	s.extLen = 1
	switch label {
	case gifExtControl:
		// read_control_extension: add_frame, and the length has to be four.
		s.addFrame()
		s.extLen++
		if first != 4 {
			return errors.New("photonimage: gif: control extension has wrong length")
		}
		flags, err := s.byte()
		if err != nil {
			return err
		}
		s.extLen++
		if flags&1 != 0 {
			s.current.transparent = 0
		}
		if _, err := s.u16(); err != nil { // the delay
			return err
		}
		s.extLen += 2
		index, err := s.byte()
		if err != nil {
			return err
		}
		s.extLen++
		if s.current.transparent >= 0 {
			s.current.transparent = int(index)
		}
		return s.extensionData(0)
	case gifExtText, gifExtComment, gifExtApplication:
		return s.extensionData(int(first))
	}
	return errors.New("photonimage: gif: unknown block type encountered")
}

// extensionData is ExtensionDataBlock: the data of the sub-block of left bytes, then a byte that is the end of the block when it is zero and
// the length of the next sub-block otherwise.
func (s *gifStream) extensionData(left int) error {
	for {
		if left > 0 {
			if s.pos >= len(s.data) {
				return errGIF
			}
			n := min(left, len(s.data)-s.pos)
			if s.extLen+n > gifMemoryLimit {
				return errors.New("photonimage: gif: memory limit reached")
			}
			s.extLen += n
			s.pos += n
			left -= n
			continue
		}
		b, err := s.byte()
		if err != nil {
			return err
		}
		if b == 0 {
			return nil
		}
		left = int(b)
	}
}

// imageDescriptor is the image block states (decoder.rs:655-665, :841-856, :606-635, :739-763): the frame's position and size, its flags, a
// local colour table, the minimum code size and the length of the first sub-block, which `LzwInit` validates.
func (s *gifStream) imageDescriptor() error {
	s.addFrame()
	f := s.current
	var err error
	// BlockStart wants a byte after the type, which is the low byte of the left position.
	if f.left, err = s.u16(); err != nil {
		return err
	}
	if f.top, err = s.u16(); err != nil {
		return err
	}
	if f.width, err = s.u16(); err != nil {
		return err
	}
	if f.height, err = s.u16(); err != nil {
		return err
	}
	flags, err := s.byte()
	if err != nil {
		return err
	}
	f.interlaced = flags&0b0100_0000 != 0
	if flags&0b1000_0000 != 0 {
		table, ok := s.take(3 * (1 << ((flags & 0b111) + 1)))
		if !ok {
			return errGIF
		}
		f.palette, f.hasPalette = table, true
	}
	if s.minCode, err = s.byte(); err != nil {
		return err
	}
	first, err := s.byte()
	if err != nil {
		return err
	}
	// LzwReader::reset validates the minimum code size.
	if s.minCode > 11 || s.minCode < 1 {
		return errors.New("photonimage: gif: invalid minimal code size")
	}
	s.lzw = newGIFLZW(s.minCode)
	s.left = int(first)
	s.frame = *f
	if !s.frame.hasPalette && s.global == nil {
		return errors.New("photonimage: gif: no color table available for current frame")
	}
	return nil
}

// windowRest is what the BufReader holds from the position on: it reads the file in blocks of 8 KiB from its start.
func (s *gifStream) windowRest() int {
	return gifBufReaderSize - s.pos%gifBufReaderSize
}

// fill is one call of the `data_callback` of `fill_buffer` (`decode_next_bytes`): it decodes the LZW data of the frame into out, as the
// DecodeSubBlock state does (decoder.rs:776-808), and returns how many bytes it wrote. Zero is the end of the frame's data. The LZW decoder is
// called with the bytes of the current sub-block that are in the BufReader's window.
func (s *gifStream) fill(out []byte) (int, error) {
	for {
		if s.left > 0 {
			if s.pos >= len(s.data) {
				return 0, errGIF
			}
			n := min(s.left, s.windowRest(), len(s.data)-s.pos)
			if s.lzw.hasEnded {
				// The rest of the data is skipped.
				s.pos += n
				s.left -= n
				continue
			}
			consumed, produced, err := s.lzw.decode(s.data[s.pos:s.pos+n], out)
			if err != nil {
				return 0, err
			}
			// Skip what the decoder cannot make progress with.
			if consumed == 0 && produced == 0 {
				consumed = n
			}
			s.pos += consumed
			s.left -= consumed
			if produced > 0 {
				return produced, nil
			}
			continue
		}
		b, err := s.byte()
		if err != nil {
			return 0, err
		}
		if b != 0 {
			s.left = int(b)
			continue
		}
		// The terminator: what the decoder still holds, then the end of the frame (DecodeSubBlock(0) leaves the byte unread).
		_, produced, err := s.lzw.decode(nil, out)
		if err != nil {
			return 0, err
		}
		s.pos--
		return produced, nil
	}
}

// gifInterlace is `InterlaceIterator` (converter.rs:190-215): the rows of an interlaced frame in the order of the file.
type gifInterlace struct {
	len, next, pass int
}

func (it *gifInterlace) nextRow() (int, bool) {
	if it.len == 0 || it.pass >= 4 {
		return 0, false
	}
	next := it.next + [4]int{8, 8, 4, 2}[it.pass]
	for next >= it.len {
		if it.pass >= 4 {
			return 0, false
		}
		next = [4]int{4, 2, 1, 0}[it.pass]
		it.pass++
	}
	row := it.next
	it.next = next
	return row, true
}

// readIntoBuffer is `PixelConverter::read_into_buffer` (converter.rs:166-187) and `fill_buffer` (:94-149) with RGBA output: buf is the frame's
// width * height * 4 bytes, filled in the order of the file's rows. A zero-length line is an error ("odd-sized buffer"), which is a frame
// of no pixels, and a frame whose data ends before it is full is an error.
func (s *gifStream) readIntoBuffer(buf []byte) error {
	f := &s.frame
	width := int(f.width) * 4
	palette := s.global
	if f.hasPalette {
		palette = f.palette
	}
	size := len(buf) / 4
	if f.interlaced {
		size = int(f.width)
	}
	indices := make([]byte, size)
	// fillLine is `fill_buffer` over one line (the whole buffer of a frame that is not interlaced): the decoder is called until the line is full.
	fillLine := func(line []byte) error {
		for {
			remaining := len(line) / 4
			if remaining == 0 {
				return errors.New("photonimage: gif: odd-sized buffer")
			}
			produced, err := s.fill(indices[:remaining])
			if err != nil {
				return err
			}
			if produced == 0 {
				return errGIFTruncated
			}
			for i := range produced {
				idx := int(indices[i])
				if 3*idx+3 <= len(palette) {
					line[4*i], line[4*i+1], line[4*i+2] = palette[3*idx], palette[3*idx+1], palette[3*idx+2]
					line[4*i+3] = 0xFF
					if f.transparent == idx {
						line[4*i+3] = 0
					}
				}
			}
			line = line[produced*4:]
			if len(line) == 0 {
				return nil
			}
		}
	}
	if !f.interlaced {
		if len(buf) == 0 {
			return errors.New("photonimage: gif: odd-sized buffer")
		}
		return fillLine(buf)
	}
	it := gifInterlace{len: int(f.height)}
	for {
		row, ok := it.nextRow()
		if !ok {
			return nil
		}
		start := row * width
		if start+width > len(buf) {
			return errors.New("photonimage: gif: buffer too small")
		}
		if width == 0 {
			return errors.New("photonimage: gif: odd-sized buffer")
		}
		if err := fillLine(buf[start : start+width]); err != nil {
			return err
		}
	}
}

// DecodeGIF returns the RGBA8 pixels Photon's `PhotonImage.new_from_byteslice(data).get_raw_pixels()` returns for a GIF file: the first frame of
// image 0.24.9's `GifDecoder` over the gif crate 0.13.1, on a logical screen of transparent black. The frame is placed at its offset when it
// does not fill the width of the screen; what is outside the frame is transparent. A frame without pixels is an error, as is a file without
// a frame, a screen of more than 512 MiB of RGBA, and every file the crate refuses.
func DecodeGIF(data []byte) (*image.NRGBA, error) {
	s := &gifStream{data: data}
	blockType, err := s.readInfo()
	if err != nil {
		return nil, err
	}
	width, height := uint32(s.width), uint32(s.height)
	// load_inner: total_bytes of Rgba8 against the allocation limit; what is left of it bounds the frame buffer of a frame that is not the screen.
	if err := reserveTotalBytes(width, height, 4); err != nil {
		return nil, err
	}
	remaining := uint64(imageMaxAlloc) - uint64(width)*uint64(height)*4
	if err := s.nextFrame(blockType); err != nil {
		return nil, err
	}
	// The ImageBuffer length of `decoder_to_image` (the screen) and of `to_rgba8`.
	if err := checkRGBAConversion(width, height, 4); err != nil {
		return nil, err
	}
	out := newImageNRGBA(width, height)

	f := &s.frame
	if f.left == 0 && uint32(f.width) == width && uint64(f.top)+uint64(f.height) <= uint64(height) {
		// The frame touches both sides of the screen: it is written to its rows directly, the rows above and below stay zero.
		lineLength := int(width) * 4
		if err := s.readIntoBuffer(out.Pix[lineLength*int(f.top) : lineLength*(int(f.top)+int(f.height))]); err != nil {
			return nil, err
		}
		return out, nil
	}
	// Any other frame is read into a buffer of its own and copied.
	bufferSize := uint64(f.width) * uint64(f.height) * 4
	if uint64(f.width)*uint64(f.height) > math.MaxUint32 || bufferSize > math.MaxUint32 {
		return nil, errImageLimits // InsufficientMemory: the size overflows wasm32's usize
	}
	if bufferSize > remaining {
		return nil, errImageLimits
	}
	frameBuffer := make([]byte, bufferSize)
	if err := s.readIntoBuffer(frameBuffer); err != nil {
		return nil, err
	}
	fw, fh := uint32(f.width), uint32(f.height)
	for y := range height {
		frameY := y - uint32(f.top)
		if frameY >= fh {
			continue
		}
		for x := range width {
			frameX := x - uint32(f.left)
			if frameX < fw {
				copy(out.Pix[(uint64(y)*uint64(width)+uint64(x))*4:][:4], frameBuffer[(uint64(frameY)*uint64(fw)+uint64(frameX))*4:][:4])
			}
		}
	}
	return out, nil
}
