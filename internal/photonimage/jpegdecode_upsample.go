// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-FileCopyrightText: Copyright (C) 1991-2014, Thomas G. Lane, Guido Vollbeding
// SPDX-License-Identifier: MIT AND IJG

package photonimage

// Ports jpeg-decoder 0.3.1 src/upsampler.rs (cited as upsampler.rs), the colour conversions of src/decoder.rs (:1339-1508, cited as
// decoder.rs) and worker/mod.rs compute_image_parallel (:97-128, the path wasm32 takes).

// jpegUpsampleKind is the `Box<dyn Upsample>` choose_upsampler picks (upsampler.rs:76-105).
type jpegUpsampleKind uint8

const (
	jpegUpsampleH1V1 jpegUpsampleKind = iota
	jpegUpsampleH2V1
	jpegUpsampleH1V2
	jpegUpsampleH2V2
	jpegUpsampleGeneric
)

// jpegUpsamplerComponent is upsampler.rs:12-17 UpsamplerComponent.
type jpegUpsamplerComponent struct {
	kind                  jpegUpsampleKind
	hScaling, vScaling    int // the generic upsampler's factors
	width, height, stride int
}

// jpegUpsampler is upsampler.rs:7-10 Upsampler.
type jpegUpsampler struct {
	components     []jpegUpsamplerComponent
	lineBufferSize int
}

// newJPEGUpsampler is upsampler.rs:20-45 Upsampler::new: it fails when a subsampling ratio is not an integer.
func newJPEGUpsampler(components []jpegComponent, outputWidth, outputHeight uint16) (*jpegUpsampler, error) {
	var hMax, vMax uint8
	for i := range components {
		hMax = max(hMax, components[i].hSampling)
		vMax = max(vMax, components[i].vSampling)
	}
	u := &jpegUpsampler{components: make([]jpegUpsamplerComponent, len(components))}
	maxWidth := 0
	for i := range components {
		c := &components[i]
		uc := &u.components[i]
		if err := chooseJPEGUpsampler(uc, c.hSampling, c.vSampling, hMax, vMax, outputWidth, outputHeight); err != nil {
			return nil, err
		}
		uc.width = int(c.size.width)
		uc.height = int(c.size.height)
		uc.stride = int(c.blockSize.width) * c.dctScale
		maxWidth = max(maxWidth, int(c.size.width))
	}
	u.lineBufferSize = maxWidth * int(hMax)
	return u, nil
}

// chooseJPEGUpsampler is upsampler.rs:76-105 choose_upsampler.
func chooseJPEGUpsampler(uc *jpegUpsamplerComponent, hs, vs, hMax, vMax uint8, outputWidth, outputHeight uint16) error {
	h1 := hs == hMax || outputWidth == 1
	v1 := vs == vMax || outputHeight == 1
	h2 := hs*2 == hMax
	v2 := vs*2 == vMax

	switch {
	case h1 && v1:
		uc.kind = jpegUpsampleH1V1
	case h2 && v1:
		uc.kind = jpegUpsampleH2V1
	case h1 && v2:
		uc.kind = jpegUpsampleH1V2
	case h2 && v2:
		uc.kind = jpegUpsampleH2V2
	case hMax%hs != 0 || vMax%vs != 0:
		return jpegUnsupported("NonIntegerSubsamplingRatio")
	default:
		uc.kind = jpegUpsampleGeneric
		uc.hScaling = int(hMax / hs)
		uc.vScaling = int(vMax / vs)
	}
	return nil
}

