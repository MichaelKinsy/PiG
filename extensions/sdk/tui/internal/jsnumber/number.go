// Package jsnumber holds the two JavaScript Number operations the pi-tui ports need.
package jsnumber

import (
	"math"
	"strconv"
	"strings"
)

// Round is Math.round: the integral value nearest value, with halves toward +Infinity. A value in [-0.5, 0) rounds to -0, one in
// (0, 0.5) to +0, and NaN, infinities and zeros return unchanged.
func Round(value float64) float64 {
	switch {
	case math.IsNaN(value) || math.IsInf(value, 0) || value == 0:
		return value
	case value > 0 && value < 0.5:
		return 0
	case value < 0 && value >= -0.5:
		return math.Copysign(0, -1)
	}
	rounded := math.Floor(value)
	if value-rounded >= 0.5 {
		rounded++
	}
	return rounded
}

// String is Number.prototype.toString(): the shortest round-trip digits, in plain notation for magnitudes in [1e-6, 1e21) and exponent
// notation (for example "1e-7", "1e+21") outside it.
func String(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	case v == 0:
		return "0"
	}
	abs := math.Abs(v)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	s := strconv.FormatFloat(v, 'e', -1, 64)
	mantissa, exp, _ := strings.Cut(s, "e")
	sign := exp[:1]
	digits := strings.TrimLeft(exp[1:], "0")
	return mantissa + "e" + sign + digits
}
