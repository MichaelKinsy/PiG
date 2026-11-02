// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"fmt"
	"testing"
)

// The tests that prove each replacement of jpegdecode*.go equal to the literal port of jpegdecode_reference_test.go.

// TestJPEGYCbCrTableMatchesTheFormula checks the tabulated ycbcr_to_rgb against the formula for every (y, cb, cr).
func TestJPEGYCbCrTableMatchesTheFormula(t *testing.T) {
	for y := range 256 {
		for cb := range 256 {
			for cr := range 256 {
				r, g, b := jpegYCbCrToRGB(uint8(y), uint8(cb), uint8(cr))
				wr, wg, wb := referenceYCbCrToRGB(uint8(y), uint8(cb), uint8(cr))
				if r != wr || g != wg || b != wb {
					t.Fatalf("ycbcr_to_rgb(%d, %d, %d) = %d %d %d, the formula gives %d %d %d", y, cb, cr, r, g, b, wr, wg, wb)
				}
			}
		}
	}
}

// jdeComponents is the components of a frame of the sampling hv.
func jdeComponents(t *testing.T, w, h int, hv [][2]uint8) ([]jpegComponent, jpegDimensions) {
	t.Helper()
	components := make([]jpegComponent, len(hv))
	for i, f := range hv {
		components[i] = jpegComponent{identifier: uint8(i + 1), hSampling: f[0], vSampling: f[1], dctScale: 8}
	}
	if _, err := jpegUpdateComponentSizes(jpegDimensions{uint16(w), uint16(h)}, components); err != nil {
		t.Fatal(err)
	}
	return components, jpegDimensions{uint16(w), uint16(h)}
}

// TestComputeImageRGBAMatchesTheLiteralPipeline runs random planes of every sampling (every upsampler), size, and colour
// conversion through the fused pipeline and the literal one, which goes through the interleaved buffer.
func TestComputeImageRGBAMatchesTheLiteralPipeline(t *testing.T) {
	rnd := corpusRNG(12345)
	hvSets := [][][2]uint8{{{1, 1}}, {{2, 2}}, {{3, 1}}}
	for _, f := range jdcSamplings {
		hvSets = append(hvSets, f.hv)
	}
	hvSets = append(hvSets,
		[][2]uint8{{1, 1}, {1, 1}, {1, 1}, {1, 1}},
		[][2]uint8{{2, 2}, {1, 1}, {1, 1}, {2, 2}},
		[][2]uint8{{2, 1}, {1, 1}, {1, 1}, {1, 1}},
		[][2]uint8{{1, 1}, {1, 1}, {1, 1}, {2, 2}},
		[][2]uint8{{2, 2}, {1, 2}, {2, 1}, {1, 1}},
		[][2]uint8{{4, 2}, {2, 1}, {1, 1}, {4, 2}},
	)
	sizes := [][2]int{{1, 1}, {2, 1}, {1, 2}, {3, 3}, {7, 5}, {8, 8}, {9, 17}, {16, 16}, {17, 9}, {33, 31}, {65, 20}, {100, 75}, {1, 33}, {33, 1}}
	transforms := map[int][]jpegColorTransform{
		1: {jpegTransformGrayscale},
		3: {jpegTransformRGB, jpegTransformYCbCr, jpegTransformYCCK, jpegTransformJcsBgYcc},
		4: {jpegTransformCMYK, jpegTransformYCCK, jpegTransformYCbCr},
	}
	cases, matched := 0, 0
	for _, hv := range hvSets {
		for _, sz := range sizes {
			components, out := jdeComponents(t, sz[0], sz[1], hv)
			planes := make([][]byte, len(components))
			for i := range components {
				planes[i] = make([]byte, int(components[i].blockSize.width)*int(components[i].blockSize.height)*64)
				for k := range planes[i] {
					planes[i][k] = byte(rnd.next() >> 8)
				}
			}
			for _, transform := range transforms[len(hv)] {
				cases++
				got, err := computeImageRGBA(components, planes, out, transform)
				want, wantErr := referenceComputeImage(components, planes, out, transform)
				if (err == nil) != (wantErr == nil) {
					t.Fatalf("%v %v transform %d: error %v, the literal pipeline's %v", hv, sz, transform, err, wantErr)
				}
				if err != nil {
					continue
				}
				if got.Rect != want.Rect || !bytes.Equal(got.Pix, want.Pix) {
					t.Fatalf("%v %v transform %d: the pixels differ from the literal pipeline's", hv, sz, transform)
				}
				matched++
			}
		}
	}
	t.Logf("%d cases, %d with pixels, all equal", cases, matched)
}

// TestJPEGUpsampleRowMatchesTheLiteralRow compares every upsampler's row with the literal one, for random planes and every row.
func TestJPEGUpsampleRowMatchesTheLiteralRow(t *testing.T) {
	rnd := corpusRNG(777)
	for _, f := range jdcSamplings {
		for _, sz := range [][2]int{{1, 1}, {2, 3}, {5, 9}, {16, 16}, {33, 31}, {65, 20}} {
			components, out := jdeComponents(t, sz[0], sz[1], f.hv)
			upsampler, err := newJPEGUpsampler(components, out.width, out.height)
			if err != nil {
				continue
			}
			for i := range components {
				plane := make([]byte, int(components[i].blockSize.width)*int(components[i].blockSize.height)*64)
				for k := range plane {
					plane[k] = byte(rnd.next() >> 8)
				}
				c := &upsampler.components[i]
				want := make([]byte, upsampler.lineBufferSize)
				buf := make([]byte, upsampler.lineBufferSize)
				for row := range sz[1] {
					for k := range want {
						want[k], buf[k] = 0, 0
					}
					referenceUpsampleRow(c, plane, row, sz[0], want)
					got := c.line(plane, row, sz[0], buf)
					if !bytes.Equal(got[:sz[0]], want[:sz[0]]) {
						t.Fatalf("%s %v component %d row %d: %v, the literal row is %v", f.name, sz, i, row, got[:sz[0]], want[:sz[0]])
					}
				}
			}
		}
	}
}

