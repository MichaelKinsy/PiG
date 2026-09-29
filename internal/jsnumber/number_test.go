package jsnumber

import (
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
