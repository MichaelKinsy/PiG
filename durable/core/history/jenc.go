// SPDX-License-Identifier: MIT

package history

import (
	"math"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// The JSON text this package writes comes from package jv, the core's one JSON.stringify implementation. The
// helpers below adapt it to WTF-8 byte slices.

// appendString appends the JSON.stringify encoding of a WTF-8 string.
func appendString(dst, w []byte) []byte { return jv.AppendString(dst, string(w)) }

// appendRawString appends the JSON.stringify encoding of the string whose raw JSON bytes (escapes in place) are raw.
// A string without an escape is valid UTF-8 that JSON.stringify prints as it is.
func appendRawString(dst, raw []byte) []byte {
	if !hasEscape(raw) {
		dst = append(dst, '"')
		dst = append(dst, raw...)
		return append(dst, '"')
	}
	var buf [128]byte
	return appendString(dst, unescape(buf[:0], raw))
}

// appendNumberText appends ECMAScript Number::toString of the number whose JSON text is num. A number beyond the
// double range parses to Infinity, which JSON.stringify prints as null.
func appendNumberText(dst, num []byte) []byte {
	f, err := strconv.ParseFloat(string(num), 64)
	if err != nil && (math.IsInf(f, 0) || math.IsNaN(f)) {
		return append(dst, "null"...)
	}
	return jv.AppendNumber(dst, f)
}

// AppendNumber appends the JSON.stringify encoding of a finite float64.
func AppendNumber(dst []byte, f float64) ([]byte, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return dst, ErrNonFinite
	}
	return jv.AppendNumber(dst, f), nil
}

// Canonical appends to dst the text JSON.stringify(JSON.parse(src)) produces. It parses and prints with package jv:
// insertion order except that integer keys come first in ascending order, a repeated key keeps its first position and
// its last value, numbers print as ECMAScript prints them (beyond the double range: null), strings are re-escaped.
func Canonical(dst, src []byte) ([]byte, error) {
	v, err := jv.Parse(src)
	if err != nil {
		return dst, ErrSyntax
	}
	return jv.Append(dst, v), nil
}
