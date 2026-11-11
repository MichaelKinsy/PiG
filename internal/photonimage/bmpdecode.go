// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"errors"
	"image"
	"math"
	"math/bits"
)

// The bitmap decoder of image 0.24.9 (codecs/bmp/decoder.rs), `BmpDecoder`, in the two forms Photon reaches: `BmpDecoder::new` for a BMP file
// (guess_format `BM`, DecodeBMP) and `BmpDecoder::new_with_ico_format` for the bitmap of an ICO entry (icoDecodeBMP). The ICO form has no
// BITMAPFILEHEADER (the bitmap starts at the entry's offset and its data where the header and palette end), always has an alpha channel (RGBA8
// output, so every 32-bit bitmap is BGRA and none is RGB32) and doubles the height field for the AND mask. A BMP file has an alpha channel
// only when a bitfield mask names one, and is RGB8 otherwise: `to_rgba8` then makes the alpha 255 for every pixel, including the ones an RLE
// bitmap leaves unwritten.
//
// The decoder fills a four-byte-per-pixel buffer in both forms (the bytes that the three-channel form would not have are overwritten with 255
// at the end); the sizes the limits and the overflow checks see are the three- or four-byte ones of the form.

// bmpSignature is image 0.24.9's guess_format signature for BMP (io/free_functions.rs:272).
var bmpSignature = []byte("BM")

// isBMP is image::guess_format's test for a BMP file.
func isBMP(data []byte) bool {
	return bytes.HasPrefix(data, bmpSignature)
}

const (
	bmpCoreHeaderSize = 12
	bmpInfoHeaderSize = 40
	bmpV2HeaderSize   = 52
	bmpV3HeaderSize   = 56
	bmpV4HeaderSize   = 108
	bmpV5HeaderSize   = 124

	// bmpMaxWidthHeight is MAX_WIDTH_HEIGHT, the soft limit of the decoder (decoder.rs:63).
	bmpMaxWidthHeight = 0xFFFF
)

var (
	errBMP        = errors.New("photonimage: bmp: invalid or truncated bitmap")
	errBMPCorrupt = errors.New("photonimage: bmp: corrupt RLE data")
)

type bmpImageType int

const (
	bmpPalette bmpImageType = iota
	bmpRGB16
	bmpRGB24
	bmpRGB32
	bmpRGBA32
	bmpRLE8
	bmpRLE4
	bmpBitfields16
	bmpBitfields32
)

// Lookup tables of decoder.rs:23-36: the 3- to 6-bit channel values scaled to 8 bits.
var (
	bmpTable3 = [8]byte{0, 36, 73, 109, 146, 182, 219, 255}
	bmpTable4 = [16]byte{0, 17, 34, 51, 68, 85, 102, 119, 136, 153, 170, 187, 204, 221, 238, 255}
	bmpTable5 = [32]byte{0, 8, 16, 25, 33, 41, 49, 58, 66, 74, 82, 90, 99, 107, 115, 123, 132, 140, 148, 156, 165, 173, 181, 189, 197, 206, 214, 222, 230, 239, 247, 255}
	bmpTable6 = [64]byte{0, 4, 8, 12, 16, 20, 24, 28, 32, 36, 40, 45, 49, 53, 57, 61, 65, 69, 73, 77, 81, 85, 89, 93, 97, 101, 105, 109, 113, 117, 121, 125, 130, 134, 138, 142, 146, 150, 154, 158,
		162, 166, 170, 174, 178, 182, 186, 190, 194, 198, 202, 206, 210, 215, 219, 223, 227, 231, 235, 239, 243, 247, 251, 255}
)

// bmpBitfield is decoder.rs:404-444 `Bitfield`.
type bmpBitfield struct {
	shift, length uint32
}

// bmpBitfieldFromMask is `Bitfield::from_mask`: a contiguous run of at most max_len bits; longer than eight bits keeps the top eight.
func bmpBitfieldFromMask(mask, maxLen uint32) (bmpBitfield, bool) {
	if mask == 0 {
		return bmpBitfield{}, true
	}
	shift := uint32(bits.TrailingZeros32(mask))
	length := uint32(bits.TrailingZeros32(^(mask >> shift)))
	if length != uint32(bits.OnesCount32(mask)) {
		return bmpBitfield{}, false
	}
	if length+shift > maxLen {
		return bmpBitfield{}, false
	}
	if length > 8 {
		shift += length - 8
		length = 8
	}
	return bmpBitfield{shift, length}, true
}

