package tui

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// formatJSNumber is JSNumberString's formatting without the 0 to 255 table, the oracle the table must match.
func formatJSNumber(v float64) string {
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
	return mantissa + "e" + exp[:1] + strings.TrimLeft(exp[1:], "0")
}

// Every channel value 0 to 255 prints as JS String(n) does (decimal, no leading zeros), and as the formatting did
// before the table; JSNumberString answers them without allocating.
func TestJSNumberStringByteValues(t *testing.T) {
	for n := range 256 {
		v := float64(n)
		got := JSNumberString(v)
		if got != strconv.Itoa(n) || got != formatJSNumber(v) {
			t.Errorf("JSNumberString(%d) = %q, want %q", n, got, formatJSNumber(v))
		}
		if allocs := testing.AllocsPerRun(10, func() { JSNumberString(v) }); allocs != 0 {
			t.Errorf("JSNumberString(%d) allocates %.0f times", n, allocs)
		}
	}
}

// Values outside the table (fractions, out of range, -0, non-finite) keep JS formatting.
func TestJSNumberStringOutsideTheTable(t *testing.T) {
	cases := map[float64]string{
		math.Copysign(0, -1): "0", 0.5: "0.5", 254.5: "254.5", 255.00000000000003: "255.00000000000003", 256: "256",
		-1: "-1", -255: "-255", 1e-7: "1e-7", 1e21: "1e+21", 0.1: "0.1", math.Inf(1): "Infinity", math.Inf(-1): "-Infinity",
		math.NaN(): "NaN", math.Nextafter(0, 1): "5e-324", math.Nextafter(255, 0): "254.99999999999997", 4294967296: "4294967296",
	}
	for in, want := range cases {
		if got := JSNumberString(in); got != want || got != formatJSNumber(in) {
			t.Errorf("JSNumberString(%v) = %q, want %q (oracle %q)", in, got, want, formatJSNumber(in))
		}
	}
}

// A truecolor sequence for any byte color is the same as formatting each channel, and costs only the sequence itself.
func TestTrueColorAnsiByteChannels(t *testing.T) {
	for n := range 256 {
		c := RgbColorValue{R: float64(n), G: float64(255 - n), B: float64(n / 2)}
		want := "\x1b[38;2;" + formatJSNumber(c.R) + ";" + formatJSNumber(c.G) + ";" + formatJSNumber(c.B) + "m"
		if got := ForegroundAnsi(c, TerminalColorModeTrueColor); got != want {
			t.Fatalf("ForegroundAnsi(%v) = %q, want %q", c, got, want)
		}
	}
	c := RgbColorValue{R: 232, G: 244, B: 240}
	if allocs := testing.AllocsPerRun(100, func() { BackgroundAnsi(c, TerminalColorModeTrueColor) }); allocs > 1 {
		t.Errorf("BackgroundAnsi allocates %.0f times, want at most the sequence", allocs)
	}
}

// BenchmarkTrueColorAnsi formats the foreground and background of one half-block cell of the pig head.
func BenchmarkTrueColorAnsi(b *testing.B) {
	top, bottom := RgbColorValue{R: 24, G: 134, B: 111}, RgbColorValue{R: 232, G: 244, B: 240}
	b.ReportAllocs()
	for b.Loop() {
		ForegroundAnsi(top, TerminalColorModeTrueColor)
		BackgroundAnsi(bottom, TerminalColorModeTrueColor)
	}
}
