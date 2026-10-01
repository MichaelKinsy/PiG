package jsstring

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// IsSpace reports whether r is JavaScript whitespace or a line terminator (the characters String.prototype.trim removes).
func IsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// Trim is String.prototype.trim.
func Trim(s string) string { return strings.TrimFunc(s, IsSpace) }

// TrimStart is String.prototype.trimStart.
func TrimStart(s string) string { return strings.TrimLeftFunc(s, IsSpace) }

// TrimEnd is String.prototype.trimEnd.
func TrimEnd(s string) string { return strings.TrimRightFunc(s, IsSpace) }

// ToFixed is Number.prototype.toFixed for 0 <= digits <= 100: the exact decimal value of x is rounded to digits
// places, and a tie takes the larger n, as the specification requires (Go's %.1f rounds ties to even).
func ToFixed(x float64, digits int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return numberSpecial(x)
	}
	negative := x < 0
	r := new(big.Rat).SetFloat64(math.Abs(x))
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	r.Mul(r, new(big.Rat).SetInt(scale))
	n := roundHalfUp(r)
	text := n.String()
	if digits > 0 {
		for len(text) <= digits {
			text = "0" + text
		}
		text = text[:len(text)-digits] + "." + text[len(text)-digits:]
	}
	if negative && n.Sign() != 0 {
		text = "-" + text
	}
	return text
}

// ToPrecision is Number.prototype.toPrecision for finite positive x and 1 <= precision <= 100.
func ToPrecision(x float64, precision int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return numberSpecial(x)
	}
	if x == 0 {
		return ToFixed(0, precision-1)
	}
	negative := x < 0
	exact := new(big.Rat).SetFloat64(math.Abs(x))
	e := int(math.Floor(math.Log10(math.Abs(x))))
	pow := func(exp int) *big.Rat {
		p := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(exp))), nil))
		if exp < 0 {
			p.Inv(p)
		}
		return p
	}
	// n = round(x / 10^(e-precision+1)), with n in [10^(precision-1), 10^precision).
	var n *big.Int
	for {
		n = roundHalfUp(new(big.Rat).Quo(exact, pow(e-precision+1)))
		low := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision-1)), nil)
		high := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
		switch {
		case n.Cmp(low) < 0:
			e--
			continue
		case n.Cmp(high) >= 0:
			e++
			continue
		}
		break
	}
	digits := n.String()
	var text string
	switch {
	case e < -6 || e >= precision:
		text = digits[:1]
		if precision > 1 {
			text += "." + digits[1:]
		}
		sign := "+"
		if e < 0 {
			sign = "-"
		}
		text += "e" + sign + strconv.Itoa(abs(e))
	case e == precision-1:
		text = digits
	case e >= 0:
		text = digits[:e+1] + "." + digits[e+1:]
	default:
		text = "0." + strings.Repeat("0", -(e+1)) + digits
	}
	if negative {
		text = "-" + text
	}
	return text
}

func roundHalfUp(r *big.Rat) *big.Int {
	twice := new(big.Rat).Mul(r, big.NewRat(2, 1))
	twice.Add(twice, big.NewRat(1, 1))
	num, den := twice.Num(), twice.Denom()
	q := new(big.Int).Quo(num, new(big.Int).Mul(den, big.NewInt(2)))
	return q
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func numberSpecial(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case x > 0:
		return "Infinity"
	}
	return "-Infinity"
}