// read is `Bitfield::read`.
func (f bmpBitfield) read(data uint32) byte {
	data >>= f.shift
	switch f.length {
	case 1:
		return byte((data & 0b1) * 0xff)
	case 2:
		return byte((data & 0b11) * 0x55)
	case 3:
		return bmpTable3[data&0b111]
	case 4:
		return bmpTable4[data&0b1111]
	case 5:
		return bmpTable5[data&0b11111]
	case 6:
		return bmpTable6[data&0b111111]
	case 7:
		return byte((data&0x7f)<<1 | (data&0x7f)>>6)
	case 8:
		return byte(data & 0xff)
	}
	panic("bmp: bitfield of no bits") // Rust's `panic!()`: a Photon exception
}

// bmpBitfields is decoder.rs:446-473 `Bitfields`.
type bmpBitfields struct {
	r, g, b, a bmpBitfield
}

var (
	bmpR5G5B5   = bmpBitfields{r: bmpBitfield{10, 5}, g: bmpBitfield{5, 5}, b: bmpBitfield{0, 5}}
	bmpR8G8B8   = bmpBitfields{r: bmpBitfield{24, 8}, g: bmpBitfield{16, 8}, b: bmpBitfield{8, 8}}
	bmpR8G8B8A8 = bmpBitfields{r: bmpBitfield{16, 8}, g: bmpBitfield{8, 8}, b: bmpBitfield{0, 8}, a: bmpBitfield{24, 8}}
)

func bmpBitfieldsFromMask(r, g, b, a, maxLen uint32) (*bmpBitfields, bool) {
	var f bmpBitfields
	var ok [4]bool
	f.r, ok[0] = bmpBitfieldFromMask(r, maxLen)
	f.g, ok[1] = bmpBitfieldFromMask(g, maxLen)
	f.b, ok[2] = bmpBitfieldFromMask(b, maxLen)
	f.a, ok[3] = bmpBitfieldFromMask(a, maxLen)
	if !ok[0] || !ok[1] || !ok[2] || !ok[3] {
		return nil, false
	}
	if f.r.length == 0 || f.g.length == 0 || f.b.length == 0 {
		return nil, false
	}
	return &f, true
}

// bmpDecoder is decoder.rs:476-495 `BmpDecoder`.
type bmpDecoder struct {
	r *icoCursor

	// noFileHeader and addAlpha are set for an ICO entry's bitmap (read_metadata_in_ico_format); addAlpha is also set by a bitfield alpha mask.
	noFileHeader bool
	addAlpha     bool

	headerSize uint32
	width      int32
	height     int32
	dataOffset uint64
	topDown    bool
	imageType  bmpImageType

	bitCount   uint16
	colorsUsed uint32
	palette    [256][3]byte
	bitfields  *bmpBitfields
}

// channels is `num_channels`: the bytes of a pixel of the decoder's own output, with no indexed colour.
func (d *bmpDecoder) channels() uint64 {
	if d.addAlpha {
		return 4
	}
	return 3
}

// bmpCheckForOverflow is `check_for_overflow(width, height, channels)` (decoder.rs:241-266) in wasm32's 32-bit `usize`: false for an empty image
// or a buffer length that does not fit.
func bmpCheckForOverflow(width, height int32, channels uint64) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	n := channels * uint64(width)
	if n > math.MaxUint32 {
		return false
	}
	return n*uint64(height) <= math.MaxUint32
}

