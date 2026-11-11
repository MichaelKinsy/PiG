// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import "encoding/binary"

// Ports jpeg-decoder 0.3.1 src/idct.rs (cited as idct.rs; based on stb_image's stbi__idct_block) and src/worker/immediate.rs (cited
// as immediate.rs). Only the 8x8 IDCT is ported: Photon never calls `Decoder::scale`, so `dct_scale` is always 8 (the 4x4, 2x2 and
// 1x1 IDCTs and `choose_idct_size` are unreachable). Every operation is a `Wrapping<i32>` one in the Rust code (idct.rs:1-5), which
// is int32 arithmetic in Go. The kernels of idct.rs are written out in the two passes instead of called, a block with no AC
// coefficient is filled with the one value it gives, and the scratch array of the first pass is the worker's, none of which changes
// a result: jpegdecode_equivalence_test.go compares jpegDequantizeAndIDCT8x8 with the line-by-line port (jpegdecode_reference_test.go)
// on every single coefficient, on random blocks and on the extremes.

// The stbi_f2f constants of idct.rs:378-448 `(x * 4096.0 + 0.5) as i32` (f32 arithmetic, truncating toward zero), checked against
// the float32 evaluation in jpegdecode_test.go.
const (
	jpegIDCTc0541 = 2217   // 0.5411961
	jpegIDCTc1847 = -7567  // -1.847759065
	jpegIDCTc0765 = 3135   // 0.765366865
	jpegIDCTc1175 = 4816   // 1.175875602
	jpegIDCTc0298 = 1223   // 0.298631336
	jpegIDCTc2053 = 8410   // 2.053119869
	jpegIDCTc3072 = 12586  // 3.072711026
	jpegIDCTc1501 = 6149   // 1.501321110
	jpegIDCTc0899 = -3685  // -0.899976223
	jpegIDCTc2562 = -10497 // -2.562915447
	jpegIDCTc1961 = -8034  // -1.961570560
	jpegIDCTc0390 = -1597  // -0.390180644

	// X_SCALE of idct.rs:333-336: 0.5 * 1<<17 to round, plus 128 << 17 to move -128..127 to 0..255.
	jpegIDCTXScale = 65536 + 128<<17
)

func jpegClamp(x int32) uint8 { return uint8(min(max(x, 0), 255)) }

