package jsnumber

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

// The expectations were printed by Node 24.19.0 (Pi 0.87.1's runtime) with Number(text) and String(number).
func TestParseMatchesNode(t *testing.T) {
	for _, tc := range [][2]string{
		{"", "0"}, {" ", "0"}, {"\u00a0 12 \u2028", "12"}, {"0x1F", "31"}, {"0X1f", "31"}, {"0o17", "15"}, {"0b101", "5"},
		{"0x", "NaN"}, {"-0x10", "NaN"}, {"+12", "12"}, {"-12.5e3", "-12500"}, {"1.", "1"}, {".5", "0.5"}, {".", "NaN"},
		{"1e", "NaN"}, {"1e+", "NaN"}, {"Infinity", "Infinity"}, {"-Infinity", "-Infinity"}, {"+Infinity", "Infinity"},
		{"infinity", "NaN"}, {"NaN", "NaN"}, {"1_000", "NaN"}, {"12abc", "NaN"}, {"0.1e-6", "1e-7"}, {"\ufeff7", "7"},
		{"1e400", "Infinity"}, {"0x1fffffffffffff1", "144115188075855860"}, {"00012", "12"}, {"-0", "-0"},
	} {
		got := Parse(tc[0])
		text := String(got)
		if got == 0 && math.Signbit(got) {
			text = "-0"
		}
		if text != tc[1] {
			t.Errorf("Parse(%q) = %s, Node %s", tc[0], text, tc[1])
		}
	}
}

func TestStringMatchesNode(t *testing.T) {
	for _, tc := range []struct {
		value float64
		want  string
	}{
		{0, "0"}, {math.Copysign(0, -1), "0"}, {1.5, "1.5"}, {1e21, "1e+21"}, {1e-7, "1e-7"}, {123456789012345680000, "123456789012345680000"},
		{0.30000000000000004, "0.30000000000000004"}, {-1e-7, "-1e-7"}, {5e-324, "5e-324"}, {math.MaxFloat64, "1.7976931348623157e+308"}, {1700000000000.25, "1700000000000.25"},
	} {
		if got := String(tc.value); got != tc.want {
			t.Errorf("String(%s) = %s, Node %s", strconv.FormatFloat(tc.value, 'g', -1, 64), got, tc.want)
		}
	}
	if got := string(JSON(math.Inf(1))); got != "null" {
		t.Errorf("JSON(Infinity) = %s, want null", got)
	}
}

func TestFromJSONAppliesToNumber(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want float64
	}{
		{"null", 0}, {"true", 1}, {"false", 0}, {`"  0x10 "`, 16}, {`""`, 0}, {"[]", 0}, {"[null]", 0}, {"[[7]]", 7}, {`["8"]`, 8},
		{"1.25", 1.25}, {"1e400", math.Inf(1)},
	} {
		if got := FromJSON([]byte(tc.raw)); got != tc.want {
			t.Errorf("FromJSON(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
	for _, raw := range []string{"", "{}", "[1,2]", `"abc"`, "[{}]", "[true]"} {
		if got := FromJSON([]byte(raw)); !math.IsNaN(got) {
			t.Errorf("FromJSON(%q) = %v, want NaN", raw, got)
		}
	}
}

// TestRoundMatchesMathRound pins Round to Node's Math.round (node -e 'console.log(Math.round(x))' for each input).
func TestRoundMatchesMathRound(t *testing.T) {
	negativeZero := math.Copysign(0, -1)
	cases := []struct{ in, want float64 }{
		{0.49999999999999994, 0},
		{-0.5, negativeZero},
		{-0.4, negativeZero},
		{0.4, 0},
		{0.5, 1},
		{1.5, 2},
		{2.5, 3},
		{-1.5, -1},
		{-2.5, -2},
		{-2.6, -3},
		{254.5, 255},
		{4503599627370497, 4503599627370497},
		{-4503599627370497, -4503599627370497},
		{negativeZero, negativeZero},
		{math.Inf(1), math.Inf(1)},
		{math.Inf(-1), math.Inf(-1)},
	}
	for _, c := range cases {
		got := Round(c.in)
		if got != c.want || math.Signbit(got) != math.Signbit(c.want) {
			t.Errorf("Round(%v) = %v (signbit %v), Math.round gives %v (signbit %v)", c.in, got, math.Signbit(got), c.want, math.Signbit(c.want))
		}
	}
	if !math.IsNaN(Round(math.NaN())) {
		t.Error("Round(NaN) is not NaN")
	}
}

// Safe integers print as their decimal digits, which is how encoding/json formats them too; the fast path must not change any result. Above 2^53 an integer prints in its shortest form, as V8 does: 2^55+24 is 36028797018963990.
func TestStringSafeIntegersMatchTheGeneralPath(t *testing.T) {
	for _, value := range []float64{1, -1, 7, 10, 99999, -100000, 1 << 31, 1<<53 - 1, 1 << 53, -(1 << 53), 123456789012345, 1<<53 + 2, 1<<55 + 24, -(1<<55 + 24)} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if got := String(value); got != string(encoded) {
			t.Errorf("String(%v) = %q, want %q", value, got, encoded)
		}
	}
	if got := String(1<<55 + 24); got != "36028797018963990" {
		t.Errorf("String(2^55+24) = %q, want V8's 36028797018963990", got)
	}
}

// Number.parseInt(text, 10): a decimal prefix after JS whitespace (U+FEFF and U+00A0 count, U+0085 does not), an optional sign, NaN without a digit.
func TestParseIntReadsADecimalPrefix(t *testing.T) {
	for _, tc := range []struct {
		text string
		want float64
	}{
		{"3", 3}, {"3beta", 3}, {"\ufeff\u00a03.9", 3}, {"+7x", 7}, {"-2", -2}, {"007", 7}, {"1e3", 1}, {"0x10", 0}, {"99999999999999999999", 1e20}, {"٣", math.NaN()}, {"\u00853", math.NaN()}, {"", math.NaN()}, {"-", math.NaN()}, {" . 5", math.NaN()},
	} {
		got := ParseInt(tc.text)
		if got != tc.want && !(math.IsNaN(got) && math.IsNaN(tc.want)) {
			t.Errorf("ParseInt(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
