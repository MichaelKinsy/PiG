// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// pnmNoise is count bytes of a xorshift sequence.
func pnmNoise(seed uint32, count int) []byte {
	state := seed*2654435761 + 3
	out := make([]byte, count)
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		out[i] = byte(state >> 11)
	}
	return out
}

// pnmASCII writes values as decimal numbers separated by single spaces, a newline after every perRow values.
func pnmASCII(values []int, perRow int) []byte {
	var out bytes.Buffer
	for i, v := range values {
		if i > 0 {
			if perRow > 0 && i%perRow == 0 {
				out.WriteByte('\n')
			} else {
				out.WriteByte(' ')
			}
		}
		out.WriteString(strconv.Itoa(v))
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// pnm16 is big endian 16-bit samples.
func pnm16(values []int) []byte {
	out := make([]byte, 0, 2*len(values))
	for _, v := range values {
		out = append(out, byte(v>>8), byte(v))
	}
	return out
}

// pnmBits packs rows of bits (1 is black) MSB first, each row padded to a byte; padBits sets the padding bits to one.
func pnmBits(width, height int, seed uint32, padBits bool) []byte {
	rowBytes := (width + 7) / 8
	noise := pnmNoise(seed, rowBytes*height)
	out := make([]byte, 0, rowBytes*height)
	for y := range height {
		row := noise[y*rowBytes : (y+1)*rowBytes]
		if pad := rowBytes*8 - width; pad > 0 {
			mask := byte(1<<pad - 1)
			if padBits {
				row[rowBytes-1] |= mask
			} else {
				row[rowBytes-1] &^= mask
			}
		}
		out = append(out, row...)
	}
	return out
}

// pnmRamp is the 16-bit values 0..65535.
func pnmRamp16() []int {
	values := make([]int, 65536)
	for i := range values {
		values[i] = i
	}
	return values
}

func pnmRamp8() []byte {
	out := make([]byte, 256)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}

func pam(header string, raster []byte) []byte {
	return append([]byte("P7\n"+header), raster...)
}

// pnmCorpus is the files whose decoding the test compares with Photon: every subtype (P1 to P7) in ASCII and binary, every maxval of the
// 8-bit scaling and a spread of 16-bit ones over every sample value, the header's tokens, comments (including the one that glues two
// numbers), whitespace and signs, PAM headers and tuple types, truncations, sample values out of range, empty images and the size limits.
func pnmCorpus() []formatCase {
	var b formatCorpusBuilder

	// Sizes with noise in every subtype.
	for _, size := range [][2]int{{1, 1}, {2, 1}, {1, 2}, {3, 2}, {7, 3}, {8, 2}, {9, 2}, {15, 3}, {16, 2}, {17, 3}, {33, 5}, {100, 4}} {
		w, h := size[0], size[1]
		n := w * h
		name := fmt.Sprintf("%dx%d", w, h)
		seed := uint32(w*37 + h)
		bits := make([]int, n)
		for i, v := range pnmNoise(seed, n) {
			bits[i] = int(v & 1)
		}
		b.add("p1-"+name, append([]byte(fmt.Sprintf("P1\n%d %d\n", w, h)), pnmASCII(bits, w)...))
		b.add("p4-"+name+"-pad0", append([]byte(fmt.Sprintf("P4\n%d %d\n", w, h)), pnmBits(w, h, seed, false)...))
		b.add("p4-"+name+"-pad1", append([]byte(fmt.Sprintf("P4\n%d %d\n", w, h)), pnmBits(w, h, seed, true)...))
		gray := pnmNoise(seed+1, n)
		grayInts := make([]int, n)
		for i, v := range gray {
			grayInts[i] = int(v)
		}
		b.add("p2-"+name, append([]byte(fmt.Sprintf("P2\n%d %d\n255\n", w, h)), pnmASCII(grayInts, w)...))
		b.add("p5-"+name, append([]byte(fmt.Sprintf("P5\n%d %d\n255\n", w, h)), gray...))
		rgb := pnmNoise(seed+2, 3*n)
		rgbInts := make([]int, 3*n)
		for i, v := range rgb {
			rgbInts[i] = int(v)
		}
		b.add("p3-"+name, append([]byte(fmt.Sprintf("P3\n%d %d\n255\n", w, h)), pnmASCII(rgbInts, 3*w)...))
		b.add("p6-"+name, append([]byte(fmt.Sprintf("P6\n%d %d\n255\n", w, h)), rgb...))
		wide := make([]int, n)
		for i, v := range pnmNoise(seed+3, 2*n) {
			if i%2 == 0 {
				wide[i/2] = int(v) << 8
			} else {
				wide[i/2] |= int(v)
			}
		}
		b.add("p5-16-"+name, append([]byte(fmt.Sprintf("P5\n%d %d\n65535\n", w, h)), pnm16(wide)...))
		b.add("p2-16-"+name, append([]byte(fmt.Sprintf("P2\n%d %d\n65535\n", w, h)), pnmASCII(wide, w)...))
		wide3 := make([]int, 3*n)
		for i, v := range pnmNoise(seed+4, 6*n) {
			if i%2 == 0 {
				wide3[i/2] = int(v) << 8
			} else {
				wide3[i/2] |= int(v)
			}
		}
		b.add("p6-16-"+name, append([]byte(fmt.Sprintf("P6\n%d %d\n65535\n", w, h)), pnm16(wide3)...))
		b.add("p3-16-"+name, append([]byte(fmt.Sprintf("P3\n%d %d\n65535\n", w, h)), pnmASCII(wide3, 3*w)...))
	}

	// The 8-bit scaling `round(v * 255 / maxval)` in f32, for every maxval below 255 and every sample value (binary, plain and colour).
	ramp8 := pnmRamp8()
	ramp8Ints := make([]int, 256)
	for i := range ramp8Ints {
		ramp8Ints[i] = i
	}
	for maxval := 1; maxval <= 256; maxval++ {
		b.add(fmt.Sprintf("p5-maxval-%d", maxval), append([]byte(fmt.Sprintf("P5\n256 1\n%d\n", maxval)), ramp8...))
	}
	for _, maxval := range []int{1, 2, 3, 7, 15, 31, 85, 100, 127, 128, 170, 200, 254} {
		b.add(fmt.Sprintf("p2-maxval-%d", maxval), append([]byte(fmt.Sprintf("P2\n16 16\n%d\n", maxval)), pnmASCII(ramp8Ints, 16)...))
		b.add(fmt.Sprintf("p6-maxval-%d", maxval), append([]byte(fmt.Sprintf("P6\n16 16\n%d\n", maxval)), append(append([]byte(nil), ramp8...), append(ramp8, ramp8...)...)...))
		b.add(fmt.Sprintf("p3-maxval-%d", maxval), append([]byte(fmt.Sprintf("P3\n16 16\n%d\n", maxval)), pnmASCII(append(append(append([]int(nil), ramp8Ints...), ramp8Ints...), ramp8Ints...), 12)...))
	}
	// 16-bit: maxval 256..65534 (a spread) over every sample value.
	ramp16 := pnm16(pnmRamp16())
	ramp16Ints := pnmRamp16()
	for _, maxval := range []int{256, 257, 258, 300, 511, 512, 513, 1000, 1023, 1024, 1500, 2047, 4095, 4096, 4097, 9999, 10000, 16383, 20000,
		21845, 32767, 32768, 32769, 40000, 50000, 60000, 65000, 65279, 65280, 65281, 65500, 65533, 65534} {
		b.add(fmt.Sprintf("p5-16-maxval-%d", maxval), append([]byte(fmt.Sprintf("P5\n256 256\n%d\n", maxval)), ramp16...))
	}
	for _, maxval := range []int{256, 1000, 4095, 32768, 65534} {
		b.add(fmt.Sprintf("p2-16-maxval-%d", maxval), append([]byte(fmt.Sprintf("P2\n256 256\n%d\n", maxval)), pnmASCII(ramp16Ints, 256)...))
		b.add(fmt.Sprintf("p6-16-maxval-%d", maxval), append([]byte(fmt.Sprintf("P6\n256 85\n%d\n", maxval)), ramp16[:256*85*6]...))
	}
	b.add("p5-16-maxval-65535-ramp", append([]byte("P5\n256 256\n65535\n"), ramp16...))
	b.add("p2-16-maxval-65535-ramp", append([]byte("P2\n256 256\n65535\n"), pnmASCII(ramp16Ints, 256)...))
	// Maxval 0, above 65535 and at the u32 edge.
	for _, maxval := range []string{"0", "65536", "65537", "100000", "4294967295", "4294967296", "-1", "+255", "0255", "1e2", "255.0", "0x10"} {
		b.add("p5-maxval-bad-"+maxval, append([]byte("P5\n2 2\n"+maxval+"\n"), 1, 2, 3, 4, 5, 6, 7, 8))
		b.add("p6-maxval-bad-"+maxval, append([]byte("P6\n1 1\n"+maxval+"\n"), 1, 2, 3, 4, 5, 6))
	}

	// Samples beyond the maxval or the sample type: they are scaled and saturate; ASCII numbers that do not fit the type are errors.
	b.add("p5-above-maxval", []byte("P5\n4 1\n10\n\x00\x0a\x0b\xff"))
	b.add("p6-above-maxval", []byte("P6\n2 1\n10\n\x00\x0a\x0b\xff\x7f\x01"))
	b.add("p2-above-maxval", []byte("P2\n4 1\n10\n0 10 11 255\n"))
	b.add("p2-over-255-8bit", []byte("P2\n2 1\n100\n0 256\n"))
	b.add("p2-over-255-8bit-at-255", []byte("P2\n2 1\n255\n0 255\n"))
	b.add("p2-over-65535", []byte("P2\n2 1\n65535\n0 65536\n"))
	b.add("p2-over-65535-16bit-maxval-300", []byte("P2\n2 1\n300\n0 65535\n"))
	b.add("p2-above-maxval-16", []byte("P2\n3 1\n1000\n0 1000 65535\n"))
	b.add("p3-above-maxval-16", []byte("P3\n1 1\n1000\n0 1001 65535\n"))
	b.add("p2-negative-sample", []byte("P2\n2 1\n255\n0 -1\n"))
	b.add("p2-negative-zero-sample", []byte("P2\n2 1\n255\n0 -0\n"))
	b.add("p2-plus-sample", []byte("P2\n2 1\n255\n+5 +0\n"))
	b.add("p2-leading-zeros", []byte("P2\n2 1\n255\n005 000000000000000000007\n"))
	b.add("p2-hex-sample", []byte("P2\n2 1\n255\n0 0x10\n"))
	b.add("p2-decimal-sample", []byte("P2\n2 1\n255\n0 1.0\n"))
	b.add("p2-text-sample", []byte("P2\n2 1\n255\n0 x\n"))
	b.add("p2-comment-in-raster", []byte("P2\n2 1\n255\n0 # comment\n 5\n"))
	b.add("p2-non-ascii-sample", []byte("P2\n2 1\n255\n0 \xc3\xa9\n"))
	b.add("p2-vertical-tab-separators", []byte("P2\n3 1\n255\n1\v2\f3\r"))
	b.add("p2-no-trailing-newline", []byte("P2\n2 1\n255\n0 255"))
	b.add("p2-trailing-text", []byte("P2\n2 1\n255\n0 255\nextra words\n"))
	b.add("p2-one-sample-short", []byte("P2\n2 2\n255\n0 255 7\n"))
	b.add("p2-no-samples", []byte("P2\n2 2\n255\n"))
	b.add("p2-nbsp-separator", []byte("P2\n2 1\n255\n1\u00a02\n"))
	b.add("p2-many-separators", []byte("P2\n2 1\n255\n\n\n \t 1 \t\r\n\r\n 2 \n\n"))

	// P1/P4.
	b.add("p1-no-separators", []byte("P1\n4 2\n10100101"))
	b.add("p1-separators-mixed", []byte("P1\n4 2\n1 0\t1\n0\r\n0 1 0\v1"))
	b.add("p1-bad-digit", []byte("P1\n2 1\n1 2\n"))
	b.add("p1-two-char", []byte("P1\n2 1\n10 \n"))
	b.add("p1-short", []byte("P1\n3 2\n1 0 1 0 1"))
	b.add("p1-trailing", []byte("P1\n2 1\n1 0\n1 1 1\n"))
	b.add("p1-comment-in-raster", []byte("P1\n2 1\n1 # c\n0\n"))
	b.add("p1-with-maxval-line", []byte("P1\n2 1\n1\n1 0\n"))
	b.add("p1-width-zero", []byte("P1\n0 3\n"))
	b.add("p1-height-zero", []byte("P1\n3 0\n"))
	b.add("p1-maxval-ignored", []byte("P1\n2 1 255\n1 0\n"))
	b.add("p4-truncated-row", append([]byte("P4\n9 2\n"), 0xAA, 0xBB, 0xCC))
	b.add("p4-extra-data", append(append([]byte("P4\n9 2\n"), pnmBits(9, 2, 4, false)...), 1, 2, 3))
	b.add("p4-width-zero", []byte("P4\n0 5\n"))
	b.add("p4-height-zero", []byte("P4\n5 0\n"))
	b.add("p4-all-ones", append([]byte("P4\n16 2\n"), 255, 255, 255, 255))
	b.add("p4-all-zeros", append([]byte("P4\n16 2\n"), 0, 0, 0, 0))
	for width := 1; width <= 24; width++ {
		b.add(fmt.Sprintf("p4-width-%d", width), append([]byte(fmt.Sprintf("P4\n%d 3\n", width)), pnmBits(width, 3, uint32(width), width%2 == 0)...))
	}

	// P7.
	rgb4 := []byte{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120}
	b.add("pam-grayscale-8", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-grayscale-8-maxval-100", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 100\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 50, 100, 200}))
	b.add("pam-grayscale-16", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 65535\nTUPLTYPE GRAYSCALE\nENDHDR\n", pnm16([]int{1, 500, 40000, 65535})))
	b.add("pam-grayscale-16-maxval-1000", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 1000\nTUPLTYPE GRAYSCALE\nENDHDR\n", pnm16([]int{1, 500, 1000, 2000})))
	b.add("pam-grayscale-depth-0", pam("WIDTH 2\nHEIGHT 2\nDEPTH 0\nMAXVAL 1000\nTUPLTYPE GRAYSCALE\nENDHDR\n", pnm16([]int{1, 500, 1000, 2000})))
	b.add("pam-grayscale-depth-0-maxval-255", pam("WIDTH 2\nHEIGHT 2\nDEPTH 0\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", pnm16([]int{1, 500, 1000, 2000})))
	b.add("pam-grayscale-depth-2", pam("WIDTH 2\nHEIGHT 2\nDEPTH 2\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4, 5, 6, 7, 8}))
	b.add("pam-grayscale-maxval-65536", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 65536\nTUPLTYPE GRAYSCALE\nENDHDR\n", pnm16([]int{1, 500, 1000, 2000})))
	b.add("pam-rgb-8", pam("WIDTH 2\nHEIGHT 2\nDEPTH 3\nMAXVAL 255\nTUPLTYPE RGB\nENDHDR\n", rgb4))
	b.add("pam-rgb-8-maxval-200", pam("WIDTH 2\nHEIGHT 2\nDEPTH 3\nMAXVAL 200\nTUPLTYPE RGB\nENDHDR\n", rgb4))
	b.add("pam-rgb-16", pam("WIDTH 2\nHEIGHT 1\nDEPTH 3\nMAXVAL 65535\nTUPLTYPE RGB\nENDHDR\n", pnm16([]int{1, 2000, 40000, 65535, 7, 9})))
	b.add("pam-rgb-16-maxval-1000", pam("WIDTH 2\nHEIGHT 1\nDEPTH 3\nMAXVAL 1000\nTUPLTYPE RGB\nENDHDR\n", pnm16([]int{1, 2000, 40000, 65535, 7, 9})))
	b.add("pam-rgb-depth-4", pam("WIDTH 2\nHEIGHT 2\nDEPTH 4\nMAXVAL 255\nTUPLTYPE RGB\nENDHDR\n", pnmNoise(1, 16)))
	b.add("pam-rgb-depth-1", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE RGB\nENDHDR\n", pnmNoise(1, 4)))
	b.add("pam-rgb-maxval-65536", pam("WIDTH 1\nHEIGHT 1\nDEPTH 3\nMAXVAL 65536\nTUPLTYPE RGB\nENDHDR\n", pnmNoise(1, 6)))
	b.add("pam-rgb-alpha", pam("WIDTH 2\nHEIGHT 1\nDEPTH 4\nMAXVAL 255\nTUPLTYPE RGB_ALPHA\nENDHDR\n", pnmNoise(1, 8)))
	b.add("pam-grayscale-alpha", pam("WIDTH 2\nHEIGHT 1\nDEPTH 2\nMAXVAL 255\nTUPLTYPE GRAYSCALE_ALPHA\nENDHDR\n", pnmNoise(1, 4)))
	b.add("pam-bw-alpha", pam("WIDTH 2\nHEIGHT 1\nDEPTH 2\nMAXVAL 1\nTUPLTYPE BLACKANDWHITE_ALPHA\nENDHDR\n", []byte{1, 0, 0, 1}))
	b.add("pam-bw", pam("WIDTH 4\nHEIGHT 2\nDEPTH 1\nMAXVAL 1\nTUPLTYPE BLACKANDWHITE\nENDHDR\n", []byte{1, 0, 0, 1, 1, 0, 0, 1}))
	b.add("pam-bw-sample-2", pam("WIDTH 4\nHEIGHT 2\nDEPTH 1\nMAXVAL 1\nTUPLTYPE BLACKANDWHITE\nENDHDR\n", []byte{1, 0, 2, 1, 1, 0, 0, 1}))
	b.add("pam-bw-sample-255", pam("WIDTH 4\nHEIGHT 2\nDEPTH 1\nMAXVAL 1\nTUPLTYPE BLACKANDWHITE\nENDHDR\n", []byte{1, 0, 255, 1, 1, 0, 0, 1}))
	b.add("pam-bw-maxval-2", pam("WIDTH 2\nHEIGHT 1\nDEPTH 1\nMAXVAL 2\nTUPLTYPE BLACKANDWHITE\nENDHDR\n", []byte{1, 0}))
	b.add("pam-bw-depth-2", pam("WIDTH 2\nHEIGHT 1\nDEPTH 2\nMAXVAL 1\nTUPLTYPE BLACKANDWHITE\nENDHDR\n", []byte{1, 0, 1, 0}))
	b.add("pam-no-tupltype-depth-1", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-no-tupltype-depth-1-maxval-1000", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 1000\nENDHDR\n", []byte{1, 2, 3, 250}))
	b.add("pam-no-tupltype-depth-2", pam("WIDTH 2\nHEIGHT 2\nDEPTH 2\nMAXVAL 255\nENDHDR\n", pnmNoise(1, 8)))
	b.add("pam-no-tupltype-depth-3", pam("WIDTH 2\nHEIGHT 2\nDEPTH 3\nMAXVAL 255\nENDHDR\n", rgb4))
	b.add("pam-no-tupltype-depth-3-maxval-65535", pam("WIDTH 2\nHEIGHT 2\nDEPTH 3\nMAXVAL 65535\nENDHDR\n", rgb4))
	b.add("pam-no-tupltype-depth-4", pam("WIDTH 2\nHEIGHT 2\nDEPTH 4\nMAXVAL 255\nENDHDR\n", pnmNoise(1, 16)))
	b.add("pam-no-tupltype-depth-5", pam("WIDTH 2\nHEIGHT 2\nDEPTH 5\nMAXVAL 255\nENDHDR\n", pnmNoise(1, 20)))
	b.add("pam-no-tupltype-depth-0", pam("WIDTH 2\nHEIGHT 2\nDEPTH 0\nMAXVAL 255\nENDHDR\n", pnmNoise(1, 4)))
	b.add("pam-maxval-zero", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 0\nTUPLTYPE GRAYSCALE\nENDHDR\n", pnmNoise(1, 4)))
	b.add("pam-custom-tupltype", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE CMYK\nENDHDR\n", pnmNoise(1, 4)))
	b.add("pam-tupltype-case", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE grayscale\nENDHDR\n", pnmNoise(1, 4)))
	b.add("pam-tupltype-twice", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAY\nTUPLTYPE SCALE\nENDHDR\n", pnmNoise(1, 4)))
	b.add("pam-tupltype-twice-rgb", pam("WIDTH 2\nHEIGHT 2\nDEPTH 3\nMAXVAL 255\nTUPLTYPE RGB\nTUPLTYPE\nENDHDR\n", rgb4))
	b.add("pam-tupltype-spaces", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE   GRAYSCALE  \nENDHDR\n", pnmNoise(1, 4)))
	b.add("pam-tupltype-empty", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE\nENDHDR\n", pnmNoise(1, 4)))
	b.add("pam-tupltype-empty-depth-3", pam("WIDTH 2\nHEIGHT 2\nDEPTH 3\nMAXVAL 255\nTUPLTYPE\nENDHDR\n", rgb4))
	b.add("pam-order-shuffled", pam("MAXVAL 255\nTUPLTYPE RGB\nDEPTH 3\nHEIGHT 2\nWIDTH 2\nENDHDR\n", rgb4))
	b.add("pam-comments", pam("# first\nWIDTH 2\n# second\nHEIGHT 2\nDEPTH 1\n#\nMAXVAL 255\nTUPLTYPE GRAYSCALE\n#end\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-comment-non-ascii", pam("# caf\u00e9\nWIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-comment-invalid-utf8", pam("# bad \xff\nWIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-header-line-non-ascii", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAY\u00e9\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-header-invalid-utf8", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAY\xff\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-missing-width", pam("HEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-missing-height", pam("WIDTH 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-missing-depth", pam("WIDTH 2\nHEIGHT 2\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-missing-maxval", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-duplicate-width", pam("WIDTH 2\nWIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-duplicate-maxval", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-unknown-line", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nCOLOR red\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-lowercase-keyword", pam("width 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-leading-space-line", pam("WIDTH 2\n HEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-leading-space-endhdr", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\n ENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-blank-line", pam("WIDTH 2\n\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-blank-line-spaces", pam("WIDTH 2\n   \nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-crlf-header", pam("WIDTH 2\r\nHEIGHT 2\r\nDEPTH 1\r\nMAXVAL 255\r\nTUPLTYPE GRAYSCALE\r\nENDHDR\r\n", []byte{1, 2, 3, 4}))
	b.add("pam-crlf-endhdr-only", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\r\n", []byte{1, 2, 3, 4}))
	b.add("pam-endhdr-trailing-text", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR ignored\n", []byte{1, 2, 3, 4}))
	b.add("pam-endhdr-prefix", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDRX\n", []byte{1, 2, 3, 4}))
	b.add("pam-endhdr-no-newline", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR", nil))
	b.add("pam-endhdr-eof-with-data-newline", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", nil))
	b.add("pam-no-endhdr", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\n", nil))
	b.add("pam-value-plus", pam("WIDTH +2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-value-tabs", pam("WIDTH\t2\nHEIGHT\t\t2\nDEPTH\t1\nMAXVAL\t255\nTUPLTYPE\tGRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-value-no-space", pam("WIDTH2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-value-empty", pam("WIDTH\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-value-two-numbers", pam("WIDTH 2 3\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-value-negative", pam("WIDTH -2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-value-overflow", pam("WIDTH 4294967296\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-value-duplicate-before-parse", pam("WIDTH 2\nWIDTH x\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4}))
	b.add("pam-no-newline-after-magic", append([]byte("P7 WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n"), 1, 2, 3, 4))
	b.add("pam-crlf-after-magic", append([]byte("P7\r\nWIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n"), 1, 2, 3, 4))
	b.add("pam-magic-only", []byte("P7"))
	b.add("pam-magic-newline-only", []byte("P7\n"))
	b.add("pam-truncated-raster", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3}))
	b.add("pam-extra-raster", pam("WIDTH 2\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", []byte{1, 2, 3, 4, 5, 6}))
	b.add("pam-empty-width", pam("WIDTH 0\nHEIGHT 2\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", nil))
	b.add("pam-empty-height", pam("WIDTH 2\nHEIGHT 0\nDEPTH 1\nMAXVAL 255\nTUPLTYPE GRAYSCALE\nENDHDR\n", nil))

	// Header tokens of P1 to P6: whitespace, comments and signs.
	hdr := func(magic string, header string, raster []byte) []byte { return append([]byte(magic+header), raster...) }
	px := []byte{9, 8, 7, 6}
	for name, header := range map[string]string{
		"plain":                 "\n2 2\n255\n",
		"spaces":                " 2 2 255 ",
		"tabs":                  "\t2\t2\t255\t",
		"all-whitespace-kinds":  "\v\f\r\n 2\v\f\r\n 2\v\f\r\n 255\n",
		"one-line":              "\n2 2 255\n",
		"crlf":                  "\r\n2 2\r\n255\r\n",
		"cr-only":               "\r2\r2\r255\r",
		"no-space-after-magic":  "2 2 255\n",
		"magic-then-token":      "\n2\n2\n255\n",
		"comment-after-magic":   "\n# comment\n2 2\n255\n",
		"comment-mid-line":      "\n2 # comment\n2\n255\n",
		"comment-glues-numbers": "\n2#c\n2\n255\n",
		"comment-glues-width":   "\n1#c\n1 2\n255\n",
		"comment-then-space":    "\n2#c\n 2\n255\n",
		"comment-cr":            "\n2#c\r2\n255\n",
		"comment-crlf":          "\n2#c\r\n 2\n255\n",
		"comment-crlf-glue":     "\n2#c\r\n2\n255\n",
		"comment-at-eof":        "\n2 2\n255#",
		"comment-no-newline":    "\n2 2\n255\n#",
		"comment-hash-token":    "\n#2\n2 2\n255\n",
		"comment-in-comment":    "\n# a # b\n2 2\n255\n",
		"comment-non-ascii":     "\n# caf\u00e9\xff\n2 2\n255\n",
		"hash-first":            "#\n2 2\n255\n",
		"hash-direct":           "# c\n2\n2\n255\n",
		"plus-width":            "\n+2 2\n255\n",
		"plus-only":             "\n+ 2\n255\n",
		"minus":                 "\n-2 2\n255\n",
		"leading-zeros":         "\n002 0002\n000255\n",
		"hex":                   "\n0x2 2\n255\n",
		"decimal":               "\n2.0 2\n255\n",
		"non-ascii-token":       "\n2\u00e9 2\n255\n",
		"non-ascii-before":      "\n\u00e9 2 2\n255\n",
		"nbsp-separator":        "\n2\u00a02\n255\n",
		"nul":                   "\n2\x002\n255\n",
		"u32-max-height-0":      "\n4294967295 0\n255\n",
		"u32-overflow":          "\n4294967296 0\n255\n",
		"width-only":            "\n2\n",
		"empty":                 "",
		"just-newline":          "\n",
		"two-values":            "\n2 2\n",
		"maxval-comment":        "\n2 2 255#\n",
		"extra-token-as-data":   "\n2 2 255 9\n",
	} {
		b.add("p5-header-"+name, hdr("P5", header, px))
		b.add("p2-header-"+name, hdr("P2", header, []byte("1 2 3 4\n")))
	}
	for name, header := range map[string]string{
		"plain":                 "\n2 2\n",
		"comment-glues-numbers": "\n2#c\n2\n",
		"comment-crlf":          "\n2#c\r\n 2\n",
		"one-token":             "\n2\n",
		"nbsp":                  "\n2\u00a02\n",
		"two-spaces-after-h":    "\n2 2  ",
	} {
		b.add("p4-header-"+name, hdr("P4", header, []byte{0xAA, 0x55}))
		b.add("p1-header-"+name, hdr("P1", header, []byte("1 0 0 1\n")))
	}
	b.add("magic-p7-vs-p1-lowercase", []byte("p5\n2 2\n255\n\x01\x02\x03\x04"))
	b.add("magic-too-short", []byte("P"))
	b.add("magic-p1-only", []byte("P1"))
	b.add("magic-p6-only", []byte("P6"))
	b.add("magic-p8", []byte("P8\n2 2\n255\n\x01\x02\x03\x04"))
	b.add("magic-p0", []byte("P0\n2 2\n255\n\x01\x02\x03\x04"))
	b.add("magic-p5-then-crlf-data", append([]byte("P5\r\n2 2\r\n255\r\n"), 1, 2, 3, 4))

	// Truncations of small files.
	for name, file := range map[string][]byte{
		"p1": append([]byte("P1\n3 2\n"), pnmASCII([]int{1, 0, 1, 0, 1, 0}, 3)...),
		"p2": append([]byte("P2\n3 2\n200\n"), pnmASCII([]int{1, 20, 133, 4, 55, 6}, 3)...),
		"p3": append([]byte("P3\n2 1\n65535\n"), pnmASCII([]int{1, 20, 133, 4, 55, 65535}, 6)...),
		"p4": append([]byte("P4\n10 2\n"), pnmBits(10, 2, 3, false)...),
		"p5": append([]byte("P5\n3 2\n255\n"), pnmNoise(5, 6)...),
		"p6": append([]byte("P6\n2 2\n65535\n"), pnmNoise(6, 24)...),
		"p7": pam("# c\nWIDTH 2\nHEIGHT 1\nDEPTH 3\nMAXVAL 255\nTUPLTYPE RGB\nENDHDR\n", pnmNoise(7, 6)),
	} {
		for n := range len(file) {
			b.add(fmt.Sprintf("truncated-%s-%03d", name, n), file[:n:n])
		}
	}

	// Empty images.
	for _, magic := range []string{"P1", "P2", "P3", "P4", "P5", "P6"} {
		suffix := "\n"
		if magic != "P1" && magic != "P4" {
			suffix = "\n255\n"
		}
		for _, size := range []string{"0 0", "0 7", "7 0", "0 4294967295", "4294967295 0", "1073741824 0", "1073741823 0", "1431655765 0", "1431655766 0", "2147483648 0"} {
			b.add("empty-"+strings.ToLower(magic)+"-"+strings.ReplaceAll(size, " ", "x"), []byte(magic+"\n"+size+suffix))
		}
	}
	for _, size := range [][2]string{{"0", "0"}, {"0", "9"}, {"9", "0"}, {"4294967295", "0"}, {"1073741824", "0"}, {"1073741823", "0"}, {"0", "4294967295"}} {
		b.add("empty-p7-"+size[0]+"x"+size[1], pam("WIDTH "+size[0]+"\nHEIGHT "+size[1]+"\nDEPTH 3\nMAXVAL 255\nTUPLTYPE RGB\nENDHDR\n", nil))
		b.add("empty-p7-gray16-"+size[0]+"x"+size[1], pam("WIDTH "+size[0]+"\nHEIGHT "+size[1]+"\nDEPTH 1\nMAXVAL 65535\nTUPLTYPE GRAYSCALE\nENDHDR\n", nil))
	}

	// Limits: width * height * bytes per pixel may not exceed 2^29 (the image type: L8 1, L16 2, Rgb8 3, Rgb16 6 bytes).
	for _, tc := range []struct {
		name, header string
		over         bool
	}{
		{"p5-8bit-over", "P5\n32769 16384\n255\n", true},
		{"p5-8bit-exact", "P5\n32768 16384\n255\n", false},
		{"p5-16bit-over", "P5\n16385 16384\n65535\n", true},
		{"p5-16bit-exact", "P5\n16384 16384\n65535\n", false},
		{"p6-8bit-over", "P6\n21846 8192\n255\n", true},
		{"p6-8bit-exact-ish", "P6\n21845 8192\n255\n", false},
		{"p6-16bit-over", "P6\n10923 8192\n65535\n", true},
		{"p6-16bit-exact-ish", "P6\n10922 8192\n65535\n", false},
		{"p4-over", "P4\n32769 16384\n", true},
		{"p1-over", "P1\n32769 16384\n", true},
		{"p2-over", "P2\n32769 16384\n255\n", true},
		{"p3-over", "P3\n21846 8192\n255\n", true},
		{"p5-u32-by-u32", "P5\n4294967295 4294967295\n255\n", true},
		{"p5-2^16-by-2^16", "P5\n65536 65536\n255\n", true},
		{"p6-2^16-by-2^16", "P6\n65536 65536\n255\n", true},
		{"p4-2^16-by-2^16", "P4\n65536 65536\n", true},
		{"p5-width-u32", "P5\n4294967295 1\n255\n", true},
		{"p5-height-u32", "P5\n1 4294967295\n255\n", true},
		{"p4-wrapping-linelen", "P4\n4294967295 1\n", true},
		{"p4-wrapping-product", "P4\n8 4294967295\n", true},
		{"p4-2^29-bits", "P4\n536870912 1\n", false},
		{"p4-2^29-plus-1-bits", "P4\n536870913 1\n", false},
	} {
		b.add("limit-header-"+tc.name, []byte(tc.header))
		b.add("limit-header-"+tc.name+"-short", append([]byte(tc.header), pnmNoise(1, 4096)...))
	}
	// An L8 image of exactly 2^29 bytes (P4 67108864x8, P5 32768x16384) is not here: Photon's wasm module needs about 3 GiB for it (the
	// input, the samples, the decoder's buffer and the 2 GiB of RGBA) and throws, which Decode does not emulate (see DecodePNM). The L16
	// image of 2^29 bytes, which Photon decodes, and one row more, which the limit refuses, are the boundary.
	b.addLazy("limit-p5-16bit-exact-data", func() []byte {
		return append([]byte("P5\n16384 16384\n65535\n"), pnmNoise(7, 1<<29)...)
	})
	b.addLazy("limit-p5-16bit-over-by-a-row-data", func() []byte {
		return append([]byte("P5\n16385 16384\n65535\n"), pnmNoise(7, 1<<29+1<<15)...)
	})
	b.addLazy("limit-p6-8bit-big-data", func() []byte {
		return append([]byte("P6\n8192 8192\n255\n"), pnmNoise(8, 8192*8192*3)...)
	})
	b.addLazy("limit-p5-8bit-half-data", func() []byte {
		return append([]byte("P5\n16384 16384\n255\n"), pnmNoise(10, 1<<28)...)
	})
	b.addLazy("limit-p4-wide-half-data", func() []byte {
		return append([]byte("P4\n33554432 8\n"), pnmNoise(11, 8*(33554432/8))...)
	})
	return b.cases
}