// upsampleRow writes the row `row` of the component, upsampled to the image's resolution, to output (upsampler.rs:119-250).
func (c *jpegUpsamplerComponent) upsampleRow(input []byte, row, outputWidth int, output []byte) {
	switch c.kind {
	case jpegUpsampleH1V1:
		// UpsamplerH1V1 (upsampler.rs:119-132)
		input = input[row*c.stride:]
		copy(output[:outputWidth], input[:outputWidth])
	case jpegUpsampleH2V1:
		// UpsamplerH2V1 (upsampler.rs:134-163)
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
		// UpsamplerH1V2 (upsampler.rs:165-189)
		near, far := jpegNearFarRows(row, c.height)
		inputNear := input[near*c.stride:][:outputWidth]
		inputFar := input[far*c.stride:][:outputWidth]
		output = output[:outputWidth]
		for i := range output {
			output[i] = byte((3*uint32(inputNear[i]) + uint32(inputFar[i]) + 2) >> 2)
		}
	case jpegUpsampleH2V2:
		// UpsamplerH2V2 (upsampler.rs:191-228)
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
		// UpsamplerGeneric (upsampler.rs:230-250): nearest neighbour.
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

// jpegNearFarRows is the row arithmetic of upsampler.rs:174-177 and :200-203, which Rust evaluates in f32:
// `row_near = row as f32 / 2.0; row_far = (row_near + row_near.fract() * 3.0 - 0.25).min((input_height - 1) as f32)`, both rows
// taken as `as usize` (truncating, and 0 for a negative value). For an even row row_far = row/2 - 0.25 truncates to row/2 - 1 (0 at
// the top); for an odd one it is row/2 + 1.75 and truncates to row/2 + 1, which the min caps at input_height - 1. jpegdecode_test.go
// compares this with the f32 formula for every row of every height.
func jpegNearFarRows(row, inputHeight int) (near, far int) {
	near = row / 2
	if row&1 == 0 {
		return near, max(near-1, 0)
	}
	return near, min(near+1, inputHeight-1)
}

// upsampleAndInterleaveRow is upsampler.rs:47-62 upsample_and_interleave_row for the row `row` of the image.
func (u *jpegUpsampler) upsampleAndInterleaveRow(planes [][]byte, row, outputWidth int, lineBuffers [][]byte, output []byte, convert func([][]byte, []byte)) {
	for i := range u.components {
		u.components[i].upsampleRow(planes[i], row, outputWidth, lineBuffers[i])
	}
	convert(lineBuffers, output)
}

// jpegColorTransform is decoder.rs:76-98 ColorTransform.
type jpegColorTransform uint8

const (
	jpegTransformNone jpegColorTransform = iota
	jpegTransformUnknown
	jpegTransformGrayscale
	jpegTransformRGB
	jpegTransformYCbCr
	jpegTransformCMYK
	jpegTransformYCCK
	jpegTransformJcsBgYcc
	jpegTransformJcsBgRgb
)

// chooseJPEGColorConvert is decoder.rs:1339-1389 choose_color_convert_func.
func chooseJPEGColorConvert(componentCount int, transform jpegColorTransform) (func([][]byte, []byte), error) {
	invalid := func(channels int, what string) error {
		return jpegFormatError("Invalid number of channels (%d) for %s data", channels, what)
	}
	switch componentCount {
	case 3:
		switch transform {
		case jpegTransformNone:
			return jpegColorNoConvert, nil
		case jpegTransformGrayscale:
			return nil, invalid(3, "Grayscale")
		case jpegTransformRGB:
			return jpegColorConvertLineRGB, nil
		case jpegTransformYCbCr:
			return jpegColorConvertLineYCbCr, nil
		case jpegTransformCMYK:
			return nil, invalid(3, "CMYK")
		case jpegTransformYCCK:
			return nil, invalid(3, "YCCK")
		case jpegTransformJcsBgYcc:
			return nil, jpegUnsupported("ColorTransform(JcsBgYcc)")
		case jpegTransformJcsBgRgb:
			return nil, jpegUnsupported("ColorTransform(JcsBgRgb)")
		}
	case 4:
		switch transform {
		case jpegTransformNone:
			return jpegColorNoConvert, nil
		case jpegTransformGrayscale:
			return nil, invalid(4, "Grayscale")
		case jpegTransformRGB:
			return nil, invalid(4, "RGB")
		case jpegTransformYCbCr:
			return nil, invalid(4, "YCbCr")
		case jpegTransformCMYK:
			return jpegColorConvertLineCMYK, nil
		case jpegTransformYCCK:
			return jpegColorConvertLineYCCK, nil
		case jpegTransformJcsBgYcc:
			return nil, jpegUnsupported("ColorTransform(JcsBgYcc)")
		case jpegTransformJcsBgRgb:
			return nil, jpegUnsupported("ColorTransform(JcsBgRgb)")
		}
	default:
		return nil, jpegFormatError("panic: unreachable component count %d", componentCount)
	}
	return nil, jpegFormatError("Unknown colour transform")
}

// jpegColorConvertLineRGB is decoder.rs:1391-1404 color_convert_line_rgb: the zip of the chunks of the output with the components
// stops at the shortest of them.
func jpegColorConvertLineRGB(data [][]byte, output []byte) {
	r, g, b := data[0], data[1], data[2]
	for j := range min(len(output)/3, len(r), len(g), len(b)) {
		output[3*j] = r[j]
		output[3*j+1] = g[j]
		output[3*j+2] = b[j]
	}
}

// jpegColorConvertLineYCbCr is decoder.rs:1406-1437 color_convert_line_ycbcr (without the arch-specific path).
func jpegColorConvertLineYCbCr(data [][]byte, output []byte) {
	y, cb, cr := data[0], data[1], data[2]
	for j := range min(len(output)/3, len(y), len(cb), len(cr)) {
		output[3*j], output[3*j+1], output[3*j+2] = jpegYCbCrToRGB(y[j], cb[j], cr[j])
	}
}

// jpegColorConvertLineYCCK is decoder.rs:1439-1456 color_convert_line_ycck.
func jpegColorConvertLineYCCK(data [][]byte, output []byte) {
	c, m, y, k := data[0], data[1], data[2], data[3]
	for j := range min(len(output)/4, len(c), len(m), len(y), len(k)) {
		output[4*j], output[4*j+1], output[4*j+2] = jpegYCbCrToRGB(c[j], m[j], y[j])
		output[4*j+3] = 255 - k[j]
	}
}

// jpegColorConvertLineCMYK is decoder.rs:1458-1474 color_convert_line_cmyk.
func jpegColorConvertLineCMYK(data [][]byte, output []byte) {
	c, m, y, k := data[0], data[1], data[2], data[3]
	for j := range min(len(output)/4, len(c), len(m), len(y), len(k)) {
		output[4*j] = 255 - c[j]
		output[4*j+1] = 255 - m[j]
		output[4*j+2] = 255 - y[j]
		output[4*j+3] = 255 - k[j]
	}
}

// jpegColorNoConvert is decoder.rs:1476-1484 color_no_convert: every component's line one after the other.
func jpegColorNoConvert(data [][]byte, output []byte) {
	n := 0
	for _, line := range data {
		n += copy(output[n:], line)
	}
}

// The fixed point of decoder.rs:1486-1487.
const (
	jpegFixedPointOffset = 20
	jpegFixedPointHalf   = (1 << jpegFixedPointOffset) / 2
)

// The stbi_f2f constants of decoder.rs:1502-1504 `(x * (1 << 20) as f32 + 0.5) as i32`, checked against the float32 evaluation in
// jpegdecode_test.go.
const (
	jpegYCbCrR   = 1470104 // 1.40200
	jpegYCbCrGCb = 360857  // 0.34414
	jpegYCbCrGCr = 748830  // 0.71414
	jpegYCbCrB   = 1858077 // 1.77200
)

// jpegYCbCrToRGB is decoder.rs:1489-1500 ycbcr_to_rgb (ITU-R BT.601, based on libjpeg-turbo's jdcolext.c).
func jpegYCbCrToRGB(y, cb, cr uint8) (r, g, b uint8) {
	yy := int32(y)*(1<<jpegFixedPointOffset) + jpegFixedPointHalf
	cbv := int32(cb) - 128
	crv := int32(cr) - 128
	return jpegClampFixedPoint(yy + jpegYCbCrR*crv),
		jpegClampFixedPoint(yy - jpegYCbCrGCb*cbv - jpegYCbCrGCr*crv),
		jpegClampFixedPoint(yy + jpegYCbCrB*cbv)
}

// jpegClampFixedPoint is decoder.rs:1506-1508 clamp_fixed_point.
func jpegClampFixedPoint(value int32) uint8 {
	return uint8(max(min(value>>jpegFixedPointOffset, 255), 0))
}
