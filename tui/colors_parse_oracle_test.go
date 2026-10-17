package tui

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
)

// The fixture is Pi 1.0.4's parseColor over accepted and rejected strings (testdata/colors-parse-oracle.mjs): JS \s whitespace
// (NBSP, U+2028, BOM, U+3000, VT, but not U+0085), case-insensitive names and units, exponents, bare dots, range and finiteness messages,
// and the 256-color and truecolor sequences of each accepted value.
func TestParseColorMatchesPiOracle(t *testing.T) {
	data, err := os.ReadFile("testdata/colors-parse-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Strings []struct {
			Input  string
			Ok     bool
			Error  string
			Kind   string
			RGB    struct{ R, G, B float64 }
			Hex    string
			Fg256  string
			FgTrue string
			Oklch  struct{ L, C, H float64 }
		}
		Numbers []struct {
			Input  string
			Ok     bool
			Error  string
			Index  int
			Fg256  string
			FgTrue string
		}
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, c := range oracle.Strings {
		color, err := ParseColor(c.Input)
		if !c.Ok {
			if err == nil || err.Error() != c.Error {
				t.Errorf("ParseColor(%q) = %v, %v; want error %q", c.Input, color, err, c.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseColor(%q) error %v, want %s", c.Input, err, c.Hex)
			continue
		}
		rgb := ColorToRgb(color)
		oklch := ColorToOklch(color)
		if ColorToHex(color) != c.Hex || ForegroundAnsi(color, TerminalColorMode256) != c.Fg256 || ForegroundAnsi(color, TerminalColorModeTrueColor) != c.FgTrue ||
			!near(rgb.R, c.RGB.R) || !near(rgb.G, c.RGB.G) || !near(rgb.B, c.RGB.B) || !near(oklch.L, c.Oklch.L) || !near(oklch.C, c.Oklch.C) || !near(oklch.H, c.Oklch.H) {
			t.Errorf("ParseColor(%q): hex %s fg256 %q fgTrue %q rgb %v oklch %v; want %s %q %q %v %v", c.Input, ColorToHex(color), ForegroundAnsi(color, TerminalColorMode256), ForegroundAnsi(color, TerminalColorModeTrueColor), rgb, oklch, c.Hex, c.Fg256, c.FgTrue, c.RGB, c.Oklch)
		}
	}
	for _, c := range oracle.Numbers {
		number, err := strconv.ParseFloat(c.Input, 64) // "NaN" and "Infinity" parse as JS prints them
		if c.Input == "Infinity" {
			number = math.Inf(1)
		} else if err != nil && c.Input != "NaN" {
			t.Fatal(err)
		}
		color, err := ParseColor(number)
		if !c.Ok {
			if err == nil || err.Error() != c.Error {
				t.Errorf("ParseColor(%s) = %v, %v; want error %q", c.Input, color, err, c.Error)
			}
			continue
		}
		if err != nil || ForegroundAnsi(color, TerminalColorMode256) != c.Fg256 || ForegroundAnsi(color, TerminalColorModeTrueColor) != c.FgTrue {
			t.Errorf("ParseColor(%s) = %v, %v", c.Input, color, err)
		}
		if intColor, intErr := ParseColor(int(number)); intErr != nil || intColor != color {
			t.Errorf("ParseColor(int %d) = %v, %v; want %v", int(number), intColor, intErr, color)
		}
	}
}