// jpegDequantizeAndIDCT8x8 is idct.rs:241-370 dequantize_and_idct_block_8x8 (the scalar path; coefficients are in natural order,
// the quantization table too) writing the 8x8 samples at out[0], out[stride], ...; temp is the scratch of the first pass, whose 64
// values it overwrites. The kernels are idct.rs:372-448 kernel_x and kernel_t: the even (e, x) and odd (t) parts of an 8-point IDCT.
func jpegDequantizeAndIDCT8x8(coefficients *[64]int16, quantizationTable *[64]uint16, stride int, out []byte, temp *[64]int32) {
	// columns
	for i := range 8 {
		if coefficients[i+8]|coefficients[i+16]|coefficients[i+24]|coefficients[i+32]|coefficients[i+40]|coefficients[i+48]|coefficients[i+56] == 0 {
			dcterm := (int32(coefficients[i]) * int32(quantizationTable[i])) << 2
			temp[i] = dcterm
			temp[i+8] = dcterm
			temp[i+16] = dcterm
			temp[i+24] = dcterm
			temp[i+32] = dcterm
			temp[i+40] = dcterm
			temp[i+48] = dcterm
			temp[i+56] = dcterm
			continue
		}
		s0 := int32(coefficients[i]) * int32(quantizationTable[i])
		s1 := int32(coefficients[i+8]) * int32(quantizationTable[i+8])
		s2 := int32(coefficients[i+16]) * int32(quantizationTable[i+16])
		s3 := int32(coefficients[i+24]) * int32(quantizationTable[i+24])
		s4 := int32(coefficients[i+32]) * int32(quantizationTable[i+32])
		s5 := int32(coefficients[i+40]) * int32(quantizationTable[i+40])
		s6 := int32(coefficients[i+48]) * int32(quantizationTable[i+48])
		s7 := int32(coefficients[i+56]) * int32(quantizationTable[i+56])

		// kernel_x over s0, s2, s4, s6; the constants scaled things up by 1<<12, which is brought back down by 10 bits, keeping 2
		// extra bits of precision, and 512 is the rounding of that shift.
		p1 := (s2 + s6) * jpegIDCTc0541
		e2 := p1 + s6*jpegIDCTc1847
		e3 := p1 + s2*jpegIDCTc0765
		e0 := (s0 + s4) << 12
		e1 := (s0 - s4) << 12
		x0 := e0 + e3 + 512
		x3 := e0 - e3 + 512
		x1 := e1 + e2 + 512
		x2 := e1 - e2 + 512

		// kernel_t over s1, s3, s5, s7
		t0, t1, t2, t3 := s7, s5, s3, s1
		p3 := t0 + t2
		p4 := t1 + t3
		q1 := t0 + t3
		q2 := t1 + t2
		p5 := (p3 + p4) * jpegIDCTc1175
		t0 *= jpegIDCTc0298
		t1 *= jpegIDCTc2053
		t2 *= jpegIDCTc3072
		t3 *= jpegIDCTc1501
		q1 = p5 + q1*jpegIDCTc0899
		q2 = p5 + q2*jpegIDCTc2562
		p3 *= jpegIDCTc1961
		p4 *= jpegIDCTc0390
		t3 += q1 + p4
		t2 += q2 + p3
		t1 += q2 + p4
		t0 += q1 + p3

		temp[i] = (x0 + t3) >> 10
		temp[i+56] = (x0 - t3) >> 10
		temp[i+8] = (x1 + t2) >> 10
		temp[i+48] = (x1 - t2) >> 10
		temp[i+16] = (x2 + t1) >> 10
		temp[i+40] = (x2 - t1) >> 10
		temp[i+24] = (x3 + t0) >> 10
		temp[i+32] = (x3 - t0) >> 10
	}

	// rows: the constants scaled things up by 1<<12, plus 1<<2 from the first pass, plus horizontal and vertical each scale by
	// sqrt(8) so together an extra 1<<3, so 1<<17 in all to remove. To round, 0.5 * 1<<17 is added, and since -128 to 127 is
	// to be 0 to 255, 128 << 17 too: that is X_SCALE, added before the shift.
	for row := range 8 {
		s := temp[row*8 : row*8+8 : row*8+8]
		o := out[row*stride : row*stride+8 : row*stride+8]
		s0 := s[0]
		if s[1]|s[2]|s[3]|s[4]|s[5]|s[6]|s[7] == 0 {
			dcterm := jpegClamp((s0<<12 + jpegIDCTXScale) >> 17)
			binary.LittleEndian.PutUint64(o, uint64(dcterm)*0x0101010101010101)
			continue
		}
		s1, s2, s3, s4, s5, s6, s7 := s[1], s[2], s[3], s[4], s[5], s[6], s[7]

		p1 := (s2 + s6) * jpegIDCTc0541
		e2 := p1 + s6*jpegIDCTc1847
		e3 := p1 + s2*jpegIDCTc0765
		e0 := (s0 + s4) << 12
		e1 := (s0 - s4) << 12
		x0 := e0 + e3 + jpegIDCTXScale
		x3 := e0 - e3 + jpegIDCTXScale
		x1 := e1 + e2 + jpegIDCTXScale
		x2 := e1 - e2 + jpegIDCTXScale

		t0, t1, t2, t3 := s7, s5, s3, s1
		p3 := t0 + t2
		p4 := t1 + t3
		q1 := t0 + t3
		q2 := t1 + t2
		p5 := (p3 + p4) * jpegIDCTc1175
		t0 *= jpegIDCTc0298
		t1 *= jpegIDCTc2053
		t2 *= jpegIDCTc3072
		t3 *= jpegIDCTc1501
		q1 = p5 + q1*jpegIDCTc0899
		q2 = p5 + q2*jpegIDCTc2562
		p3 *= jpegIDCTc1961
		p4 *= jpegIDCTc0390
		t3 += q1 + p4
		t2 += q2 + p3
		t1 += q2 + p4
		t0 += q1 + p3

		o[0] = jpegClamp((x0 + t3) >> 17)
		o[7] = jpegClamp((x0 - t3) >> 17)
		o[1] = jpegClamp((x1 + t2) >> 17)
		o[6] = jpegClamp((x1 - t2) >> 17)
		o[2] = jpegClamp((x2 + t1) >> 17)
		o[5] = jpegClamp((x2 - t1) >> 17)
		o[3] = jpegClamp((x3 + t0) >> 17)
		o[4] = jpegClamp((x3 - t0) >> 17)
	}
}

