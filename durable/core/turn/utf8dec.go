// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import "unicode/utf8"

// utf8Decoder is the WHATWG UTF-8 decoder with replacement, in stream mode (TextDecoder with ignoreBOM): a byte sequence
// that ends a chunk waits for the next one, an invalid or truncated sequence becomes one U+FFFD per maximal subpart, and a
// byte-order mark is kept as text.
type utf8Decoder struct {
	point        rune
	needed, seen int
	lower, upper byte
}

// decode returns the text of chunk; an incomplete sequence at its end stays in the decoder.
func (d *utf8Decoder) decode(chunk string) string {
	out := make([]byte, 0, len(chunk))
	for i := 0; i < len(chunk); i++ {
		b := chunk[i]
		if d.needed == 0 {
			switch {
			case b <= 0x7f:
				out = append(out, b)
			case b >= 0xc2 && b <= 0xdf:
				d.needed, d.point, d.lower, d.upper = 1, rune(b&0x1f), 0x80, 0xbf
			case b >= 0xe0 && b <= 0xef:
				d.needed, d.point, d.lower, d.upper = 2, rune(b&0xf), 0x80, 0xbf
				if b == 0xe0 {
					d.lower = 0xa0
				}
				if b == 0xed {
					d.upper = 0x9f
				}
			case b >= 0xf0 && b <= 0xf4:
				d.needed, d.point, d.lower, d.upper = 3, rune(b&0x7), 0x80, 0xbf
				if b == 0xf0 {
					d.lower = 0x90
				}
				if b == 0xf4 {
					d.upper = 0x8f
				}
			default:
				out = utf8.AppendRune(out, utf8.RuneError)
			}
			continue
		}
		if b < d.lower || b > d.upper {
			d.needed, d.seen, d.point = 0, 0, 0
			out = utf8.AppendRune(out, utf8.RuneError)
			i-- // the byte is processed again as the start of a sequence
			continue
		}
		d.lower, d.upper = 0x80, 0xbf
		d.point = d.point<<6 | rune(b&0x3f)
		d.seen++
		if d.seen == d.needed {
			out = utf8.AppendRune(out, d.point)
			d.needed, d.seen, d.point = 0, 0, 0
		}
	}
	return string(out)
}

// flush ends the stream: an incomplete sequence becomes one replacement character.
func (d *utf8Decoder) flush() string {
	if d.needed == 0 {
		return ""
	}
	d.needed, d.seen, d.point = 0, 0, 0
	return "\ufffd"
}

// decodeLossy decodes a complete byte string, replacing invalid sequences.
func decodeLossy(s string) string {
	var d utf8Decoder
	return d.decode(s) + d.flush()
}
