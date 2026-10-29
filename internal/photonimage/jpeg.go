// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: Copyright (c) 2015 nwin
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-FileCopyrightText: Copyright (C) 1991-2014, Thomas G. Lane, Guido Vollbeding
// SPDX-License-Identifier: MIT AND IJG

package photonimage

import (
	"encoding/binary"
	"errors"
	"image"
	"math"
)

// Photon's `PhotonImage.get_bytes_jpeg(quality)` (photon-rs 0.3.3 lib.rs:203-211) hands its RGBA8 pixels to image 0.24.9
// `DynamicImage::write_to(ImageOutputFormat::Jpeg(quality))`, which is `JpegEncoder::new_with_quality(w, quality).write_image(..)`
// (image io/free_functions.rs:207-211) and ends in `JpegEncoder::encode_image` over an `Rgba<u8>` buffer. EncodeJPEG ports that
// encoder, so the JPEG the model receives has Photon's bytes, and with them Photon's size, which decides whether the PNG or the
// JPEG of a resize is returned.

// Ports image 0.24.9 src/codecs/jpeg/encoder.rs (the file named below as encoder.rs), transform.rs (the IJG jfdctint.c fdct) and
// entropy.rs (the Huffman code tables).

// huffEntry is one (size, code) pair of build_huff_lut_const (entropy.rs:52-63); a value without a code has size 17.
type huffEntry struct {
	size uint8
	code uint16
}

