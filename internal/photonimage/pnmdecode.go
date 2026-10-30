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
	"unicode"
	"unicode/utf8"
)

// isPNM is image::guess_format's test for a PNM: the signatures P1 to P7 (io/free_functions.rs:278-284).
func isPNM(data []byte) bool {
	return len(data) >= 2 && data[0] == 'P' && data[1] >= '1' && data[1] <= '7'
}

var (
	errPNMTooShort = errors.New("photonimage: pnm: not enough data to decode the image")
)

// pnmTuple is decoder.rs:226-233 `TupleType`.
type pnmTuple int

const (
	pnmPbmBit pnmTuple = iota
	pnmBWBit
	pnmGrayU8
	pnmGrayU16
	pnmRGBU8
	pnmRGBU16
)

// bytesPerPixel, channels and sampleSize are the sizes of the colour type (L8, L8, L8, L16, Rgb8, Rgb16) the decoder reports.
func (t pnmTuple) bytesPerPixel() uint64 {
	switch t {
	case pnmGrayU16:
		return 2
	case pnmRGBU8:
		return 3
	case pnmRGBU16:
		return 6
	}
	return 1
}

func (t pnmTuple) channels() uint64 {
	if t == pnmRGBU8 || t == pnmRGBU16 {
		return 3
	}
	return 1
}

func (t pnmTuple) sampleSize() int {
	if t == pnmGrayU16 || t == pnmRGBU16 {
		return 2
	}
	return 1
}

// pnmHeader is the decoded header: the dimensions, the maximal sample (`PnmHeader::maximal_sample`), whether the samples are binary
// and the type of the samples.
type pnmHeader struct {
	width, height uint32
	maxSample     uint32
	binary        bool
	tuple         pnmTuple
}

// pnmReader is the `BufRead` over the byte slice that `PnmDecoder::new` reads.
type pnmReader struct {
	data []byte
	pos  int
}

// DecodePNM returns the RGBA8 pixels Photon's `PhotonImage.new_from_byteslice(data).get_raw_pixels()` returns for a PNM file: image
// 0.24.9's PnmDecoder (codecs/pnm/decoder.rs, header.rs), P1 to P7, and `to_rgba8` of the colour type it reports (L8 for P1 and P4 bitmaps
// and 8-bit graymaps, L16, Rgb8, Rgb16).
//
// The details of the decoder are kept: a comment in the preamble ends at the newline, which is not a separator, so `1#c\n2` is the number
// 12; the PAM header lines are split by the position of their first whitespace in the line before it is trimmed; samples of an 8-bit
// image that are above the maxval are scaled and saturate, those of a bitmap are inverted (1 is black); a maxval other than 255 or 65535 scales every
// sample by `round(v * (255 | 65535) / maxval)` in f32. An image of more than 2^29 bytes in the colour type is an error (imageMaxAlloc),
// as is an empty image whose RGBA buffer length overflows wasm32's `usize` (checkRGBAConversion).
func DecodePNM(data []byte) (*image.NRGBA, error) {
	if !isPNM(data) {
		return nil, errors.New("photonimage: pnm: not a PNM (no P1 to P7 signature)")
	}
	r := &pnmReader{data: data, pos: 2}
	h, err := r.readHeader(data[1])
	if err != nil {
		return nil, err
	}
	// codecs/pnm/decoder.rs:288-303 check_dimension_overflow and load_inner's reservation of total_bytes.
	if err := reserveTotalBytes(h.width, h.height, h.tuple.bytesPerPixel()); err != nil {
		return nil, err
	}
	if err := checkRGBAConversion(h.width, h.height, h.tuple.channels()); err != nil {
		return nil, err
	}
	return r.readImage(h)
}

// readHeader is PnmDecoder::new after the magic constant: `read_{bitmap,graymap,pixmap,arbitrary}_header` and `tuple_type`.
func (r *pnmReader) readHeader(kind byte) (pnmHeader, error) {
	switch kind {
	case '1', '4':
		w, h, err := r.readTwoU32()
		if err != nil {
			return pnmHeader{}, err
		}
		return pnmHeader{width: w, height: h, maxSample: 1, binary: kind == '4', tuple: pnmPbmBit}, nil
	case '2', '3', '5', '6':
		w, h, err := r.readTwoU32()
		if err != nil {
			return pnmHeader{}, err
		}
		maxval, err := r.readNextU32()
		if err != nil {
			return pnmHeader{}, err
		}
		tuple, err := pnmTupleOf(kind == '3' || kind == '6', maxval)
		if err != nil {
			return pnmHeader{}, err
		}
		return pnmHeader{width: w, height: h, maxSample: maxval, binary: kind == '5' || kind == '6', tuple: tuple}, nil
	}
	return r.readArbitraryHeader()
}

