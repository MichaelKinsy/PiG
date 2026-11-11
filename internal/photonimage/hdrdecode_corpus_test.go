// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"fmt"
)

// hdrPixel is one RGBE pixel.
type hdrPixel [4]byte

// hdrNoise is count pixels of a xorshift sequence with exponents in [lo, hi] and a first pixel that old-style scanlines can start with.
func hdrNoise(seed uint32, count int, lo, hi byte) []hdrPixel {
	state := seed*2654435761 + 7
	next := func() uint32 {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		return state
	}
	pixels := make([]hdrPixel, count)
	for i := range pixels {
		v := next()
		pixels[i] = hdrPixel{byte(v), byte(v >> 8), byte(v >> 16), lo + byte((v>>24)%(uint32(hi)-uint32(lo)+1))}
	}
	// Runs, so that run length encoders have something to compress.
	for i := 3; i+5 < count; i += 17 {
		for j := 1; j < 5; j++ {
			pixels[i+j] = pixels[i]
		}
	}
	return pixels
}

// hdrHeader is the signature line, the attribute lines, the empty line and the dimensions line.
func hdrHeader(attributes string, width, height int) []byte {
	return []byte("#?RADIANCE\n" + attributes + "\n" + fmt.Sprintf("-Y %d +X %d\n", height, width))
}

// hdrFlat writes scanlines as plain RGBE pixels (old-style: no run markers). A first pixel that reads as the new-style marker
// (2, 2, <128) is the caller's mistake.
func hdrFlat(header []byte, rows ...[]hdrPixel) []byte {
	out := append([]byte(nil), header...)
	for _, row := range rows {
		for _, p := range row {
			out = append(out, p[:]...)
		}
	}
	return out
}

// hdrNewRLEScanline is one scanline in the new run length encoding: the marker pixel (2, 2, width high, width low) and the four
// components, each as runs (128 + length, value; three or more equal values) and literal blocks (length, values; at most 128).
func hdrNewRLEScanline(row []hdrPixel) []byte {
	out := []byte{2, 2, byte(len(row) >> 8), byte(len(row))}
	for component := range 4 {
		values := make([]byte, len(row))
		for i, p := range row {
			values[i] = p[component]
		}
		out = append(out, hdrRLEComponent(values)...)
	}
	return out
}

func hdrRLEComponent(values []byte) []byte {
	var out []byte
	for i := 0; i < len(values); {
		run := 1
		for i+run < len(values) && values[i+run] == values[i] && run < 127 {
			run++
		}
		if run >= 3 {
			out = append(out, byte(128+run), values[i])
			i += run
			continue
		}
		start := i
		for i < len(values) && i-start < 128 {
			ahead := 1
			for i+ahead < len(values) && values[i+ahead] == values[i] && ahead < 3 {
				ahead++
			}
			if ahead >= 3 {
				break
			}
			i++
		}
		if i == start {
			i++
		}
		out = append(out, byte(i-start))
		out = append(out, values[start:i]...)
	}
	return out
}

func hdrNewRLE(header []byte, rows ...[]hdrPixel) []byte {
	out := append([]byte(nil), header...)
	for _, row := range rows {
		out = append(out, hdrNewRLEScanline(row)...)
	}
	return out
}

// hdrOldRLEScanline writes a row whose runs of the previous pixel are (1, 1, 1, n) markers, a marker for every run of 2 or more pixels
// that follow the first.
func hdrOldRLEScanline(row []hdrPixel) []byte {
	var out []byte
	for i := 0; i < len(row); {
		p := row[i]
		out = append(out, p[:]...)
		i++
		run := 0
		for i+run < len(row) && row[i+run] == p && run < 255 {
			run++
		}
		if run >= 2 {
			out = append(out, 1, 1, 1, byte(run))
			i += run
		}
	}
	return out
}

func hdrOldRLE(header []byte, rows ...[]hdrPixel) []byte {
	out := append([]byte(nil), header...)
	for _, row := range rows {
		out = append(out, hdrOldRLEScanline(row)...)
	}
	return out
}

func hdrSafeFirst(row []hdrPixel) []hdrPixel {
	if row[0][0] == 2 && row[0][1] == 2 && row[0][2] < 128 {
		row[0][0] = 3
	}
	if row[0][0] == 1 && row[0][1] == 1 && row[0][2] == 1 {
		row[0][0] = 3
	}
	return row
}

