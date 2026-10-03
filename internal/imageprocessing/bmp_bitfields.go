package imageprocessing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"math/bits"
)

// DecodeImage decodes the bytes Pi hands to Photon's PhotonImage.new_from_byteslice. Photon 0.3.4 decodes with the image 0.24.9 crate, which also accepts 16-bit BMPs and BI_BITFIELDS BMPs under BITMAPINFOHEADER, BITMAPV2INFOHEADER and BITMAPV3INFOHEADER. golang.org/x/image/bmp rejects those layouts, so they fall back to decodeBitfieldBMP. Windows places such bitmaps on the clipboard as CF_DIB for Print Screen and GDI captures.
// upstream: packages/coding-agent/src/utils/image-convert.ts:convertImageBytesToPng
func DecodeImage(data []byte) (image.Image, error) {
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err == nil {
		return decoded, nil
	}
	fallback, fallbackErr := decodeBitfieldBMP(data)
	if fallbackErr != nil {
		return nil, err
	}
	return fallback, nil
}

// bmpChannel is the image crate's Bitfield: a channel wider than eight bits keeps only its eight most significant bits.
type bmpChannel struct {
	shift uint
	len   uint
}

// newBMPChannel mirrors the image crate's Bitfield::from_mask. A zero mask yields an absent channel. A non-contiguous mask, or one that extends past the pixel's maxLen bits, is invalid.
func newBMPChannel(mask uint32, maxLen uint) (bmpChannel, error) {
	if mask == 0 {
		return bmpChannel{}, nil
	}
	shift := uint(bits.TrailingZeros32(mask))
	length := uint(bits.TrailingZeros32(^(mask >> shift)))
	if length != uint(bits.OnesCount32(mask)) || length+shift > maxLen {
		return bmpChannel{}, errUnsupportedBMP
	}
	if length > 8 {
		shift += length - 8
		length = 8
	}
	return bmpChannel{shift: shift, len: length}, nil
}

// scale mirrors the image crate's Bitfield::read, which expands a channel of one to seven bits to the nearest eight-bit value. Its three- to six-bit lookup tables and seven-bit replication equal this rounding.
func (c bmpChannel) scale(pixel uint32) uint8 {
	maxValue := uint32(1)<<c.len - 1
	return uint8(((pixel>>c.shift&maxValue)*255 + maxValue/2) / maxValue)
}

var errUnsupportedBMP = errors.New("unsupported BMP")

// decodeBitfieldBMP decodes uncompressed 16-bit BI_RGB (5-5-5) and 16/32-bit BI_BITFIELDS bitmaps as the image crate does. The BITMAPV3INFOHEADER and later headers carry an alpha mask; a missing alpha mask yields opaque pixels.
func decodeBitfieldBMP(data []byte) (image.Image, error) {
	const fileHeaderSize, infoHeaderSize = 14, 40
	if len(data) < fileHeaderSize+infoHeaderSize || data[0] != 'B' || data[1] != 'M' {
		return nil, errUnsupportedBMP
	}
	pixelOffset := int64(binary.LittleEndian.Uint32(data[10:]))
	headerSize := binary.LittleEndian.Uint32(data[14:])
	width := int64(int32(binary.LittleEndian.Uint32(data[18:])))
	height := int64(int32(binary.LittleEndian.Uint32(data[22:])))
	planes := binary.LittleEndian.Uint16(data[26:])
	depth := binary.LittleEndian.Uint16(data[28:])
	compression := binary.LittleEndian.Uint32(data[30:])
	topDown := height < 0
	if topDown {
		height = -height
	}
	if width <= 0 || height <= 0 || width > 1<<20 || height > 1<<20 || planes != 1 {
		return nil, errUnsupportedBMP
	}
	maskCount := 3
	switch headerSize {
	case infoHeaderSize, 52:
	case 56, 108, 124:
		maskCount = 4
	default:
		return nil, errUnsupportedBMP
	}
	masks := [4]uint32{0x7c00, 0x03e0, 0x001f, 0}
	switch {
	case depth == 16 && compression == 0:
	case (depth == 16 || depth == 32) && compression == 3:
		if len(data) < fileHeaderSize+infoHeaderSize+maskCount*4 {
			return nil, errUnsupportedBMP
		}
		masks = [4]uint32{}
		for i := range maskCount {
			masks[i] = binary.LittleEndian.Uint32(data[fileHeaderSize+infoHeaderSize+i*4:])
		}
	default:
		return nil, errUnsupportedBMP
	}
	var channels [4]bmpChannel
	for i, mask := range masks {
		channel, err := newBMPChannel(mask, uint(depth))
		if err != nil || (i < 3 && channel.len == 0) {
			return nil, errUnsupportedBMP
		}
		channels[i] = channel
	}
	hasAlpha := channels[3].len != 0
	stride := (width*int64(depth) + 31) / 32 * 4
	if pixelOffset < fileHeaderSize+infoHeaderSize || pixelOffset+stride*height > int64(len(data)) {
		return nil, errUnsupportedBMP
	}
	out := image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := range int(height) {
		row := data[pixelOffset+stride*int64(y):]
		dy := int(height) - 1 - y
		if topDown {
			dy = y
		}
		for x := range int(width) {
			var pixel uint32
			if depth == 16 {
				pixel = uint32(binary.LittleEndian.Uint16(row[x*2:]))
			} else {
				pixel = binary.LittleEndian.Uint32(row[x*4:])
			}
			a := uint8(255)
			if hasAlpha {
				a = channels[3].scale(pixel)
			}
			offset := dy*out.Stride + x*4
			out.Pix[offset], out.Pix[offset+1], out.Pix[offset+2], out.Pix[offset+3] = channels[0].scale(pixel), channels[1].scale(pixel), channels[2].scale(pixel), a
		}
	}
	return out, nil
}
