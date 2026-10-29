// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"slices"
	"testing"
)

// The tests of the faster code of the lossless decoder against the literal port in webpdecode_reference_test.go. The corpus of
// TestDecodeWebPMatchesPhoton is the test of the whole; these are the tests of the parts, with more variety than a file gives them.

func TestWebPBitReaderMatchesLiteral(t *testing.T) {
	rng := newWebPRNG(101)
	for trial := range 3000 {
		buf := rng.bytes(rng.intn(40))
		var fast losslessBitReader
		fast.init(buf)
		ref := refBitReader{buf: buf}
		for read := range 60 {
			num := uint8(rng.intn(41))
			if rng.chance(70) {
				num = uint8(rng.intn(9))
			}
			got, gotErr := fast.readBits(num)
			want, wantErr := ref.readBits(num)
			if (gotErr != nil) != (wantErr != nil) || gotErr == nil && got != want {
				t.Fatalf("trial %d read %d of %d bits: got %d, %v; want %d, %v", trial, read, num, got, gotErr, want, wantErr)
			}
			if gotErr != nil {
				break
			}
			if fast.pos != uint64(ref.index)*8+uint64(ref.bitCount) {
				t.Fatalf("trial %d read %d: position %d, want %d", trial, read, fast.pos, ref.index*8+int(ref.bitCount))
			}
		}
	}
}

// randomLengths gives code lengths for a Huffman tree: a complete code over some of the symbols, or any lengths at all.
func randomLengths(rng *webpRNG, alphabet int) []uint16 {
	lengths := make([]uint16, alphabet)
	if rng.chance(60) {
		n := 1 + rng.intn(min(alphabet, 80))
		used := make([]int, 0, n)
		seen := map[int]bool{}
		for len(used) < n {
			s := rng.intn(alphabet)
			if !seen[s] {
				seen[s] = true
				used = append(used, s)
			}
		}
		slices.Sort(used)
		if n == 1 {
			lengths[used[0]] = uint16(1 + rng.intn(15))
			return lengths
		}
		for s, l := range webpHuffmanLengths(rng, used, 15) {
			lengths[s] = uint16(l)
		}
		return lengths
	}
	for range 1 + rng.intn(12) {
		lengths[rng.intn(alphabet)] = uint16(rng.intn(17))
	}
	return lengths
}

func TestWebPHuffmanSymbolsMatchLiteral(t *testing.T) {
	rng := newWebPRNG(103)
	trees := 0
	for trial := range 4000 {
		lengths := randomLengths(rng, []int{19, 40, 256, 280, 312}[rng.intn(5)])
		tree, err := buildWebPHuffmanImplicit(lengths)
		if err != nil {
			continue
		}
		trees++
		buf := rng.bytes(rng.intn(200))
		var fast losslessBitReader
		fast.init(buf)
		ref := refBitReader{buf: buf}
		for read := range 400 {
			got, gotErr := tree.readSymbol(&fast)
			want, wantErr := refReadSymbol(tree, &ref)
			if (gotErr != nil) != (wantErr != nil) || gotErr == nil && got != want {
				t.Fatalf("trial %d symbol %d: got %d, %v; want %d, %v", trial, read, got, gotErr, want, wantErr)
			}
			if gotErr != nil {
				break
			}
			if fast.pos != uint64(ref.index)*8+uint64(ref.bitCount) {
				t.Fatalf("trial %d symbol %d: position %d, want %d", trial, read, fast.pos, ref.index*8+int(ref.bitCount))
			}
		}
	}
	if trees < 1000 {
		t.Fatalf("only %d trees built", trees)
	}
}

