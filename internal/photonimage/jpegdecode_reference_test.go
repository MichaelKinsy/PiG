// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import "image"

// The literal ports of the functions of jpeg-decoder 0.3.1 that jpegdecode*.go replaces with faster ones that give the same results
// (the first versions of the production code, which followed the Rust line by line): the tests of jpegdecode_equivalence_test.go
// prove each replacement equal to them.

// referenceKernel is idct.rs:236-285 kernel_x and kernel_t.
func referenceKernel(s0, s1, s2, s3, s4, s5, s6, s7, xScale int32) (x0, x1, x2, x3, t0, t1, t2, t3 int32) {
	p1 := (s2 + s6) * jpegIDCTc0541
	e2 := p1 + s6*jpegIDCTc1847
	e3 := p1 + s2*jpegIDCTc0765

	e0 := (s0 + s4) << 12
	e1 := (s0 - s4) << 12

	x0 = e0 + e3 + xScale
	x3 = e0 - e3 + xScale
	x1 = e1 + e2 + xScale
	x2 = e1 - e2 + xScale

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

// referenceIDCT8x8 is idct.rs:201-296 dequantize_and_idct_block_8x8 (scalar).
func referenceIDCT8x8(coefficients *[64]int16, quantizationTable *[64]uint16, stride int, out []byte) {
	var temp [64]int32

	for i := range 8 {
		if coefficients[i+8] == 0 && coefficients[i+16] == 0 && coefficients[i+24] == 0 && coefficients[i+32] == 0 &&
			coefficients[i+40] == 0 && coefficients[i+48] == 0 && coefficients[i+56] == 0 {
			dcterm := (int32(coefficients[i]) * int32(quantizationTable[i])) << 2
			for k := range 8 {
				temp[i+8*k] = dcterm
			}
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

		x0, x1, x2, x3, t0, t1, t2, t3 := referenceKernel(s0, s1, s2, s3, s4, s5, s6, s7, 512)

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
		chunk := temp[row*8 : row*8+8]
		o := out[row*stride : row*stride+8]
		if chunk[1] == 0 && chunk[2] == 0 && chunk[3] == 0 && chunk[4] == 0 && chunk[5] == 0 && chunk[6] == 0 && chunk[7] == 0 {
			dcterm := jpegClamp((chunk[0]<<12 + jpegIDCTXScale) >> 17)
			for k := range 8 {
				o[k] = dcterm
			}
			continue
		}
		x0, x1, x2, x3, t0, t1, t2, t3 := referenceKernel(chunk[0], chunk[1], chunk[2], chunk[3], chunk[4], chunk[5], chunk[6], chunk[7], jpegIDCTXScale)
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

// referenceYCbCrToRGB is decoder.rs:1506-1517 ycbcr_to_rgb.
func referenceYCbCrToRGB(y, cb, cr uint8) (r, g, b uint8) {
	yy := int32(y)*(1<<20) + (1<<20)/2
	cbv := int32(cb) - 128
	crv := int32(cr) - 128
	clamp := func(v int32) uint8 { return uint8(max(min(v>>20, 255), 0)) }
	return clamp(yy + 1470104*crv), clamp(yy - 360857*cbv - 748830*crv), clamp(yy + 1858077*cbv)
}

// referenceUpsampleRow is upsampler.rs:114-250 for the row `row` of a component.
func referenceUpsampleRow(c *jpegUpsamplerComponent, input []byte, row, outputWidth int, output []byte) {
	switch c.kind {
	case jpegUpsampleH1V1:
		input = input[row*c.stride:]
		copy(output[:outputWidth], input[:outputWidth])
	case jpegUpsampleH2V1:
		input = input[row*c.stride:]
		inputWidth := c.width
		if inputWidth == 1 {
			output[0] = input[0]
			output[1] = input[0]
			return
		}
		output[0] = input[0]
		output[1] = byte((uint32(input[0])*3 + uint32(input[1]) + 2) >> 2)
		for i := 1; i < inputWidth-1; i++ {
			sample := 3*uint32(input[i]) + 2
			output[i*2] = byte((sample + uint32(input[i-1])) >> 2)
			output[i*2+1] = byte((sample + uint32(input[i+1])) >> 2)
		}
		output[(inputWidth-1)*2] = byte((uint32(input[inputWidth-1])*3 + uint32(input[inputWidth-2]) + 2) >> 2)
		output[(inputWidth-1)*2+1] = input[inputWidth-1]
	case jpegUpsampleH1V2:
		near, far := jpegNearFarRows(row, c.height)
		inputNear := input[near*c.stride:][:outputWidth]
		inputFar := input[far*c.stride:][:outputWidth]
		output = output[:outputWidth]
		for i := range output {
			output[i] = byte((3*uint32(inputNear[i]) + uint32(inputFar[i]) + 2) >> 2)
		}
	case jpegUpsampleH2V2:
		near, far := jpegNearFarRows(row, c.height)
		inputNear := input[near*c.stride:]
		inputFar := input[far*c.stride:]
		inputWidth := c.width
		if inputWidth == 1 {
			value := byte((3*uint32(inputNear[0]) + uint32(inputFar[0]) + 2) >> 2)
			output[0] = value
			output[1] = value
			return
		}
		t1 := 3*uint32(inputNear[0]) + uint32(inputFar[0])
		output[0] = byte((t1 + 2) >> 2)
		for i := 1; i < inputWidth; i++ {
			t0 := t1
			t1 = 3*uint32(inputNear[i]) + uint32(inputFar[i])
			output[i*2-1] = byte((3*t0 + t1 + 8) >> 4)
			output[i*2] = byte((3*t1 + t0 + 8) >> 4)
		}
		output[inputWidth*2-1] = byte((t1 + 2) >> 2)
	default:
		index := 0
		start := (row / c.vScaling) * c.stride
		for _, val := range input[start : start+c.width] {
			for range c.hScaling {
				output[index] = val
				index++
			}
		}
	}
}

// referenceHuffmanReader is the bit reader of huffman.rs:13-144 exactly as ported first: byte by byte.
type referenceHuffmanReader struct {
	bits      uint64
	numBits   uint8
	hasMarker bool
	marker    byte
}

func (h *referenceHuffmanReader) readBits(r *jpegReader) error {
	for h.numBits <= 56 {
		var byt byte
		if !h.hasMarker {
			b, err := r.readU8()
			if err != nil {
				return err
			}
			byt = b
		}
		if byt == 0xFF {
			next, err := r.readU8()
			if err != nil {
				return err
			}
			if next != 0x00 {
				for next == 0xFF {
					next, err = r.readU8()
					if err != nil {
						return err
					}
				}
				if next == 0x00 {
					return jpegFormatError("FF 00 found where marker was expected")
				}
				h.marker, h.hasMarker = next, true
				continue
			}
		}
		h.bits |= uint64(byt) << (56 - h.numBits)
		h.numBits += 8
	}
	return nil
}

// referenceColorConvertLineRGB is decoder.rs:1391-1404 color_convert_line_rgb.
func referenceColorConvertLineRGB(data [][]byte, output []byte) {
	r, g, b := data[0], data[1], data[2]
	for j := range min(len(output)/3, len(r), len(g), len(b)) {
		output[3*j] = r[j]
		output[3*j+1] = g[j]
		output[3*j+2] = b[j]
	}
}

// referenceColorConvertLineYCbCr is decoder.rs:1406-1437 color_convert_line_ycbcr (without the arch-specific path).
func referenceColorConvertLineYCbCr(data [][]byte, output []byte) {
	y, cb, cr := data[0], data[1], data[2]
	for j := range min(len(output)/3, len(y), len(cb), len(cr)) {
		output[3*j], output[3*j+1], output[3*j+2] = referenceYCbCrToRGB(y[j], cb[j], cr[j])
	}
}

// referenceColorConvertLineYCCK is decoder.rs:1439-1456 color_convert_line_ycck.
func referenceColorConvertLineYCCK(data [][]byte, output []byte) {
	c, m, y, k := data[0], data[1], data[2], data[3]
	for j := range min(len(output)/4, len(c), len(m), len(y), len(k)) {
		output[4*j], output[4*j+1], output[4*j+2] = referenceYCbCrToRGB(c[j], m[j], y[j])
		output[4*j+3] = 255 - k[j]
	}
}

// referenceColorConvertLineCMYK is decoder.rs:1458-1474 color_convert_line_cmyk.
func referenceColorConvertLineCMYK(data [][]byte, output []byte) {
	c, m, y, k := data[0], data[1], data[2], data[3]
	for j := range min(len(output)/4, len(c), len(m), len(y), len(k)) {
		output[4*j] = 255 - c[j]
		output[4*j+1] = 255 - m[j]
		output[4*j+2] = 255 - y[j]
		output[4*j+3] = 255 - k[j]
	}
}

// referenceComputeImage is decoder.rs:1300-1337 compute_image and worker/mod.rs:97-128 compute_image_parallel followed by what
// image 0.24.9 does to the buffer (cmyk_to_rgb for four components) and `to_rgba8`: the literal pipeline, through the interleaved
// RGB or CMYK buffer.
func referenceComputeImage(components []jpegComponent, data [][]byte, outputSize jpegDimensions, transform jpegColorTransform) (*image.NRGBA, error) {
	width, height := int(outputSize.width), int(outputSize.height)
	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	if len(components) == 1 {
		component := &components[0]
		decoded := append([]byte(nil), data[0]...)
		w := int(component.size.width)
		h := int(component.size.height)
		lineStride := int(component.blockSize.width) * component.dctScale
		if width != lineStride {
			for y := 1; y < h; y++ {
				copy(decoded[y*w:], decoded[y*lineStride:y*lineStride+w])
			}
		}
		for i, v := range decoded[:w*h] {
			img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = v, v, v, 255
		}
		return img, nil
	}

	var convert func([][]byte, []byte)
	switch {
	case len(components) == 3 && transform == jpegTransformRGB:
		convert = referenceColorConvertLineRGB
	case len(components) == 3 && transform == jpegTransformYCbCr:
		convert = referenceColorConvertLineYCbCr
	case len(components) == 4 && transform == jpegTransformCMYK:
		convert = referenceColorConvertLineCMYK
	case len(components) == 4 && transform == jpegTransformYCCK:
		convert = referenceColorConvertLineYCCK
	default:
		return nil, jpegFormatError("no conversion")
	}
	upsampler, err := newJPEGUpsampler(components, outputSize.width, outputSize.height)
	if err != nil {
		return nil, err
	}
	lineSize := width * len(components)
	buffer := make([]byte, lineSize*height)
	lineBuffers := make([][]byte, len(components))
	for i := range lineBuffers {
		lineBuffers[i] = make([]byte, upsampler.lineBufferSize)
	}
	for row := range height {
		for i := range upsampler.components {
			referenceUpsampleRow(&upsampler.components[i], data[i], row, width, lineBuffers[i])
		}
		convert(lineBuffers, buffer[row*lineSize:(row+1)*lineSize])
	}
	if len(components) == 4 {
		buffer = jpegCMYKToRGB(buffer)
	}
	for i := range width * height {
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = buffer[3*i], buffer[3*i+1], buffer[3*i+2], 255
	}
	return img, nil
}