// DecodeBMP returns the RGBA8 pixels Photon's `PhotonImage.new_from_byteslice(data).get_raw_pixels()` returns for a BMP file: image 0.24.9's
// `BmpDecoder::new` (codecs/bmp/decoder.rs) driven by `load_from_memory`, then `to_rgba8`. The file header's size and reserved fields are
// not read, the bitmap is read from the header's data offset, and every layout the decoder knows is accepted: the core header, the
// BITMAPINFOHEADER and its V2 to V5 extensions with the palette, RLE4 and RLE8, 16-bit, 24-bit and 32-bit colour, bitfields, top-down
// bitmaps. The result has an alpha channel only through an alpha bitfield mask. Everything else is an error, as is an image of more than
// 512 MiB of RGB8 (or RGBA8) or whose buffer length overflows wasm32's `usize`.
func DecodeBMP(data []byte) (*image.NRGBA, error) {
	if !isBMP(data) {
		return nil, errors.New("photonimage: bmp: not a BMP (no BM signature)")
	}
	d := &bmpDecoder{r: &icoCursor{data: data}}
	if err := d.readMetadata(); err != nil {
		return nil, err
	}
	width, height := uint32(d.width), uint32(d.height)
	// load_inner: total_bytes of Rgb8 or Rgba8 against the allocation limit, then the two buffer lengths of decoder_to_image and to_rgba8.
	if err := reserveTotalBytes(width, height, d.channels()); err != nil {
		return nil, err
	}
	if err := checkRGBAConversion(width, height, d.channels()); err != nil {
		return nil, err
	}
	out := newImageNRGBA(width, height)
	if err := d.readImageData(out.Pix); err != nil {
		return nil, err
	}
	if !d.addAlpha {
		for i := 3; i < len(out.Pix); i += 4 {
			out.Pix[i] = 0xFF
		}
	}
	return out, nil
}

// readFileHeader is `read_file_header` (decoder.rs:564-583).
func (d *bmpDecoder) readFileHeader() error {
	if d.noFileHeader {
		return nil
	}
	r := d.r
	signature, ok := r.take(2)
	if !ok || !bytes.Equal(signature, bmpSignature) {
		return errBMP
	}
	// The file size and the reserved fields.
	if _, ok := r.u32(); !ok {
		return errBMP
	}
	if _, ok := r.u32(); !ok {
		return errBMP
	}
	offset, ok := r.u32()
	if !ok {
		return errBMP
	}
	d.dataOffset = uint64(offset)
	return nil
}

// readMetadata is `read_metadata` (decoder.rs:783-847).
func (d *bmpDecoder) readMetadata() error {
	if err := d.readFileHeader(); err != nil {
		return err
	}
	r := d.r
	headerOffset := r.pos
	size, ok := r.u32()
	if !ok {
		return errBMP
	}
	headerEnd := headerOffset + uint64(size)
	switch size {
	case bmpCoreHeaderSize, bmpInfoHeaderSize, bmpV2HeaderSize, bmpV3HeaderSize, bmpV4HeaderSize, bmpV5HeaderSize:
		d.headerSize = size
	default:
		if size < bmpCoreHeaderSize {
			return errors.New("photonimage: bmp: bitmap header too small")
		}
		return errors.New("photonimage: bmp: unknown bitmap header type")
	}
	if size == bmpCoreHeaderSize {
		if err := d.readCoreHeader(); err != nil {
			return err
		}
	} else if err := d.readInfoHeader(); err != nil {
		return err
	}
	if d.imageType == bmpBitfields16 || d.imageType == bmpBitfields32 {
		if err := d.readBitmasks(); err != nil {
			return err
		}
	}
	r.pos = headerEnd
	if d.imageType == bmpPalette || d.imageType == bmpRLE4 || d.imageType == bmpRLE8 {
		if err := d.readPalette(); err != nil {
			return err
		}
	}
	if d.noFileHeader {
		// Use the offset of the end of metadata instead of reading a BMP file header.
		d.dataOffset = r.pos
	}
	return nil
}

// readCoreHeader is `read_bitmap_core_header` (decoder.rs:588-615).
func (d *bmpDecoder) readCoreHeader() error {
	r := d.r
	w, ok1 := r.u16()
	h, ok2 := r.u16()
	if !ok1 || !ok2 {
		return errBMP
	}
	d.width, d.height = int32(w), int32(h)
	if !bmpCheckForOverflow(d.width, d.height, d.channels()) {
		return errBMP
	}
	planes, ok := r.u16()
	if !ok || planes != 1 {
		return errBMP
	}
	if d.bitCount, ok = r.u16(); !ok {
		return errBMP
	}
	switch d.bitCount {
	case 1, 4, 8:
		d.imageType = bmpPalette
	case 24:
		d.imageType = bmpRGB24
	default:
		return errBMP
	}
	return nil
}

