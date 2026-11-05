// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"testing"
)

// The tests of the faster code of the lossy decoder against the literal port in webpdecode_reference_test.go.

func TestWebPYUVToRGBExhaustive(t *testing.T) {
	var got, want [3]byte
	for y := range 256 {
		for u := range 256 {
			for v := range 256 {
				vp8FillSingle(uint8(y), uint8(u), uint8(v), got[:])
				refVP8FillSingle(uint8(y), uint8(u), uint8(v), want[:])
				if got != want {
					t.Fatalf("y %d u %d v %d: %v, want %v", y, u, v, got, want)
				}
			}
		}
	}
}

func TestWebPFillMatchesLiteral(t *testing.T) {
	rng := newWebPRNG(201)
	for trial := range 600 {
		w, h := 1+rng.intn(40), 1+rng.intn(30)
		f := &vp8Frame{width: uint16(w), height: uint16(h)}
		f.ybuf = rng.bytes(w * h)
		f.ubuf = rng.bytes(int(f.chromaWidth()) * int(f.chromaHeight()))
		f.vbuf = rng.bytes(len(f.ubuf))
		for _, stride := range []int{3, 4} {
			size := w*h*stride + rng.intn(5) - 2
			if rng.chance(70) {
				size = w * h * stride
			}
			got := make([]byte, size)
			want := make([]byte, size)
			f.fill(got, stride)
			refFill(f, want, stride)
			for i := range got {
				if stride == 4 && i%4 == 3 {
					if i/4 < len(f.ybuf) && got[i] != 255 {
						t.Fatalf("trial %d: alpha %d at %d", trial, got[i], i)
					}
					continue
				}
				if got[i] != want[i] {
					t.Fatalf("trial %d stride %d: byte %d is %d, want %d", trial, stride, i, got[i], want[i])
				}
			}
		}
	}
}

func TestWebPBoolReaderMatchesLiteral(t *testing.T) {
	rng := newWebPRNG(203)
	for trial := range 3000 {
		buf := rng.bytes(2 + rng.intn(40))
		if rng.chance(10) {
			buf[0], buf[1] = 0xff, 0xff
		}
		var fast vp8BoolReader
		if err := fast.init(buf); err != nil {
			t.Fatal(err)
		}
		var ref refBoolReader
		ref.init(buf)
		for i := range 600 {
			prob := uint8(rng.intn(256))
			if rng.chance(20) {
				prob = []uint8{0, 1, 128, 254, 255}[rng.intn(5)]
			}
			if fast.readBool(prob) != ref.readBool(prob) {
				t.Fatalf("trial %d bool %d: different value", trial, i)
			}
			if fast.rng != ref.rng || fast.value != ref.value || fast.bitCount != ref.bitCount || fast.index != ref.index {
				t.Fatalf("trial %d bool %d: state %+v, literal %+v", trial, i, fast, ref)
			}
		}
	}
}

func TestWebPTokenReaderMatchesLiteral(t *testing.T) {
	rng := newWebPRNG(205)
	for trial := range 2000 {
		buf := rng.bytes(2 + rng.intn(60))
		var fast vp8BoolReader
		_ = fast.init(buf)
		var ref refBoolReader
		ref.init(buf)
		for i := range 300 {
			var probs [vp8NumDCTTokens - 1]uint8
			for j := range probs {
				probs[j] = uint8(rng.intn(256))
			}
			afterZero := rng.chance(30)
			start := 0
			if afterZero {
				start = 2
			}
			got := fast.readToken(&probs, afterZero)
			want := ref.readWithTree(vp8DCTTokenTree[:], probs[:], start)
			if got != want {
				t.Fatalf("trial %d token %d: %d, want %d", trial, i, got, want)
			}
			if fast.rng != ref.rng || fast.value != ref.value || fast.bitCount != ref.bitCount || fast.index != ref.index {
				t.Fatalf("trial %d token %d: state differs", trial, i)
			}
		}
	}
}