// pnmTupleOf is GraymapHeader::tuple_type and PixmapHeader::tuple_type (decoder.rs:830-850).
func pnmTupleOf(colour bool, maxval uint32) (pnmTuple, error) {
	switch {
	case maxval == 0:
		return 0, errors.New("photonimage: pnm: image MAXVAL is zero")
	case maxval <= 0xFF && colour:
		return pnmRGBU8, nil
	case maxval <= 0xFF:
		return pnmGrayU8, nil
	case maxval <= 0xFFFF && colour:
		return pnmRGBU16, nil
	case maxval <= 0xFFFF:
		return pnmGrayU16, nil
	}
	return 0, errors.New("photonimage: pnm: image MAXVAL exceeds 65535")
}

func (r *pnmReader) readTwoU32() (w, h uint32, err error) {
	if w, err = r.readNextU32(); err != nil {
		return 0, 0, err
	}
	h, err = r.readNextU32()
	return w, h, err
}

func (r *pnmReader) readNextU32() (uint32, error) {
	s, err := r.readNextString()
	if err != nil {
		return 0, err
	}
	v, ok := rustParseUint(s, 32)
	if !ok {
		return 0, errors.New("photonimage: pnm: error parsing number in preamble")
	}
	return uint32(v), nil
}

// readNextString is decoder.rs:374-420 `read_next_string`: the next run of non-whitespace bytes outside comments, and the single
// whitespace after it. A comment starts at `#` and ends at the next CR or LF, which it swallows: the byte after that is read as if
// no whitespace had come between it and what precedes the comment.
func (r *pnmReader) readNextString() (string, error) {
	var token []byte
	enabled := true
	for r.pos < len(r.data) {
		c := r.data[r.pos]
		r.pos++
		current := enabled && c != '#'
		enabled = current || c == '\r' || c == '\n'
		if !current {
			continue
		}
		if pnmIsSpace(c) {
			if len(token) > 0 {
				break
			}
			continue
		}
		if c >= 0x80 {
			return "", errors.New("photonimage: pnm: non-ASCII byte in header")
		}
		token = append(token, c)
	}
	if len(token) == 0 {
		return "", errPNMTooShort
	}
	return string(token), nil
}

// pnmIsSpace is the separators of the decoder: tab, newline, vertical tab, form feed, carriage return and space.
func pnmIsSpace(c byte) bool {
	return c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r' || c == ' '
}

// readLine is `BufRead::read_line`: the bytes up to and including a newline, which must be valid UTF-8.
func (r *pnmReader) readLine() (string, error) {
	rest := r.data[r.pos:]
	n := len(rest)
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		n = i + 1
	}
	line := rest[:n]
	r.pos += n
	if !utf8.Valid(line) {
		return "", errors.New("photonimage: pnm: stream did not contain valid UTF-8")
	}
	return string(line), nil
}

// readArbitraryHeader is decoder.rs:466-568: a PAM header after `P7\n`.
func (r *pnmReader) readArbitraryHeader() (pnmHeader, error) {
	if r.pos >= len(r.data) {
		return pnmHeader{}, errPNMTooShort
	}
	if r.data[r.pos] != '\n' {
		return pnmHeader{}, errors.New("photonimage: pnm: expected newline after P7 magic")
	}
	r.pos++

	var height, width, depth, maxval *uint32
	var tupltype *string
	parseSingle := func(slot **uint32, rest string) error {
		if *slot != nil {
			return errors.New("photonimage: pnm: duplicate header line")
		}
		v, ok := rustParseUint(strings.TrimSpace(rest), 32)
		if !ok {
			return errors.New("photonimage: pnm: error parsing header line value")
		}
		u := uint32(v)
		*slot = &u
		return nil
	}
header:
	for {
		line, err := r.readLine()
		if err != nil {
			return pnmHeader{}, err
		}
		if len(line) == 0 {
			return pnmHeader{}, errors.New("photonimage: pnm: unexpected end of PNM header")
		}
		if line[0] == '#' {
			continue
		}
		for i := range len(line) {
			if line[i] >= 0x80 {
				return pnmHeader{}, errors.New("photonimage: pnm: non-ASCII line in PAM header")
			}
		}
		// `line.trim_left().split_at(line.find(char::is_whitespace).unwrap_or(line.len()))`: the index is a position in the untrimmed
		// line (it is 0 whenever trimming removed something).
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		at := strings.IndexFunc(line, unicode.IsSpace)
		if at < 0 {
			at = len(line)
		}
		identifier, rest := trimmed[:at], trimmed[at:]
		switch identifier {
		case "ENDHDR":
			break header
		case "HEIGHT":
			err = parseSingle(&height, rest)
		case "WIDTH":
			err = parseSingle(&width, rest)
		case "DEPTH":
			err = parseSingle(&depth, rest)
		case "MAXVAL":
			err = parseSingle(&maxval, rest)
		case "TUPLTYPE":
			value := strings.TrimSpace(rest)
			if tupltype != nil {
				value = *tupltype + " " + value
			}
			tupltype = &value
		default:
			err = errors.New("photonimage: pnm: unknown header line")
		}
		if err != nil {
			return pnmHeader{}, err
		}
	}
	if height == nil || width == nil || depth == nil || maxval == nil {
		return pnmHeader{}, errors.New("photonimage: pnm: missing header line")
	}
	tuple, err := pnmArbitraryTuple(*depth, *maxval, tupltype)
	if err != nil {
		return pnmHeader{}, err
	}
	return pnmHeader{width: *width, height: *height, maxSample: *maxval, binary: true, tuple: tuple}, nil
}