// jpegDequantizeDCOnly8x8 is jpegDequantizeAndIDCT8x8 for a block whose 63 AC coefficients are zero: the first pass gives every
// row the dc term in its first value and zeros after it, so that the second pass gives all 64 samples one value.
func jpegDequantizeDCOnly8x8(dc int16, quantization uint16, stride int, out []byte) {
	dcterm := (int32(dc) * int32(quantization)) << 2
	value := uint64(jpegClamp((dcterm<<12+jpegIDCTXScale)>>17)) * 0x0101010101010101
	for row := range 8 {
		binary.LittleEndian.PutUint64(out[row*stride:], value)
	}
}

// jpegWorker is immediate.rs:11-79 ImmediateWorker: it turns the MCU rows of coefficients of up to four components into their
// sample planes.
type jpegWorker struct {
	offsets    [jpegMaxComponents]int
	results    [jpegMaxComponents][]byte
	components [jpegMaxComponents]jpegComponent
	quant      [jpegMaxComponents]*[64]uint16
	temp       [64]int32
}

// start is immediate.rs:30-37 start_immediate: it asserts the result slot is empty (a panic in Rust, so an error here).
func (w *jpegWorker) start(index int, component *jpegComponent, quantizationTable *[64]uint16) error {
	if len(w.results[index]) != 0 {
		return jpegFormatError("assertion failed: self.results[data.index].is_empty()")
	}
	w.offsets[index] = 0
	w.results[index] = make([]byte, int(component.blockSize.width)*int(component.blockSize.height)*component.dctScale*component.dctScale)
	w.components[index] = *component
	w.quant[index] = quantizationTable
	return nil
}

// appendRow is immediate.rs:39-60 append_row_immediate: one MCU row of coefficients (block_width * vertical_sampling_factor blocks
// of 64) of component `index` becomes samples.
func (w *jpegWorker) appendRow(index int, data []int16) {
	component := &w.components[index]
	quantizationTable := w.quant[index]
	blockWidth := int(component.blockSize.width)
	blockCount := blockWidth * int(component.vSampling)
	lineStride := blockWidth * component.dctScale
	result := w.results[index]
	base := w.offsets[index]

	for i := range blockCount {
		x := (i % blockWidth) * component.dctScale
		y := (i / blockWidth) * component.dctScale
		coefficients := (*[64]int16)(data[i*64 : (i+1)*64])
		jpegIDCTBlock(coefficients, quantizationTable, lineStride, result[base+y*lineStride+x:], &w.temp)
	}
	w.offsets[index] += blockCount * component.dctScale * component.dctScale
}

// jpegIDCTBlock is the IDCT of one block: the dc-only shortcut for a block without AC coefficients, jpegDequantizeAndIDCT8x8
// for any other.
func jpegIDCTBlock(coefficients *[64]int16, quantizationTable *[64]uint16, stride int, out []byte, temp *[64]int32) {
	for _, c := range coefficients[1:] {
		if c != 0 {
			jpegDequantizeAndIDCT8x8(coefficients, quantizationTable, stride, out, temp)
			return
		}
	}
	jpegDequantizeDCOnly8x8(coefficients[0], quantizationTable[0], stride, out)
}

// getResult is immediate.rs:62-65 get_result_immediate (`mem::take`).
func (w *jpegWorker) getResult(index int) []byte {
	result := w.results[index]
	w.results[index] = nil
	return result
}
