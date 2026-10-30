// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"errors"
	"image"
	"math"
	"math/bits"
)

// The bitmap decoder of image 0.24.9 (codecs/bmp/decoder.rs) as the ICO decoder uses it, `BmpDecoder::new_with_ico_format`: no
// BITMAPFILEHEADER (the bitmap starts at the entry's offset and its data where the header and palette end), an alpha channel (RGBA8 output,
// so every 32-bit bitmap is BGRA and no bitmap is RGB32), and the height field doubled for the AND mask. The routing of plain BMP files
// stays with the standard library decoders and decodeBitfieldBMP: they decode what Photon decodes.

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

var errBMP = errors.New("photonimage: bmp: invalid or truncated bitmap")

type bmpImageType int

const (
	bmpPalette bmpImageType = iota
	bmpRGB16
	bmpRGB24
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

// icoBMP is decoder.rs:476-495 `BmpDecoder` in ICO mode.
type icoBMP struct {
	r *icoCursor

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

// bmpCheckForOverflow is `check_for_overflow(width, height, 4)` (decoder.rs:241-266) in wasm32's 32-bit `usize`: false for an empty image or a
// buffer length that does not fit.
func bmpCheckForOverflow(width, height int32) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	n := uint64(4) * uint64(width)
	if n > math.MaxUint32 {
		return false
	}
	return n*uint64(height) <= math.MaxUint32
}

// icoDecodeBMP is the BMP arm of IcoDecoder (ico/decoder.rs:334-402) over BmpDecoder::new_with_ico_format.
func icoDecodeBMP(r *icoCursor, entry icoDirEntry) (*image.NRGBA, error) {
	d := &icoBMP{r: r}
	if err := d.readMetadata(); err != nil {
		return nil, err
	}
	// The height field of an ICO is doubled to account for the AND mask.
	d.height /= 2
	width, height := uint32(d.width), uint32(d.height)

	// load_inner: total_bytes of Rgba8 against the allocation limit.
	if err := reserveTotalBytes(width, height, 4); err != nil {
		return nil, err
	}
	if !entry.matchesDimensions(width, height) {
		return nil, errICOEntry
	}
	if err := checkRGBAConversion(width, height, 4); err != nil {
		return nil, err
	}
	out := newImageNRGBA(width, height)
	if err := d.readImageData(out.Pix); err != nil {
		return nil, err
	}

	imageEnd := r.pos
	dataEnd := uint64(entry.imageOffset) + uint64(entry.imageLength)
	maskRowBytes := uint64((width+31)/32) * 4
	maskLength := maskRowBytes * uint64(height)
	// data_end should be image_end plus the mask; a bitmap without a mask is accepted.
	switch {
	case dataEnd >= imageEnd+maskLength:
		for y := range height {
			x := uint32(0)
			for range maskRowBytes {
				maskByte, ok := r.u8()
				if !ok {
					return nil, errBMP
				}
				for bit := 7; bit >= 0; bit-- {
					if x >= width {
						break
					}
					if maskByte&(1<<bit) != 0 {
						out.Pix[((uint64(height-y-1)*uint64(width)+uint64(x))*4)+3] = 0
					}
					x++
				}
			}
		}
		return out, nil
	case dataEnd == imageEnd:
		return out, nil
	}
	return nil, errors.New("photonimage: ico: image data size did not match the expected size")
}

// readMetadata is `read_metadata` (decoder.rs:783-847) for a bitmap without a file header.
func (d *icoBMP) readMetadata() error {
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
	// No file header: the data starts where the metadata ends.
	d.dataOffset = r.pos
	return nil
}

// readCoreHeader is `read_bitmap_core_header` (decoder.rs:588-615).
func (d *icoBMP) readCoreHeader() error {
	r := d.r
	w, ok1 := r.u16()
	h, ok2 := r.u16()
	if !ok1 || !ok2 {
		return errBMP
	}
	d.width, d.height = int32(w), int32(h)
	if !bmpCheckForOverflow(d.width, d.height) {
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

// readInfoHeader is `read_bitmap_info_header` (decoder.rs:621-752) with `add_alpha_channel` set.
func (d *icoBMP) readInfoHeader() error {
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
	if !bmpCheckForOverflow(d.width, d.height) {
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
			d.imageType = bmpRGBA32 // add_alpha_channel is set
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

// readBitmasks is `read_bitmasks` (decoder.rs:754-781): the alpha mask is part of the V3 header and later ones.
func (d *icoBMP) readBitmasks() error {
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
	return nil
}

// readPalette is `read_palette` (decoder.rs:885-924): 256 entries are kept whatever the palette size; a short palette is an error and
// one with more than 256 entries cannot occur (colors_used is limited to 1 << bit_count).
func (d *icoBMP) readPalette() error {
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

// rowOrder is the order of the rows of `with_rows` and `rows` (decoder.rs:270-297, :941-952): bottom-up unless the DIB is top-down.
func (d *icoBMP) rowOrder() []int {
	order := make([]int, d.height)
	for i := range order {
		if d.topDown {
			order[i] = i
		} else {
			order[i] = int(d.height) - 1 - i
		}
	}
	return order
}

// withRows calls fn with each row of buf (four bytes per pixel) in file order.
func (d *icoBMP) withRows(buf []byte, fn func(row []byte) bool) bool {
	stride := int(d.width) * 4
	for _, y := range d.rowOrder() {
		if !fn(buf[y*stride : (y+1)*stride]) {
			return false
		}
	}
	return true
}

// readImageData is `read_image_data` (decoder.rs:1305-1329).
func (d *icoBMP) readImageData(buf []byte) error {
	var ok bool
	switch d.imageType {
	case bmpPalette:
		ok = d.readPalettizedPixelData(buf)
	case bmpRGB16:
		ok = d.read16BitPixelData(buf, &bmpR5G5B5)
	case bmpRGB24:
		ok = d.readFullBytePixelData(buf, bmpFormatRGB24)
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

// readPalettizedPixelData is decoder.rs:954-1003.
func (d *icoBMP) readPalettizedPixelData(buf []byte) bool {
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
			x := 0
			for _, idx := range indices {
				for bit := byte(0x80); bit != 0; bit >>= 1 {
					if x >= width {
						return true
					}
					d.setPixel(row, x, d.palette[b2i(idx&bit != 0)])
					x++
				}
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

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// setPixel writes the colour into the first three channels of pixel x of row (the alpha channel is left as it is).
func (d *icoBMP) setPixel(row []byte, x int, c [3]byte) {
	row[4*x], row[4*x+1], row[4*x+2] = c[0], c[1], c[2]
}

// read16BitPixelData is decoder.rs:1005-1047.
func (d *icoBMP) read16BitPixelData(buf []byte, f *bmpBitfields) bool {
	r := d.r
	padding := uint64(d.width) % 2 * 2
	r.pos = d.dataOffset
	return d.withRows(buf, func(row []byte) bool {
		for x := range int(d.width) {
			v, ok := r.u16()
			if !ok {
				return false
			}
			data := uint32(v)
			row[4*x], row[4*x+1], row[4*x+2] = f.r.read(data), f.g.read(data), f.b.read(data)
			if f.a.length != 0 {
				row[4*x+3] = f.a.read(data)
			} else {
				row[4*x+3] = 0xFF
			}
		}
		_, ok := r.take(padding)
		return ok
	})
}

// read32BitPixelData is decoder.rs:1050-1084.
func (d *icoBMP) read32BitPixelData(buf []byte) bool {
	r := d.r
	f := d.bitfields
	r.pos = d.dataOffset
	return d.withRows(buf, func(row []byte) bool {
		for x := range int(d.width) {
			data, ok := r.u32()
			if !ok {
				return false
			}
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
	bmpFormatRGBA32
	bmpFormat888
)

// readFullBytePixelData is decoder.rs:1087-1137: colours stored as bytes, blue first.
func (d *icoBMP) readFullBytePixelData(buf []byte, format bmpFullBytes) bool {
	r := d.r
	padding := uint64(0)
	if format == bmpFormatRGB24 {
		padding = (4 - uint64(d.width)*3%4) % 4
	}
	r.pos = d.dataOffset
	return d.withRows(buf, func(row []byte) bool {
		for x := range int(d.width) {
			if format == bmpFormat888 {
				if _, ok := r.u8(); !ok {
					return false
				}
			}
			bgr, ok := r.take(3)
			if !ok {
				return false
			}
			row[4*x], row[4*x+1], row[4*x+2] = bgr[2], bgr[1], bgr[0]
			if format == bmpFormatRGBA32 {
				a, ok := r.u8()
				if !ok {
					return false
				}
				row[4*x+3] = a
			} else {
				row[4*x+3] = 0xFF
			}
		}
		_, ok := r.take(padding)
		return ok
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
func (d *icoBMP) set8BitRun(pixels *bmpPixels, indices func(i int) (byte, bool), n int) bool {
	for i := 0; i < n; i++ {
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

func (d *icoBMP) set4BitRun(pixels *bmpPixels, indices func(i int) (byte, bool), n int) bool {
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
func (d *icoBMP) readRLEData(buf []byte) error {
	r := d.r
	r.pos = d.dataOffset
	corrupt := errors.New("photonimage: bmp: corrupt RLE data")

	stride := int(d.width) * 4
	order := d.rowOrder()
	nextIndex := 0
	nextRow := func() ([]byte, bool) {
		if nextIndex >= len(order) {
			return nil, false
		}
		y := order[nextIndex]
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
					// The skipped pixels become black (and transparent).
					if yDelta > 0 {
						pixels.zeroRest()
						for range int(yDelta) - 1 {
							skipped, ok := nextRow()
							if !ok {
								return corrupt
							}
							clear(skipped)
						}
						next, ok := nextRow()
						if !ok {
							return corrupt
						}
						pixels = &bmpPixels{row: next}
						for range x {
							px, ok := pixels.next()
							if !ok {
								return corrupt
							}
							clear(px)
						}
					}
					for range int(xDelta) {
						px, ok := pixels.next()
						if !ok {
							return corrupt
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
							return corrupt
						}
					} else if !d.set4BitRun(pixels, at, int(op)) {
						return corrupt
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
					return corrupt
				}
			} else if !d.set4BitRun(pixels, forever, int(control)) {
				return corrupt
			}
			x += int(control)
		}
	}
}