// TestJPEGReadBitsMatchesTheByteLoop drives the word-at-a-time bit reader and the byte loop through the same random streams, with
// stuffed bytes, fill bytes, markers, short tails and a random number of bits consumed between reads: the bits, the count, the
// marker, the position and the errors must be the same after every read.
func TestJPEGReadBitsMatchesTheByteLoop(t *testing.T) {
	rnd := corpusRNG(2024)
	special := []byte{0xFF, 0xFF, 0xFF, 0x00, 0x00, 0xD0, 0xD9, 0xC4, 0x01, 0x7F, 0x80}
	for trial := range 20000 {
		data := make([]byte, int(rnd.next()>>8)%200)
		for i := range data {
			if rnd.next()%(4+uint32(trial%40)) == 0 {
				data[i] = special[rnd.next()>>8%uint32(len(special))]
			} else {
				data[i] = byte(rnd.next() >> 8)
			}
		}
		var fast jpegHuffmanDecoder
		var slow referenceHuffmanReader
		rf, rs := jpegReader{data: data}, jpegReader{data: data}
		for step := range 80 {
			errFast, errSlow := fast.readBits(&rf), slow.readBits(&rs)
			if (errFast == nil) != (errSlow == nil) {
				t.Fatalf("trial %d step %d: errors %v and %v", trial, step, errFast, errSlow)
			}
			if errFast != nil {
				break
			}
			if fast.bits != slow.bits || fast.numBits != slow.numBits || fast.hasMarker != slow.hasMarker || fast.marker != slow.marker || rf.pos != rs.pos {
				t.Fatalf("trial %d step %d: fast %x/%d/%v/%x at %d, byte loop %x/%d/%v/%x at %d", trial, step,
					fast.bits, fast.numBits, fast.hasMarker, fast.marker, rf.pos, slow.bits, slow.numBits, slow.hasMarker, slow.marker, rs.pos)
			}
			// consume some of the bits
			n := uint8(rnd.next()>>8) % (fast.numBits + 1)
			fast.bits <<= n
			fast.numBits -= n
			slow.bits <<= n
			slow.numBits -= n
		}
	}
}

func ExampleDecodeJPEG() {
	var b jdcBuilder
	s := jdcNewSpec(2, 2, [][2]uint8{{1, 1}}, []uint8{1}, 90)
	s.fillImage(b.image("flat-white", 2, 2), "gray")
	s.scans = jdcSeqInterleaved(1)
	img, err := DecodeJPEG(s.build())
	fmt.Println(img.Rect, err)
	// Output: (0,0)-(2,2) <nil>
}

// TestJPEGIDCTMatchesTheLiteralPort compares the IDCT (the dc-only shortcut and the two-pass kernel with the kernels written out)
// with the line-by-line port for every value of every single coefficient with three quantization tables, and for random blocks of
// every density, with extreme coefficients and quantization values (idct.rs wraps in i32, and so does the Go code).
func TestJPEGIDCTMatchesTheLiteralPort(t *testing.T) {
	const stride = 11
	var temp [64]int32
	check := func(c *[64]int16, q *[64]uint16) {
		var got, want [8 * stride]byte
		jpegIDCTBlock(c, q, stride, got[:], &temp)
		referenceIDCT8x8(c, q, stride, want[:])
		if got != want {
			t.Fatalf("coefficients %v quantization %v: the IDCT gives %v, the literal port %v", *c, *q, got, want)
		}
	}
	for _, qv := range []uint16{1, 255, 65535} {
		var q [64]uint16
		for k := range q {
			q[k] = qv
		}
		for pos := range 64 {
			for v := -32768; v <= 32767; v++ {
				var c [64]int16
				c[pos] = int16(v)
				check(&c, &q)
			}
		}
	}
	rnd := corpusRNG(99)
	for trial := range 1_000_000 {
		var c [64]int16
		var q [64]uint16
		density := 1 + rnd.next()%64
		for k := range 64 {
			if rnd.next()%64 < density {
				switch rnd.next() % 4 {
				case 0:
					c[k] = int16(rnd.next())
				case 1:
					c[k] = int16(int(rnd.next()>>8)%64 - 32)
				case 2:
					c[k] = []int16{-32768, 32767, -1, 1}[rnd.next()>>8%4]
				default:
					c[k] = int16(int(rnd.next()>>8)%2048 - 1024)
				}
			}
			switch trial % 3 {
			case 0:
				q[k] = uint16(1 + rnd.next()>>8%255)
			case 1:
				q[k] = uint16(1 + rnd.next()>>8%65535)
			default:
				q[k] = uint16(1 + rnd.next()>>8%16)
			}
		}
		check(&c, &q)
	}
}
