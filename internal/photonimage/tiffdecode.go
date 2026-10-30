// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"unicode/utf8"
)

// DecodeTIFF returns the RGBA8 pixels Photon's `PhotonImage.new_from_byteslice(data).get_raw_pixels()` returns for a TIFF: tiff 0.9.1
// (the decoder of image 0.24.9's `TiffDecoder`, which Photon enables) over the first image of the file, as `to_rgba8` converts it.
// image's `guess_format` (io/free_functions.rs:268-312) finds a TIFF by "II*\0" or "MM\0*" (so BigTIFF, "II+\0", is no TIFF there), which
// is what Decode routes here.
//
// What the decoder accepts is what `TiffDecoder::new` and `read_image` do: 8- and 16-bit grayscale (BlackIsZero and WhiteIsZero),
// RGB and RGBA (RGB photometric with three or four samples; ExtraSamples is not read) and 8-bit CMYK (converted to RGB by image's
// cmyk_to_rgb), unsigned samples only; compression none, LZW, PackBits, Deflate (zlib, tag 8 and 0x80B2) and JPEG (tag 7, with or
// without JPEGTables); the horizontal predictor; strips and tiles, chunky or planar (the first plane only: a planar RGB image
// panics in image's `copy_from_slice`, a planar CMYK image gives the pixels of the first plane read as CMYK); both byte orders.
// Palette, bilevel, 1- to 7-bit, 12-bit, 32-bit and floating point images, YCbCr, CIELab, transparency masks, the floating point
// predictor, Fax, old-style JPEG and anything else are errors, as are the corrupt files tiff reports an error for and the images
// whose decoded size is over the 512 MiB of image's default limits (see tiffLimits).
//
// The 4 GiB address space of the wasm module, where Photon's allocation would fail first, is not emulated; the limits keep the
// memory an input can ask for to what the Rust decoder asks for.
func DecodeTIFF(data []byte) (img *image.NRGBA, err error) {
	// A Go runtime panic (an index out of range) is where the Rust decoder panics, which Photon's `unwrap` turns into an exception.
	defer func() {
		if r := recover(); r != nil {
			img, err = nil, fmt.Errorf("photonimage: tiff: decoder panic: %v", r)
		}
	}()
	if !isTIFF(data) {
		return nil, errors.New("photonimage: tiff: not a TIFF (no II*\\0 or MM\\0* signature)")
	}
	d := &tiffDecoder{data: data, little: data[0] == 'I'}
	return d.decode()
}

// isTIFF is image::guess_format's test for a TIFF: the signatures `MM\0*` and `II*\0` (io/free_functions.rs:272-273).
func isTIFF(data []byte) bool {
	return bytes.HasPrefix(data, []byte("MM\x00*")) || bytes.HasPrefix(data, []byte("II*\x00"))
}

var (
	errTIFFEOF       = errors.New("photonimage: tiff: unexpected end of file")
	errTIFFLimits    = errors.New("photonimage: tiff: limits exceeded")
	errTIFFFormat    = errors.New("photonimage: tiff: format error")
	errTIFFUnsupport = errors.New("photonimage: tiff: unsupported")
)

func tiffFormatError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errTIFFFormat, fmt.Sprintf(format, args...))
}

func tiffUnsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errTIFFUnsupport, fmt.Sprintf(format, args...))
}

// The limits of tiff 0.9.1 decoder/mod.rs `Limits::default()` as the tag reading sees them (TiffDecoder::new runs before image's
// `set_limits`), and image 0.24.9's `Limits::default().max_alloc` (io/mod.rs:63-71).
const (
	tiffDefaultDecodingBufferSize = 256 * 1024 * 1024
	tiffMaxAlloc                  = 512 * 1024 * 1024
	// tiffValueSize is `mem::size_of::<Value>()` on wasm32 (ifd.rs:20-37): the largest variant holds two 8-byte integers and the
	// enum is aligned to 8.
	tiffValueSize = 24
)

// tiffTag values of tiff 0.9.1 tags.rs.
const (
	tiffTagImageWidth                = 256
	tiffTagImageLength               = 257
	tiffTagBitsPerSample             = 258
	tiffTagCompression               = 259
	tiffTagPhotometricInterpretation = 262
	tiffTagStripOffsets              = 273
	tiffTagSamplesPerPixel           = 277
	tiffTagRowsPerStrip              = 278
	tiffTagStripByteCounts           = 279
	tiffTagPlanarConfiguration       = 284
	tiffTagPredictor                 = 317
	tiffTagTileWidth                 = 322
	tiffTagTileLength                = 323
	tiffTagTileOffsets               = 324
	tiffTagTileByteCounts            = 325
	tiffTagSampleFormat              = 339
	tiffTagJPEGTables                = 347
)

// Entry types of tags.rs `Type`.
const (
	tiffTypeByte      = 1
	tiffTypeASCII     = 2
	tiffTypeShort     = 3
	tiffTypeLong      = 4
	tiffTypeRational  = 5
	tiffTypeSByte     = 6
	tiffTypeUndefined = 7
	tiffTypeSShort    = 8
	tiffTypeSLong     = 9
	tiffTypeSRational = 10
	tiffTypeFloat     = 11
	tiffTypeDouble    = 12
	tiffTypeIFD       = 13
	tiffTypeLong8     = 16
	tiffTypeSLong8    = 17
	tiffTypeIFD8      = 18
)

// Compression methods, photometric interpretations and predictors of tags.rs.
const (
	tiffCompressionNone       = 1
	tiffCompressionLZW        = 5
	tiffCompressionModernJPEG = 7
	tiffCompressionDeflate    = 8
	tiffCompressionOldDeflate = 0x80B2
	tiffCompressionPackBits   = 0x8005

	tiffWhiteIsZero = 0
	tiffBlackIsZero = 1
	tiffRGB         = 2
	tiffCMYK        = 5
	tiffYCbCr       = 6

	tiffPredictorNone       = 1
	tiffPredictorHorizontal = 2
	tiffPredictorFloat      = 3

	tiffPlanarChunky = 1
	tiffPlanarPlanar = 2
)

// tiffDecoder is decoder/mod.rs Decoder over the in-memory input (the image crate hands it a `Cursor` over the byte slice).
type tiffDecoder struct {
	data   []byte
	little bool
}