// hdrCorpus is the files whose decoding the test compares with Photon: every exponent and mantissa byte (the gamma conversion), the
// scanline encodings (flat, old and new run length encoding, widths around the limits of each, marker chains that wrap the run length
// multiplier), the header attributes and their parsing (numbers, whitespace, line endings, invalid UTF-8), the dimensions line, every
// truncation, run lengths past the width, empty images and the 512 MiB limit.
func hdrCorpus() []formatCase {
	var b formatCorpusBuilder
	// All 65536 (exponent, mantissa) pairs: row e holds mantissa 0..255 in all three channels, flat and in both run length encodings.
	var exhaustive [][]hdrPixel
	for e := range 256 {
		row := make([]hdrPixel, 256)
		for c := range 256 {
			row[c] = hdrPixel{byte(c), byte(c), byte(c), byte(e)}
		}
		exhaustive = append(exhaustive, row)
	}
	b.add("exhaustive-flat", hdrFlat(hdrHeader("FORMAT=32-bit_rle_rgbe\n", 256, 256), exhaustive...))
	b.add("exhaustive-new-rle", hdrNewRLE(hdrHeader("", 256, 256), exhaustive...))
	b.add("exhaustive-old-rle", hdrOldRLE(hdrHeader("", 256, 256), exhaustive...))
	// Mixed channels around the clamping thresholds: the largest mantissas at the exponents where 2^(e-136) * c crosses 1.
	var mixed [][]hdrPixel
	for e := 120; e < 142; e++ {
		row := make([]hdrPixel, 256)
		for c := range 256 {
			row[c] = hdrPixel{byte(255 - c), byte(c), byte(c * 5), byte(e)}
		}
		mixed = append(mixed, hdrSafeFirst(row))
	}
	b.add("thresholds-flat", hdrFlat(hdrHeader("", 256, len(mixed)), mixed...))

	// Sizes and encodings with noise.
	for _, width := range []int{1, 2, 3, 7, 8, 9, 31, 127, 128, 129, 130, 255, 256, 257, 1000, 4097} {
		for _, height := range []int{1, 3} {
			rows := make([][]hdrPixel, height)
			for y := range rows {
				rows[y] = hdrSafeFirst(hdrNoise(uint32(width*131+y), width, 112, 150))
			}
			head := hdrHeader("", width, height)
			b.add(fmt.Sprintf("noise-%dx%d-flat", width, height), hdrFlat(head, rows...))
			b.add(fmt.Sprintf("noise-%dx%d-old-rle", width, height), hdrOldRLE(head, rows...))
			b.add(fmt.Sprintf("noise-%dx%d-new-rle", width, height), hdrNewRLE(head, rows...))
		}
	}
	// Widths that new-style encoders never use (the marker's width bytes are ignored by the decoder): above 32767 the third byte of the
	// marker is 128 or more, so the row is read as flat pixels.
	{
		wide := hdrSafeFirst(hdrNoise(77, 40000, 118, 138))
		b.add("noise-40000x1-old-rle", hdrOldRLE(hdrHeader("", 40000, 1), wide))
		b.add("noise-40000x1-new-rle-marker-misread", hdrNewRLE(hdrHeader("", 40000, 1), wide))
		narrow := hdrSafeFirst(hdrNoise(78, 32767, 118, 138))
		b.add("noise-32767x1-new-rle", hdrNewRLE(hdrHeader("", 32767, 1), narrow))
		b.add("noise-32768x1-new-rle", hdrNewRLE(hdrHeader("", 32768, 1), hdrSafeFirst(hdrNoise(79, 32768, 118, 138))))
	}
	// A new-style marker with other width bytes than the real width: ignored.
	{
		row := hdrSafeFirst(hdrNoise(5, 20, 120, 140))
		data := hdrNewRLE(hdrHeader("", 20, 1), row)
		i := bytes.Index(data, []byte{2, 2, 0, 20})
		for _, widthBytes := range [][2]byte{{0, 0}, {0, 19}, {0, 21}, {127, 255}, {5, 5}} {
			d := append([]byte(nil), data...)
			d[i+2], d[i+3] = widthBytes[0], widthBytes[1]
			b.add(fmt.Sprintf("marker-width-bytes-%d-%d", widthBytes[0], widthBytes[1]), d)
		}
		// (2, 2, 127) is the largest third byte read as new-style, (2, 2, 128) is a flat pixel.
		d := append([]byte(nil), data...)
		d[i+2] = 128
		b.add("marker-third-byte-128", d)
		d = append([]byte(nil), data...)
		d[i+2] = 127
		b.add("marker-third-byte-127", d)
		d = append([]byte(nil), data...)
		d[i+1] = 3
		b.add("marker-second-byte-3", d)
	}

	// Component runs and literals at their limits.
	{
		head := hdrHeader("", 300, 1)
		build := func(components ...[]byte) []byte {
			out := append([]byte(nil), head...)
			out = append(out, 2, 2, 1, 44)
			for _, c := range components {
				out = append(out, c...)
			}
			return out
		}
		run := func(n int, v byte) []byte { return []byte{byte(128 + n), v} }
		literal := func(n int) []byte {
			out := []byte{byte(n)}
			for i := range n {
				out = append(out, byte(i*3+1))
			}
			return out
		}
		cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
		full := cat(run(127, 9), run(127, 9), run(46, 9))
		b.add("component-runs-127", build(full, full, full, cat(run(127, 130), run(127, 130), run(46, 130))))
		b.add("component-literal-128", build(cat(literal(128), literal(128), literal(44)), full, full, full))
		b.add("component-rl-128-is-a-literal-of-128", build(cat(literal(128), literal(128), literal(44)), cat([]byte{128}, run(127, 5), run(127, 5), run(46, 5)), full, full))
		b.add("component-run-over-width", build(cat(run(127, 1), run(127, 1), run(47, 1)), full, full, full))
		b.add("component-literal-over-width", build(cat(literal(128), literal(128), literal(45)), full, full, full))
		b.add("component-literal-zero-length-stalls", build(cat([]byte{0, 0, 0}, run(127, 1), run(127, 1), run(46, 1)), full, full, full))
		b.add("component-zero-run", build(cat([]byte{128, 7}, run(127, 1), run(127, 1), run(46, 1)), full, full, full))
		b.add("component-last-run-short", build(cat(run(127, 1), run(127, 1), run(45, 1)), full, full, full))
		b.add("component-literal-truncated", build(cat(literal(128), literal(128), []byte{44, 1, 2}), full, full, full))
		b.add("component-run-without-value", build(cat(run(127, 1), run(127, 1), []byte{128 + 46}), full, full, full))
	}

	// Old-style run markers.
	{
		head := hdrHeader("", 40, 1)
		row := func(parts ...hdrPixel) []byte {
			out := append([]byte(nil), head...)
			for _, p := range parts {
				out = append(out, p[:]...)
			}
			return out
		}
		a := hdrPixel{10, 20, 30, 128}
		bpix := hdrPixel{200, 100, 50, 131}
		mark := func(n byte) hdrPixel { return hdrPixel{1, 1, 1, n} }
		b.add("old-rle-first-pixel-is-a-marker", row(mark(5), a, a))
		b.add("old-rle-run-fills-row", row(a, mark(39)))
		b.add("old-rle-run-one-past", row(a, mark(40)))
		b.add("old-rle-run-zero", row(a, mark(0), bpix, mark(37)))
		b.add("old-rle-marker-after-marker", row(a, bpix, mark(3), mark(1), a, mark(30)))
		b.add("old-rle-chain-of-two", row(a, mark(1), mark(0), bpix, mark(35)))
		// Consecutive markers multiply the run by 256 each; the multiplier (a 32-bit usize) wraps to zero after four.
		b.add("old-rle-chain-of-two-overflows-row", row(a, mark(1), mark(1), a))
		b.add("old-rle-chain-of-two-at-width", row(a, mark(39), mark(0)))
		b.add("old-rle-chain-of-three-zero-runs", row(a, mark(0), mark(0), mark(0), bpix, mark(36)))
		chainRow := func(markers int, tail byte) []byte {
			parts := []hdrPixel{a}
			for range markers {
				parts = append(parts, mark(0))
			}
			parts = append(parts, mark(tail))
			return row(parts...)
		}
		for _, n := range []int{2, 3, 4, 5, 6, 8} {
			b.add(fmt.Sprintf("old-rle-chain-%d-zero-markers-then-run", n), chainRow(n, 3))
		}
		bigMarkers := []hdrPixel{a, mark(1), mark(1), mark(1), mark(1), mark(1), bpix}
		b.add("old-rle-chain-of-five-ones", row(bigMarkers...))
		wrap := append([]hdrPixel{a}, mark(1), mark(1), mark(1), mark(1), mark(1), mark(1), mark(1), mark(1))
		b.add("old-rle-chain-of-eight-ones", row(wrap...))
		// A marker whose first three bytes are not 1.
		b.add("old-rle-not-a-marker", row(a, hdrPixel{1, 1, 2, 5}, hdrPixel{1, 2, 1, 5}, hdrPixel{0, 1, 1, 5}, mark(36)))
		// A (2, 2, 128+) pixel at the start of a row is flat.
		b.add("old-rle-first-pixel-2-2-128", hdrFlat(hdrHeader("", 3, 1), []hdrPixel{{2, 2, 128, 130}, {1, 2, 3, 129}, {9, 9, 9, 120}}))
		b.add("old-rle-first-pixel-2-2-127-is-new-style", hdrFlat(hdrHeader("", 3, 1), []hdrPixel{{2, 2, 127, 130}, {1, 2, 3, 129}, {9, 9, 9, 120}}))
		b.add("old-rle-first-pixel-2-3-0", hdrFlat(hdrHeader("", 3, 1), []hdrPixel{{2, 3, 0, 130}, {1, 2, 3, 129}, {9, 9, 9, 120}}))
		// The old style mixes with the new style per scanline.
		r1 := hdrSafeFirst(hdrNoise(31, 20, 120, 140))
		r2 := hdrSafeFirst(hdrNoise(32, 20, 120, 140))
		r3 := hdrSafeFirst(hdrNoise(33, 20, 120, 140))
		mixedFile := hdrHeader("", 20, 3)
		mixedFile = append(mixedFile, hdrNewRLEScanline(r1)...)
		mixedFile = append(mixedFile, hdrOldRLEScanline(r2)...)
		mixedFile = append(mixedFile, hdrNewRLEScanline(r3)...)
		b.add("scanlines-mixed-styles", mixedFile)
	}

	// Header attributes. The pixel data is the same one-pixel row everywhere.
	pixel := []hdrPixel{{128, 64, 32, 136}}
	withHeader := func(header string) []byte {
		out := []byte("#?RADIANCE\n" + header + "\n-Y 1 +X 1\n")
		return hdrFlat(out, pixel)
	}
	for name, attrs := range map[string]string{
		"format-valid":                 "FORMAT=32-bit_rle_rgbe\n",
		"format-valid-spaces":          "FORMAT= \t32-bit_rle_rgbe \t\n",
		"format-valid-key-spaces":      "\t FORMAT\t =32-bit_rle_rgbe\n",
		"format-xyze":                  "FORMAT=32-bit_rle_xyze\n",
		"format-empty-value":           "FORMAT=\n",
		"format-case":                  "FORMAT=32-bit_RLE_rgbe\n",
		"format-lowercase-key":         "format=32-bit_rle_xyze\n",
		"format-twice-bad-last":        "FORMAT=32-bit_rle_rgbe\nFORMAT=other\n",
		"format-twice-bad-first":       "FORMAT=other\nFORMAT=32-bit_rle_rgbe\n",
		"format-cr":                    "FORMAT=32-bit_rle_rgbe\r\n",
		"format-nbsp":                  "FORMAT=\u00a032-bit_rle_rgbe\u00a0\n",
		"format-ideographic-space":     "FORMAT=\u300032-bit_rle_rgbe\u3000\n",
		"format-zero-width-space":      "FORMAT=\u200b32-bit_rle_rgbe\n",
		"format-equals-in-value":       "FORMAT==32-bit_rle_rgbe\n",
		"format-equals-in-value-2":     "FORMAT=32-bit_rle_rgbe=\n",
		"format-no-value-at-end":       "FORMAT=",
		"equals-first":                 "=FORMAT=other\n",
		"equals-last-after-key":        "FORMAT=\n",
		"equals-only":                  "=\n",
		"line-without-equals":          "just a comment line\n",
		"comment-hash":                 "# FORMAT=other\n",
		"comment-hash-after-space":     " # FORMAT=other\n",
		"comment-only":                 "#\n",
		"software":                     "SOFTWARE=pig 1.0\nVIEW= -vta -vp 0 0 0\n",
		"exposure-int":                 "EXPOSURE=2\n",
		"exposure-float":               "EXPOSURE=1.5\n",
		"exposure-exp":                 "EXPOSURE=1e-3\n",
		"exposure-exp-upper":           "EXPOSURE=1.5E+3\n",
		"exposure-exp-no-digits":       "EXPOSURE=1e\n",
		"exposure-exp-sign-only":       "EXPOSURE=1e+\n",
		"exposure-point-lead":          "EXPOSURE=.5\n",
		"exposure-point-trail":         "EXPOSURE=5.\n",
		"exposure-point-only":          "EXPOSURE=.\n",
		"exposure-sign-plus":           "EXPOSURE=+2\n",
		"exposure-sign-minus":          "EXPOSURE=-2\n",
		"exposure-sign-only":           "EXPOSURE=+\n",
		"exposure-double-sign":         "EXPOSURE=+-2\n",
		"exposure-inf":                 "EXPOSURE=inf\n",
		"exposure-infinity-upper":      "EXPOSURE=INFINITY\n",
		"exposure-minus-inf":           "EXPOSURE=-Inf\n",
		"exposure-infini" + "t":        "EXPOSURE=infini" + "t\n",
		"exposure-nan":                 "EXPOSURE=NaN\n",
		"exposure-nan-sign":            "EXPOSURE=-nan\n",
		"exposure-nan-payload":         "EXPOSURE=nan(0x1)\n",
		"exposure-hex":                 "EXPOSURE=0x10\n",
		"exposure-hex-float":           "EXPOSURE=0x1p4\n",
		"exposure-underscore":          "EXPOSURE=1_0\n",
		"exposure-huge":                "EXPOSURE=1e999\n",
		"exposure-tiny":                "EXPOSURE=1e-999\n",
		"exposure-long-digits":         "EXPOSURE=0.0000000000000000000000000000000000000000000000000000001\n",
		"exposure-empty-after-trim":    "EXPOSURE=  \n",
		"exposure-text":                "EXPOSURE=abc\n",
		"exposure-two-numbers":         "EXPOSURE=1 2\n",
		"exposure-trailing-text":       "EXPOSURE=1x\n",
		"exposure-nbsp-around":         "EXPOSURE=\u00a02\u00a0\n",
		"exposure-twice":               "EXPOSURE=2\nEXPOSURE=3\n",
		"exposure-twice-bad":           "EXPOSURE=2\nEXPOSURE=x\n",
		"exposure-key-case":            "exposure=x\n",
		"exposure-key-prefix":          "EXPOSURE2=x\n",
		"exposure-cr":                  "EXPOSURE=2\r\n",
		"exposure-bad-then-good":       "EXPOSURE=x\nFORMAT=32-bit_rle_rgbe\n",
		"exposure-invalid-utf8":        "EXPOSURE=2\xff\n",
		"exposure-invalid-utf8-key":    "EXPO\xffSURE=x\n",
		"exposure-replacement-char":    "EXPOSURE=\ufffd\n",
		"exposure-fullwidth-digit":     "EXPOSURE=\uff12\n",
		"exposure-arabic-digit":        "EXPOSURE=\u0662\n",
		"pixaspect-valid":              "PIXASPECT=0.5\n",
		"pixaspect-invalid":            "PIXASPECT=wide\n",
		"pixaspect-empty":              "PIXASPECT=\n",
		"pixaspect-inf":                "PIXASPECT=inf\n",
		"colorcorr-three":              "COLORCORR=1.0 0.9 0.8\n",
		"colorcorr-three-tabs":         "COLORCORR=\t1.0\t0.9\t0.8\t\n",
		"colorcorr-two":                "COLORCORR=1.0 0.9\n",
		"colorcorr-one":                "COLORCORR=1.0\n",
		"colorcorr-blank":              "COLORCORR=  \n",
		"colorcorr-four":               "COLORCORR=1.0 0.9 0.8 0.7\n",
		"colorcorr-four-bad-fourth":    "COLORCORR=1.0 0.9 0.8 nope\n",
		"colorcorr-bad-second":         "COLORCORR=1.0 x 0.8\n",
		"colorcorr-bad-third-short":    "COLORCORR=1.0 0.9 x\n",
		"colorcorr-short-and-bad":      "COLORCORR=1.0 x\n",
		"colorcorr-special":            "COLORCORR=inf nan -0\n",
		"colorcorr-nbsp-separated":     "COLORCORR=1\u00a02\u00a03\n",
		"colorcorr-twice":              "COLORCORR=1 2 3\nCOLORCORR=2 3 4\n",
		"many-attributes":              "FORMAT=32-bit_rle_rgbe\nEXPOSURE=1\nPIXASPECT=1\nCOLORCORR=1 1 1\nSOFTWARE=x\n# note\nVIEW=y\n",
		"blank-signature-rest":         "",
		"unknown-attribute-bad-number": "GAMMA=oops\n",
		"empty-key-with-spaces":        " =x\n",
	} {
		b.add("header-"+name, withHeader(attrs))
	}

	// Signature line and the end of the header.
	body := hdrFlat(nil, pixel)
	b.add("signature-extra-text", append([]byte("#?RADIANCE extra words\n\n-Y 1 +X 1\n"), body...))
	b.add("signature-cr-lf", append([]byte("#?RADIANCE\r\n\n-Y 1 +X 1\n"), body...))
	b.add("signature-no-newline-eof", []byte("#?RADIANCE"))
	b.add("signature-then-eof-after-newline", []byte("#?RADIANCE\n"))
	b.add("signature-then-empty-line-eof", []byte("#?RADIANCE\n\n"))
	b.add("signature-lowercase", append([]byte("#?radiance\n\n-Y 1 +X 1\n"), body...))
	b.add("signature-9-bytes", []byte("#?RADIANC"))
	b.add("signature-rgbe-name", append([]byte("#?RGBE\n\n-Y 1 +X 1\n"), body...))
	b.add("header-crlf-lines-never-end", append([]byte("#?RADIANCE\r\nFORMAT=32-bit_rle_rgbe\r\n\r\n-Y 1 +X 1\r\n"), body...))
	b.add("header-crlf-then-lf-empty", append([]byte("#?RADIANCE\r\nFORMAT=32-bit_rle_rgbe\r\n\n-Y 1 +X 1\r\n"), body...))
	b.add("header-no-empty-line", []byte("#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n-Y 1 +X 1\n"))
	b.add("header-eof-in-attribute", []byte("#?RADIANCE\nFORMAT=32-bit_rle_rgbe"))
	b.add("header-comment-then-dims", append([]byte("#?RADIANCE\n#\n#c\n\n-Y 1 +X 1\n"), body...))
	b.add("header-no-dims-line", []byte("#?RADIANCE\n\n"))
	b.add("header-dims-no-newline", append([]byte("#?RADIANCE\n\n-Y 1 +X 1"), body...))
	b.add("header-dims-eof-no-pixels", []byte("#?RADIANCE\n\n-Y 1 +X 1"))
	b.add("header-dims-eof-with-newline", []byte("#?RADIANCE\n\n-Y 1 +X 1\n"))

	// The dimensions line.
	dims := func(line string, pixels int) []byte {
		out := []byte("#?RADIANCE\n\n" + line + "\n")
		row := make([]hdrPixel, pixels)
		for i := range row {
			row[i] = hdrPixel{byte(10 + i), 200, 30, 130}
		}
		if pixels > 0 {
			return hdrFlat(out, row)
		}
		return out
	}
	for name, d := range map[string]struct {
		line   string
		pixels int
	}{
		"standard":             {"-Y 2 +X 3", 6},
		"tabs":                 {"-Y\t2\t+X\t3", 6},
		"many-spaces":          {"  -Y   2   +X   3  ", 6},
		"nbsp":                 {"-Y\u00a02\u00a0+X\u00a03", 6},
		"ideographic":          {"-Y\u30002\u3000+X\u30003", 6},
		"cr-end":               {"-Y 2 +X 3\r", 6},
		"plus-sign":            {"-Y +2 +X +3", 6},
		"minus-sign":           {"-Y -2 +X 3", 6},
		"zero-padded":          {"-Y 002 +X 0003", 6},
		"fullwidth":            {"-Y \uff12 +X 3", 6},
		"hex":                  {"-Y 0x2 +X 3", 6},
		"underscore":           {"-Y 2_0 +X 3", 6},
		"decimal":              {"-Y 2.0 +X 3", 6},
		"exponent":             {"-Y 2e0 +X 3", 6},
		"empty-number-plus":    {"-Y + +X 3", 6},
		"u32-max-height-zero":  {"-Y 4294967295 +X 0", 0},
		"u32-overflow-height":  {"-Y 4294967296 +X 0", 0},
		"u32-overflow-width":   {"-Y 0 +X 4294967296", 0},
		"u32-max-width-zero":   {"-Y 0 +X 4294967295", 0},
		"u32-max-by-max":       {"-Y 4294967295 +X 4294967295", 0},
		"u32-max-width-1":      {"-Y 1 +X 4294967295", 0},
		"2^30-wide-empty":      {"-Y 0 +X 1073741824", 0},
		"2^30-1-wide-empty":    {"-Y 0 +X 1073741823", 0},
		"1431655765-empty":     {"-Y 0 +X 1431655765", 0},
		"1431655766-empty":     {"-Y 0 +X 1431655766", 0},
		"empty-width":          {"-Y 5 +X 0", 0},
		"empty-height":         {"-Y 0 +X 5", 0},
		"empty-both":           {"-Y 0 +X 0", 0},
		"empty-height-huge":    {"-Y 4294967295 +X 0", 0},
		"empty-width-huge-h-0": {"-Y 0 +X 4294967295", 0},
		"flipped-y":            {"+Y 2 +X 3", 6},
		"flipped-x":            {"-Y 2 -X 3", 6},
		"swapped-axes":         {"+X 3 -Y 2", 6},
		"plus-y-minus-x":       {"+Y 2 -X 3", 6},
		"x-first":              {"-X 3 +Y 2", 6},
		"lowercase":            {"-y 2 +x 3", 6},
		"too-short-3":          {"-Y 2 +X", 6},
		"too-short-2":          {"-Y 2", 6},
		"too-short-1":          {"-Y", 6},
		"too-short-0":          {"", 6},
		"too-long":             {"-Y 2 +X 3 +Z 4", 6},
		"too-long-extra-word":  {"-Y 2 +X 3 x", 6},
		"words":                {"-Y two +X three", 6},
		"empty-tags":           {"2 3", 6},
		"width-1-height-6":     {"-Y 6 +X 1", 6},
		"width-6-height-1":     {"-Y 1 +X 6", 6},
		"data-short":           {"-Y 2 +X 3", 5},
		"data-for-empty":       {"-Y 0 +X 3", 3},
	} {
		b.add("dims-"+name, dims(d.line, d.pixels))
	}

	// Truncations of small files in each encoding.
	smallRows := [][]hdrPixel{hdrSafeFirst(hdrNoise(41, 12, 125, 135)), hdrSafeFirst(hdrNoise(42, 12, 125, 135))}
	for name, file := range map[string][]byte{
		"flat":    hdrFlat(hdrHeader("FORMAT=32-bit_rle_rgbe\n", 12, 2), smallRows...),
		"new-rle": hdrNewRLE(hdrHeader("FORMAT=32-bit_rle_rgbe\n", 12, 2), smallRows...),
		"old-rle": hdrOldRLE(hdrHeader("FORMAT=32-bit_rle_rgbe\n", 12, 2), smallRows...),
	} {
		for n := range len(file) {
			b.add(fmt.Sprintf("truncated-%s-%03d", name, n), file[:n:n])
		}
		b.add("trailing-"+name, append(append([]byte(nil), file...), 1, 2, 3, 4, 5))
	}

	// Limits: width * height * 3 may not exceed 2^29 bytes (178956970 pixels). The first row decodes (so Photon's allocation is made).
	limitFile := func(width, height int) func() []byte {
		return func() []byte {
			row := hdrSafeFirst(hdrNoise(99, width, 120, 136))
			scanline := hdrNewRLEScanline(row)
			out := hdrHeader("", width, height)
			for range height {
				out = append(out, scanline...)
			}
			return out
		}
	}
	b.add("limit-over-header-only", hdrHeader("", 16384, 10923))
	b.add("limit-over-header-and-row", append(hdrHeader("", 178956971, 1), hdrNewRLEScanline(hdrSafeFirst(hdrNoise(1, 16, 120, 130)))...))
	b.add("limit-exact-header-only", hdrHeader("", 178956970, 1))
	b.add("limit-over-u32-by-u32", hdrHeader("", 4294967295, 4294967295))
	b.add("limit-over-wrapped-product", hdrHeader("", 65536, 65537))
	b.add("limit-over-2^32-pixels", hdrHeader("", 65536, 65536))
	b.addLazy("limit-just-under-16384x10922", limitFile(16384, 10922))
	b.addLazy("limit-over-by-one-row-16384x10923", limitFile(16384, 10923))
	b.addLazy("limit-wide-1-row-32768", limitFile(32768, 1))
	return b.cases
}