func TestWebPPixelOperationsMatchLiteral(t *testing.T) {
	rng := newWebPRNG(107)
	special := []uint32{0, 0xffffffff, 0xff000000, 0x00ff0000, 0x0000ff00, 0x000000ff, 0x80808080, 0x7f7f7f7f, 0x01010101, 0xfefefefe}
	pixel := func() uint32 {
		if rng.chance(10) {
			return special[rng.intn(len(special))]
		}
		return rng.next()
	}
	for i := range 2_000_000 {
		a, b, c := pixel(), pixel(), pixel()
		if got, want := losslessAddPixels(a, b), refLosslessAddPixels(a, b); got != want {
			t.Fatalf("add %08x %08x: %08x, want %08x", a, b, got, want)
		}
		if got, want := losslessAverage2(a, b), refLosslessAverage2(a, b); got != want {
			t.Fatalf("average2 %08x %08x: %08x, want %08x", a, b, got, want)
		}
		if got, want := losslessSelect(a, b, c), refLosslessSelect(a, b, c); got != want {
			t.Fatalf("select %08x %08x %08x: %08x, want %08x", a, b, c, got, want)
		}
		if got, want := losslessClampAddSubtractFull(a, b, c), refLosslessClampAddSubtractFull(a, b, c); got != want {
			t.Fatalf("full %08x %08x %08x: %08x, want %08x", a, b, c, got, want)
		}
		if got, want := losslessClampAddSubtractHalf(a, b), refLosslessClampAddSubtractHalf(a, b); got != want {
			t.Fatalf("half %08x %08x: %08x, want %08x", a, b, got, want)
		}
		if got, want := losslessAddGreen(a), refLosslessAddGreen(a); got != want {
			t.Fatalf("add green %08x: %08x, want %08x", a, got, want)
		}
		if got, want := losslessTransformColor(b, a), refLosslessTransformColor(b, a); got != want {
			t.Fatalf("transform color %08x %08x: %08x, want %08x", b, a, got, want)
		}
		_ = i
	}
}

// TestWebPAddPixelsExhaustive checks the two-lanes-at-a-time arithmetic on every pair of bytes in each lane.
func TestWebPAverageAndAddExhaustive(t *testing.T) {
	for a := range uint32(256) {
		for b := range uint32(256) {
			for _, shift := range []uint{0, 8, 16, 24} {
				x, y := a<<shift|0x00ff00ff&^(0xff<<shift), b<<shift|0xff00ff00&^(0xff<<shift)
				if got, want := losslessAddPixels(x, y), refLosslessAddPixels(x, y); got != want {
					t.Fatalf("add %08x %08x: %08x, want %08x", x, y, got, want)
				}
				if got, want := losslessAverage2(x, y), refLosslessAverage2(x, y); got != want {
					t.Fatalf("average2 %08x %08x: %08x, want %08x", x, y, got, want)
				}
			}
		}
	}
}

func TestWebPTransformsMatchLiteral(t *testing.T) {
	rng := newWebPRNG(109)
	for trial := range 6000 {
		w := 1 + rng.intn(40)
		h := 1 + rng.intn(30)
		if rng.chance(10) {
			w = 1 + rng.intn(300)
		}
		kind := uint8(rng.intn(4))
		tr := &losslessTransformType{kind: kind}
		dataLen := w * h
		switch kind {
		case losslessPredictor, losslessColorTransform:
			tr.sizeBits = uint8(2 + rng.intn(8))
			bx := int(losslessSubsampleSize(uint16(w), tr.sizeBits))
			by := int(losslessSubsampleSize(uint16(h), tr.sizeBits))
			n := bx * by
			if rng.chance(5) {
				n = max(0, n-1-rng.intn(3))
			}
			tr.data = make([]uint32, n)
			for i := range tr.data {
				tr.data[i] = rng.next()
				if kind == losslessPredictor && rng.chance(90) {
					tr.data[i] = tr.data[i]&0xffff00ff | uint32(rng.intn(16))<<8
				}
			}
		case losslessColorIndexing:
			tr.tableLen = uint16(1 + rng.intn(20))
			if rng.chance(20) {
				tr.tableLen = uint16(1 + rng.intn(256))
			}
			tr.data = make([]uint32, tr.tableLen)
			for i := range tr.data {
				tr.data[i] = rng.next()
			}
			bits := losslessWidthBits(tr.tableLen)
			dataLen = h * ((w + 1<<bits - 1) >> bits)
		}
		if rng.chance(5) {
			dataLen = max(0, dataLen-1-rng.intn(w))
		}
		image := make([]uint32, dataLen)
		for i := range image {
			image[i] = rng.next()
			if rng.chance(20) {
				image[i] &= 0x0f0f0f0f
			}
		}

		got, gotErr := tr.apply(slices.Clone(image), uint16(w), uint16(h))
		want, wantErr := refApply(tr, slices.Clone(image), uint16(w), uint16(h))
		if (gotErr != nil) != (wantErr != nil) {
			t.Fatalf("trial %d kind %d %dx%d: error %v, literal %v", trial, kind, w, h, gotErr, wantErr)
		}
		if gotErr == nil && !slices.Equal(got, want) {
			t.Fatalf("trial %d kind %d %dx%d: pixels differ", trial, kind, w, h)
		}
	}
}
