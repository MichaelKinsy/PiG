package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type systemThemeOracleColor struct {
	R float64 `json:"r"`
	G float64 `json:"g"`
	B float64 `json:"b"`
}

type systemThemeOracleInput struct {
	Foreground     *systemThemeOracleColor  `json:"foreground,omitempty"`
	Background     *systemThemeOracleColor  `json:"background,omitempty"`
	Palette        []systemThemeOracleColor `json:"palette,omitempty"`
	Saturation     *float64                 `json:"saturation,omitempty"`
	AppearanceHint string                   `json:"appearanceHint,omitempty"`
}

type systemThemeOracleResult struct {
	Colors     map[string]json.RawMessage `json:"colors"`
	Dim        []string                   `json:"dim"`
	Appearance string                     `json:"appearance"`
}

func (c *systemThemeOracleColor) rgb() *RgbColor {
	if c == nil {
		return nil
	}
	return &RgbColor{R: c.R, G: c.G, B: c.B}
}

// TestGenerateSystemThemeColorsMatchesUpstream runs the pinned upstream generator (system-theme.ts generateSystemThemeColors) over a seeded sweep of backgrounds, foregrounds, palettes and saturations, including mid-gray backgrounds that need relaxation, and compares every token.
func TestGenerateSystemThemeColorsMatchesUpstream(t *testing.T) {
	random := rand.New(rand.NewPCG(99, 991))
	channel := func() float64 { return float64(random.IntN(256)) }
	color := func() *systemThemeOracleColor { return &systemThemeOracleColor{channel(), channel(), channel()} }
	gray := func(value float64) *systemThemeOracleColor { return &systemThemeOracleColor{value, value, value} }
	palette := func() []systemThemeOracleColor {
		colors := make([]systemThemeOracleColor, 16)
		for i := range colors {
			colors[i] = *color()
		}
		return colors
	}
	var cases []systemThemeOracleInput
	for _, level := range []float64{0, 16, 40, 80, 100, 118, 128, 140, 160, 200, 235, 255} {
		cases = append(cases,
			systemThemeOracleInput{Background: gray(level)},
			systemThemeOracleInput{Background: gray(level), Foreground: gray(255 - level)},
			systemThemeOracleInput{Background: gray(level), Foreground: gray(255 - level), Palette: palette()},
		)
	}
	half := 0.5
	zero := 0.0
	for range 120 {
		input := systemThemeOracleInput{Background: color()}
		if random.IntN(2) == 0 {
			input.Foreground = color()
		}
		if random.IntN(2) == 0 {
			input.Palette = palette()
		}
		switch random.IntN(4) {
		case 0:
			input.Saturation = &half
		case 1:
			input.Saturation = &zero
		}
		cases = append(cases, input)
	}
	cases = append(cases,
		systemThemeOracleInput{}, systemThemeOracleInput{AppearanceHint: "light"}, systemThemeOracleInput{AppearanceHint: "dark", Saturation: &zero},
		systemThemeOracleInput{Foreground: color(), Palette: palette()},
	)

	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(t.TempDir(), "system-theme-cases.json")
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/system_theme_oracle.mjs", pigversion.UpstreamVersion, inputPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("upstream oracle: %v: %s", err, stderr.String())
	}
	var results []systemThemeOracleResult
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(cases) {
		t.Fatalf("upstream returned %d results for %d cases", len(results), len(cases))
	}
	for i, tc := range cases {
		var palette []RgbColor
		for _, c := range tc.Palette {
			palette = append(palette, *c.rgb())
		}
		got := GenerateSystemThemeColors(SystemThemeInput{
			Foreground: tc.Foreground.rgb(), Background: tc.Background.rgb(), Palette: palette,
			Saturation: tc.Saturation, AppearanceHint: TerminalTheme(tc.AppearanceHint),
		})
		want := results[i]
		label := fmt.Sprintf("case %d", i)
		if string(got.Appearance) != want.Appearance {
			t.Errorf("%s: appearance = %q, upstream %q", label, got.Appearance, want.Appearance)
		}
		if !slices.Equal(got.Dim, want.Dim) {
			t.Errorf("%s: dim = %v, upstream %v", label, got.Dim, want.Dim)
		}
		for token, raw := range want.Colors {
			var wantValue string
			if json.Unmarshal(raw, &wantValue) != nil {
				var index int
				if err := json.Unmarshal(raw, &index); err != nil {
					t.Fatalf("%s: token %s: %s", label, token, raw)
				}
				wantValue = fmt.Sprintf("#%d", index)
			}
			value := got.Colors[token]
			gotValue := value.Text
			if value.IsIndex {
				gotValue = fmt.Sprintf("#%d", value.Index)
			}
			if gotValue != wantValue {
				t.Errorf("%s (background %+v, foreground %+v, palette %v, saturation %v): %s = %q, upstream %q", label, tc.Background, tc.Foreground, len(tc.Palette) > 0, tc.Saturation, token, gotValue, wantValue)
				break
			}
		}
		if len(got.Colors) != len(want.Colors) {
			t.Errorf("%s: %d tokens, upstream %d", label, len(got.Colors), len(want.Colors))
		}
	}
}
