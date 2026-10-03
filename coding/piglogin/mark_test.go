package piglogin_test

import (
	"image/color"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

const reset = "\x1b[0m"

func rgb(t *testing.T, c color.RGBA) tui.Color {
	t.Helper()
	value, err := tui.NewRgbColor(float64(c.R), float64(c.G), float64(c.B))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// The text mark stands in for the art where it cannot be drawn. It is as wide as Pi's logo (pi-logo.ts, 4 cells), "PiG" is
// bold in the terminal's own foreground so a pale sprite stays legible on a light background, and only the period is colored.
func TestTextMarkIsALegibleOneLinePiG(t *testing.T) {
	if piglogin.TextMarkWidth != 4 {
		t.Fatalf("TextMarkWidth = %d, want Pi's logo width 4", piglogin.TextMarkWidth)
	}
	for _, variant := range piglogin.Variants {
		for _, mode := range []tui.TerminalColorMode{tui.TerminalColorModeTrueColor, tui.TerminalColorMode256} {
			mark := piglogin.TextMark(variant, mode)
			want := "\x1b[1mPiG" + tui.ForegroundAnsi(rgb(t, piglogin.LogoFor(variant).Period), mode) + "." + reset
			if mark != want {
				t.Errorf("%s/%s: text mark = %q, want %q", variant.ID, mode, mark, want)
			}
			if widthx.VisibleWidth(mark) != piglogin.TextMarkWidth {
				t.Errorf("%s/%s: text mark is %d cells wide", variant.ID, mode, widthx.VisibleWidth(mark))
			}
			if mode == tui.TerminalColorMode256 && strings.Contains(mark, "38;2;") {
				t.Errorf("%s: 256-color text mark contains a truecolor sequence", variant.ID)
			}
		}
	}
}

// The default text mark's period is the website wordmark's green accent, the pixel art's period color.
func TestDefaultTextMarkPeriodIsTheWordmarkAccent(t *testing.T) {
	mark := piglogin.TextMark(piglogin.Default(), tui.TerminalColorModeTrueColor)
	if !strings.Contains(mark, "\x1b[38;2;22;134;111m.") {
		t.Errorf("default text mark = %q, want the period in #16866F", mark)
	}
	if strings.Contains(mark, "38;2;72;163;129") {
		t.Errorf("default text mark = %q colors the letters with the pale-on-light-unsafe body color", mark)
	}
}

// Every sprite draws a distinct login: no two catalogue entries share their mascot pixels and palette.
func TestSpritesDiffer(t *testing.T) {
	seen := map[string]string{}
	for _, variant := range piglogin.Variants {
		definition := piglogin.LoginDefinitionFor(variant)
		var key strings.Builder
		key.WriteString(strings.Join(definition.Mascot, "\n"))
		for _, row := range append(append([]string(nil), definition.Hero...), definition.Mascot...) {
			for i := range len(row) {
				key.WriteString(definition.Palette[string(row[i])])
			}
		}
		if other, dup := seen[key.String()]; dup {
			t.Errorf("%s and %s draw the same login", variant.ID, other)
		}
		seen[key.String()] = variant.ID
	}
}

func TestSheriffWearsTheHat(t *testing.T) {
	sheriff, ok := piglogin.ByID("sheriff")
	if !ok {
		t.Fatal("no sheriff sprite")
	}
	definition := piglogin.LoginDefinitionFor(sheriff)
	if definition.Palette["H"] != "#6B4C3A" || !strings.Contains(definition.Mascot[3], "HHHHHHHHHHHHHHHH") {
		t.Errorf("the sheriff's login has no hat brim: palette H = %q, row 3 = %q", definition.Palette["H"], definition.Mascot[3])
	}
	if _, hat := piglogin.LoginDefinitionFor(piglogin.Default()).Palette["H"]; hat {
		t.Error("the default pig wears the sheriff's hat")
	}
}
