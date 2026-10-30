// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: The image-rs Developers
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"errors"
	"image"
	"math"
	"strings"
	"sync"
)

// hdrSignature is image 0.24.9's guess_format signature for Radiance HDR (io/free_functions.rs:277) and the signature the decoder
// checks (codecs/hdr/decoder.rs:236, :349-354).
const hdrSignature = "#?RADIANCE"

// isHDR is image::guess_format's test for a Radiance HDR file.
func isHDR(data []byte) bool {
	return bytes.HasPrefix(data, []byte(hdrSignature))
}

var errHDR = errors.New("photonimage: hdr: invalid or truncated file")

// hdrReader is the `BufRead` over the byte slice that `HdrAdapter::new(BufReader::new(Cursor))` reads.
type hdrReader struct {
	data []byte
	pos  int
}

// readExact is `Read::read_exact`: false when fewer than n bytes are left.
func (r *hdrReader) readExact(n int) ([]byte, bool) {
	if len(r.data)-r.pos < n {
		r.pos = len(r.data)
		return nil, false
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, true
}

// readLine is decoder.rs:970-982 `read_line_u8`: the bytes up to a `\n` (not included), or false at the end of the input.
func (r *hdrReader) readLine() ([]byte, bool) {
	if r.pos >= len(r.data) {
		return nil, false
	}
	rest := r.data[r.pos:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		r.pos += i + 1
		return rest[:i], true
	}
	r.pos = len(r.data)
	return rest, true
}

// DecodeHDR returns the RGBA8 pixels Photon's `PhotonImage.new_from_byteslice(data).get_raw_pixels()` returns for a Radiance HDR file:
// image 0.24.9's HdrAdapter (codecs/hdr/decoder.rs, strict mode as `load_decoder` calls `HdrAdapter::new`), whose colour type is Rgb8:
// the RGBE pixels are converted by `Rgbe8Pixel::to_ldr` (`(exp2(e - 136) * c) ** 2.2 * 255 + 0.5` in f32 with libm's powf and exp2f) and `to_rgba8` adds an opaque alpha. Only the orientation `-Y height +X width` is accepted, with the 32-bit_rle_rgbe FORMAT.
//
// What the decoder rejects is what it rejects: a signature line, header lines until an empty line (`FORMAT=` other than 32-bit_rle_rgbe,
// unparsable EXPOSURE, PIXASPECT and COLORCORR values and a COLORCORR with more than three numbers are errors in strict mode), the
// dimensions line, and scanlines (new-style RLE, flat or old-style RLE) that are cut or run past the width. An image of more than
// 2^29 bytes as Rgb8 (width * height * 3) is an error (imageMaxAlloc), and an image with no pixels reads no scanline.
func DecodeHDR(data []byte) (*image.NRGBA, error) {
	r := &hdrReader{data: data}
	sig, ok := r.readExact(len(hdrSignature))
	if !ok || string(sig) != hdrSignature {
		return nil, errors.New("photonimage: hdr: Radiance HDR signature not found")
	}
	// Skip the rest of the signature line (`read_line_u8(r)?` is Ok(None) at the end of the input).
	r.readLine()
	// Read header data until the empty line.
	for {
		line, ok := r.readLine()
		if !ok {
			return nil, errors.New("photonimage: hdr: EOF in header")
		}
		if len(line) == 0 {
			break
		}
		if line[0] == '#' {
			continue
		}
		if err := hdrUpdateHeaderInfo(hdrLossy(line)); err != nil {
			return nil, err
		}
	}
	dimensions, ok := r.readLine()
	if !ok {
		return nil, errors.New("photonimage: hdr: EOF in dimensions line")
	}
	width, height, err := hdrParseDimensionsLine(hdrLossy(dimensions))
	if err != nil {
		return nil, err
	}
	// decoder.rs:397-408 check_dimension_overflow(width, height, 3) and load_inner's reservation of total_bytes.
	if err := reserveTotalBytes(width, height, 3); err != nil {
		return nil, err
	}
	if err := checkRGBAConversion(width, height, 3); err != nil {
		return nil, err
	}
	// read_image_transform reads nothing for an empty image.
	if width == 0 || height == 0 {
		return newImageNRGBA(width, height), nil
	}

	ldr := hdrLDR()
	buf := make([][4]byte, width)
	var out *image.NRGBA
	for y := range int(height) {
		if err := hdrReadScanline(r, buf); err != nil {
			return nil, err
		}
		if out == nil {
			// The pixels are allocated once a scanline has decoded: a header alone asks for up to 683 MiB of RGBA.
			out = newImageNRGBA(width, height)
		}
		row := out.Pix[y*int(width)*4:]
		for x, p := range buf {
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = ldr[p[3]][p[0]], ldr[p[3]][p[1]], ldr[p[3]][p[2]], 255
		}
	}
	return out, nil
}

// hdrLossy is `String::from_utf8_lossy`: invalid UTF-8 becomes U+FFFD, which is neither whitespace, a digit nor a separator, so a
// run of invalid bytes standing for one or several replacement characters behaves the same.
func hdrLossy(b []byte) string {
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// hdrSplitAtFirst is decoder.rs:935-941 `split_at_first(s, "=")`: no split for an `=` at the start or the end of the line.
func hdrSplitAtFirst(s string) (key, value string, ok bool) {
	p := strings.IndexByte(s, '=')
	if p <= 0 || p >= len(s)-1 {
		return "", "", false
	}
	return s[:p], s[p+1:], true
}

// hdrUpdateHeaderInfo is `HdrMetadata::update_header_info(line, strict = true)` (decoder.rs:769-855); the attributes it computes do not
// reach the pixels, so only its errors are kept.
func hdrUpdateHeaderInfo(line string) error {
	key, value, ok := hdrSplitAtFirst(line)
	if !ok {
		return nil
	}
	switch strings.TrimSpace(key) {
	case "FORMAT":
		if strings.TrimSpace(value) != "32-bit_rle_rgbe" {
			return errors.New("photonimage: hdr: unsupported FORMAT")
		}
	case "EXPOSURE", "PIXASPECT":
		if !rustFloatSyntax(strings.TrimSpace(value)) {
			return errors.New("photonimage: hdr: cannot parse " + strings.TrimSpace(key) + " value as f32")
		}
	case "COLORCORR":
		numbers := strings.Fields(value)
		if len(numbers) < 3 {
			return errors.New("photonimage: hdr: not enough numbers in COLORCORR")
		}
		for _, number := range numbers[:3] {
			if !rustFloatSyntax(number) {
				return errors.New("photonimage: hdr: cannot parse COLORCORR value as f32")
			}
		}
		if len(numbers) > 3 {
			return errors.New("photonimage: hdr: extra numbers in COLORCORR")
		}
	}
	return nil
}

// hdrParseDimensionsLine is decoder.rs:876-921 in strict mode: `-Y height +X width`.
func hdrParseDimensionsLine(line string) (width, height uint32, err error) {
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return 0, 0, errors.New("photonimage: hdr: dimensions line too short")
	}
	if len(parts) > 4 {
		return 0, 0, errors.New("photonimage: hdr: dimensions line too long")
	}
	if parts[0] != "-Y" || parts[2] != "+X" {
		return 0, 0, errors.New("photonimage: hdr: unsupported orientation")
	}
	h64, ok := rustParseUint(parts[1], 32)
	if !ok {
		return 0, 0, errors.New("photonimage: hdr: cannot parse height dimension as u32")
	}
	w64, ok := rustParseUint(parts[3], 32)
	if !ok {
		return 0, 0, errors.New("photonimage: hdr: cannot parse width dimension as u32")
	}
	return uint32(w64), uint32(h64), nil
}

// rustFloatSyntax is whether `str::parse::<f32>` accepts s (core::num::dec2flt): an optional sign, then `inf`, `infinity` or `nan` in
// any case, or decimal digits with an optional point (at least one digit) and an optional exponent that has digits. An out of range
// number parses (to infinity or zero).
func rustFloatSyntax(s string) bool {
	if s != "" && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	switch strings.ToLower(s) {
	case "inf", "infinity", "nan":
		return true
	}
	i, digits := 0, 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exponent := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			exponent++
		}
		if exponent == 0 {
			return false
		}
	}
	return i == len(s)
}

