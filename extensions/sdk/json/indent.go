// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package json

import (
	"bytes"
	"math/bits"
	"strconv"
)

// HTMLEscape appends to dst the JSON-encoded src with <, >, &, U+2028 and U+2029
// characters inside string literals changed to \u003c, \u003e, \u0026, \u2028, \u2029
// so that the JSON will be safe to embed inside HTML <script> tags.
// For historical reasons, web browsers don't honor standard HTML
// escaping within <script> tags, so an alternative JSON encoding must be used.
func HTMLEscape(dst *bytes.Buffer, src []byte) {
	dst.Grow(len(src))
	dst.Write(appendHTMLEscape(dst.AvailableBuffer(), src))
}

func appendHTMLEscape(dst, src []byte) []byte {
	// The characters can only appear in string literals,
	// so just scan the string one byte at a time.
	start := 0
	for i, c := range src {
		if c == '<' || c == '>' || c == '&' {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			start = i + 1
		}
		// Convert U+2028 and U+2029 (E2 80 A8 and E2 80 A9).
		if c == 0xE2 && i+2 < len(src) && src[i+1] == 0x80 && src[i+2]&^1 == 0xA8 {
			dst = append(dst, src[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hex[src[i+2]&0xF])
			start = i + len("\u2029")
		}
	}
	return append(dst, src[start:]...)
}

// Compact appends to dst the JSON-encoded src with
// insignificant space characters elided.
func Compact(dst *bytes.Buffer, src []byte) error {
	dst.Grow(len(src))
	b := dst.AvailableBuffer()
	b, err := appendCompact(b, src, false)
	dst.Write(b)
	return err
}

func appendCompact(dst, src []byte, escape bool) ([]byte, error) {
	origLen := len(dst)
	scan := newScanner()
	defer freeScanner(scan)
	start := 0
	for i, c := range src {
		if escape && (c == '<' || c == '>' || c == '&') {
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			start = i + 1
		}
		// Convert U+2028 and U+2029 (E2 80 A8 and E2 80 A9).
		if escape && c == 0xE2 && i+2 < len(src) && src[i+1] == 0x80 && src[i+2]&^1 == 0xA8 {
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			dst = append(dst, '\\', 'u', '2', '0', '2', hex[src[i+2]&0xF])
			start = i + 3
		}
		v := scan.step(scan, c)
		if v >= scanSkipSpace {
			if v == scanError {
				break
			}
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			start = i + 1
		}
	}
	if scan.eof() == scanError {
		return dst[:origLen], scan.err
	}
	if start < len(src) {
		dst = append(dst, src[start:]...)
	}
	return dst, nil
}

// appendCompactJS is appendCompact without HTML escaping for the JSON.stringify mode. It also rewrites what JSON.stringify would not write
// and an encoder may have: inside a string the escapes \u003c, \u003C, \u003e, \u003E, \u0026, \u2028 and \u2029 become the characters, and
// outside one a negative number equal to zero becomes 0. Other escapes stay as written. String content is skipped eight bytes at a time.
// pig additive (lw-json): one pass where jsstring used to compact and then rewrite the whole output again.
func appendCompactJS(dst, src []byte) ([]byte, error) {
	origLen := len(dst)
	scan := newScanner()
	defer freeScanner(scan)
	start := 0
	inString := false
	// step feeds src[from:to] to the scanner; every byte there is part of one token, so none is skipped space.
	step := func(from, to int) bool {
		for j := from; j < to; j++ {
			if scan.step(scan, src[j]) == scanError {
				return false
			}
		}
		return true
	}
scan:
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			if c >= 0x20 && c != '"' && c != '\\' {
				// The scanner stays inside the string for every such byte, so they need no step.
				if i = stringContentEnd(src, i+1); i == len(src) {
					break
				}
				c = src[i]
			}
			switch c {
			case '"':
				inString = false
			case '\\':
				if i+6 <= len(src) {
					if replacement := jsUnescape(src[i : i+6]); replacement != "" {
						if !step(i, i+6) {
							break scan
						}
						dst = append(dst, src[start:i]...)
						dst = append(dst, replacement...)
						i += 5
						start = i + 1
						continue
					}
				}
				end := i + 2
				if i+1 < len(src) && src[i+1] == 'u' {
					end = i + 6
				}
				if !step(i, min(end, len(src))) {
					break scan
				}
				i = min(end, len(src)) - 1
				continue
			}
		} else {
			switch c {
			case '"':
				inString = true
			case '-':
				end := i + 1
				for end < len(src) && isNumberByte(src[end]) {
					end++
				}
				if value, err := strconv.ParseFloat(string(src[i:end]), 64); value == 0 && err == nil {
					// JSON.stringify writes negative zero as 0.
					if !step(i, end) {
						break scan
					}
					dst = append(dst, src[start:i]...)
					dst = append(dst, '0')
					i = end - 1
					start = end
					continue
				}
			}
		}
		v := scan.step(scan, c)
		if v >= scanSkipSpace {
			if v == scanError {
				break
			}
			if start < i {
				dst = append(dst, src[start:i]...)
			}
			start = i + 1
		}
	}
	if scan.eof() == scanError {
		return dst[:origLen], scan.err
	}
	if start < len(src) {
		dst = append(dst, src[start:]...)
	}
	return dst, nil
}

