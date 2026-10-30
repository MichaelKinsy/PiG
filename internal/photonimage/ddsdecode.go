// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
)

const ddsMagic = "DDS "

// isDDS is image::guess_format's test for a DDS file: the signature "DDS " (io/free_functions.rs:274).
func isDDS(data []byte) bool {
	return bytes.HasPrefix(data, []byte(ddsMagic))
}

// ddsVariant is dxt.rs `DxtVariant`.
type ddsVariant int

const (
	ddsDXT1 ddsVariant = iota
	ddsDXT3
	ddsDXT5
)

// encodedBytesPerBlock and channels are dxt.rs:43-56: DXT1 is Rgb8 in 8-byte blocks, DXT3 and DXT5 Rgba8 in 16-byte blocks.
func (v ddsVariant) encodedBytesPerBlock() int {
	if v == ddsDXT1 {
		return 8
	}
	return 16
}

func (v ddsVariant) channels() int {
	if v == ddsDXT1 {
		return 3
	}
	return 4
}

var errDDS = errors.New("photonimage: dds: invalid or truncated file")

// DecodeDDS returns the RGBA8 pixels Photon's `PhotonImage.new_from_byteslice(data).get_raw_pixels()` returns for a DDS file: image
// 0.24.9's DdsDecoder (codecs/dds.rs) over its DxtDecoder (codecs/dxt.rs), as `to_rgba8` converts the Rgb8 (DXT1) or Rgba8 (DXT3,
// DXT5) it reports. Only the first level of DXT1, DXT3 and DXT5 (and BC1, BC2 and BC3 through the DX10 header) is read; any other
// pixel format, FourCC or DXGI format is an error, as are a width or height that is not a multiple of four (zero is one), a file without
// every block of the first level, an image of more than 2^29 bytes (imageMaxAlloc), and an empty image whose RGBA buffer length overflows
// wasm32's `usize` (checkRGBAConversion).
func DecodeDDS(data []byte) (*image.NRGBA, error) {
	if !isDDS(data) {
		return nil, errors.New("photonimage: dds: not a DDS file (no \"DDS \" signature)")
	}
	r := data[4:]
	u32 := func() (uint32, bool) {
		if len(r) < 4 {
			return 0, false
		}
		v := binary.LittleEndian.Uint32(r)
		r = r[4:]
		return v, true
	}
	skip := func(n int) bool {
		if len(r) < n {
			return false
		}
		r = r[n:]
		return true
	}

	// Header::from_reader (dds.rs:141-185).
	size, ok := u32()
	if !ok || size != 124 {
		return nil, errDDS
	}
	const requiredFlags, validFlags = 0x1 | 0x2 | 0x4 | 0x1000, 0x1 | 0x2 | 0x4 | 0x8 | 0x1000 | 0x20000 | 0x80000 | 0x800000
	flags, ok := u32()
	if !ok || flags&(requiredFlags|^uint32(validFlags)) != requiredFlags {
		return nil, errDDS
	}
	height, ok1 := u32()
	width, ok2 := u32()
	if !ok1 || !ok2 || !skip(4+4+4+4*11) { // pitch or linear size, depth, mipmap count, dwReserved1
		return nil, errDDS
	}
	// PixelFormat::from_reader (dds.rs:118-137).
	pfSize, ok := u32()
	if !ok || pfSize != 32 {
		return nil, errDDS
	}
	pfFlags, ok := u32()
	if !ok || len(r) < 4 {
		return nil, errDDS
	}
	fourCC := string(r[:4])
	r = r[4:]
	if !skip(4*5) || !skip(4+4+12) { // bit count and four masks; caps, caps2, dwCaps3, dwCaps4, dwReserved2
		return nil, errDDS
	}

	if pfFlags&0x4 == 0 {
		return nil, errors.New("photonimage: dds: only DXT variants are supported")
	}
	var variant ddsVariant
	switch fourCC {
	case "DXT1":
		variant = ddsDXT1
	case "DXT3":
		variant = ddsDXT3
	case "DXT5":
		variant = ddsDXT5
	case "DX10":
		// DX10Header::from_reader and validate (dds.rs:188-238).
		var dx [5]uint32
		for i := range dx {
			if dx[i], ok = u32(); !ok {
				return nil, errDDS
			}
		}
		dxgiFormat, resourceDimension, miscFlag, arraySize, miscFlags2 := dx[0], dx[1], dx[2], dx[3], dx[4]
		if dxgiFormat > 132 || resourceDimension < 2 || resourceDimension > 4 || (miscFlag != 0 && miscFlag != 4) ||
			(resourceDimension == 4 && arraySize != 1) || miscFlags2 > 4 {
			return nil, errors.New("photonimage: dds: invalid DX10 header")
		}
		switch {
		case dxgiFormat >= 70 && dxgiFormat <= 72:
			variant = ddsDXT1
		case dxgiFormat >= 73 && dxgiFormat <= 75:
			variant = ddsDXT3
		case dxgiFormat >= 76 && dxgiFormat <= 78:
			variant = ddsDXT5
		default:
			return nil, errors.New("photonimage: dds: unsupported DXGI format")
		}
	default:
		return nil, errors.New("photonimage: dds: unsupported FourCC")
	}

	// DdsDecoder::new: check_dimension_overflow, then DxtDecoder::new wants multiples of four (dxt.rs:82-89).
	if err := reserveTotalBytes(width, height, uint64(variant.channels())); err != nil {
		return nil, err
	}
	if width%4 != 0 || height%4 != 0 {
		return nil, errors.New("photonimage: dds: width and height must be multiples of 4")
	}
	if err := checkRGBAConversion(width, height, uint64(variant.channels())); err != nil {
		return nil, err
	}

	widthBlocks, heightBlocks := uint64(width/4), uint64(height/4)
	// read_image reads a row of blocks at a time (dxt.rs:101-120, :151-159) and an empty image reads none.
	rowBytes := uint64(variant.encodedBytesPerBlock()) * widthBlocks
	if widthBlocks == 0 || heightBlocks == 0 {
		return newImageNRGBA(width, height), nil
	}
	if uint64(len(r)) < rowBytes*heightBlocks {
		return nil, errDDS
	}
	out := newImageNRGBA(width, height)
	for blockRow := range heightBlocks {
		src := r[blockRow*rowBytes : (blockRow+1)*rowBytes]
		for x := range int(widthBlocks) {
			ddsDecodeBlock(variant, src[x*variant.encodedBytesPerBlock():], out, x*4, int(blockRow)*4)
		}
	}
	return out, nil
}