// hdrReadScanline is decoder.rs:585-603 `read_scanline`: the first four bytes tell the new component-wise run length encoding
// (2, 2, <128) from the old pixel-wise one.
func hdrReadScanline(r *hdrReader, buf [][4]byte) error {
	fb, ok := r.readExact(4)
	if !ok {
		return errHDR
	}
	if fb[0] == 2 && fb[1] == 2 && fb[2] < 128 {
		for component := range 4 {
			if err := hdrDecodeComponent(r, buf, component); err != nil {
				return err
			}
		}
		return nil
	}
	return hdrDecodeOldRLE(r, [4]byte{fb[0], fb[1], fb[2], fb[3]}, buf)
}

// hdrDecodeComponent is decoder.rs:614-656 `decode_component`.
func hdrDecodeComponent(r *hdrReader, buf [][4]byte, component int) error {
	width := len(buf)
	pos := 0
	for pos < width {
		rlb, ok := r.readExact(1)
		if !ok {
			return errHDR
		}
		rl := int(rlb[0])
		if rl <= 128 {
			if pos+rl > width {
				return errors.New("photonimage: hdr: wrong scanline length")
			}
			values, ok := r.readExact(rl)
			if !ok {
				return errHDR
			}
			for offset, value := range values {
				buf[pos+offset][component] = value
			}
			pos += rl
		} else {
			rl -= 128
			if pos+rl > width {
				return errors.New("photonimage: hdr: wrong scanline length")
			}
			value, ok := r.readExact(1)
			if !ok {
				return errHDR
			}
			for offset := range rl {
				buf[pos+offset][component] = value[0]
			}
			pos += rl
		}
	}
	return nil
}

