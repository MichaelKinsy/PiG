// Package jsparity writes JSON exactly as JavaScript's JSON.stringify writes it. Its primitives are those of durable/core/jv, which the TinyGo
// Durable core builds; this package is their one entry point for the rest of PiG and adds the forms jv does not need (non-finite numbers,
// 64-bit integers, booleans, three-state members).
//
// A string is a sequence of UTF-16 code units held as WTF-8: a lone surrogate is its three-byte generalized UTF-8 form, as jv defines.
package jsparity

import (
	"math"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// AppendString appends JSON.stringify(s) (ECMA-262 QuoteJSONString): the quote, the backslash and C0 controls are escaped (\b \f \n \r \t, other
// controls as \u00xx), a lone surrogate prints as \udxxx, a high surrogate directly followed by a low one prints as the character they form, and
// every other character prints as itself (no HTML escaping; U+2028, U+2029 and U+007F literal). A byte that is not WTF-8 prints as U+FFFD, the
// character a decoded JavaScript string would hold.
//
// It is jv.AppendString, which finds runs that need no escape eight bytes at a time and copies them whole.
func AppendString(dst []byte, s string) []byte { return jv.AppendString(dst, s) }

// AppendNumber appends JSON.stringify(f): Number::toString for a finite value (shortest round trip; exponent form from 1e21 and below 1e-6; -0
// prints 0) and null for NaN and the infinities.
func AppendNumber(dst []byte, f float64) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(dst, "null"...)
	}
	return jv.AppendNumber(dst, f)
}

// maxSafeInteger is Number.MAX_SAFE_INTEGER, 2^53-1.
const maxSafeInteger = 1<<53 - 1

// AppendInt appends JSON.stringify of the number JavaScript holds for n: n itself when |n| is at most 2^53-1, and otherwise the nearest double.
func AppendInt(dst []byte, n int64) []byte {
	if -maxSafeInteger <= n && n <= maxSafeInteger {
		return strconv.AppendInt(dst, n, 10)
	}
	return AppendNumber(dst, float64(n))
}

// AppendBool appends JSON.stringify(b).
func AppendBool(dst []byte, b bool) []byte {
	if b {
		return append(dst, "true"...)
	}
	return append(dst, "false"...)
}

// IsIndexKey reports whether key is a canonical ECMAScript array index (a decimal integer without leading zeros in 0..2^32-2). An object
// enumerates those keys first, in ascending order, before its other keys in insertion order.
func IsIndexKey(key string) bool { return jv.IsIndexKey(key) }