// ddsDecodeBlock is decode_dxt1_block, decode_dxt3_block and decode_dxt5_block (dxt.rs:366-410) with the copy of decode_dxt*_row, writing
// the 16 pixels of the block at (x0, y0) as RGBA8; DXT1's opaque alpha is `to_rgba8` of Rgb8.
func ddsDecodeBlock(variant ddsVariant, block []byte, out *image.NRGBA, x0, y0 int) {
	var alpha [16]byte
	colors := block
	switch variant {
	case ddsDXT1:
		for i := range alpha {
			alpha[i] = 255
		}
	case ddsDXT3:
		// Four-bit alpha values scaled by 0x11 (dxt.rs:388-404).
		table := binary.LittleEndian.Uint64(block[:8])
		for i := range alpha {
			alpha[i] = byte(table>>(4*i)&0xF) * 0x11
		}
		colors = block[8:]
	case ddsDXT5:
		// A table of eight alphas and three-bit indices (dxt.rs:305-319, :366-385).
		var raw [8]byte
		copy(raw[:], block[2:8])
		table := binary.LittleEndian.Uint64(raw[:])
		alphas := ddsAlphaTable(block[0], block[1])
		for i := range alpha {
			alpha[i] = alphas[table>>(3*i)&7]
		}
		colors = block[8:]
	}
	palette := ddsColors(colors, variant == ddsDXT1)
	indices := binary.LittleEndian.Uint32(colors[4:8])
	for i := range 16 {
		c := palette[indices>>(2*i)&3]
		o := out.PixOffset(x0+i%4, y0+i/4)
		out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = c[0], c[1], c[2], alpha[i]
	}
}

// ddsEnc565Decode is enc565_decode (dxt.rs:266-275): 5-6-5 bits to 8-bit channels, 0x1F and 0x3F mapping to 0xFF.
func ddsEnc565Decode(value uint16) [3]byte {
	red := (value >> 11) & 0x1F
	green := (value >> 5) & 0x3F
	blue := value & 0x1F
	return [3]byte{byte(red * 0xFF / 0x1F), byte(green * 0xFF / 0x3F), byte(blue * 0xFF / 0x1F)}
}

// ddsColors is the colour table of decode_dxt_colors (dxt.rs:323-363): the two endpoints, and two interpolated colours when
// color0 > color1 or the variant is not DXT1, else their midpoint and black.
func ddsColors(source []byte, isDXT1 bool) [4][3]byte {
	color0 := uint16(source[0]) | uint16(source[1])<<8
	color1 := uint16(source[2]) | uint16(source[3])<<8
	var colors [4][3]byte
	colors[0] = ddsEnc565Decode(color0)
	colors[1] = ddsEnc565Decode(color1)
	if color0 > color1 || !isDXT1 {
		for i := range 3 {
			colors[2][i] = byte((uint16(colors[0][i])*2 + uint16(colors[1][i]) + 1) / 3)
			colors[3][i] = byte((uint16(colors[0][i]) + uint16(colors[1][i])*2 + 1) / 3)
		}
	} else {
		for i := range 3 {
			colors[2][i] = byte((uint16(colors[0][i]) + uint16(colors[1][i]) + 1) / 2)
		}
	}
	return colors
}

// ddsAlphaTable is alpha_table_dxt5 (dxt.rs:305-319).
func ddsAlphaTable(alpha0, alpha1 byte) [8]byte {
	table := [8]byte{alpha0, alpha1, 0, 0, 0, 0, 0, 0xFF}
	if alpha0 > alpha1 {
		for i := uint16(2); i < 8; i++ {
			table[i] = byte(((8-i)*uint16(alpha0) + (i-1)*uint16(alpha1)) / 7)
		}
	} else {
		for i := uint16(2); i < 6; i++ {
			table[i] = byte(((6-i)*uint16(alpha0) + (i-1)*uint16(alpha1)) / 5)
		}
	}
	return table
}