// readInfoHeader is `read_bitmap_info_header` (decoder.rs:621-752).
func (d *bmpDecoder) readInfoHeader() error {
	r := d.r
	w, ok1 := r.u32()
	h, ok2 := r.u32()
	if !ok1 || !ok2 {
		return errBMP
	}
	d.width, d.height = int32(w), int32(h)
	if d.width < 0 {
		return errBMP
	} else if d.width > bmpMaxWidthHeight || d.height > bmpMaxWidthHeight {
		return errBMP
	}
	if d.height == math.MinInt32 {
		return errBMP
	}
	// A negative height indicates a top-down DIB.
	if d.height < 0 {
		d.height = -d.height
		d.topDown = true
	}
	if !bmpCheckForOverflow(d.width, d.height, d.channels()) {
		return errBMP
	}
	planes, ok := r.u16()
	if !ok || planes != 1 {
		return errBMP
	}
	if d.bitCount, ok = r.u16(); !ok {
		return errBMP
	}
	compression, ok := r.u32()
	if !ok {
		return errBMP
	}
	// Top-down DIBs cannot be compressed.
	if d.topDown && compression != 0 && compression != 3 {
		return errBMP
	}
	switch compression {
	case 0:
		switch d.bitCount {
		case 1, 2, 4, 8:
			d.imageType = bmpPalette
		case 16:
			d.imageType = bmpRGB16
		case 24:
			d.imageType = bmpRGB24
		case 32:
			if d.addAlpha {
				d.imageType = bmpRGBA32
			} else {
				d.imageType = bmpRGB32
			}
		default:
			return errBMP
		}
	case 1:
		if d.bitCount != 8 {
			return errBMP
		}
		d.imageType = bmpRLE8
	case 2:
		if d.bitCount != 4 {
			return errBMP
		}
		d.imageType = bmpRLE4
	case 3:
		switch d.bitCount {
		case 16:
			d.imageType = bmpBitfields16
		case 32:
			d.imageType = bmpBitfields32
		default:
			return errBMP
		}
	default: // JPEG (4), PNG (5), CMYK (11 to 13) and unknown compressions
		return errors.New("photonimage: bmp: unsupported or unknown compression")
	}
	// The data array size and the printing resolutions.
	for range 3 {
		if _, ok := r.u32(); !ok {
			return errBMP
		}
	}
	if d.colorsUsed, ok = r.u32(); !ok {
		return errBMP
	}
	// The number of important colours.
	if _, ok := r.u32(); !ok {
		return errBMP
	}
	return nil
}

// readBitmasks is `read_bitmasks` (decoder.rs:754-781): the alpha mask is part of the V3 header and later ones, and a mask that names an
// alpha channel gives the bitmap one.
func (d *bmpDecoder) readBitmasks() error {
	r := d.r
	rMask, ok1 := r.u32()
	gMask, ok2 := r.u32()
	bMask, ok3 := r.u32()
	if !ok1 || !ok2 || !ok3 {
		return errBMP
	}
	aMask := uint32(0)
	if d.headerSize == bmpV3HeaderSize || d.headerSize == bmpV4HeaderSize || d.headerSize == bmpV5HeaderSize {
		var ok bool
		if aMask, ok = r.u32(); !ok {
			return errBMP
		}
	}
	maxLen := uint32(32)
	if d.imageType == bmpBitfields16 {
		maxLen = 16
	}
	var ok bool
	if d.bitfields, ok = bmpBitfieldsFromMask(rMask, gMask, bMask, aMask, maxLen); !ok {
		return errBMP
	}
	if aMask != 0 {
		d.addAlpha = true
	}
	return nil
}