// Standard quantization and Huffman tables (encoder.rs:34-127).
var (
	stdLumaQTable = [64]uint8{
		16, 11, 10, 16, 24, 40, 51, 61,
		12, 12, 14, 19, 26, 58, 60, 55,
		14, 13, 16, 24, 40, 57, 69, 56,
		14, 17, 22, 29, 51, 87, 80, 62,
		18, 22, 37, 56, 68, 109, 103, 77,
		24, 35, 55, 64, 81, 104, 113, 92,
		49, 64, 78, 87, 103, 121, 120, 101,
		72, 92, 95, 98, 112, 100, 103, 99,
	}
	stdChromaQTable = [64]uint8{
		17, 18, 24, 47, 99, 99, 99, 99,
		18, 21, 26, 66, 99, 99, 99, 99,
		24, 26, 56, 99, 99, 99, 99, 99,
		47, 66, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
	}

	stdLumaDCCodeLengths = [16]uint8{0x00, 0x01, 0x05, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	stdDCValues          = [12]uint8{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B}

	stdChromaDCCodeLengths = [16]uint8{0x00, 0x03, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}

	stdLumaACCodeLengths = [16]uint8{0x00, 0x02, 0x01, 0x03, 0x03, 0x02, 0x04, 0x03, 0x05, 0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7D}
	stdLumaACValues      = [162]uint8{
		0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07,
		0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xA1, 0x08, 0x23, 0x42, 0xB1, 0xC1, 0x15, 0x52, 0xD1, 0xF0,
		0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0A, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x25, 0x26, 0x27, 0x28,
		0x29, 0x2A, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3A, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49,
		0x4A, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5A, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69,
		0x6A, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7A, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89,
		0x8A, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9A, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6, 0xA7,
		0xA8, 0xA9, 0xAA, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6, 0xB7, 0xB8, 0xB9, 0xBA, 0xC2, 0xC3, 0xC4, 0xC5,
		0xC6, 0xC7, 0xC8, 0xC9, 0xCA, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7, 0xD8, 0xD9, 0xDA, 0xE1, 0xE2,
		0xE3, 0xE4, 0xE5, 0xE6, 0xE7, 0xE8, 0xE9, 0xEA, 0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8,
		0xF9, 0xFA,
	}

	stdChromaACCodeLengths = [16]uint8{0x00, 0x02, 0x01, 0x02, 0x04, 0x04, 0x03, 0x04, 0x07, 0x05, 0x04, 0x04, 0x00, 0x01, 0x02, 0x77}
	stdChromaACValues      = [162]uint8{
		0x00, 0x01, 0x02, 0x03, 0x11, 0x04, 0x05, 0x21, 0x31, 0x06, 0x12, 0x41, 0x51, 0x07, 0x61, 0x71,
		0x13, 0x22, 0x32, 0x81, 0x08, 0x14, 0x42, 0x91, 0xA1, 0xB1, 0xC1, 0x09, 0x23, 0x33, 0x52, 0xF0,
		0x15, 0x62, 0x72, 0xD1, 0x0A, 0x16, 0x24, 0x34, 0xE1, 0x25, 0xF1, 0x17, 0x18, 0x19, 0x1A, 0x26,
		0x27, 0x28, 0x29, 0x2A, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3A, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48,
		0x49, 0x4A, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5A, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68,
		0x69, 0x6A, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7A, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87,
		0x88, 0x89, 0x8A, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9A, 0xA2, 0xA3, 0xA4, 0xA5,
		0xA6, 0xA7, 0xA8, 0xA9, 0xAA, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6, 0xB7, 0xB8, 0xB9, 0xBA, 0xC2, 0xC3,
		0xC4, 0xC5, 0xC6, 0xC7, 0xC8, 0xC9, 0xCA, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7, 0xD8, 0xD9, 0xDA,
		0xE2, 0xE3, 0xE4, 0xE5, 0xE6, 0xE7, 0xE8, 0xE9, 0xEA, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8,
		0xF9, 0xFA,
	}

	stdLumaDCHuffLUT   = buildHuffLUT(&stdLumaDCCodeLengths, stdDCValues[:])
	stdChromaDCHuffLUT = buildHuffLUT(&stdChromaDCCodeLengths, stdDCValues[:])
	stdLumaACHuffLUT   = buildHuffLUT(&stdLumaACCodeLengths, stdLumaACValues[:])
	stdChromaACHuffLUT = buildHuffLUT(&stdChromaACCodeLengths, stdChromaACValues[:])
)

// unzigzag is the permutation of DCT coefficients (encoder.rs:139-150).
var unzigzag = [64]uint8{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

// buildHuffLUT is entropy.rs derive_codes_and_sizes (:4-50) and build_huff_lut_const (:52-63), including the u8 and u16 wrapping the
// Rust code relies on.
func buildHuffLUT(bits *[16]uint8, huffval []uint8) [256]huffEntry {
	var huffsize [256]uint8
	var huffcode [256]uint16
	k := 0
	for i := range 16 {
		for range bits[i] {
			huffsize[k] = uint8(i + 1)
			k++
		}
	}
	huffsize[k] = 0

	k = 0
	var code uint16
	size := huffsize[0]
	for huffsize[k] != 0 {
		huffcode[k] = code
		code++
		k++
		if huffsize[k] == size {
			continue
		}
		diff := huffsize[k] - size
		if diff < 16 {
			code <<= diff
		} else {
			code = 0
		}
		size += diff
	}

	var lut [256]huffEntry
	for i := range lut {
		lut[i] = huffEntry{size: 17}
	}
	for i, value := range huffval {
		lut[value] = huffEntry{size: huffsize[i], code: huffcode[i]}
	}
	return lut
}

// jpegQuantTables is new_with_quality's tables (encoder.rs:392-409): the libjpeg quality scaling of the standard tables.
func jpegQuantTables(quality uint8) (luma, chroma [64]uint8) {
	q := uint32(min(max(quality, 1), 100))
	scale := 200 - q*2
	if q < 50 {
		scale = 5000 / q
	}
	luma, chroma = stdLumaQTable, stdChromaQTable
	for _, table := range []*[64]uint8{&luma, &chroma} {
		for i, v := range table {
			table[i] = uint8(min(max((uint32(v)*scale+50)/100, 1), 255))
		}
	}
	return luma, chroma
}

// The fixed-point constants of transform.rs:54-68.
const (
	constBits  = 13
	pass1Bits  = 2
	fix0298631 = 2446
	fix0390180 = 3196
	fix0541196 = 4433
	fix0765366 = 6270
	fix0899976 = 7373
	fix1175875 = 9633
	fix1501321 = 12299
	fix1847759 = 15137
	fix1961570 = 16069
	fix2053119 = 16819
	fix2562915 = 20995
	fix3072711 = 25172
)

// fdct is transform.rs fdct (:70-196), the IJG jfdctint.c forward DCT: samples are unsigned level-unshifted bytes, the result is
// scaled up by 8.
func fdct(samples *[64]uint8, coeffs *[64]int32) {
	// Pass 1: rows.
	for y := 0; y < 64; y += 8 {
		s := samples[y : y+8 : y+8]
		t0 := int32(s[0]) + int32(s[7])
		t1 := int32(s[1]) + int32(s[6])
		t2 := int32(s[2]) + int32(s[5])
		t3 := int32(s[3]) + int32(s[4])

		t10 := t0 + t3
		t12 := t0 - t3
		t11 := t1 + t2
		t13 := t1 - t2

		t0 = int32(s[0]) - int32(s[7])
		t1 = int32(s[1]) - int32(s[6])
		t2 = int32(s[2]) - int32(s[5])
		t3 = int32(s[3]) - int32(s[4])

		coeffs[y] = (t10 + t11 - 8*128) << pass1Bits
		coeffs[y+4] = (t10 - t11) << pass1Bits

		z1 := (t12 + t13) * fix0541196
		z1 += 1 << (constBits - pass1Bits - 1)

		coeffs[y+2] = (z1 + t12*fix0765366) >> (constBits - pass1Bits)
		coeffs[y+6] = (z1 - t13*fix1847759) >> (constBits - pass1Bits)

		t12 = t0 + t2
		t13 = t1 + t3

		z1 = (t12 + t13) * fix1175875
		z1 += 1 << (constBits - pass1Bits - 1)

		t12 = t12 * -fix0390180
		t13 = t13 * -fix1961570
		t12 += z1
		t13 += z1

		z1 = (t0 + t3) * -fix0899976
		t0 *= fix1501321
		t3 *= fix0298631
		t0 += z1 + t12
		t3 += z1 + t13

		z1 = (t1 + t2) * -fix2562915
		t1 *= fix3072711
		t2 *= fix2053119
		t1 += z1 + t13
		t2 += z1 + t12

		coeffs[y+1] = t0 >> (constBits - pass1Bits)
		coeffs[y+3] = t1 >> (constBits - pass1Bits)
		coeffs[y+5] = t2 >> (constBits - pass1Bits)
		coeffs[y+7] = t3 >> (constBits - pass1Bits)
	}

	// Pass 2: columns.
	for x := 7; x >= 0; x-- {
		t0 := coeffs[x] + coeffs[x+8*7]
		t1 := coeffs[x+8] + coeffs[x+8*6]
		t2 := coeffs[x+8*2] + coeffs[x+8*5]
		t3 := coeffs[x+8*3] + coeffs[x+8*4]

		t10 := t0 + t3 + (1 << (pass1Bits - 1))
		t12 := t0 - t3
		t11 := t1 + t2
		t13 := t1 - t2

		t0 = coeffs[x] - coeffs[x+8*7]
		t1 = coeffs[x+8] - coeffs[x+8*6]
		t2 = coeffs[x+8*2] - coeffs[x+8*5]
		t3 = coeffs[x+8*3] - coeffs[x+8*4]

		coeffs[x] = (t10 + t11) >> pass1Bits
		coeffs[x+8*4] = (t10 - t11) >> pass1Bits

		z1 := (t12 + t13) * fix0541196
		z1 += 1 << (constBits + pass1Bits - 1)

		coeffs[x+8*2] = (z1 + t12*fix0765366) >> (constBits + pass1Bits)
		coeffs[x+8*6] = (z1 - t13*fix1847759) >> (constBits + pass1Bits)

		t12 = t0 + t2
		t13 = t1 + t3

		z1 = (t12 + t13) * fix1175875
		z1 += 1 << (constBits - pass1Bits - 1)

		t12 = t12 * -fix0390180
		t13 = t13 * -fix1961570
		t12 += z1
		t13 += z1

		z1 = (t0 + t3) * -fix0899976
		t0 *= fix1501321
		t3 *= fix0298631
		t0 += z1 + t12
		t3 += z1 + t13

		z1 = (t1 + t2) * -fix2562915
		t1 *= fix3072711
		t2 *= fix2053119
		t1 += z1 + t13
		t2 += z1 + t12

		coeffs[x+8] = t0 >> (constBits + pass1Bits)
		coeffs[x+8*3] = t1 >> (constBits + pass1Bits)
		coeffs[x+8*5] = t2 >> (constBits + pass1Bits)
		coeffs[x+8*7] = t3 >> (constBits + pass1Bits)
	}
}

// jpegBitWriter is encoder.rs BitWriter (:177-282): big-endian bit packing with 0xFF byte stuffing.
type jpegBitWriter struct {
	out         []byte
	accumulator uint32
	nbits       uint8
}

func (w *jpegBitWriter) writeBits(bits uint16, size uint8) {
	if size == 0 {
		return
	}
	w.nbits += size
	w.accumulator |= uint32(bits) << (32 - w.nbits)
	for w.nbits >= 8 {
		b := byte(w.accumulator >> 24)
		w.out = append(w.out, b)
		if b == 0xFF {
			w.out = append(w.out, 0x00)
		}
		w.nbits -= 8
		w.accumulator <<= 8
	}
}

var errBadHuffmanValue = errors.New("photonimage: bad huffman value")

func (w *jpegBitWriter) huffmanEncode(val uint8, table *[256]huffEntry) error {
	e := table[val]
	if e.size > 16 {
		return errBadHuffmanValue // Photon panics here (encoder.rs:222-224)
	}
	w.writeBits(e.code, e.size)
	return nil
}

func (w *jpegBitWriter) writeMarker(marker byte) {
	w.out = append(w.out, 0xFF, marker)
}

func (w *jpegBitWriter) writeSegment(marker byte, data []byte) {
	w.out = append(w.out, 0xFF, marker)
	w.out = binary.BigEndian.AppendUint16(w.out, uint16(len(data)+2))
	w.out = append(w.out, data...)
}

// encodeCoefficient is encoder.rs encode_coefficient (:768-786).
func encodeCoefficient(coefficient int32) (uint8, uint16) {
	magnitude := uint16(max(coefficient, -coefficient))
	var numBits uint8
	for magnitude > 0 {
		magnitude >>= 1
		numBits++
	}
	mask := uint16(1)<<numBits - 1
	if coefficient < 0 {
		return numBits, uint16(coefficient-1) & mask
	}
	return numBits, uint16(coefficient) & mask
}

// writeBlock is encoder.rs write_block (:229-271) and returns the block's DC value.
func (w *jpegBitWriter) writeBlock(block *[64]int32, prevDC int32, dcTable, acTable *[256]huffEntry) (int32, error) {
	dcval := block[0]
	size, value := encodeCoefficient(dcval - prevDC)
	if err := w.huffmanEncode(size, dcTable); err != nil {
		return 0, err
	}
	w.writeBits(value, size)

	zeroRun := 0
	for _, k := range unzigzag[1:] {
		if block[k] == 0 {
			zeroRun++
			continue
		}
		for zeroRun > 15 {
			if err := w.huffmanEncode(0xF0, acTable); err != nil {
				return 0, err
			}
			zeroRun -= 16
		}
		size, value := encodeCoefficient(block[k])
		if err := w.huffmanEncode(uint8(zeroRun<<4)|size, acTable); err != nil {
			return 0, err
		}
		w.writeBits(value, size)
		zeroRun = 0
	}
	if block[unzigzag[63]] == 0 {
		if err := w.huffmanEncode(0x00, acTable); err != nil {
			return 0, err
		}
	}
	return dcval, nil
}

// Single-precision JPEG File Interchange Format (Version 1.02) coefficients, multiplied for 255 maximum (encoder.rs:799-802). They
// are variables, not constants: Rust divides f32 values, rounding after each operation, and Go folds typed constants exactly.
var (
	yfR, yfG, yfB    float32 = 76.245, 149.685, 29.07
	cbfR, cbfG, cbfB float32 = -43.0185, -84.4815, 127.5
	crfR, crfG, crfB float32 = 127.5, -106.7685, -20.7315
	chromaOffset     float32 = 128
	jpegMaxSubpixel  float32 = 255
	yR, yG, yB               = yfR / jpegMaxSubpixel, yfG / jpegMaxSubpixel, yfB / jpegMaxSubpixel
	cbR, cbG, cbB            = cbfR / jpegMaxSubpixel, cbfG / jpegMaxSubpixel, cbfB / jpegMaxSubpixel
	crR, crG, crB            = crfR / jpegMaxSubpixel, crfG / jpegMaxSubpixel, crfB / jpegMaxSubpixel
)

// saturateU8 is Rust's saturating `f32 as u8`.
func saturateU8(v float32) uint8 {
	if !(v > 0) {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

// rgbToYCbCr is encoder.rs rgb_to_ycbcr (:789-805). Every product is converted to float32 so that no platform fuses it with the
// addition that follows, as Rust does not.
func rgbToYCbCr(r, g, b uint8) (uint8, uint8, uint8) {
	fr, fg, fb := float32(r), float32(g), float32(b)
	y := float32(yR*fr) + float32(yG*fg) + float32(yB*fb)
	cb := float32(cbR*fr) - float32(-cbG*fg) + float32(cbB*fb) + chromaOffset
	cr := float32(crR*fr) - float32(-crG*fg) - float32(-crB*fb) + chromaOffset
	return saturateU8(y), saturateU8(cb), saturateU8(cr)
}

// quantize is `((coefficient / 8) as f32 / f32::from(table[i])).round() as i32` (encoder.rs:647-650).
func quantize(coefficient int32, q uint8) int32 {
	return int32(math.Round(float64(float32(coefficient/8) / float32(q))))
}

// EncodeJPEG is img as the JPEG Photon's `get_bytes_jpeg(quality)` writes: baseline, 8-bit, three components without chroma
// subsampling, the standard Huffman tables, the quantization tables of quality scaled the libjpeg way, a JFIF 1.02 header with a
// pixel aspect ratio of 1, and the alpha channel dropped. quality is Photon's `u8`. It fails where Photon panics: a side over 65535
// (encoder.rs:518-527) and a coefficient the standard Huffman tables cannot code (encoder.rs:222-224).
func EncodeJPEG(img *image.NRGBA, quality uint8) ([]byte, error) {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	if width > math.MaxUint16 || height > math.MaxUint16 {
		return nil, errors.New("photonimage: image too large for JPEG")
	}
	lumaTable, chromaTable := jpegQuantTables(quality)

	w := &jpegBitWriter{out: make([]byte, 0, 1024+width*height/4)}
	w.writeMarker(0xD8) // SOI
	// build_jfif_header (:682-698).
	w.writeSegment(0xE0, []byte{'J', 'F', 'I', 'F', 0, 0x01, 0x02, 0x00, 0, 1, 0, 1, 0, 0})
	// build_frame_header (:700-718): 8-bit, three components of sampling factor 1x1.
	frame := []byte{8, byte(height >> 8), byte(height), byte(width >> 8), byte(width), 3, 1, 0x11, 0, 2, 0x11, 1, 3, 0x11, 1}
	w.writeSegment(0xC0, frame)
	// build_quantization_segment (:755-766).
	for i, table := range []*[64]uint8{&lumaTable, &chromaTable} {
		segment := make([]byte, 0, 65)
		segment = append(segment, byte(i))
		for _, k := range unzigzag {
			segment = append(segment, table[k])
		}
		w.writeSegment(0xDB, segment)
	}
	// build_huffman_segment (:734-753).
	for _, t := range []struct {
		class, destination uint8
		lengths            *[16]uint8
		values             []uint8
	}{
		{0, 0, &stdLumaDCCodeLengths, stdDCValues[:]},
		{1, 0, &stdLumaACCodeLengths, stdLumaACValues[:]},
		{0, 1, &stdChromaDCCodeLengths, stdDCValues[:]},
		{1, 1, &stdChromaACCodeLengths, stdChromaACValues[:]},
	} {
		segment := append([]byte{t.class<<4 | t.destination}, t.lengths[:]...)
		w.writeSegment(0xC4, append(segment, t.values...))
	}
	// build_scan_header (:720-732).
	w.writeSegment(0xDA, []byte{3, 1, 0x00, 2, 0x11, 3, 0x11, 0, 63, 0})

	if err := encodeRGB(w, img, &lumaTable, &chromaTable); err != nil {
		return nil, err
	}
	w.writeBits(0x7F, 7) // pad_byte
	w.writeMarker(0xD9)  // EOI
	return w.out, nil
}

// encodeRGB is encoder.rs encode_rgb (:620-666).
func encodeRGB(w *jpegBitWriter, img *image.NRGBA, lumaTable, chromaTable *[64]uint8) error {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	var yDCPrev, cbDCPrev, crDCPrev int32
	var dctY, dctCb, dctCr [64]int32
	var blockY, blockCb, blockCr [64]uint8

	for y0 := 0; y0 < height; y0 += 8 {
		for x0 := 0; x0 < width; x0 += 8 {
			// copy_blocks_ycbcr (:818-836), with pixel_at_or_near (:810-816) repeating the edge pixel.
			for y := range 8 {
				for x := range 8 {
					o := img.PixOffset(img.Rect.Min.X+min(x0+x, width-1), img.Rect.Min.Y+min(y0+y, height-1))
					blockY[y*8+x], blockCb[y*8+x], blockCr[y*8+x] = rgbToYCbCr(img.Pix[o], img.Pix[o+1], img.Pix[o+2])
				}
			}
			fdct(&blockY, &dctY)
			fdct(&blockCb, &dctCb)
			fdct(&blockCr, &dctCr)
			for i := range 64 {
				dctY[i] = quantize(dctY[i], lumaTable[i])
				dctCb[i] = quantize(dctCb[i], chromaTable[i])
				dctCr[i] = quantize(dctCr[i], chromaTable[i])
			}
			var err error
			if yDCPrev, err = w.writeBlock(&dctY, yDCPrev, &stdLumaDCHuffLUT, &stdLumaACHuffLUT); err != nil {
				return err
			}
			if cbDCPrev, err = w.writeBlock(&dctCb, cbDCPrev, &stdChromaDCHuffLUT, &stdChromaACHuffLUT); err != nil {
				return err
			}
			if crDCPrev, err = w.writeBlock(&dctCr, crDCPrev, &stdChromaDCHuffLUT, &stdChromaACHuffLUT); err != nil {
				return err
			}
		}
	}
	return nil
}