func (d *tiffDecoder) u16(at uint64) (uint16, error) {
	if at+2 > uint64(len(d.data)) {
		return 0, errTIFFEOF
	}
	b := d.data[at:]
	if d.little {
		return uint16(b[0]) | uint16(b[1])<<8, nil
	}
	return uint16(b[1]) | uint16(b[0])<<8, nil
}

func (d *tiffDecoder) u32(at uint64) (uint32, error) {
	if at+4 > uint64(len(d.data)) {
		return 0, errTIFFEOF
	}
	return tiffU32(d.data[at:], d.little), nil
}

func tiffU32(b []byte, little bool) uint32 {
	if little {
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	}
	return uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
}

func tiffU16(b []byte, little bool) uint16 {
	if little {
		return uint16(b[0]) | uint16(b[1])<<8
	}
	return uint16(b[1]) | uint16(b[0])<<8
}

func (d *tiffDecoder) u64(at uint64) (uint64, error) {
	if at+8 > uint64(len(d.data)) {
		return 0, errTIFFEOF
	}
	b := d.data[at:]
	var v uint64
	if d.little {
		for i := 7; i >= 0; i-- {
			v = v<<8 | uint64(b[i])
		}
	} else {
		for i := range 8 {
			v = v<<8 | uint64(b[i])
		}
	}
	return v, nil
}

// tiffEntry is ifd.rs Entry of a classic TIFF: the type, the count and the four bytes of the value or its offset.
type tiffEntry struct {
	typ    uint16
	count  uint32
	offset [4]byte
}

type tiffValueKind uint8

const (
	tvByte tiffValueKind = iota
	tvShort
	tvSigned
	tvSignedBig
	tvUnsigned
	tvUnsignedBig
	tvFloat
	tvDouble
	tvList
	tvRational
	tvSRational
	tvAscii
	tvIfd
	tvIfdBig
)

// tiffValue is ifd.rs Value, kept as much as the conversions the decoder uses need: the integer and byte values, a Rational's two
// parts, an ASCII string as its code points, and for a list the kind of its elements and the values of the integer kinds (the
// others only make `into_u64_vec` and `into_u8_vec` fail, so their values are not kept).
type tiffValue struct {
	kind tiffValueKind
	u, d uint64
	// ints are the code points of an Ascii or the values of a list of integers.
	ints []uint64
	// elem is the kind of the elements of a list; n their count.
	elem tiffValueKind
	n    int
}

func (k tiffValueKind) keepsValue() bool {
	switch k {
	case tvByte, tvShort, tvUnsigned, tvUnsignedBig, tvIfd, tvIfdBig:
		return true
	}
	return false
}

// intoU16 is Value::into_u16.
func (v tiffValue) intoU16() (uint16, error) {
	switch v.kind {
	case tvShort:
		return uint16(v.u), nil
	case tvUnsigned, tvUnsignedBig:
		if v.u > 0xFFFF {
			return 0, tiffFormatError("integer out of range")
		}
		return uint16(v.u), nil
	}
	return 0, tiffFormatError("unsigned integer expected")
}

// intoU32 is Value::into_u32.
func (v tiffValue) intoU32() (uint32, error) {
	switch v.kind {
	case tvShort, tvUnsigned, tvIfd:
		return uint32(v.u), nil
	case tvUnsignedBig, tvIfdBig:
		if v.u > 0xFFFFFFFF {
			return 0, tiffFormatError("integer out of range")
		}
		return uint32(v.u), nil
	}
	return 0, tiffFormatError("unsigned integer expected")
}

// intoU64Vec is Value::into_u64_vec.
func (v tiffValue) intoU64Vec() ([]uint64, error) {
	switch v.kind {
	case tvList:
		switch v.elem {
		case tvShort, tvUnsigned, tvUnsignedBig, tvIfd, tvIfdBig:
			return v.ints, nil
		}
		if v.n == 0 {
			return nil, nil
		}
		return nil, tiffFormatError("unsigned integer expected")
	case tvUnsigned, tvUnsignedBig, tvIfd, tvIfdBig:
		return []uint64{v.u}, nil
	case tvRational:
		return []uint64{v.u, v.d}, nil
	case tvAscii:
		return v.ints, nil
	}
	return nil, tiffFormatError("unsigned integer expected")
}

// intoU8Vec is Value::into_u8_vec.
func (v tiffValue) intoU8Vec() ([]byte, error) {
	switch v.kind {
	case tvList:
		if v.elem != tvByte && v.n > 0 {
			return nil, tiffFormatError("byte expected")
		}
		out := make([]byte, len(v.ints))
		for i, x := range v.ints {
			out[i] = byte(x)
		}
		return out, nil
	case tvByte:
		return []byte{byte(v.u)}, nil
	}
	return nil, tiffFormatError("unsigned integer expected")
}

func tiffTypeSize(typ uint16) uint64 {
	switch typ {
	case tiffTypeByte, tiffTypeSByte, tiffTypeASCII, tiffTypeUndefined:
		return 1
	case tiffTypeShort, tiffTypeSShort:
		return 2
	case tiffTypeLong, tiffTypeSLong, tiffTypeFloat, tiffTypeIFD:
		return 4
	}
	return 8
}

func tiffKnownType(typ uint16) bool {
	switch typ {
	case tiffTypeByte, tiffTypeASCII, tiffTypeShort, tiffTypeLong, tiffTypeRational, tiffTypeSByte, tiffTypeUndefined, tiffTypeSShort,
		tiffTypeSLong, tiffTypeSRational, tiffTypeFloat, tiffTypeDouble, tiffTypeIFD, tiffTypeLong8, tiffTypeSLong8, tiffTypeIFD8:
		return true
	}
	return false
}