// readPalette is `read_palette` (decoder.rs:885-924): 256 entries are kept whatever the palette size; a short palette is an error and
// one with more than 256 entries cannot occur (colors_used is limited to 1 << bit_count).
func (d *bmpDecoder) readPalette() error {
	r := d.r
	bytesPerColor := uint64(4)
	if d.headerSize == bmpCoreHeaderSize {
		bytesPerColor = 3
	}
	var paletteSize uint64
	if d.colorsUsed == 0 {
		paletteSize = 1 << d.bitCount
	} else {
		if uint64(d.colorsUsed) > 1<<d.bitCount {
			return errBMP
		}
		paletteSize = uint64(d.colorsUsed)
	}
	const maxPaletteSize = 256
	maxLength := maxPaletteSize * bytesPerColor
	length := paletteSize * bytesPerColor
	buf, ok := r.take(min(length, maxLength))
	if !ok {
		return errBMP
	}
	full := make([]byte, maxLength)
	copy(full, buf)
	if length > maxLength {
		r.pos += length - maxLength
	}
	for i := range maxPaletteSize {
		b, g, rr := full[bytesPerColor*uint64(i)], full[bytesPerColor*uint64(i)+1], full[bytesPerColor*uint64(i)+2]
		d.palette[i] = [3]byte{rr, g, b}
	}
	return nil
}

// rowIndex is the buffer row of the i-th row of the file: the order of `with_rows` and `rows` (decoder.rs:270-297, :941-952), bottom-up
// unless the DIB is top-down.
func (d *bmpDecoder) rowIndex(i int) int {
	if d.topDown {
		return i
	}
	return int(d.height) - 1 - i
}

// withRows calls fn with each row of buf (four bytes per pixel) in file order.
func (d *bmpDecoder) withRows(buf []byte, fn func(row []byte) bool) bool {
	stride := int(d.width) * 4
	for i := range int(d.height) {
		y := d.rowIndex(i)
		if !fn(buf[y*stride : (y+1)*stride]) {
			return false
		}
	}
	return true
}

// readImageData is `read_image_data` (decoder.rs:1305-1329).
func (d *bmpDecoder) readImageData(buf []byte) error {
	var ok bool
	switch d.imageType {
	case bmpPalette:
		ok = d.readPalettizedPixelData(buf)
	case bmpRGB16:
		ok = d.read16BitPixelData(buf, &bmpR5G5B5)
	case bmpRGB24:
		ok = d.readFullBytePixelData(buf, bmpFormatRGB24)
	case bmpRGB32:
		ok = d.readFullBytePixelData(buf, bmpFormatRGB32)
	case bmpRGBA32:
		ok = d.readFullBytePixelData(buf, bmpFormatRGBA32)
	case bmpRLE8, bmpRLE4:
		return d.readRLEData(buf)
	case bmpBitfields16:
		ok = d.read16BitPixelData(buf, d.bitfields)
	case bmpBitfields32:
		switch *d.bitfields {
		case bmpR8G8B8:
			ok = d.readFullBytePixelData(buf, bmpFormat888)
		case bmpR8G8B8A8:
			ok = d.readFullBytePixelData(buf, bmpFormatRGBA32)
		default:
			ok = d.read32BitPixelData(buf)
		}
	}
	if !ok {
		return errBMP
	}
	return nil
}

// readPalettizedPixelData is decoder.rs:954-1003. Each row is read whole (`read_exact` of the row's padded bytes), so a short row fails
// before any of its pixels is set.
func (d *bmpDecoder) readPalettizedPixelData(buf []byte) bool {
	r := d.r
	rowByteLength := uint64((int32(d.bitCount)*d.width + 31) / 32 * 4)
	width := int(d.width)
	r.pos = d.dataOffset
	for i := 3; i < len(buf); i += 4 {
		buf[i] = 0xFF
	}
	return d.withRows(buf, func(row []byte) bool {
		indices, ok := r.take(rowByteLength)
		if !ok {
			return false
		}
		switch d.bitCount {
		case 1:
			// set_1bit_pixel_run: every bit of every byte until the row is full.
			for x := range width {
				d.setPixel(row, x, d.palette[indices[x/8]>>(7-x%8)&1])
			}
		case 2:
			for x := range width {
				d.setPixel(row, x, d.palette[indices[x/4]>>(6-2*(x%4))&3])
			}
		case 4:
			for x := range width {
				d.setPixel(row, x, d.palette[indices[x/2]>>(4-4*(x%2))&0xf])
			}
		case 8:
			for x := range width {
				d.setPixel(row, x, d.palette[indices[x]])
			}
		}
		return true
	})
}

// setPixel writes the colour into the first three channels of pixel x of row (the alpha channel is left as it is).
func (d *bmpDecoder) setPixel(row []byte, x int, c [3]byte) {
	row[4*x], row[4*x+1], row[4*x+2] = c[0], c[1], c[2]
}

