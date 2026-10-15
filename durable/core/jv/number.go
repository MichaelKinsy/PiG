package jv

import "strconv"

// AppendNumber appends Number::toString of f (ECMA-262 6.1.6.1.20) for a finite
// f. Negative zero prints "0".
func AppendNumber(dst []byte, f float64) []byte {
	if f == 0 {
		return append(dst, '0')
	}
	if f < 0 {
		dst = append(dst, '-')
		f = -f
	}
	if f < 1e15 && f == float64(int64(f)) {
		// An integer below 10^15 prints as its digits, the form strconv's shortest-digits path ends in; it is a large function
		// that V8 compiles with TurboFan within the first turn, and ids, versions and counts are all such integers.
		var digits [16]byte
		i := len(digits)
		for n := int64(f); ; n /= 10 {
			i--
			digits[i] = byte('0' + n%10)
			if n < 10 {
				break
			}
		}
		return append(dst, digits[i:]...)
	}
	return appendFloat(dst, f)
}

// appendFloat is AppendNumber for a positive finite f that is not a small integer: the shortest round-trip digits laid out as
// Number::toString lays them out. It is apart so that AppendNumber stays small (V8 compiles a hot function whole).
//
//go:noinline
func appendFloat(dst []byte, f float64) []byte {
	var buf [32]byte
	// Shortest digits that round-trip: d.ddde±XX
	e := strconv.AppendFloat(buf[:0], f, 'e', -1, 64)
	var digits [24]byte
	nd := 0
	i := 0
	for ; i < len(e) && e[i] != 'e'; i++ {
		if e[i] != '.' {
			digits[nd] = e[i]
			nd++
		}
	}
	exp, _ := strconv.Atoi(string(e[i+1:]))
	n := exp + 1 // position of the decimal point relative to the digits
	k := nd
	d := digits[:nd]
	switch {
	case k <= n && n <= 21:
		dst = append(dst, d...)
		for j := 0; j < n-k; j++ {
			dst = append(dst, '0')
		}
	case 0 < n && n <= 21:
		dst = append(dst, d[:n]...)
		dst = append(dst, '.')
		dst = append(dst, d[n:]...)
	case -6 < n && n <= 0:
		dst = append(dst, '0', '.')
		for j := 0; j < -n; j++ {
			dst = append(dst, '0')
		}
		dst = append(dst, d...)
	default:
		x := n - 1
		dst = append(dst, d[0])
		if k > 1 {
			dst = append(dst, '.')
			dst = append(dst, d[1:]...)
		}
		dst = append(dst, 'e')
		if x < 0 {
			dst = append(dst, '-')
			x = -x
		} else {
			dst = append(dst, '+')
		}
		dst = strconv.AppendInt(dst, int64(x), 10)
	}
	return dst
}

// FormatNumber returns Number::toString of f for a finite f.
func FormatNumber(f float64) string { return string(AppendNumber(nil, f)) }
