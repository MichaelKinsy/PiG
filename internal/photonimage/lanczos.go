package photonimage

import (
	"image"
	"math"
)

// ResizeLanczos3 is Photon's `resize(image, width, height, SamplingFilter.Lanczos3)` (packages/durable/src/images/index.ts:102, and
// image-resize-core.ts tryEncodings in the coding agent): the image crate 0.24.9 `imageops::resize` with its Lanczos3 filter. It scales the
// rows first, into 32-bit floats, then the columns, with the weights, the window and the f32 arithmetic of that code, so the pixels are
// Photon's own. The four channels are filtered as stored, not premultiplied. An image already of the requested size is returned as a copy.
func ResizeLanczos3(src *image.NRGBA, width, height int) *image.NRGBA {
	bounds := src.Bounds()
	srcWidth, srcHeight := bounds.Dx(), bounds.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	if srcWidth == 0 || srcHeight == 0 || width == 0 || height == 0 {
		return dst
	}
	if srcWidth == width && srcHeight == height {
		for y := range height {
			copy(dst.Pix[y*dst.Stride:y*dst.Stride+width*4], src.Pix[y*src.Stride:y*src.Stride+width*4])
		}
		return dst
	}
	middle := verticalSample(src, srcWidth, srcHeight, height)
	horizontalSample(middle, srcWidth, height, width, dst)
	return dst
}

// filterWindow is the part of one axis the output pixel at out is made of: the input pixels left..left+len(weights), with weights that sum
// to one (image 0.24.9 sample.rs, the loop head shared by horizontal_sample and vertical_sample).
func filterWindow(out, inSize, outSize int) (left int, weights []float32) {
	const support = float32(3.0)
	ratio := float32(inSize) / float32(outSize)
	sratio := ratio
	if ratio < 1 {
		sratio = 1
	}
	srcSupport := float32(support * sratio)
	// The centre of the output pixel in the input.
	input := float32((float32(out) + 0.5) * ratio)
	l := int64(math.Floor(float64(float32(input - srcSupport)))) //portlint:allow numbers the window is finite and within the image plus its support
	l = min(max(l, 0), int64(inSize)-1)
	r := int64(math.Ceil(float64(float32(input + srcSupport)))) //portlint:allow numbers as above
	r = min(max(r, l+1), int64(inSize))
	// Back to the left edge of the pixel: the kernel treats the centre of a pixel as 0.
	input = float32(input - 0.5)
	weights = make([]float32, 0, r-l)
	var sum float32
	for i := l; i < r; i++ {
		w := lanczos3(float32(float32(float32(i)-input) / sratio))
		weights = append(weights, w)
		sum = float32(sum + w)
	}
	for i := range weights {
		weights[i] = float32(weights[i] / sum)
	}
	return int(l), weights
}

// verticalSample is the columns of src scaled to newHeight, in floats (image 0.24.9 vertical_sample).
func verticalSample(src *image.NRGBA, width, height, newHeight int) []float32 {
	out := make([]float32, width*newHeight*4)
	for outY := range newHeight {
		left, weights := filterWindow(outY, height, newHeight)
		for x := range width {
			var t0, t1, t2, t3 float32
			for i, w := range weights {
				p := src.Pix[(left+i)*src.Stride+x*4:]
				t0 = float32(t0 + float32(float32(p[0])*w))
				t1 = float32(t1 + float32(float32(p[1])*w))
				t2 = float32(t2 + float32(float32(p[2])*w))
				t3 = float32(t3 + float32(float32(p[3])*w))
			}
			o := out[(outY*width+x)*4:]
			o[0], o[1], o[2], o[3] = t0, t1, t2, t3
		}
	}
	return out
}

// horizontalSample is the rows of the float image src scaled to newWidth, rounded to bytes (image 0.24.9 horizontal_sample).
func horizontalSample(src []float32, width, height, newWidth int, dst *image.NRGBA) {
	for outX := range newWidth {
		left, weights := filterWindow(outX, width, newWidth)
		for y := range height {
			var t0, t1, t2, t3 float32
			for i, w := range weights {
				p := src[(y*width+left+i)*4:]
				t0 = float32(t0 + float32(p[0]*w))
				t1 = float32(t1 + float32(p[1]*w))
				t2 = float32(t2 + float32(p[2]*w))
				t3 = float32(t3 + float32(p[3]*w))
			}
			o := dst.Pix[y*dst.Stride+outX*4:]
			o[0], o[1], o[2], o[3] = floatNearest(t0), floatNearest(t1), floatNearest(t2), floatNearest(t3)
		}
	}
}

// floatNearest is `NumCast::from(FloatNearest(clamp(v, 0, 255)))`: the nearest byte, halves away from zero.
func floatNearest(v float32) uint8 {
	switch {
	case !(v > 0): // NaN too
		return 0
	case v >= 255:
		return 255
	}
	return uint8(float32(math.Round(float64(v)))) //portlint:allow numbers v is finite and in (0, 255) here: the switch takes NaN and both ends
}

// lanczos3 is the filter's kernel, `lanczos(3.0, x)`: sinc(x) * sinc(x / 3) inside the support, 0 outside.
func lanczos3(x float32) float32 {
	const t = float32(3.0)
	if float32(abs32(x)) < t {
		return float32(sinc(x) * sinc(float32(x/t)))
	}
	return 0
}

func sinc(x float32) float32 {
	if x == 0 {
		return 1
	}
	a := float32(x * float32(math.Pi))
	return float32(float32(math.Sin(float64(a))) / a)
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