// read16BitPixelData is decoder.rs:1005-1047.
func (d *bmpDecoder) read16BitPixelData(buf []byte, f *bmpBitfields) bool {
	r := d.r
	width := int(d.width)
	rowBytes := uint64(width)*2 + uint64(width)%2*2
	r.pos = d.dataOffset
	return d.withRows(buf, func(row []byte) bool {
		src, ok := r.take(rowBytes)
		if !ok {
			return false
		}
		for x := range width {
			data := uint32(src[2*x]) | uint32(src[2*x+1])<<8
			row[4*x], row[4*x+1], row[4*x+2] = f.r.read(data), f.g.read(data), f.b.read(data)
			if f.a.length != 0 {
				row[4*x+3] = f.a.read(data)
			} else {
				row[4*x+3] = 0xFF
			}
		}
		return true
	})
}

// read32BitPixelData is decoder.rs:1050-1084.
func (d *bmpDecoder) read32BitPixelData(buf []byte) bool {
	r := d.r
	f := d.bitfields
	width := int(d.width)
	r.pos = d.dataOffset
	return d.withRows(buf, func(row []byte) bool {
		src, ok := r.take(uint64(width) * 4)
		if !ok {
			return false
		}
		for x := range width {
			data := uint32(src[4*x]) | uint32(src[4*x+1])<<8 | uint32(src[4*x+2])<<16 | uint32(src[4*x+3])<<24
			row[4*x], row[4*x+1], row[4*x+2] = f.r.read(data), f.g.read(data), f.b.read(data)
			if f.a.length != 0 {
				row[4*x+3] = f.a.read(data)
			} else {
				row[4*x+3] = 0xff
			}
		}
		return true
	})
}

type bmpFullBytes int

const (
	bmpFormatRGB24 bmpFullBytes = iota
	bmpFormatRGB32
	bmpFormatRGBA32
	bmpFormat888
)

// readFullBytePixelData is decoder.rs:1087-1137: colours stored as bytes, blue first. RGB24 pads its rows to four bytes, RGB32 skips the
// fourth byte of a pixel, Format888 skips the first and RGBA32 reads the alpha channel as the fourth.
func (d *bmpDecoder) readFullBytePixelData(buf []byte, format bmpFullBytes) bool {
	r := d.r
	width := int(d.width)
	pixelBytes, padding := 4, uint64(0)
	if format == bmpFormatRGB24 {
		pixelBytes, padding = 3, (4-uint64(width)*3%4)%4
	}
	rowBytes := uint64(width*pixelBytes) + padding
	r.pos = d.dataOffset
	return d.withRows(buf, func(row []byte) bool {
		src, ok := r.take(rowBytes)
		if !ok {
			return false
		}
		switch format {
		case bmpFormatRGB24:
			for x := range width {
				row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = src[3*x+2], src[3*x+1], src[3*x], 0xFF
			}
		case bmpFormatRGB32:
			for x := range width {
				row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = src[4*x+2], src[4*x+1], src[4*x], 0xFF
			}
		case bmpFormatRGBA32:
			for x := range width {
				row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = src[4*x+2], src[4*x+1], src[4*x], src[4*x+3]
			}
		case bmpFormat888:
			for x := range width {
				row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = src[4*x+3], src[4*x+2], src[4*x+1], 0xFF
			}
		}
		return true
	})
}

// bmpPixels is the `ChunksMut` of a row's pixels: next gives the next pixel, as `pixel_iter` does.
type bmpPixels struct {
	row []byte
	i   int
}

func (p *bmpPixels) next() ([]byte, bool) {
	if p.i*4 >= len(p.row) {
		return nil, false
	}
	px := p.row[p.i*4 : p.i*4+4]
	p.i++
	return px, true
}

// zeroRest is `pixel_iter.for_each(|p| p.fill(0))`.
func (p *bmpPixels) zeroRest() {
	clear(p.row[min(p.i*4, len(p.row)):])
	p.i = len(p.row) / 4
}

