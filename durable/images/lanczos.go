package images

import (
	"image"
	"math"
)

// Ports packages/durable/src/images/index.ts: the Lanczos3 resize of a prepared image (index.ts:102,
// `resize(image, size.width, size.height, SamplingFilter.Lanczos3)`).

// pig divergence (D165): Go's Lanczos3, not Photon's `resize` with `SamplingFilter.Lanczos3`; the pixels differ by a mean of 0.01 of 255.
// resizeLanczos3 is src scaled to width by height with the Lanczos filter of radius 3, the sampling filter
// images/index.ts gives Photon's resize (SamplingFilter.Lanczos3). The channels are filtered as stored, not premultiplied.
func resizeLanczos3(src *image.NRGBA, width, height int) *image.NRGBA {
	bounds := src.Bounds()
	srcWidth, srcHeight := bounds.Dx(), bounds.Dy()
	horizontal := lanczosWeights(srcWidth, width)
	vertical := lanczosWeights(srcHeight, height)
	// Horizontal pass: every source row to the new width, in floats, so the vertical pass does not compound rounding.
	middle := make([]float32, width*srcHeight*4)
	for y := range srcHeight {
		row := src.Pix[y*src.Stride:]
		for x := range width {
			var sum [4]float32
			start, weights := horizontal[x].start, horizontal[x].weights
			for i, weight := range weights {
				p := row[(start+i)*4:]
				sum[0] += float32(p[0]) * weight //portlint:allow fusedmuladd D165: Go's Lanczos3, not Photon's, so no bit-exact counterpart
				sum[1] += float32(p[1]) * weight //portlint:allow fusedmuladd D165 (as above)
				sum[2] += float32(p[2]) * weight //portlint:allow fusedmuladd D165 (as above)
				sum[3] += float32(p[3]) * weight //portlint:allow fusedmuladd D165 (as above)
			}
			copy(middle[(y*width+x)*4:], sum[:])
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		start, weights := vertical[y].start, vertical[y].weights
		for x := range width {
			var sum [4]float32
			for i, weight := range weights {
				p := middle[((start+i)*width+x)*4:]
				sum[0] += p[0] * weight //portlint:allow fusedmuladd D165: Go's Lanczos3, not Photon's, so no bit-exact counterpart
				sum[1] += p[1] * weight //portlint:allow fusedmuladd D165 (as above)
				sum[2] += p[2] * weight //portlint:allow fusedmuladd D165 (as above)
				sum[3] += p[3] * weight //portlint:allow fusedmuladd D165 (as above)
			}
			out := dst.Pix[y*dst.Stride+x*4:]
			for c := range 4 {
				out[c] = clampByte(sum[c])
			}
		}
	}
	return dst
}

func clampByte(v float32) uint8 {
	switch {
	case !(v > 0): // NaN too: Photon's saturating `as u8` makes it 0
		return 0
	case v >= 255:
		return 255
	}
	return uint8(v + 0.5) //portlint:allow numbers v is finite and in (0, 255) here: the switch takes NaN and both ends
}

// contribution is the source pixels, from start, that one destination pixel is made of, and their weights, which sum to 1.
type contribution struct {
	start   int
	weights []float32
}

func lanczosWeights(srcSize, dstSize int) []contribution {
	const radius = 3.0
	scale := float64(srcSize) / float64(dstSize)
	// Shrinking widens the filter so every source pixel counts.
	filterScale := math.Max(scale, 1)
	support := radius * filterScale
	out := make([]contribution, dstSize)
	for i := range dstSize {
		center := (float64(i) + 0.5) * scale
		//portlint:allow fusedmuladd D165: Go's Lanczos3 window, not Photon's, so no bit-exact counterpart
		start := max(0, int(math.Floor(center-support+0.5)))     //portlint:allow numbers center and support are finite and within ±(srcSize+support) of 0: the sizes are positive ints
		end := min(srcSize, int(math.Floor(center+support+0.5))) //portlint:allow fusedmuladd D165 (as above)
		if end <= start {
			start, end = min(max(int(center), 0), srcSize-1), min(max(int(center), 0), srcSize-1)+1 //portlint:allow numbers center is finite and below srcSize: the sizes are positive ints
		}
		weights := make([]float32, end-start)
		var total float64
		for j := range weights {
			w := lanczos((float64(start+j) + 0.5 - center) / filterScale) //portlint:allow fusedmuladd D165 (as above)
			weights[j] = float32(w)
			total += w
		}
		if total == 0 {
			weights[0], total = 1, 1
		}
		for j := range weights {
			weights[j] = float32(float64(weights[j]) / total)
		}
		out[i] = contribution{start: start, weights: weights}
	}
	return out
}

func lanczos(x float64) float64 {
	if x == 0 {
		return 1
	}
	if x <= -3 || x >= 3 {
		return 0
	}
	px := math.Pi * x
	return 3 * math.Sin(px) * math.Sin(px/3) / (px * px)
}