// hdrDecodeOldRLE is decoder.rs:661-713 `decode_old_rle`. The run length multiplier is a wasm32 `usize`, which wraps to zero after four
// consecutive markers (Photon's release build has no overflow checks), and a run whose end overflows `usize` is a panic (an error).
func hdrDecodeOldRLE(r *hdrReader, fb [4]byte, buf [][4]byte) error {
	width := len(buf)
	marker := func(p [4]byte) (int, bool) {
		if p[0] == 1 && p[1] == 1 && p[2] == 1 {
			return int(p[3]), true
		}
		return 0, false
	}
	if _, isMarker := marker(fb); isMarker {
		return errors.New("photonimage: hdr: first pixel of a scanline is a run length marker")
	}
	buf[0] = fb
	xOff := 1
	rlMult := uint32(1)
	prev := fb
	for xOff < width {
		raw, ok := r.readExact(4)
		if !ok {
			return errHDR
		}
		pix := [4]byte{raw[0], raw[1], raw[2], raw[3]}
		if rl, isMarker := marker(pix); isMarker {
			run := uint64(uint32(rl) * rlMult) // wrapping u32 product
			rlMult *= 256
			if uint64(xOff)+run > uint64(width) {
				return errors.New("photonimage: hdr: wrong scanline length")
			}
			for i := range int(run) {
				buf[xOff+i] = prev
			}
			xOff += int(run)
		} else {
			rlMult = 1
			prev = pix
			buf[xOff] = pix
			xOff++
		}
	}
	return nil
}

var (
	hdrLDROnce  sync.Once
	hdrLDRTable [256][256]byte
)

// hdrLDR is `Rgbe8Pixel::to_ldr` (decoder.rs:266-325) for every exponent and mantissa byte: `[e][c]`. `to_hdr` gives `exp2(e - 136) * c`
// in f32 (zero for e == 0; the product of a power of two and an integer below 256 is exact), `to_ldr_scale_gamma(1.0, 2.2)` then
// `powf(v * 1.0, 2.2) * 255 + 0.5`, clamped to [0, 255] and truncated. Photon's powf is libm's (a float32 routine); math.Pow rounded to float32
// gives the same 8-bit value for each of the 65,536 pairs, as TestHDRConversionTableIsLibms proves against a literal port of libm.
func hdrLDR() *[256][256]byte {
	hdrLDROnce.Do(func() {
		gamma := float64(float32(2.2))
		for e := 1; e < 256; e++ {
			exp := float32(math.Ldexp(1, e-136))
			for c := range 256 {
				v := float32(exp * float32(c))
				fv := float32(float32(math.Pow(float64(v), gamma))*255) + 0.5
				switch {
				case fv < 0:
					hdrLDRTable[e][c] = 0
				case fv > 255:
					hdrLDRTable[e][c] = 255
				default:
					hdrLDRTable[e][c] = byte(fv)
				}
			}
		}
	})
	return &hdrLDRTable
}