// set8BitRun and set4BitRun write the palette colour of each index (a byte for RLE8, two nibbles for RLE4) to the next pixels, as `set_8bit_pixel_run` and
// `set_4bit_pixel_run` do; false when the row has no more pixels.
func (d *bmpDecoder) set8BitRun(pixels *bmpPixels, indices func(i int) (byte, bool), n int) bool {
	for i := range n {
		idx, ok := indices(i)
		if !ok {
			break
		}
		px, ok := pixels.next()
		if !ok {
			return false
		}
		c := d.palette[idx]
		px[0], px[1], px[2] = c[0], c[1], c[2]
	}
	return true
}

func (d *bmpDecoder) set4BitRun(pixels *bmpPixels, indices func(i int) (byte, bool), n int) bool {
	for i := 0; ; i++ {
		idx, ok := indices(i)
		if !ok {
			return true
		}
		for _, nibble := range [2]byte{idx >> 4, idx & 0xf} {
			if n == 0 {
				return true
			}
			px, ok := pixels.next()
			if !ok {
				return false
			}
			c := d.palette[nibble]
			px[0], px[1], px[2] = c[0], c[1], c[2]
			n--
		}
	}
}

// readRLEData is decoder.rs:1139-1301.
func (d *bmpDecoder) readRLEData(buf []byte) error {
	r := d.r
	r.pos = d.dataOffset

	stride := int(d.width) * 4
	nextIndex := 0
	nextRow := func() ([]byte, bool) {
		if nextIndex >= int(d.height) {
			return nil, false
		}
		y := d.rowIndex(nextIndex)
		nextIndex++
		return buf[y*stride : (y+1)*stride], true
	}
	for {
		row, ok := nextRow()
		if !ok {
			return nil
		}
		pixels := &bmpPixels{row: row}
		x := 0
		for rowDone := false; !rowDone; {
			control, ok := r.u8()
			if !ok {
				return errBMP
			}
			if control == 0 { // RLE_ESCAPE
				op, ok := r.u8()
				if !ok {
					return errBMP
				}
				switch op {
				case 0: // end of row
					pixels.zeroRest()
					rowDone = true
				case 1: // end of file
					pixels.zeroRest()
					for {
						rest, ok := nextRow()
						if !ok {
							return nil
						}
						clear(rest)
					}
				case 2: // delta
					xDelta, ok1 := r.u8()
					yDelta, ok2 := r.u8()
					if !ok1 || !ok2 {
						return errBMP
					}
					// The skipped pixels become black (and transparent where there is an alpha channel).
					if yDelta > 0 {
						pixels.zeroRest()
						for range int(yDelta) - 1 {
							skipped, ok := nextRow()
							if !ok {
								return errBMPCorrupt
							}
							clear(skipped)
						}
						next, ok := nextRow()
						if !ok {
							return errBMPCorrupt
						}
						pixels = &bmpPixels{row: next}
						for range x {
							px, ok := pixels.next()
							if !ok {
								return errBMPCorrupt
							}
							clear(px)
						}
					}
					for range int(xDelta) {
						px, ok := pixels.next()
						if !ok {
							return errBMPCorrupt
						}
						clear(px)
					}
					x += int(xDelta)
				default: // absolute mode
					length := int(op)
					if d.imageType == bmpRLE4 {
						length = (length + 1) / 2
					}
					length += length & 1
					indices, ok := r.take(uint64(length))
					if !ok {
						return errBMP
					}
					at := func(i int) (byte, bool) {
						if i >= len(indices) {
							return 0, false
						}
						return indices[i], true
					}
					if d.imageType == bmpRLE8 {
						if !d.set8BitRun(pixels, at, int(op)) {
							return errBMPCorrupt
						}
					} else if !d.set4BitRun(pixels, at, int(op)) {
						return errBMPCorrupt
					}
					x += int(op)
				}
				continue
			}
			// An encoded run: `control` pixels of one palette index (RLE8) or alternating nibbles (RLE4).
			index, ok := r.u8()
			if !ok {
				return errBMP
			}
			forever := func(int) (byte, bool) { return index, true }
			if d.imageType == bmpRLE8 {
				if !d.set8BitRun(pixels, forever, int(control)) {
					return errBMPCorrupt
				}
			} else if !d.set4BitRun(pixels, forever, int(control)) {
				return errBMPCorrupt
			}
			x += int(control)
		}
	}
}