// val is ifd.rs Entry::val for a classic TIFF (the `bigtiff` branches never run: image's guess_format does not find a BigTIFF).
func (d *tiffDecoder) val(e tiffEntry) (tiffValue, error) {
	// Case 1: there are no values.
	if e.count == 0 {
		return tiffValue{kind: tvList}, nil
	}
	size := tiffTypeSize(e.typ)
	valueBytes := uint64(e.count) * size
	little := d.little

	// Case 2: there is one value.
	if e.count == 1 {
		o := e.offset[:]
		switch e.typ {
		case tiffTypeByte:
			return tiffValue{kind: tvUnsigned, u: uint64(o[0])}, nil
		case tiffTypeSByte:
			return tiffValue{kind: tvSigned}, nil
		case tiffTypeUndefined:
			return tiffValue{kind: tvByte, u: uint64(o[0])}, nil
		case tiffTypeShort:
			return tiffValue{kind: tvUnsigned, u: uint64(tiffU16(o, little))}, nil
		case tiffTypeSShort:
			return tiffValue{kind: tvSigned}, nil
		case tiffTypeLong:
			return tiffValue{kind: tvUnsigned, u: uint64(tiffU32(o, little))}, nil
		case tiffTypeSLong:
			return tiffValue{kind: tvSigned}, nil
		case tiffTypeFloat:
			return tiffValue{kind: tvFloat}, nil
		case tiffTypeASCII:
			if o[0] == 0 {
				return tiffValue{kind: tvAscii}, nil
			}
			return tiffValue{}, tiffFormatError("invalid tag")
		}
		// The 8-byte types: the value is at the offset the four bytes give.
		at := uint64(tiffU32(o, little))
		switch e.typ {
		case tiffTypeLong8:
			v, err := d.u64(at)
			return tiffValue{kind: tvUnsignedBig, u: v}, err
		case tiffTypeSLong8:
			_, err := d.u64(at)
			return tiffValue{kind: tvSignedBig}, err
		case tiffTypeDouble:
			_, err := d.u64(at)
			return tiffValue{kind: tvDouble}, err
		case tiffTypeRational:
			n, err := d.u32(at)
			if err != nil {
				return tiffValue{}, err
			}
			den, err := d.u32(at + 4)
			return tiffValue{kind: tvRational, u: uint64(n), d: uint64(den)}, err
		case tiffTypeSRational:
			if _, err := d.u32(at); err != nil {
				return tiffValue{}, err
			}
			_, err := d.u32(at + 4)
			return tiffValue{kind: tvSRational}, err
		case tiffTypeIFD:
			return tiffValue{kind: tvIfd, u: uint64(tiffU32(o, little))}, nil
		default: // IFD8
			v, err := d.u64(at)
			return tiffValue{kind: tvIfdBig, u: v}, err
		}
	}

	// Case 3: there is more than one value, but it fits in the offset field.
	if valueBytes <= 4 {
		o := e.offset[:]
		n := int(e.count)
		list := func(kind tiffValueKind, value func(i int) uint64) tiffValue {
			v := tiffValue{kind: tvList, elem: kind, n: n}
			if kind.keepsValue() {
				v.ints = make([]uint64, n)
				for i := range n {
					v.ints[i] = value(i)
				}
			}
			return v
		}
		switch e.typ {
		case tiffTypeByte:
			return list(tvUnsigned, func(i int) uint64 { return uint64(o[i]) }), nil
		case tiffTypeSByte:
			return list(tvSigned, nil), nil
		case tiffTypeASCII:
			buf := o[:n]
			if o[n-1] != 0 || !asciiOnly(buf) {
				return tiffValue{}, tiffFormatError("invalid tag")
			}
			trimmed := bytes.Trim(buf, "\x00")
			v := tiffValue{kind: tvAscii, ints: make([]uint64, len(trimmed))}
			for i, c := range trimmed {
				v.ints[i] = uint64(c)
			}
			return v, nil
		case tiffTypeUndefined:
			return list(tvByte, func(i int) uint64 { return uint64(o[i]) }), nil
		case tiffTypeShort:
			return list(tvShort, func(i int) uint64 { return uint64(tiffU16(o[2*i:], little)) }), nil
		default: // SSHORT (the other types need more than four bytes for more than one value)
			return list(tvSigned, nil), nil
		}
	}

	// Case 4: there is more than one value, and it does not fit in the offset field.
	if e.typ == tiffTypeASCII {
		if uint64(e.count) > tiffDefaultDecodingBufferSize {
			return tiffValue{}, errTIFFLimits
		}
		at := uint64(tiffU32(e.offset[:], little))
		if at+uint64(e.count) > uint64(len(d.data)) {
			return tiffValue{}, errTIFFEOF
		}
		out := d.data[at : at+uint64(e.count)]
		if i := bytes.IndexByte(out, 0); i >= 0 {
			out = out[:i]
		}
		if !utf8.Valid(out) {
			return tiffValue{}, tiffFormatError("invalid tag")
		}
		v := tiffValue{kind: tvAscii}
		for _, r := range string(out) {
			v.ints = append(v.ints, uint64(r))
		}
		return v, nil
	}
	// decode_offset (ifd.rs:419-452).
	if uint64(e.count) > tiffDefaultDecodingBufferSize/tiffValueSize {
		return tiffValue{}, errTIFFLimits
	}
	at := uint64(tiffU32(e.offset[:], little))
	if at+valueBytes > uint64(len(d.data)) {
		return tiffValue{}, errTIFFEOF
	}
	var kind tiffValueKind
	switch e.typ {
	case tiffTypeByte, tiffTypeShort, tiffTypeLong8:
		kind = tvUnsignedBig
	case tiffTypeSByte, tiffTypeSShort, tiffTypeSLong8:
		kind = tvSignedBig
	case tiffTypeLong:
		kind = tvUnsigned
	case tiffTypeSLong:
		kind = tvSigned
	case tiffTypeFloat:
		kind = tvFloat
	case tiffTypeDouble:
		kind = tvDouble
	case tiffTypeRational:
		kind = tvRational
	case tiffTypeSRational:
		kind = tvSRational
	case tiffTypeIFD:
		kind = tvIfd
	case tiffTypeIFD8:
		kind = tvIfdBig
	default: // UNDEFINED
		kind = tvByte
	}
	n := int(e.count)
	v := tiffValue{kind: tvList, elem: kind, n: n}
	if kind.keepsValue() {
		v.ints = make([]uint64, n)
		for i := range n {
			p := d.data[at+uint64(i)*size:]
			switch e.typ {
			case tiffTypeByte, tiffTypeUndefined:
				v.ints[i] = uint64(p[0])
			case tiffTypeShort:
				v.ints[i] = uint64(tiffU16(p, d.little))
			case tiffTypeLong, tiffTypeIFD:
				v.ints[i] = uint64(tiffU32(p, d.little))
			default: // LONG8, IFD8
				x, _ := d.u64(at + uint64(i)*size)
				v.ints[i] = x
			}
		}
	}
	return v, nil
}

