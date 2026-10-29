// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

// Ports jpeg-decoder 0.3.1 src/idct.rs (cited as idct.rs; based on stb_image's stbi__idct_block) and src/worker/immediate.rs (cited
// as immediate.rs). Only the 8x8 IDCT is ported: Photon never calls `Decoder::scale`, so `dct_scale` is always 8 (the 4x4, 2x2 and
// 1x1 IDCTs and `choose_idct_size` are unreachable). Every operation is a `Wrapping<i32>` one in the Rust code (idct.rs:1-5), which
// is int32 arithmetic in Go.

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

// jpegKernel is idct.rs:372-448 kernel_x, kernel_t and kernel: the even (xs) and odd (ts) parts of one 8-point IDCT.
func jpegKernel(s0, s1, s2, s3, s4, s5, s6, s7, xScale int32) (x0, x1, x2, x3, t0, t1, t2, t3 int32) {
	// kernel_x over the even inputs s0, s2, s4, s6.
	p1 := (s2 + s6) * jpegIDCTc0541
	e2 := p1 + s6*jpegIDCTc1847
	e3 := p1 + s2*jpegIDCTc0765

	e0 := (s0 + s4) << 12
	e1 := (s0 - s4) << 12

	x0 = e0 + e3 + xScale
	x3 = e0 - e3 + xScale
	x1 = e1 + e2 + xScale
	x2 = e1 - e2 + xScale

	// kernel_t over the odd inputs s1, s3, s5, s7.
	t0, t1, t2, t3 = s7, s5, s3, s1
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
	return x0, x1, x2, x3, t0, t1, t2, t3
}

func jpegClamp(x int32) uint8 { return uint8(min(max(x, 0), 255)) }

// jpegDequantizeAndIDCT8x8 is idct.rs:241-370 dequantize_and_idct_block_8x8 (the scalar path; coefficients are in natural order,
// the quantization table too) writing the 8x8 samples at out[0], out[stride], ...
func jpegDequantizeAndIDCT8x8(coefficients *[64]int16, quantizationTable *[64]uint16, stride int, out []byte) {
	var temp [64]int32

	// columns
	for i := range 8 {
		if coefficients[i+8] == 0 && coefficients[i+16] == 0 && coefficients[i+24] == 0 && coefficients[i+32] == 0 &&
			coefficients[i+40] == 0 && coefficients[i+48] == 0 && coefficients[i+56] == 0 {
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

		// constants scaled things up by 1<<12; bring them back down, but keep 2 extra bits of precision
		x0, x1, x2, x3, t0, t1, t2, t3 := jpegKernel(s0, s1, s2, s3, s4, s5, s6, s7, 512)

		temp[i] = (x0 + t3) >> 10
		temp[i+56] = (x0 - t3) >> 10
		temp[i+8] = (x1 + t2) >> 10
		temp[i+48] = (x1 - t2) >> 10
		temp[i+16] = (x2 + t1) >> 10
		temp[i+40] = (x2 - t1) >> 10
		temp[i+24] = (x3 + t0) >> 10
		temp[i+32] = (x3 - t0) >> 10
	}

	for row := range 8 {
		chunk := temp[row*8 : row*8+8 : row*8+8]
		o := out[row*stride : row*stride+8 : row*stride+8]
		if chunk[1] == 0 && chunk[2] == 0 && chunk[3] == 0 && chunk[4] == 0 && chunk[5] == 0 && chunk[6] == 0 && chunk[7] == 0 {
			dcterm := jpegClamp((chunk[0]<<12 + jpegIDCTXScale) >> 17)
			o[0], o[1], o[2], o[3], o[4], o[5], o[6], o[7] = dcterm, dcterm, dcterm, dcterm, dcterm, dcterm, dcterm, dcterm
			continue
		}
		x0, x1, x2, x3, t0, t1, t2, t3 := jpegKernel(chunk[0], chunk[1], chunk[2], chunk[3], chunk[4], chunk[5], chunk[6], chunk[7], jpegIDCTXScale)
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

// jpegWorker is immediate.rs:11-79 ImmediateWorker: it turns the MCU rows of coefficients of up to four components into their
// sample planes.
type jpegWorker struct {
	offsets    [jpegMaxComponents]int
	results    [jpegMaxComponents][]byte
	components [jpegMaxComponents]jpegComponent
	quant      [jpegMaxComponents]*[64]uint16
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
	blockCount := int(component.blockSize.width) * int(component.vSampling)
	lineStride := int(component.blockSize.width) * component.dctScale

	for i := range blockCount {
		x := (i % int(component.blockSize.width)) * component.dctScale
		y := (i / int(component.blockSize.width)) * component.dctScale
		coefficients := (*[64]int16)(data[i*64 : (i+1)*64])
		jpegDequantizeAndIDCT8x8(coefficients, quantizationTable, lineStride, w.results[index][w.offsets[index]+y*lineStride+x:])
	}
	w.offsets[index] += blockCount * component.dctScale * component.dctScale
}

// getResult is immediate.rs:62-65 get_result_immediate (`mem::take`).
func (w *jpegWorker) getResult(index int) []byte {
	result := w.results[index]
	w.results[index] = nil
	return result
}
