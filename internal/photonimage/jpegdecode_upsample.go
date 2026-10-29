// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-FileCopyrightText: Copyright (C) 1991-2014, Thomas G. Lane, Guido Vollbeding
// SPDX-License-Identifier: MIT AND IJG

package photonimage

import "image"

// Ports jpeg-decoder 0.3.1 src/upsampler.rs (cited as upsampler.rs), the colour conversions of src/decoder.rs (:1300-1508, cited as
// decoder.rs) and worker/mod.rs compute_image_parallel (:97-128, the path wasm32 takes), fused with the conversion of image 0.24.9
// to RGBA8: the Rust code interleaves the components into an RGB (or CMYK) buffer, the image crate turns CMYK into RGB, and
// `to_rgba8` adds the alpha; here each row goes from the upsampled components straight to the RGBA row, which gives the same
// bytes (jpegdecode_equivalence_test.go compares it with the line-by-line port of the Rust code).

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

// line is the row `row` of the component at the resolution of the image (upsampler.rs:119-250): the row of the plane itself where
// the component needs no upsampling (UpsamplerH1V1 copies it), otherwise buf, filled. The result has at least outputWidth samples.
func (c *jpegUpsamplerComponent) line(input []byte, row, outputWidth int, buf []byte) []byte {
	switch c.kind {
	case jpegUpsampleH1V1:
		// UpsamplerH1V1 (upsampler.rs:119-132)
		return input[row*c.stride:][:outputWidth]
	case jpegUpsampleH2V1:
		// UpsamplerH2V1 (upsampler.rs:134-163)
		inputWidth := c.width
		input = input[row*c.stride:]
		if inputWidth == 1 {
			buf[0] = input[0]
			buf[1] = input[0]
			return buf
		}
		input = input[:inputWidth]
		buf = buf[:2*inputWidth]
		buf[0] = input[0]
		buf[1] = byte((uint32(input[0])*3 + uint32(input[1]) + 2) >> 2)
		for i := 1; i < inputWidth-1; i++ {
			sample := 3*uint32(input[i]) + 2
			buf[i*2] = byte((sample + uint32(input[i-1])) >> 2)
			buf[i*2+1] = byte((sample + uint32(input[i+1])) >> 2)
		}
		buf[(inputWidth-1)*2] = byte((uint32(input[inputWidth-1])*3 + uint32(input[inputWidth-2]) + 2) >> 2)
		buf[(inputWidth-1)*2+1] = input[inputWidth-1]
		return buf
	case jpegUpsampleH1V2:
		// UpsamplerH1V2 (upsampler.rs:165-189)
		near, far := jpegNearFarRows(row, c.height)
		inputNear := input[near*c.stride:][:outputWidth]
		inputFar := input[far*c.stride:][:outputWidth]
		buf = buf[:outputWidth]
		for i := range buf {
			buf[i] = byte((3*uint32(inputNear[i]) + uint32(inputFar[i]) + 2) >> 2)
		}
		return buf
	case jpegUpsampleH2V2:
		// UpsamplerH2V2 (upsampler.rs:191-228)
		near, far := jpegNearFarRows(row, c.height)
		inputWidth := c.width
		inputNear := input[near*c.stride:]
		inputFar := input[far*c.stride:]
		if inputWidth == 1 {
			value := byte((3*uint32(inputNear[0]) + uint32(inputFar[0]) + 2) >> 2)
			buf[0] = value
			buf[1] = value
			return buf
		}
		inputNear = inputNear[:inputWidth]
		inputFar = inputFar[:inputWidth]
		buf = buf[:2*inputWidth]
		t1 := 3*uint32(inputNear[0]) + uint32(inputFar[0])
		buf[0] = byte((t1 + 2) >> 2)
		for i := 1; i < inputWidth; i++ {
			t0 := t1
			t1 = 3*uint32(inputNear[i]) + uint32(inputFar[i])
			buf[i*2-1] = byte((3*t0 + t1 + 8) >> 4)
			buf[i*2] = byte((3*t1 + t0 + 8) >> 4)
		}
		buf[inputWidth*2-1] = byte((t1 + 2) >> 2)
		return buf
	default:
		// UpsamplerGeneric (upsampler.rs:230-250): nearest neighbour.
		index := 0
		start := (row / c.vScaling) * c.stride
		for _, val := range input[start : start+c.width] {
			for range c.hScaling {
				buf[index] = val
				index++
			}
		}
		return buf
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

// jpegColorTransform is decoder.rs:76-98 ColorTransform. `None` and `Unknown` are not here: determine_color_transform gives
// `None` only through `set_color_transform`, which the image crate never calls, and `Unknown` only for a frame of other than 1, 3
// or 4 components, which decode_internal has refused.
type jpegColorTransform uint8

const (
	jpegTransformGrayscale jpegColorTransform = iota
	jpegTransformRGB
	jpegTransformYCbCr
	jpegTransformCMYK
	jpegTransformYCCK
	jpegTransformJcsBgYcc
	jpegTransformJcsBgRgb
)

// jpegConvert is the colour conversion of choose_color_convert_func.
type jpegConvert uint8

const (
	jpegConvertRGB jpegConvert = iota
	jpegConvertYCbCr
	jpegConvertCMYK
	jpegConvertYCCK
)

// chooseJPEGColorConvert is decoder.rs:1339-1389 choose_color_convert_func for the combinations determine_color_transform gives.
func chooseJPEGColorConvert(componentCount int, transform jpegColorTransform) (jpegConvert, error) {
	invalid := func(channels int, what string) error {
		return jpegFormatError("Invalid number of channels (%d) for %s data", channels, what)
	}
	switch {
	case transform == jpegTransformJcsBgYcc:
		return 0, jpegUnsupported("ColorTransform(JcsBgYcc)")
	case transform == jpegTransformJcsBgRgb:
		return 0, jpegUnsupported("ColorTransform(JcsBgRgb)")
	case componentCount == 3 && transform == jpegTransformRGB:
		return jpegConvertRGB, nil
	case componentCount == 3 && transform == jpegTransformYCbCr:
		return jpegConvertYCbCr, nil
	case componentCount == 3 && transform == jpegTransformYCCK:
		return 0, invalid(3, "YCCK")
	case componentCount == 4 && transform == jpegTransformCMYK:
		return jpegConvertCMYK, nil
	case componentCount == 4 && transform == jpegTransformYCCK:
		return jpegConvertYCCK, nil
	case componentCount == 4 && transform == jpegTransformYCbCr:
		return 0, invalid(4, "YCbCr")
	}
	return 0, jpegFormatError("Unknown colour transform")
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

// jpegYCbCrTable holds the four products of decoder.rs:1491-1500 ycbcr_to_rgb for every chroma value, in the same i32 arithmetic.
type jpegYCbCrTable struct{ crR, cbB, cbG, crG [256]int32 }

var jpegYCbCr = func() *jpegYCbCrTable {
	t := new(jpegYCbCrTable)
	for v := range 256 {
		d := int32(v) - 128
		t.crR[v] = jpegYCbCrR * d
		t.cbB[v] = jpegYCbCrB * d
		t.cbG[v] = jpegYCbCrGCb * d
		t.crG[v] = jpegYCbCrGCr * d
	}
	return t
}()

// jpegClampFixedPoint is decoder.rs:1506-1508 clamp_fixed_point.
func jpegClampFixedPoint(value int32) uint8 {
	return uint8(max(min(value>>jpegFixedPointOffset, 255), 0))
}

// jpegYCbCrToRGB is decoder.rs:1491-1500 ycbcr_to_rgb (ITU-R BT.601, based on libjpeg-turbo's jdcolext.c).
func jpegYCbCrToRGB(y, cb, cr uint8) (r, g, b uint8) {
	t := jpegYCbCr
	yy := int32(y)*(1<<jpegFixedPointOffset) + jpegFixedPointHalf
	return jpegClampFixedPoint(yy + t.crR[cr]), jpegClampFixedPoint(yy - t.cbG[cb] - t.crG[cr]), jpegClampFixedPoint(yy + t.cbB[cb])
}

// jpegConvertRow writes the n = len(dst)/4 pixels of a row as RGBA: the upsampled lines of the components through the colour
// conversion (decoder.rs:1391-1474 color_convert_line_*) and then what image 0.24.9 does with the result: a CMYK image goes
// through cmyk_to_rgb (codecs/jpeg/decoder.rs:122-143) and `to_rgba8` gives the alpha 255.
func jpegConvertRow(kind jpegConvert, dst []byte, lines *[jpegMaxComponents][]byte) {
	n := len(dst) / 4
	switch kind {
	case jpegConvertRGB:
		r, g, b := lines[0][:n], lines[1][:n], lines[2][:n]
		for i := range n {
			d := dst[4*i : 4*i+4 : 4*i+4]
			d[0], d[1], d[2], d[3] = r[i], g[i], b[i], 255
		}
	case jpegConvertYCbCr:
		t := jpegYCbCr
		y, cb, cr := lines[0][:n], lines[1][:n], lines[2][:n]
		for i := range n {
			yy := int32(y[i])*(1<<jpegFixedPointOffset) + jpegFixedPointHalf
			d := dst[4*i : 4*i+4 : 4*i+4]
			d[0] = jpegClampFixedPoint(yy + t.crR[cr[i]])
			d[1] = jpegClampFixedPoint(yy - t.cbG[cb[i]] - t.crG[cr[i]])
			d[2] = jpegClampFixedPoint(yy + t.cbB[cb[i]])
			d[3] = 255
		}
	case jpegConvertCMYK:
		// color_convert_line_cmyk stores 255 - c, and cmyk_to_rgb gives (255 - (255 - k)) * (255 - (255 - c)) / 255.
		c, m, y, k := lines[0][:n], lines[1][:n], lines[2][:n], lines[3][:n]
		for i := range n {
			kk := uint16(k[i])
			d := dst[4*i : 4*i+4 : 4*i+4]
			d[0] = byte(kk * uint16(c[i]) / 255)
			d[1] = byte(kk * uint16(m[i]) / 255)
			d[2] = byte(kk * uint16(y[i]) / 255)
			d[3] = 255
		}
	default: // jpegConvertYCCK
		// color_convert_line_ycck stores the RGB of (c, m, y) and 255 - k, and cmyk_to_rgb gives k * (255 - r) / 255.
		t := jpegYCbCr
		c, m, y, k := lines[0][:n], lines[1][:n], lines[2][:n], lines[3][:n]
		for i := range n {
			yy := int32(c[i])*(1<<jpegFixedPointOffset) + jpegFixedPointHalf
			r := jpegClampFixedPoint(yy + t.crR[y[i]])
			g := jpegClampFixedPoint(yy - t.cbG[m[i]] - t.crG[y[i]])
			b := jpegClampFixedPoint(yy + t.cbB[m[i]])
			kk := uint16(k[i])
			d := dst[4*i : 4*i+4 : 4*i+4]
			d[0] = byte(kk * (255 - uint16(r)) / 255)
			d[1] = byte(kk * (255 - uint16(g)) / 255)
			d[2] = byte(kk * (255 - uint16(b)) / 255)
			d[3] = 255
		}
	}
}

// computeImageRGBA is decoder.rs:1300-1337 compute_image and worker/mod.rs:97-128 compute_image_parallel followed by the
// conversion to RGBA: the upsampled and converted samples of the planes. A single component's plane is read at the stride of its
// blocks (compute_image moves its rows to a stride of the width first, which gives the same samples).
func computeImageRGBA(components []jpegComponent, data [][]byte, outputSize jpegDimensions, transform jpegColorTransform) (*image.NRGBA, error) {
	if len(data) == 0 {
		return nil, jpegFormatError("not all components have data")
	}
	for _, plane := range data {
		if len(plane) == 0 {
			return nil, jpegFormatError("not all components have data")
		}
	}
	width, height := int(outputSize.width), int(outputSize.height)

	if len(components) == 1 {
		component := &components[0]
		lineStride := int(component.blockSize.width) * component.dctScale
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		for y := range height {
			src := data[0][y*lineStride:][:width]
			dst := img.Pix[y*img.Stride:][:4*width]
			for x, v := range src {
				d := dst[4*x : 4*x+4 : 4*x+4]
				d[0], d[1], d[2], d[3] = v, v, v, 255
			}
		}
		return img, nil
	}

	kind, err := chooseJPEGColorConvert(len(components), transform)
	if err != nil {
		return nil, err
	}
	upsampler, err := newJPEGUpsampler(components, outputSize.width, outputSize.height)
	if err != nil {
		return nil, err
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	var buffers [jpegMaxComponents][]byte
	for i := range components {
		if upsampler.components[i].kind != jpegUpsampleH1V1 {
			buffers[i] = make([]byte, upsampler.lineBufferSize)
		}
	}
	for row := range height {
		var lines [jpegMaxComponents][]byte
		for i := range components {
			lines[i] = upsampler.components[i].line(data[i], row, width, buffers[i])
		}
		jpegConvertRow(kind, img.Pix[row*img.Stride:][:4*width], &lines)
	}
	return img, nil
}
