package photonimage

import (
	"math"
	"math/bits"
	"testing"
)

// The functions below are encoder.rs written the way Rust evaluates it (single-precision operations one after the other, f32::round,
// encode_coefficient on u16), the way EncodeJPEG's first version ran them. EncodeJPEG replaces them with lookups, a reciprocal
// and bit tricks; the tests prove each replacement gives the same result for every input.

// referenceRGBToYCbCr is encoder.rs rgb_to_ycbcr (:789-805). Every product is converted to float32 so that no platform fuses it with
// the addition that follows, as Rust does not.
func referenceRGBToYCbCr(r, g, b uint8) (uint8, uint8, uint8) {
	yR, yG, yB := yfR/jpegMaxSubpixel, yfG/jpegMaxSubpixel, yfB/jpegMaxSubpixel
	cbR, cbG, cbB := cbfR/jpegMaxSubpixel, cbfG/jpegMaxSubpixel, cbfB/jpegMaxSubpixel
	crR, crG, crB := crfR/jpegMaxSubpixel, crfG/jpegMaxSubpixel, crfB/jpegMaxSubpixel
	fr, fg, fb := float32(r), float32(g), float32(b)
	y := float32(yR*fr) + float32(yG*fg) + float32(yB*fb)
	cb := float32(cbR*fr) - float32(-cbG*fg) + float32(cbB*fb) + chromaOffset
	cr := float32(crR*fr) - float32(-crG*fg) - float32(-crB*fb) + chromaOffset
	return referenceSaturate(y), referenceSaturate(cb), referenceSaturate(cr)
}

// referenceSaturate is Rust's saturating `f32 as u8`.
func referenceSaturate(v float32) uint8 {
	if !(v > 0) {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

// referenceQuantize is `((coefficient / 8) as f32 / f32::from(table[i])).round() as i32` (encoder.rs:647-650); coefficient is already
// divided by 8.
func referenceQuantize(a int32, q uint8) int32 {
	return int32(math.Round(float64(float32(a) / float32(q))))
}

// referenceEncodeCoefficient is encoder.rs encode_coefficient (:768-786).
func referenceEncodeCoefficient(coefficient int32) (uint8, uint16) {
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

// TestColorTablesMatchRgbToYCbCr compares the lookups of loadBlock with rgb_to_ycbcr for all 2^24 colours.
func TestColorTablesMatchRgbToYCbCr(t *testing.T) {
	c := jpegColor
	for r := range 256 {
		for g := range 256 {
			for b := range 256 {
				wantY, wantCb, wantCr := referenceRGBToYCbCr(uint8(r), uint8(g), uint8(b))
				y := toU8(c.yR[r] + c.yG[g] + c.yB[b])
				cb := toU8(c.cbR[r] - c.cbG[g] + c.cbB[b] + chromaOffset)
				cr := toU8(c.crR[r] - c.crG[g] - c.crB[b] + chromaOffset)
				if y != wantY || cb != wantCb || cr != wantCr {
					t.Fatalf("rgb(%d,%d,%d): lookups give %d,%d,%d, rgb_to_ycbcr %d,%d,%d", r, g, b, y, cb, cr, wantY, wantCb, wantCr)
				}
			}
		}
	}
}

// TestQuantizeMatchesFloat32 compares quantize's reciprocal with the float32 division and f32::round, for every quantization
// table entry and every DCT coefficient |d| < 2^17 (the DCT of a block of bytes stays within |d| <= 8192), including the ones the
// division by 8 truncates.
func TestQuantizeMatchesFloat32(t *testing.T) {
	for q := 1; q <= 255; q++ {
		var table [64]uint8
		for i := range table {
			table[i] = uint8(q)
		}
		qt := newQuantTable(&table)
		var dct, original [64]int32
		for d := int32(-1 << 17); d < 1<<17; d += 64 {
			for i := range dct {
				dct[i] = d + int32(i)
			}
			original = dct
			nonzero := quantize(&dct, qt)
			for i := range dct {
				want := referenceQuantize(original[i]/8, uint8(q))
				if dct[i] != want {
					t.Fatalf("q=%d dct=%d: got %d, float32 gives %d", q, original[i], dct[i], want)
				}
				if i > 0 && (nonzero>>zigzagOf[i]&1 == 1) != (want != 0) {
					t.Fatalf("q=%d dct=%d: nonzero bit %d disagrees with %d", q, original[i], zigzagOf[i], want)
				}
			}
			if nonzero&1 != 0 {
				t.Fatalf("the DC coefficient is in the AC set")
			}
		}
	}
}

// TestQuantizeKeepsTruncatingTheDCTBy8 checks the division by 8 that precedes the quantization truncates toward zero as Rust's does.
func TestQuantizeKeepsTruncatingTheDCTBy8(t *testing.T) {
	var table [64]uint8
	for i := range table {
		table[i] = 1
	}
	qt := newQuantTable(&table)
	for _, c := range []struct{ dct, want int32 }{{7, 0}, {-7, 0}, {8, 1}, {-8, -1}, {15, 1}, {-15, -1}, {-16, -2}} {
		var dct [64]int32
		dct[0] = c.dct
		quantize(&dct, qt)
		if dct[0] != c.want {
			t.Errorf("dct %d: quantized to %d, want %d", c.dct, dct[0], c.want)
		}
	}
}

// TestCoefficientBitsMatchEncodeCoefficient compares writeBlock's inline size and value with encode_coefficient.
func TestCoefficientBitsMatchEncodeCoefficient(t *testing.T) {
	for c := int32(-4095); c <= 4095; c++ {
		size, value := referenceEncodeCoefficient(c)
		gotSize := uint8(bits.Len32(uint32(max(c, -c))))
		gotValue := uint16(uint32(c+c>>31) & (1<<gotSize - 1))
		if gotSize != size || gotValue != value {
			t.Fatalf("c=%d: size %d value %d, encode_coefficient %d %d", c, gotSize, gotValue, size, value)
		}
	}
}
