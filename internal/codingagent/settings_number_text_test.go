package codingagent

import (
	"math"
	"testing"
)

// String(number) of ECMAScript, which Pi's settings text uses for a configured number: -0 prints 0 and a magnitude of 1e21 or more, or below 1e-6, prints an exponent.
func TestSettingsNumberTextIsString(t *testing.T) {
	for _, c := range []struct {
		value float64
		want  string
	}{
		{0, "0"}, {math.Copysign(0, -1), "0"}, {5, "5"}, {-3, "-3"}, {1.5, "1.5"}, {0.000001, "0.000001"}, {1e-7, "1e-7"}, {1.5e-9, "1.5e-9"},
		{123456789012345680000, "123456789012345680000"}, {1e21, "1e+21"}, {-1.5e25, "-1.5e+25"}, {math.NaN(), "NaN"}, {math.Inf(1), "Infinity"}, {math.Inf(-1), "-Infinity"},
	} {
		if got := compactionNumberText(c.value); got != c.want {
			t.Errorf("compactionNumberText(%v) = %q, want %q", c.value, got, c.want)
		}
		if c.value >= 0 && !math.IsNaN(c.value) && !math.IsInf(c.value, 0) {
			if got := wheelScrollLinesLabel(WheelScrollLines{Lines: c.value}); got != c.want {
				t.Errorf("wheelScrollLinesLabel(%v) = %q, want %q", c.value, got, c.want)
			}
		}
	}
	if got := wheelScrollLinesLabel(WheelScrollLines{Lines: math.Copysign(0, -1)}); got != "0" {
		t.Errorf("wheelScrollLinesLabel(-0) = %q, want 0", got)
	}
	values := wheelScrollLinesValues(WheelScrollLines{Lines: 1e21})
	if values[len(values)-1] != "1e+21" {
		t.Errorf("wheelScrollLinesValues(1e21) = %v, want the count as 1e+21", values)
	}
}
