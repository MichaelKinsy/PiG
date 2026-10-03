package tui

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// The fixture is upstream's own colors.ts run over seeded random inputs (testdata/colors-oracle.mjs, upstream 0.99.1). Integer channels and ANSI sequences must match exactly; unrounded OKLCH and OKHSL channels may differ by floating-point noise, because Go's math.Pow and math.Cbrt are not V8's fdlibm ports.
type colorsOracle struct {
	OklchToRgb []struct {
		L, C, H float64
		RGB     struct{ R, G, B float64 } `json:"rgb"`
	} `json:"oklchToRgb"`
	OkhslToRgb []struct {
		H, S, L float64
		RGB     struct{ R, G, B float64 } `json:"rgb"`
	} `json:"okhslToRgb"`
	HexToOkhsl []struct {
		Hex   string
		Okhsl struct{ H, S, L float64 } `json:"okhsl"`
	} `json:"hexToOkhsl"`
	HexToOklch []struct {
		Hex   string
		Oklch struct{ L, C, H float64 } `json:"oklch"`
	} `json:"hexToOklch"`
	Mixes []struct {
		First, Second string
		Amount        float64
		Space         string
		Hex           string
		RGB           struct{ R, G, B float64 } `json:"rgb"`
	} `json:"mixes"`
	Ansi []struct {
		Hex, Fg256, Bg256, FgTrue string
	} `json:"ansi"`
}

func loadColorsOracle(t *testing.T) colorsOracle {
	t.Helper()
	data, err := os.ReadFile("testdata/colors-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle colorsOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9 }

func TestColorsMatchUpstreamOracle(t *testing.T) {
	oracle := loadColorsOracle(t)
	t.Run("oklch to sRGB", func(t *testing.T) {
		for _, c := range oracle.OklchToRgb {
			got := ColorToRgb(oklchOK(t, c.L, c.C, c.H))
			if got != (RgbColor{R: c.RGB.R, G: c.RGB.G, B: c.RGB.B}) {
				t.Errorf("oklch(%v %v %v) = %v, want %v", c.L, c.C, c.H, got, c.RGB)
			}
		}
	})
	t.Run("okhsl to sRGB", func(t *testing.T) {
		for _, c := range oracle.OkhslToRgb {
			got := ColorToRgb(okhslOK(t, c.H, c.S, c.L))
			if got != (RgbColor{R: c.RGB.R, G: c.RGB.G, B: c.RGB.B}) {
				t.Errorf("okhsl(%v %v %v) = %v, want %v", c.H, c.S, c.L, got, c.RGB)
			}
		}
	})
	t.Run("hex to okhsl", func(t *testing.T) {
		for _, c := range oracle.HexToOkhsl {
			got := ColorToOkhsl(parseColorOK(t, c.Hex))
			if !near(got.H, c.Okhsl.H) || !near(got.S, c.Okhsl.S) || !near(got.L, c.Okhsl.L) {
				t.Errorf("%s = %+v, want %+v", c.Hex, got, c.Okhsl)
			}
		}
	})
	t.Run("hex to oklch", func(t *testing.T) {
		for _, c := range oracle.HexToOklch {
			got := ColorToOklch(parseColorOK(t, c.Hex))
			// A gray's hue is the arctangent of rounding noise around zero, so it is compared only when the chroma is real.
			if !near(got.L, c.Oklch.L) || !near(got.C, c.Oklch.C) || (c.Oklch.C > 1e-9 && !near(got.H, c.Oklch.H)) {
				t.Errorf("%s = %+v, want %+v", c.Hex, got, c.Oklch)
			}
		}
	})
	t.Run("mix", func(t *testing.T) {
		for _, c := range oracle.Mixes {
			mixed, err := MixColors(parseColorOK(t, c.First), parseColorOK(t, c.Second), c.Amount, ColorMixSpace(c.Space))
			if err != nil {
				t.Fatal(err)
			}
			if got := ColorToHex(mixed); got != c.Hex {
				t.Errorf("mix(%s, %s, %v, %s) = %s, want %s", c.First, c.Second, c.Amount, c.Space, got, c.Hex)
			}
			if got := ColorToRgb(mixed); got != (RgbColor{R: c.RGB.R, G: c.RGB.G, B: c.RGB.B}) {
				t.Errorf("mix(%s, %s, %v, %s) rgb = %v, want %v", c.First, c.Second, c.Amount, c.Space, got, c.RGB)
			}
		}
	})
	t.Run("ANSI sequences", func(t *testing.T) {
		for _, c := range oracle.Ansi {
			color := parseColorOK(t, c.Hex)
			if got := ForegroundAnsi(color, TerminalColorMode256); got != c.Fg256 {
				t.Errorf("%s fg 256 = %q, want %q", c.Hex, got, c.Fg256)
			}
			if got := BackgroundAnsi(color, TerminalColorMode256); got != c.Bg256 {
				t.Errorf("%s bg 256 = %q, want %q", c.Hex, got, c.Bg256)
			}
			if got := ForegroundAnsi(color, TerminalColorModeTrueColor); got != c.FgTrue {
				t.Errorf("%s fg truecolor = %q, want %q", c.Hex, got, c.FgTrue)
			}
		}
	})
}
