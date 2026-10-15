package jsstring

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// IsSpace reports whether r is JavaScript whitespace or a line terminator (the characters String.prototype.trim removes).
func IsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// SpaceClass is the body of an RE2 character class that matches what JavaScript's \s matches: the characters IsSpace reports. RE2's own \s is
// ASCII only. Use it inside brackets, for example `[` + SpaceClass + `,]`.
const SpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// Trim is String.prototype.trim.
func Trim(s string) string { return strings.TrimFunc(s, IsSpace) }

// TrimStart is String.prototype.trimStart.
func TrimStart(s string) string { return strings.TrimLeftFunc(s, IsSpace) }

// TrimEnd is String.prototype.trimEnd.
func TrimEnd(s string) string { return strings.TrimRightFunc(s, IsSpace) }

// ToFixed is Number.prototype.toFixed for 0 <= digits <= 100: the exact decimal value of x is rounded to digits
// places, and a tie takes the larger n, as the specification requires (Go's %.1f rounds ties to even). A magnitude of
// at least 1e21 formats as Number::toString, as step 7 of the specification requires.
func ToFixed(x float64, digits int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return numberSpecial(x)
	}
	if math.Abs(x) >= 1e21 {
		return jsnumber.String(x)
	}
	if text, ok := toFixedUnambiguous(x, digits); ok {
		return text
	}
	return toFixedExact(x, digits)
}

// toFixedUnambiguous formats x with strconv, which rounds the exact decimal value to nearest and ties to even. That is toFixed's result unless the exact value is a tie, and a tie has exactly
// digits+1 fractional digits ending in 5, so a correctly rounded digits+1 expansion that does not end in 5 rules one out. ok is false when it does, and the caller takes the exact path.
func toFixedUnambiguous(x float64, digits int) (string, bool) {
	var scratch [48]byte
	longer := strconv.AppendFloat(scratch[:0], math.Abs(x), 'f', digits+1, 64)
	if longer[len(longer)-1] == '5' {
		return "", false
	}
	var buf [48]byte
	text := buf[:0]
	if x < 0 {
		// Step 4 of the specification takes the sign before rounding, so -0.04 is "-0.0".
		text = append(text, '-')
	}
	return string(strconv.AppendFloat(text, math.Abs(x), 'f', digits, 64)), true
}

// toFixedExact is ToFixed over big.Rat arithmetic: the exact decimal value of x rounded half up.
func toFixedExact(x float64, digits int) string {
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
	if negative {
		// Step 4 of the specification takes the sign before rounding, so -0.04 is "-0.0".
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
	// math.Log10 rounds to a power of ten for a value just below one (1e-11 is a little under 10^-11), so e is settled against the exact value: 10^e <= x < 10^(e+1). Rounding to the precision may then carry to 10^precision, which moves e up by one below.
	for exact.Cmp(pow(e)) < 0 {
		e--
	}
	for exact.Cmp(pow(e+1)) >= 0 {
		e++
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