func TestWebPCoefficientsMatchLiteral(t *testing.T) {
	rng := newWebPRNG(207)
	for trial := range 4000 {
		d := newVP8Decoder(nil)
		for i := range d.tokenProbs {
			for j := range d.tokenProbs[i] {
				for k := range d.tokenProbs[i][j] {
					for m := range d.tokenProbs[i][j][k] {
						if rng.chance(50) {
							d.tokenProbs[i][j][k][m] = uint8(rng.intn(256))
						}
					}
				}
			}
		}
		buf := rng.bytes(2 + rng.intn(100))
		if err := d.partitions[0].init(buf); err != nil {
			t.Fatal(err)
		}
		var ref refBoolReader
		ref.init(buf)

		for block := range 24 {
			plane := []int{0, 1, 2, 3}[rng.intn(4)]
			complexity := rng.intn(3)
			dcq, acq := int16(4+rng.intn(300)), int16(4+rng.intn(300))
			var got, want [16]int32
			gotAny, gotAC := d.readCoefficients(got[:], 0, plane, complexity, dcq, acq)
			wantAny := refReadCoefficients(d.tokenProbs, &ref, want[:], plane, complexity, dcq, acq)
			if gotAny != wantAny || got != want {
				t.Fatalf("trial %d block %d plane %d: %v %v, literal %v %v", trial, block, plane, got, gotAny, want, wantAny)
			}
			ac := false
			for _, v := range got[1:] {
				ac = ac || v != 0
			}
			if gotAC != ac {
				t.Fatalf("trial %d block %d: hasAC %v with %v", trial, block, gotAC, got)
			}
		}
	}
}

func TestWebPResidueMatchesLiteral(t *testing.T) {
	rng := newWebPRNG(209)
	for trial := range 200000 {
		var block [16]int32
		first := int32(rng.next())
		if rng.chance(50) {
			first = int32(rng.intn(200000)) - 100000
		}
		block[0] = first
		hasAC := rng.chance(30)
		if hasAC {
			for i := 1; i < 16; i++ {
				if rng.chance(30) {
					block[i] = int32(rng.intn(60000)) - 30000
				}
			}
		}
		got, want := block, block
		vp8Residue(got[:], hasAC)
		refVP8IDCT4x4(want[:])
		if got != want {
			t.Fatalf("trial %d: %v, literal %v for %v", trial, got, want, block)
		}
	}
}

// edgePixels are pixels around an edge for a loop filter: steps and noise of the size the limits are, so that the filters do something.
func edgePixels(rng *webpRNG, n int) []byte {
	pixels := make([]byte, n)
	value := rng.intn(256)
	spread := 1 + rng.intn(40)
	for i := range pixels {
		if rng.chance(15) {
			value = rng.intn(256)
		}
		pixels[i] = byte(min(max(value+rng.intn(spread)-spread/2, 0), 255))
	}
	return pixels
}

func TestWebPLoopFiltersMatchLiteral(t *testing.T) {
	rng := newWebPRNG(211)
	filtered := 0
	for trial := range 300000 {
		stride := []int{1, 1, 7, 33}[rng.intn(4)]
		pixels := edgePixels(rng, 8*stride)
		point := 4 * stride
		hev := uint8(1 + rng.intn(3))
		interior := uint8(1 + rng.intn(63))
		edge := uint8(rng.intn(200))
		if rng.chance(10) {
			interior, edge = 63, 255
		}

		got := append([]byte(nil), pixels...)
		want := append([]byte(nil), pixels...)
		switch trial % 3 {
		case 0:
			vp8MacroblockFilter(hev, interior, edge, got, point, stride)
			refVP8MacroblockFilter(hev, interior, edge, want, point, stride)
		case 1:
			vp8SubblockFilter(hev, interior, edge, got, point, stride)
			refVP8SubblockFilter(hev, interior, edge, want, point, stride)
		default:
			vp8SimpleSegment(edge, got, point, stride)
			refVP8SimpleSegment(edge, want, point, stride)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("trial %d filter %d stride %d: %v, literal %v for %v", trial, trial%3, stride, got, want, pixels)
		}
		if !bytes.Equal(got, pixels) {
			filtered++
		}
	}
	if filtered < 50000 {
		t.Fatalf("only %d of the lines were filtered", filtered)
	}
}

func TestWebPUnfilterAlphaMatchesLiteral(t *testing.T) {
	rng := newWebPRNG(213)
	for trial := range 4000 {
		w, h := 1+rng.intn(30), 1+rng.intn(20)
		data := rng.bytes(w * h)
		for method := range uint8(4) {
			got := rng.bytes(w * h * 4) // the colors are in place, the alpha is whatever
			want := bytes.Clone(got)
			webpUnfilterAlpha(method, got, data, w, h)
			refUnfilterAlpha(method, want, data, w, h)
			if !bytes.Equal(got, want) {
				t.Fatalf("trial %d method %d %dx%d: differs", trial, method, w, h)
			}
		}
	}
}