// pnmArbitraryTuple is ArbitraryHeader::tuple_type (decoder.rs:852-936).
func pnmArbitraryTuple(depth, maxval uint32, tupltype *string) (pnmTuple, error) {
	unsupported := errors.New("photonimage: pnm: unsupported colour type")
	if maxval == 0 {
		return 0, errors.New("photonimage: pnm: image MAXVAL is zero")
	}
	if tupltype == nil {
		switch depth {
		case 1:
			return pnmGrayU8, nil
		case 3:
			return pnmRGBU8, nil
		case 2, 4:
			return 0, unsupported
		}
		return 0, errors.New("photonimage: pnm: tuple type not recognized")
	}
	switch *tupltype {
	case "BLACKANDWHITE":
		if maxval == 1 && depth == 1 {
			return pnmBWBit, nil
		}
		return 0, errors.New("photonimage: pnm: invalid depth or maxval for tuple type BLACKANDWHITE")
	case "GRAYSCALE":
		switch {
		case depth == 1 && maxval <= 0xFF:
			return pnmGrayU8, nil
		case depth <= 1 && maxval <= 0xFFFF:
			return pnmGrayU16, nil
		}
		return 0, errors.New("photonimage: pnm: invalid depth or maxval for tuple type GRAYSCALE")
	case "RGB":
		switch {
		case depth == 3 && maxval <= 0xFF:
			return pnmRGBU8, nil
		case depth == 3 && maxval <= 0xFFFF:
			return pnmRGBU16, nil
		}
		return 0, errors.New("photonimage: pnm: invalid depth for tuple type RGB")
	}
	// BLACKANDWHITE_ALPHA, GRAYSCALE_ALPHA, RGB_ALPHA and custom tuple types.
	return 0, unsupported
}

// readImage is `PnmDecoder::read_image` and `read_samples`, then `to_rgba8`.
func (r *pnmReader) readImage(h pnmHeader) (*image.NRGBA, error) {
	count := uint64(h.width) * uint64(h.height) * h.tuple.channels() // samples
	size := h.tuple.sampleSize()
	var samples []byte // 8-bit samples, or 16-bit samples as little endian (the host's order)
	if count > 0 {
		var err error
		if samples, err = r.readSamples(h, count, size); err != nil {
			return nil, err
		}
	}
	scalePNMSamples(samples, h, size)

	out := newImageNRGBA(h.width, h.height)
	pix := out.Pix
	pixels := int(uint64(h.width) * uint64(h.height))
	switch h.tuple {
	case pnmPbmBit, pnmBWBit, pnmGrayU8:
		for i := range pixels {
			v := samples[i]
			pix[4*i], pix[4*i+1], pix[4*i+2], pix[4*i+3] = v, v, v, 255
		}
	case pnmGrayU16:
		for i := range pixels {
			v := downscale16(uint16(samples[2*i]) | uint16(samples[2*i+1])<<8)
			pix[4*i], pix[4*i+1], pix[4*i+2], pix[4*i+3] = v, v, v, 255
		}
	case pnmRGBU8:
		for i := range pixels {
			pix[4*i], pix[4*i+1], pix[4*i+2], pix[4*i+3] = samples[3*i], samples[3*i+1], samples[3*i+2], 255
		}
	case pnmRGBU16:
		for i := range pixels {
			for c := range 3 {
				pix[4*i+c] = downscale16(uint16(samples[6*i+2*c]) | uint16(samples[6*i+2*c+1])<<8)
			}
			pix[4*i+3] = 255
		}
	}
	return out, nil
}