// jsUnescape is the character JSON.stringify writes for the six-byte escape, or "" when it writes the escape itself.
func jsUnescape(escape []byte) string {
	switch string(escape) {
	case `\u003c`, `\u003C`:
		return "<"
	case `\u003e`, `\u003E`:
		return ">"
	case `\u0026`:
		return "&"
	case `\u2028`:
		return "\u2028"
	case `\u2029`:
		return "\u2029"
	}
	// pig (LW-JSON-1): U+FFFD, in any hex case, is written raw as JSON.stringify writes it; a nested encoder writes it for ill-formed UTF-8.
	if bytes.EqualFold(escape[2:], []byte("fffd")) {
		return "\ufffd"
	}
	return ""
}

func isNumberByte(c byte) bool {
	return c >= '0' && c <= '9' || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

// stringContentEnd is the index of the first byte at or after i that ends a run of string content: a byte below 0x20, '"' or '\\' (or
// len(src)).
func stringContentEnd(src []byte, i int) int {
	for ; i+8 <= len(src); i += 8 {
		w := load8(src[i : i+8])
		// (w - 0x20 per lane) &^ w marks a lane below 0x20 and no lane from 0x80.
		if stops := (w-swarLSB*0x20)&^w | swarZero(w^(swarLSB*'"')) | swarZero(w^(swarLSB*'\\')); stops&swarMSB != 0 {
			return i + bits.TrailingZeros64(stops&swarMSB)>>3
		}
	}
	for ; i < len(src); i++ {
		if c := src[i]; c < 0x20 || c == '"' || c == '\\' {
			return i
		}
	}
	return i
}

func appendNewline(dst []byte, prefix, indent string, depth int) []byte {
	dst = append(dst, '\n')
	dst = append(dst, prefix...)
	for i := 0; i < depth; i++ {
		dst = append(dst, indent...)
	}
	return dst
}

// indentGrowthFactor specifies the growth factor of indenting JSON input.
// Empirically, the growth factor was measured to be between 1.4x to 1.8x
// for some set of compacted JSON with the indent being a single tab.
// Specify a growth factor slightly larger than what is observed
// to reduce probability of allocation in appendIndent.
// A factor no higher than 2 ensures that wasted space never exceeds 50%.
const indentGrowthFactor = 2

// Indent appends to dst an indented form of the JSON-encoded src.
// Each element in a JSON object or array begins on a new,
// indented line beginning with prefix followed by one or more
// copies of indent according to the indentation nesting.
// The data appended to dst does not begin with the prefix nor
// any indentation, to make it easier to embed inside other formatted JSON data.
// Although leading space characters (space, tab, carriage return, newline)
// at the beginning of src are dropped, trailing space characters
// at the end of src are preserved and copied to dst.
// For example, if src has no trailing spaces, neither will dst;
// if src ends in a trailing newline, so will dst.
func Indent(dst *bytes.Buffer, src []byte, prefix, indent string) error {
	dst.Grow(indentGrowthFactor * len(src))
	b := dst.AvailableBuffer()
	b, err := appendIndent(b, src, prefix, indent)
	dst.Write(b)
	return err
}

func appendIndent(dst, src []byte, prefix, indent string) ([]byte, error) {
	origLen := len(dst)
	scan := newScanner()
	defer freeScanner(scan)
	needIndent := false
	depth := 0
	for _, c := range src {
		scan.bytes++
		v := scan.step(scan, c)
		if v == scanSkipSpace {
			continue
		}
		if v == scanError {
			break
		}
		if needIndent && v != scanEndObject && v != scanEndArray {
			needIndent = false
			depth++
			dst = appendNewline(dst, prefix, indent, depth)
		}

		// Emit semantically uninteresting bytes
		// (in particular, punctuation in strings) unmodified.
		if v == scanContinue {
			dst = append(dst, c)
			continue
		}

		// Add spacing around real punctuation.
		switch c {
		case '{', '[':
			// delay indent so that empty object and array are formatted as {} and [].
			needIndent = true
			dst = append(dst, c)
		case ',':
			dst = append(dst, c)
			dst = appendNewline(dst, prefix, indent, depth)
		case ':':
			dst = append(dst, c, ' ')
		case '}', ']':
			if needIndent {
				// suppress indent in empty object/array
				needIndent = false
			} else {
				depth--
				dst = appendNewline(dst, prefix, indent, depth)
			}
			dst = append(dst, c)
		default:
			dst = append(dst, c)
		}
	}
	if scan.eof() == scanError {
		return dst[:origLen], scan.err
	}
	return dst, nil
}
