// Package utf8stream decodes UTF-8 chunks the way Node does: TextDecoder with { stream: true } and StringDecoder.
package utf8stream

import (
	"bytes"
	"unicode/utf8"
)

// Decoder mirrors the WHATWG UTF-8 decoder behind TextDecoder with
// { stream: true }: an incomplete trailing sequence waits for the next chunk,
// and each maximal invalid subpart becomes one U+FFFD.
//
// With StripBOM set it also mirrors TextDecoder's default ignoreBOM: false,
// dropping a U+FEFF that is the stream's first code point, even when its
// bytes arrive split across chunks. Buffer.toString (file reads) keeps the
// BOM, so those callers leave StripBOM unset.
type Decoder struct {
	StripBOM    bool
	started     bool
	codePoint   rune
	bytesSeen   int
	bytesNeeded int
	lower       byte
	upper       byte
}

// Decode returns the decoding of data. Well-formed UTF-8 decodes to itself, so it is copied once; anything else runs the decoder.
func (d *Decoder) Decode(data []byte, stream bool) string {
	if d.bytesNeeded == 0 && utf8.Valid(data) {
		if !d.started && len(data) > 0 {
			d.started = true
			if d.StripBOM && bytes.HasPrefix(data, utf8BOM) {
				data = data[len(utf8BOM):]
			}
		}
		return string(data)
	}
	return string(d.AppendDecode(make([]byte, 0, len(data)+utf8.UTFMax), data, stream))
}

var utf8BOM = []byte("\uFEFF")

// AppendDecode appends the decoding of data to dst. Between characters, the well-formed prefix of a chunk that ends at a character boundary is appended
// as it is; the decoder walks only what is left (an incomplete last character, or a chunk with an invalid sequence).
//
//perf:hot
func (d *Decoder) AppendDecode(dst, data []byte, stream bool) []byte {
	if d.lower == 0 {
		d.lower, d.upper = 0x80, 0xBF
	}
	if d.started && d.bytesNeeded == 0 {
		if whole := len(data) - incompleteSuffix(data); utf8.Valid(data[:whole]) {
			dst = append(dst, data[:whole]...)
			data = data[whole:]
		}
	}
	for i := 0; i < len(data); i++ {
		c := data[i]
		if d.bytesNeeded == 0 {
			if d.started && c <= 0x7F {
				// A run of ASCII after the first code point is copied whole: no BOM check applies and each byte is its own code point.
				run := i + 1
				for run < len(data) && data[run] <= 0x7F {
					run++
				}
				dst = append(dst, data[i:run]...)
				i = run - 1
				continue
			}
			switch {
			case c <= 0x7F:
				dst = d.emit(dst, rune(c))
			case c >= 0xC2 && c <= 0xDF:
				d.bytesNeeded, d.codePoint = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				switch c {
				case 0xE0:
					d.lower = 0xA0
				case 0xED:
					d.upper = 0x9F
				}
				d.bytesNeeded, d.codePoint = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				switch c {
				case 0xF0:
					d.lower = 0x90
				case 0xF4:
					d.upper = 0x8F
				}
				d.bytesNeeded, d.codePoint = 3, rune(c&0x07)
			default:
				dst = d.emit(dst, '�')
			}
			continue
		}
		if c < d.lower || c > d.upper {
			d.reset()
			dst = d.emit(dst, '�')
			i-- // reprocess this byte as the start of a new sequence
			continue
		}
		d.lower, d.upper = 0x80, 0xBF
		d.codePoint = d.codePoint<<6 | rune(c&0x3F)
		d.bytesSeen++
		if d.bytesSeen == d.bytesNeeded {
			dst = d.emit(dst, d.codePoint)
			d.reset()
		}
	}
	if !stream && d.bytesNeeded != 0 {
		d.reset()
		dst = d.emit(dst, '�')
	}
	return dst
}

// emit appends a decoded code point, dropping a BOM that starts the stream when StripBOM is set.
func (d *Decoder) emit(dst []byte, r rune) []byte {
	if !d.started {
		d.started = true
		if d.StripBOM && r == '\uFEFF' {
			return dst
		}
	}
	return utf8.AppendRune(dst, r)
}

// incompleteSuffix is the length of the multi-byte character data ends inside of, 0 when data ends at a character boundary. A malformed ending counts
// as complete: the caller then finds the data malformed and decodes it in full.
func incompleteSuffix(data []byte) int {
	for back := 1; back <= utf8.UTFMax-1 && back <= len(data); back++ {
		c := data[len(data)-back]
		switch {
		case c < 0x80:
			return 0
		case c >= 0xC0:
			if back < sequenceLength(c) {
				return back
			}
			return 0
		}
	}
	return 0
}

// sequenceLength is the byte length a UTF-8 lead byte announces.
func sequenceLength(lead byte) int {
	switch {
	case lead >= 0xF0:
		return 4
	case lead >= 0xE0:
		return 3
	default:
		return 2
	}
}

func (d *Decoder) reset() {
	d.codePoint, d.bytesSeen, d.bytesNeeded = 0, 0, 0
	d.lower, d.upper = 0x80, 0xBF
}