// readSamples reads count samples of size bytes into a buffer of the colour type: binary samples are copied (16-bit ones turned to the
// host's byte order), ASCII samples are numbers, and a bitmap is the bits expanded and inverted.
func (r *pnmReader) readSamples(h pnmHeader, count uint64, size int) ([]byte, error) {
	rest := r.data[r.pos:]
	buf := func() []byte { return make([]byte, count*uint64(size)) }
	switch {
	case h.tuple == pnmPbmBit && h.binary:
		// PbmBit::bytelen and from_bytes: rows of bits padded to bytes.
		rowBytes := (uint64(h.width) + 7) / 8
		want := rowBytes * uint64(h.height)
		if uint64(len(rest)) < want {
			return nil, errPNMTooShort
		}
		out := buf()
		for y := range uint64(h.height) {
			row := rest[y*rowBytes : (y+1)*rowBytes]
			for x := range uint64(h.width) {
				if row[x/8]>>(7-x%8)&1 == 0 {
					out[y*uint64(h.width)+x] = 255
				}
			}
		}
		return out, nil
	case h.tuple == pnmPbmBit:
		// PbmBit::from_ascii: one `0` (white) or `1` (black) per sample, whitespace between them is skipped.
		if uint64(len(rest)) < count {
			return nil, errPNMTooShort
		}
		out := buf()
		pos := 0
		for i := range out {
			for {
				if pos >= len(rest) {
					return nil, errPNMTooShort
				}
				c := rest[pos]
				pos++
				if pnmIsSpace(c) {
					continue
				}
				switch c {
				case '0':
					out[i] = 255
				case '1':
					out[i] = 0
				default:
					return nil, errors.New("photonimage: pnm: unexpected character within sample raster")
				}
				break
			}
		}
		return out, nil
	case h.binary:
		need := count * uint64(size)
		if uint64(len(rest)) < need {
			return nil, errPNMTooShort
		}
		out := buf()
		copy(out, rest)
		if size == 2 {
			for i := 0; i < len(out); i += 2 {
				out[i], out[i+1] = out[i+1], out[i] // big endian to the host's little endian
			}
		}
		if h.tuple == pnmBWBit {
			for _, v := range out {
				if v > 1 {
					return nil, errors.New("photonimage: pnm: sample value outside of bounds")
				}
			}
		}
		return out, nil
	}
	// ASCII numbers: at least a character each.
	if uint64(len(rest)) < count {
		return nil, errPNMTooShort
	}
	out := buf()
	pos := 0
	bits := 8 * size
	for i := range count {
		// read_separated_ascii: skip separators, take up to the next one (consumed).
		for pos < len(rest) && pnmIsSpace(rest[pos]) {
			pos++
		}
		start := pos
		for pos < len(rest) && !pnmIsSpace(rest[pos]) {
			pos++
		}
		token := rest[start:pos]
		if pos < len(rest) {
			pos++
		}
		for _, c := range token {
			if c >= 0x80 {
				return nil, errors.New("photonimage: pnm: non-ASCII character where sample value was expected")
			}
		}
		v, ok := rustParseUint(string(token), bits)
		if !ok {
			return nil, errors.New("photonimage: pnm: error parsing sample")
		}
		if size == 1 {
			out[i] = byte(v)
		} else {
			out[2*i], out[2*i+1] = byte(v), byte(v>>8)
		}
	}
	return out, nil
}

// scalePNMSamples is the end of `read_samples` (decoder.rs:671-688): a maxval other than the type's maximum scales every sample
// by `target / maxval` in f32, rounded half away from zero and saturated.
func scalePNMSamples(samples []byte, h pnmHeader, size int) {
	target := uint32(1)<<(8*size) - 1
	if h.maxSample == target {
		return
	}
	factor := float32(target) / float32(h.maxSample)
	if size == 1 {
		for i, v := range samples {
			samples[i] = byte(saturate(float32(float32(v)*factor), 255))
		}
		return
	}
	for i := 0; i+1 < len(samples); i += 2 {
		v := uint16(samples[i]) | uint16(samples[i+1])<<8
		scaled := saturate(float32(float32(v)*factor), 65535)
		samples[i], samples[i+1] = byte(scaled), byte(scaled>>8)
	}
}

// saturate is `x.round() as T` for an unsigned T with the given maximum: Rust's float to integer cast saturates and sends NaN to zero.
func saturate(x float32, limit uint32) uint32 {
	r := math.Round(float64(x))
	switch {
	case r != r || r < 0:
		return 0
	case r > float64(limit):
		return limit
	}
	return uint32(r)
}
