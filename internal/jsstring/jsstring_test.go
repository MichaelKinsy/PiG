package jsstring_test

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Expected values were measured with Node v24 (Number.prototype.toFixed and toPrecision).
func TestToFixedMatchesNode(t *testing.T) {
	for _, c := range []struct {
		x      float64
		digits int
		want   string
	}{
		{0.25, 1, "0.3"}, {0.15, 1, "0.1"}, {0.05, 1, "0.1"}, {0.35, 1, "0.3"}, {1.005, 2, "1.00"}, {0.125, 2, "0.13"},
		{0, 1, "0.0"}, {12.345, 0, "12"}, {0.5, 0, "1"}, {2.5, 0, "3"}, {0.02, 2, "0.02"}, {123456.789, 1, "123456.8"}, {-0.25, 1, "-0.3"},
		// A negative value that rounds to zero keeps its sign; negative zero does not have one (x < 0 is false).
		{-0.04, 1, "-0.0"}, {-0.00001, 4, "-0.0000"}, {math.Copysign(0, -1), 1, "0.0"},
		// A magnitude of at least 1e21 is Number::toString(x); a smaller one keeps fixed notation.
		{1e21, 1, "1e+21"}, {-1e21, 2, "-1e+21"}, {1.1034710812568662e+22, 1, "1.1034710812568662e+22"}, {123e30, 0, "1.23e+32"},
		{999999999999999900000, 1, "999999999999999868928.0"},
	} {
		if got := jsstring.ToFixed(c.x, c.digits); got != c.want {
			t.Errorf("ToFixed(%v, %d) = %q, want %q", c.x, c.digits, got, c.want)
		}
	}
}

func TestToPrecisionMatchesNode(t *testing.T) {
	for _, c := range []struct {
		x         float64
		precision int
		want      string
	}{
		{0.000012936, 2, "0.000013"}, {0.0099, 2, "0.0099"}, {0.00123456, 2, "0.0012"}, {0.00000012, 2, "1.2e-7"},
		{0.0000012, 2, "0.0000012"}, {0.5, 2, "0.50"}, {0.00999, 2, "0.010"}, {123456, 2, "1.2e+5"}, {99.99, 2, "1.0e+2"}, {7, 2, "7.0"},
	} {
		if got := jsstring.ToPrecision(c.x, c.precision); got != c.want {
			t.Errorf("ToPrecision(%v, %d) = %q, want %q", c.x, c.precision, got, c.want)
		}
	}
}

// TrimEnd removes JavaScript whitespace by character: a trailing character whose last UTF-8 byte is 0xA0 or 0x85 (the
// Latin-1 values of NBSP and NEL) is not whitespace and stays whole.
func TestTrimEndRemovesJavaScriptWhitespaceByCharacter(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a\u0920 \u00a0\n\t\u2028\ufeff", "a\u0920"},
		{"x\u0105", "x\u0105"},
		{"x\u0085", "x\u0085"},
		{"  ", ""},
	} {
		if got := jsstring.TrimEnd(c.in); got != c.want {
			t.Errorf("TrimEnd(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