func asciiOnly(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

// tiffDirectory is ifd.rs Directory. A tag that occurs twice keeps its last entry, as HashMap::insert does.
type tiffDirectory map[uint16]tiffEntry

// readIFD is Decoder::read_ifd (decoder/mod.rs:837-880) for a classic TIFF; it returns the directory and the offset of the next one
// (0 for none).
func (d *tiffDecoder) readIFD(at uint64) (tiffDirectory, uint32, error) {
	count, err := d.u16(at)
	if err != nil {
		return nil, 0, err
	}
	pos := at + 2
	dir := tiffDirectory{}
	for range count {
		tag, err := d.u16(pos)
		if err != nil {
			return nil, 0, err
		}
		typ, err := d.u16(pos + 2)
		if err != nil {
			return nil, 0, err
		}
		n, err := d.u32(pos + 4)
		if err != nil {
			return nil, 0, err
		}
		if pos+12 > uint64(len(d.data)) {
			return nil, 0, errTIFFEOF
		}
		// An entry of a type that is not known is skipped (its count and value are read, 8 bytes).
		if tiffKnownType(typ) {
			e := tiffEntry{typ: typ, count: n}
			copy(e.offset[:], d.data[pos+8:pos+12])
			dir[tag] = e
		}
		pos += 12
	}
	next, err := d.u32(pos)
	if err != nil {
		return nil, 0, err
	}
	return dir, next, nil
}

// tiffImage is decoder/image.rs Image.
type tiffImage struct {
	width, height uint32
	bitsPerSample uint8
	samples       uint16
	photometric   uint16
	compression   uint16
	predictor     uint16
	jpegTables    []byte
	hasJPEGTables bool
	planar        uint16

	tiled        bool
	rowsPerStrip uint32
	tileWidth    uint32
	tileLength   uint32

	chunkOffsets []uint64
	chunkBytes   []uint64
}

// find is TagReader::find_tag.
func (d *tiffDecoder) find(dir tiffDirectory, tag uint16) (tiffValue, bool, error) {
	e, ok := dir[tag]
	if !ok {
		return tiffValue{}, false, nil
	}
	v, err := d.val(e)
	return v, true, err
}

func (d *tiffDecoder) require(dir tiffDirectory, tag uint16) (tiffValue, error) {
	v, ok, err := d.find(dir, tag)
	if err != nil {
		return v, err
	}
	if !ok {
		return v, tiffFormatError("required tag %d not found", tag)
	}
	return v, nil
}

// optionalU16 is `find_tag(tag)?.map(Value::into_u16).transpose()?`.
func (d *tiffDecoder) optionalU16(dir tiffDirectory, tag uint16) (uint16, bool, error) {
	v, ok, err := d.find(dir, tag)
	if err != nil || !ok {
		return 0, false, err
	}
	x, err := v.intoU16()
	return x, err == nil, err
}

// uintVec is TagReader::find_tag_uint_vec: the values of a tag as unsigned integers of at most max.
func (d *tiffDecoder) uintVec(dir tiffDirectory, tag uint16, max uint64) ([]uint64, bool, error) {
	v, ok, err := d.find(dir, tag)
	if err != nil || !ok {
		return nil, false, err
	}
	vals, err := v.intoU64Vec()
	if err != nil {
		return nil, false, err
	}
	for _, x := range vals {
		if x > max {
			return nil, false, tiffFormatError("invalid tag value type")
		}
	}
	return vals, true, nil
}

// readImage is Image::from_reader (decoder/image.rs:81-278).
func (d *tiffDecoder) readImage(dir tiffDirectory) (*tiffImage, error) {
	img := &tiffImage{}
	w, err := d.require(dir, tiffTagImageWidth)
	if err != nil {
		return nil, err
	}
	if img.width, err = w.intoU32(); err != nil {
		return nil, err
	}
	h, err := d.require(dir, tiffTagImageLength)
	if err != nil {
		return nil, err
	}
	if img.height, err = h.intoU32(); err != nil {
		return nil, err
	}
	if img.width == 0 || img.height == 0 {
		return nil, tiffFormatError("invalid dimensions %dx%d", img.width, img.height)
	}

	photometric, ok, err := d.optionalU16(dir, tiffTagPhotometricInterpretation)
	if err != nil {
		return nil, err
	}
	switch photometric {
	case 0, 1, 2, 3, 4, 5, 6, 8:
	default:
		ok = false
	}
	if !ok {
		return nil, tiffUnsupported("unknown photometric interpretation")
	}
	img.photometric = photometric

	img.compression = tiffCompressionNone
	if c, ok, err := d.optionalU16(dir, tiffTagCompression); err != nil {
		return nil, err
	} else if ok {
		img.compression = c
	}

	if _, has := dir[tiffTagJPEGTables]; img.compression == tiffCompressionModernJPEG && has {
		v, _, err := d.find(dir, tiffTagJPEGTables)
		if err != nil {
			return nil, err
		}
		tables, err := v.intoU8Vec()
		if err != nil {
			return nil, err
		}
		if len(tables) < 2 {
			return nil, tiffFormatError("invalid JPEGTables")
		}
		img.jpegTables, img.hasJPEGTables = tables, true
	}

	img.samples = 1
	if s, ok, err := d.optionalU16(dir, tiffTagSamplesPerPixel); err != nil {
		return nil, err
	} else if ok {
		img.samples = s
	}
	if img.samples == 0 {
		return nil, tiffFormatError("samples per pixel is zero")
	}

	formats, ok, err := d.uintVec(dir, tiffTagSampleFormat, 0xFFFF)
	if err != nil {
		return nil, err
	}
	if ok {
		// Only homogeneous sample formats are supported.
		for _, f := range formats {
			if f != formats[0] {
				return nil, tiffUnsupported("inconsistent sample formats")
			}
		}
	}

	bits, ok, err := d.uintVec(dir, tiffTagBitsPerSample, 0xFF)
	if err != nil {
		return nil, err
	}
	if !ok {
		bits = []uint64{1}
	}
	if len(bits) != int(img.samples) && len(bits) != 1 {
		return nil, tiffFormatError("inconsistent sizes")
	}
	for _, b := range bits {
		if b != bits[0] {
			return nil, tiffUnsupported("inconsistent bits per sample")
		}
	}
	img.bitsPerSample = uint8(bits[0])

	img.predictor = tiffPredictorNone
	if p, ok, err := d.optionalU16(dir, tiffTagPredictor); err != nil {
		return nil, err
	} else if ok {
		if p < tiffPredictorNone || p > tiffPredictorFloat {
			return nil, tiffFormatError("unknown predictor %d", p)
		}
		img.predictor = p
	}

	img.planar = tiffPlanarChunky
	if p, ok, err := d.optionalU16(dir, tiffTagPlanarConfiguration); err != nil {
		return nil, err
	} else if ok {
		if p != tiffPlanarChunky && p != tiffPlanarPlanar {
			return nil, tiffFormatError("unknown planar configuration %d", p)
		}
		img.planar = p
	}
	planes := uint32(1)
	if img.planar == tiffPlanarPlanar {
		planes = uint32(img.samples)
	}

	_, hasStripByteCounts := dir[tiffTagStripByteCounts]
	_, hasStripOffsets := dir[tiffTagStripOffsets]
	_, hasTileByteCounts := dir[tiffTagTileByteCounts]
	_, hasTileOffsets := dir[tiffTagTileOffsets]
	switch {
	case hasStripByteCounts && hasStripOffsets && !hasTileByteCounts && !hasTileOffsets:
		if img.chunkOffsets, _, err = d.uintVec(dir, tiffTagStripOffsets, ^uint64(0)); err != nil {
			return nil, err
		}
		if img.chunkBytes, _, err = d.uintVec(dir, tiffTagStripByteCounts, ^uint64(0)); err != nil {
			return nil, err
		}
		img.rowsPerStrip = img.height
		if v, ok, err := d.find(dir, tiffTagRowsPerStrip); err != nil {
			return nil, err
		} else if ok {
			if img.rowsPerStrip, err = v.intoU32(); err != nil {
				return nil, err
			}
		}
		// u32 arithmetic of a wasm32 build without overflow checks.
		if len(img.chunkOffsets) != len(img.chunkBytes) || img.rowsPerStrip == 0 ||
			uint32(len(img.chunkOffsets)) != (satSub32(img.height, 1)/img.rowsPerStrip+1)*planes {
			return nil, tiffFormatError("inconsistent sizes")
		}
	case !hasStripByteCounts && !hasStripOffsets && hasTileByteCounts && hasTileOffsets:
		img.tiled = true
		v, err := d.require(dir, tiffTagTileWidth)
		if err != nil {
			return nil, err
		}
		if img.tileWidth, err = v.intoU32(); err != nil {
			return nil, err
		}
		v, err = d.require(dir, tiffTagTileLength)
		if err != nil {
			return nil, err
		}
		if img.tileLength, err = v.intoU32(); err != nil {
			return nil, err
		}
		if img.tileWidth == 0 || img.tileLength == 0 {
			return nil, tiffFormatError("invalid tile size")
		}
		if img.chunkOffsets, _, err = d.uintVec(dir, tiffTagTileOffsets, ^uint64(0)); err != nil {
			return nil, err
		}
		if img.chunkBytes, _, err = d.uintVec(dir, tiffTagTileByteCounts, ^uint64(0)); err != nil {
			return nil, err
		}
		if len(img.chunkOffsets) != len(img.chunkBytes) || uint32(len(img.chunkOffsets)) != img.tilesDown()*img.tilesAcross()*planes {
			return nil, tiffFormatError("inconsistent sizes")
		}
	default:
		return nil, tiffFormatError("strip and tile tags conflict")
	}
	return img, nil
}

func satSub32(a, b uint32) uint32 {
	if a < b {
		return 0
	}
	return a - b
}

// tilesAcross, tilesDown, paddingRight and paddingDown are image.rs TileAttributes, in the u32 `usize` of wasm32.
func (img *tiffImage) tilesAcross() uint32 { return (img.width + img.tileWidth - 1) / img.tileWidth }
func (img *tiffImage) tilesDown() uint32   { return (img.height + img.tileLength - 1) / img.tileLength }

func (img *tiffImage) padding(tile uint32) (right, down uint32) {
	across, downCount := img.tilesAcross(), img.tilesDown()
	if across == 0 {
		panic("attempt to divide by zero")
	}
	row, column := tile/across, tile%across
	if column == across-1 {
		right = (img.tileWidth - img.width%img.tileWidth) % img.tileWidth
	}
	if row == downCount-1 {
		down = (img.tileLength - img.height%img.tileLength) % img.tileLength
	}
	return right, down
}

// tiffColor is the colour types of tiff 0.9.1 ColorType that image's TiffDecoder::new accepts, with image's ColorType for them.
type tiffColor uint8

const (
	tiffGray tiffColor = iota
	tiffRGBColor
	tiffRGBAColor
	tiffCMYKColor
	tiffYCbCrColor
)

// colortype is Image::colortype (decoder/image.rs:280-348).
func (img *tiffImage) colortype() (tiffColor, error) {
	switch img.photometric {
	case tiffRGB:
		switch img.samples {
		case 3:
			return tiffRGBColor, nil
		case 4:
			return tiffRGBAColor, nil
		}
	case tiffCMYK:
		if img.samples == 4 {
			return tiffCMYKColor, nil
		}
	case tiffYCbCr:
		if img.samples == 3 {
			return tiffYCbCrColor, nil
		}
	case tiffBlackIsZero, tiffWhiteIsZero:
		if img.samples == 1 {
			return tiffGray, nil
		}
	}
	return 0, tiffUnsupported("photometric interpretation %d with %d samples", img.photometric, img.samples)
}

// tiffLimits is what image's `load_inner` and `TiffDecoder::set_limits` (codecs/tiff.rs:208-228) hand the tiff decoder for an
// image of totalBytes bytes of its colour type: the reservation of `total_bytes` in the 512 MiB of `Limits::default()` (the rest is
// maxAlloc), tiff's `decoding_buffer_size` (what `read_image` may allocate) and `intermediate_buffer_size` (the largest compressed chunk).
type tiffLimits struct {
	decodingBufferSize     uint64
	intermediateBufferSize uint64
}

func (d *tiffDecoder) decode() (*image.NRGBA, error) {
	// Decoder::new (decoder/mod.rs:512-596): the byte order, the version (42 by the signature), the offset of the first IFD.
	first, err := d.u32(4)
	if err != nil {
		return nil, err
	}
	dir, next, err := d.readIFD(uint64(first))
	if err != nil {
		return nil, err
	}
	// next_ifd: an offset that was seen (the first IFD's) is a cycle.
	if next != 0 && next == first {
		return nil, tiffFormatError("cycle in IFD offsets")
	}
	img, err := d.readImage(dir)
	if err != nil {
		return nil, err
	}

	// TiffDecoder::new (codecs/tiff.rs:34-92).
	color, err := img.colortype()
	if err != nil {
		return nil, err
	}
	formats, ok, err := d.uintVec(dir, tiffTagSampleFormat, 0xFFFF)
	if err != nil {
		return nil, err
	}
	if ok {
		for _, f := range formats {
			switch f {
			case 1:
			case 2, 3, 4:
				return nil, tiffUnsupported("sample format %d", f)
			default:
				return nil, tiffFormatError("unknown sample format %d", f)
			}
		}
	}
	bps := img.bitsPerSample
	switch color {
	case tiffGray, tiffRGBColor, tiffRGBAColor:
		if bps != 8 && bps != 16 {
			return nil, tiffUnsupported("%d bits per sample", bps)
		}
	case tiffCMYKColor:
		if bps != 8 {
			return nil, tiffUnsupported("CMYK with %d bits per sample", bps)
		}
	default:
		return nil, tiffUnsupported("YCbCr")
	}
	return d.readPixels(img, color)
}

func (c tiffColor) channels() uint64 {
	switch c {
	case tiffGray:
		return 1
	case tiffRGBColor, tiffCMYKColor:
		return 3 // image's colour type of CMYK is Rgb8
	}
	return 4
}

// readPixels is image's load_inner limits, `decoder_to_image`, the tiff decoder's `read_image` and `to_rgba8`.
func (d *tiffDecoder) readPixels(img *tiffImage, color tiffColor) (*image.NRGBA, error) {
	width, height := uint64(img.width), uint64(img.height)
	bytesPerSample := uint64(1)
	if img.bitsPerSample == 16 {
		bytesPerSample = 2
	}
	totalBytes := width * height * color.channels() * bytesPerSample
	if totalBytes > tiffMaxAlloc {
		return nil, fmt.Errorf("%w: image exceeds the 512 MiB allocation limit of image 0.24.9", errTIFFLimits)
	}
	// set_limits: total_bytes_buffer counts a CMYK pixel as 16 bytes.
	totalBytesBuffer := totalBytes
	if color == tiffCMYKColor {
		totalBytesBuffer = width * height * 16
	}
	maxAlloc := tiffMaxAlloc - totalBytes
	maxIntermediate := uint64(0)
	if maxAlloc > totalBytesBuffer {
		maxIntermediate = maxAlloc - totalBytesBuffer
	}
	limits := tiffLimits{decodingBufferSize: maxAlloc - maxIntermediate, intermediateBufferSize: maxIntermediate}

	pixels, samplesPerPixel, err := d.readTIFFImage(img, &limits)
	if err != nil {
		return nil, err
	}

	// Image's read_image (codecs/tiff.rs:255-300) puts the decoded buffer in one of total_bytes bytes: `copy_from_slice` panics when
	// the lengths differ (a planar image of more than one sample), and a CMYK buffer is converted four bytes at a time.
	out := image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
	count := int(width * height)
	if color == tiffCMYKColor {
		rgb := make([]byte, 3*count)
		w := 0
		for i := 0; i+4 <= len(pixels); i += 4 {
			r, g, b := tiffCMYKToRGB(pixels[i], pixels[i+1], pixels[i+2], pixels[i+3])
			rgb[w], rgb[w+1], rgb[w+2] = r, g, b
			w += 3
		}
		for i := range count {
			out.Pix[4*i], out.Pix[4*i+1], out.Pix[4*i+2], out.Pix[4*i+3] = rgb[3*i], rgb[3*i+1], rgb[3*i+2], 255
		}
		return out, nil
	}
	if uint64(len(pixels)) != totalBytes || samplesPerPixel != int(color.channels()) {
		return nil, errors.New("photonimage: tiff: decoded buffer does not have the size of the image (`copy_from_slice` panics)")
	}
	pix := out.Pix
	switch {
	case color == tiffGray && bytesPerSample == 1:
		for i, v := range pixels {
			pix[4*i], pix[4*i+1], pix[4*i+2], pix[4*i+3] = v, v, v, 255
		}
	case color == tiffGray:
		for i := range count {
			v := tiffDownscale16(pixels[2*i], pixels[2*i+1])
			pix[4*i], pix[4*i+1], pix[4*i+2], pix[4*i+3] = v, v, v, 255
		}
	case color == tiffRGBColor && bytesPerSample == 1:
		for i := range count {
			pix[4*i], pix[4*i+1], pix[4*i+2], pix[4*i+3] = pixels[3*i], pixels[3*i+1], pixels[3*i+2], 255
		}
	case color == tiffRGBColor:
		for i := range count {
			for c := range 3 {
				pix[4*i+c] = tiffDownscale16(pixels[6*i+2*c], pixels[6*i+2*c+1])
			}
			pix[4*i+3] = 255
		}
	case bytesPerSample == 1:
		copy(pix, pixels)
	default:
		for i := range 4 * count {
			pix[i] = tiffDownscale16(pixels[2*i], pixels[2*i+1])
		}
	}
	return out, nil
}

// tiffDownscale16 is image color.rs:408-420 FromPrimitive<u16> for u8, `(c + 128) / 257`, for the little endian sample lo, hi.
func tiffDownscale16(lo, hi byte) byte {
	return byte((uint32(lo) | uint32(hi)<<8 + 128) / 257)
}

// tiffCMYKToRGB is image codecs/tiff.rs:308-318 cmyk_to_rgb, in f32.
func tiffCMYKToRGB(c, m, y, k byte) (r, g, b byte) {
	kf := float32(1) - float32(float32(k)/float32(255))
	r = byte(float32(float32(255)-float32(c)) * kf)
	g = byte(float32(float32(255)-float32(m)) * kf)
	b = byte(float32(float32(255)-float32(y)) * kf)
	return r, g, b
}

// readTIFFImage is Decoder::read_image (decoder/mod.rs:1107-1180). The samples come back as bytes in the order of the host (little
// endian for 16-bit samples) and samplesPerPixel is the count of samples of each pixel in them (1 for a planar image).
func (d *tiffDecoder) readTIFFImage(img *tiffImage, limits *tiffLimits) (pixels []byte, samplesPerPixel int, err error) {
	samplesPerPixel = int(img.samples)
	if img.planar == tiffPlanarPlanar {
		samplesPerPixel = 1
	}
	byteLen := 1
	if img.bitsPerSample == 16 {
		byteLen = 2
	}

	// result_buffer: the element count in `usize` (u32), and the limit of the decoding buffer.
	elements := uint64(img.width) * uint64(img.height)
	if elements > 0xFFFFFFFF {
		return nil, 0, errTIFFLimits
	}
	elements *= uint64(samplesPerPixel)
	if elements > 0xFFFFFFFF {
		return nil, 0, errTIFFLimits
	}
	limitUsize := limits.decodingBufferSize
	if limitUsize > 0xFFFFFFFF {
		limitUsize = 0xFFFFFFFF
	}
	if elements > limitUsize/uint64(byteLen) {
		return nil, 0, errTIFFLimits
	}
	result := make([]byte, int(elements)*byteLen)

	chunkW, chunkH := img.chunkDimensions()
	chunkW, chunkH = min(chunkW, img.width), min(chunkH, img.height)
	if chunkW == 0 || chunkH == 0 {
		return nil, 0, tiffFormatError("inconsistent sizes")
	}
	width := int(img.width)
	chunksAcross := int((img.width-1)/chunkW + 1)
	stripSamples := uint32(width) * chunkH * uint32(samplesPerPixel)

	stripsPerPixel := 1
	if img.planar == tiffPlanarPlanar {
		stripsPerPixel = int(img.samples)
	}
	imageChunks := len(img.chunkOffsets) / stripsPerPixel
	for chunk := range imageChunks {
		x, y := chunk%chunksAcross, chunk/chunksAcross
		offset := uint32(y)*stripSamples + uint32(x)*chunkW*uint32(samplesPerPixel)
		if int(offset)*byteLen > len(result) {
			return nil, 0, errors.New("photonimage: tiff: chunk offset past the buffer (slice index panics)")
		}
		if err := d.expandChunk(img, result[int(offset)*byteLen:], width, samplesPerPixel, byteLen, uint32(chunk), limits); err != nil {
			return nil, 0, err
		}
	}
	return result, samplesPerPixel, nil
}

// chunkDimensions is Image::chunk_dimensions.
func (img *tiffImage) chunkDimensions() (uint32, uint32) {
	if img.tiled {
		return img.tileWidth, img.tileLength
	}
	return img.width, img.rowsPerStrip
}

// chunkDataDimensions is Image::chunk_data_dimensions: the size of the data of a chunk without its padding.
func (img *tiffImage) chunkDataDimensions(chunk uint32) (uint32, uint32, error) {
	w, h := img.chunkDimensions()
	if !img.tiled {
		stripsPerBand := satSub32(img.height, 1)/img.rowsPerStrip + 1
		product := uint64(chunk%stripsPerBand) * uint64(h)
		if product > 0xFFFFFFFF || uint32(product) > img.height {
			return 0, 0, errors.New("photonimage: tiff: invalid chunk index")
		}
		return w, min(h, img.height-uint32(product)), nil
	}
	right, down := img.padding(chunk)
	return w - right, h - down, nil
}

// expandChunk is Image::expand_chunk (decoder/image.rs:433-577): the samples of one strip or tile into buf, which starts where the
// chunk's first sample goes in the image; outputWidth is the width of the image in pixels.
func (d *tiffDecoder) expandChunk(img *tiffImage, buf []byte, outputWidth, samples, byteLen int, chunk uint32, limits *tiffLimits) error {
	// The buffer types Rust gives always agree with the colour type's bit depth here, and the predictor must suit the integer samples.
	if img.predictor == tiffPredictorFloat {
		return tiffUnsupported("floating point predictor")
	}
	compressed := img.chunkBytes[chunk]
	if compressed > limits.intermediateBufferSize {
		return errTIFFLimits
	}

	chunkW, _ := img.chunkDimensions()
	dataW, dataH, err := img.chunkDataDimensions(chunk)
	if err != nil {
		return err
	}
	paddingRight := chunkW - dataW

	r, err := d.createReader(img, img.chunkOffsets[chunk], compressed)
	if err != nil {
		return err
	}
	whiteIsZero := img.photometric == tiffWhiteIsZero

	if outputWidth == int(dataW) && paddingRight == 0 {
		total := int(dataW) * int(dataH) * samples
		tile := buf[:total*byteLen]
		if err := tiffReadExact(r, tile); err != nil {
			return err
		}
		for row := range int(dataH) {
			start, end := row*outputWidth*samples, (row+1)*outputWidth*samples
			d.fixEndiannessAndPredict(buf[start*byteLen:end*byteLen], samples, byteLen, img.predictor)
		}
		if whiteIsZero {
			tiffInvert(buf[:total*byteLen], byteLen)
		}
		return nil
	}
	for row := range int(dataH) {
		start := row * outputWidth * samples
		end := start + int(dataW)*samples
		rowBytes := buf[start*byteLen : end*byteLen]
		if err := tiffReadExact(r, rowBytes); err != nil {
			return err
		}
		// Skip the horizontal padding.
		if paddingRight > 0 {
			skip := uint32(paddingRight) * uint32(samples) * uint32(byteLen)
			if err := tiffSkip(r, uint64(skip)); err != nil {
				return err
			}
		}
		d.fixEndiannessAndPredict(rowBytes, samples, byteLen, img.predictor)
		if whiteIsZero {
			tiffInvert(rowBytes, byteLen)
		}
	}
	return nil
}

// fixEndiannessAndPredict is mod.rs fix_endianness_and_predict for the integer sample types: the samples of a row from the file's
// byte order to the host's (little endian), then the horizontal predictor undone.
func (d *tiffDecoder) fixEndiannessAndPredict(row []byte, samples, byteLen int, predictor uint16) {
	if byteLen == 2 && !d.little {
		for i := 0; i+1 < len(row); i += 2 {
			row[i], row[i+1] = row[i+1], row[i]
		}
	}
	if predictor != tiffPredictorHorizontal {
		return
	}
	// rev_hpredict_nsamp: every sample past the first pixel adds the same channel of the pixel before it (wrapping).
	if byteLen == 1 {
		for col := samples; col < len(row); col++ {
			row[col] += row[col-samples]
		}
		return
	}
	for col := samples; col < len(row)/2; col++ {
		sum := uint16(row[2*col]) | uint16(row[2*col+1])<<8
		sum += uint16(row[2*(col-samples)]) | uint16(row[2*(col-samples)+1])<<8
		row[2*col], row[2*col+1] = byte(sum), byte(sum>>8)
	}
}

// tiffInvert is mod.rs invert_colors for Gray(8) and Gray(16): max - sample.
func tiffInvert(buf []byte, byteLen int) {
	if byteLen == 1 {
		for i := range buf {
			buf[i] = 0xFF - buf[i]
		}
		return
	}
	for i := 0; i+1 < len(buf); i += 2 {
		sample := uint16(buf[i]) | uint16(buf[i+1])<<8
		sample = 0xFFFF - sample
		buf[i], buf[i+1] = byte(sample), byte(sample>>8)
	}
}

// tiffByteReader is the `Read` of the readers create_reader makes: it fills p as far as it can and returns the count, 0 at the end of
// the data (Ok(0) in Rust), or an error.
type tiffByteReader interface {
	read(p []byte) (int, error)
}

// tiffReadExact is Read::read_exact.
func tiffReadExact(r tiffByteReader, p []byte) error {
	for len(p) > 0 {
		n, err := r.read(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return errTIFFEOF
		}
		p = p[n:]
	}
	return nil
}

// tiffSkip is `io::copy(&mut reader.by_ref().take(n), &mut io::sink())`: it reads n bytes through an 8 KiB buffer and stops without an
// error at the end of the data.
func tiffSkip(r tiffByteReader, n uint64) error {
	var scratch [8192]byte
	for n > 0 {
		chunk := scratch[:min(n, uint64(len(scratch)))]
		got, err := r.read(chunk)
		if err != nil {
			return err
		}
		if got == 0 {
			return nil
		}
		n -= uint64(got)
	}
	return nil
}

// tiffRawReader reads the file from an offset on: the reader of uncompressed data (which does not stop at the chunk's byte count)
// and of the Deflate input and a decoded JPEG.
type tiffRawReader struct {
	data []byte
	pos  int
}

func (r *tiffRawReader) read(p []byte) (int, error) {
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// createReader is Image::create_reader (decoder/image.rs:350-431): the reader of the chunk at offset, whose compressed size is length.
func (d *tiffDecoder) createReader(img *tiffImage, offset, length uint64) (tiffByteReader, error) {
	// The reader of the file, seeked to offset: the data from there (empty past the end).
	var rest []byte
	if offset < uint64(len(d.data)) {
		rest = d.data[offset:]
	}
	// take is `reader.take(length)`: what the file has of the next length bytes.
	take := rest
	if length < uint64(len(take)) {
		take = take[:length]
	}
	switch img.compression {
	case tiffCompressionNone:
		return &tiffRawReader{data: rest}, nil
	case tiffCompressionLZW:
		return newTIFFLZWReader(take), nil
	case tiffCompressionPackBits:
		return &tiffPackBitsReader{data: take, limit: length}, nil
	case tiffCompressionDeflate, tiffCompressionOldDeflate:
		return newTIFFInflater(rest), nil
	case tiffCompressionModernJPEG:
		var stream []byte
		if img.hasJPEGTables {
			if length < 2 {
				return nil, tiffFormatError("invalid JPEGTables")
			}
			// The first two bytes of the chunk (its SOI) are dropped, and the tables lose their last two (EOI).
			if len(take) < 2 {
				return nil, errTIFFEOF
			}
			tables := img.jpegTables
			stream = append(append(make([]byte, 0, len(tables)-2+len(take)-2), tables[:len(tables)-2]...), take[2:]...)
		} else {
			stream = take
		}
		pixels, err := decodeJPEGForTIFF(stream, img.photometric)
		if err != nil {
			return nil, err
		}
		return &tiffRawReader{data: pixels}, nil
	}
	return nil, tiffUnsupported("compression method %d", img.compression)
}

// tiffPackBitsReader is stream.rs:159-216 PackBitsReader over `reader.take(length)`: data is what the file has of those bytes and
// limit what is left of length.
type tiffPackBitsReader struct {
	data  []byte
	pos   int
	limit uint64
	// state: 0 header, 1 literal, 2 repeat.
	state int
	count int
	value byte
}

func (r *tiffPackBitsReader) take(p []byte) int {
	if r.limit == 0 {
		return 0
	}
	n := copy(p[:min(uint64(len(p)), r.limit)], r.data[r.pos:])
	r.pos += n
	r.limit -= uint64(n)
	return n
}

func (r *tiffPackBitsReader) readByte() (byte, error) {
	var b [1]byte
	if r.limit == 0 || r.take(b[:]) == 0 {
		return 0, errTIFFEOF
	}
	return b[0], nil
}

func (r *tiffPackBitsReader) read(p []byte) (int, error) {
	for r.state == 0 {
		if r.limit == 0 {
			return 0, nil
		}
		header, err := r.readByte()
		if err != nil {
			return 0, err
		}
		h := int8(header)
		switch {
		case h >= -127 && h <= -1:
			value, err := r.readByte()
			if err != nil {
				return 0, err
			}
			r.state, r.value, r.count = 2, value, 1-int(h)
		case h >= 0:
			r.state, r.count = 1, int(h)+1
		}
		// h = -128 is a no-op.
	}
	length := min(len(p), r.count)
	var actual int
	if r.state == 1 {
		actual = r.take(p[:length])
	} else {
		for i := range length {
			p[i] = r.value
		}
		actual = length
	}
	r.count -= actual
	if r.count == 0 {
		r.state = 0
	}
	return actual, nil
}
