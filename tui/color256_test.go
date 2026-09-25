package tui

import (
	"image/color"
	"strings"
	"testing"
)

func TestRGBTo256(t *testing.T) {
	rgb := func(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 0xFF} }
	cases := []struct {
		name string
		in   color.RGBA
		want uint8
	}{
		{"black -> cube 16", rgb(0, 0, 0), 16},
		{"white -> cube 231", rgb(255, 255, 255), 231},
		{"pure red -> cube 196", rgb(255, 0, 0), 196},
		{"pure green -> cube 46", rgb(0, 255, 0), 46},
		{"pure blue -> cube 21", rgb(0, 0, 255), 21},
		{"mid gray -> grayscale ramp", rgb(128, 128, 128), 244},
	}
	for _, c := range cases {
		if got := RGBTo256(c.in); got != c.want {
			t.Errorf("%s: RGBTo256(%v) = %d, want %d", c.name, c.in, got, c.want)
		}
	}
}

func TestFgBgSeq_TrueColorVsDownsample(t *testing.T) {
	green := color.RGBA{0x16, 0xA3, 0x6A, 0xFF} // Green PiG accent
	if got := FgSeq(green, true); got != "\x1b[38;2;22;163;106m" {
		t.Errorf("truecolor fg = %q, want 24-bit 38;2 form", got)
	}
	got := FgSeq(green, false)
	if !strings.HasPrefix(got, "\x1b[38;5;") {
		t.Errorf("non-truecolor fg = %q, want 256-color 38;5 form", got)
	}
	// Background mirrors foreground on the 48; channel.
	if bg := BgSeq(green, false); !strings.HasPrefix(bg, "\x1b[48;5;") {
		t.Errorf("non-truecolor bg = %q, want 256-color 48;5 form", bg)
	}
	if bg := BgSeq(green, true); bg != "\x1b[48;2;22;163;106m" {
		t.Errorf("truecolor bg = %q, want 24-bit 48;2 form", bg)
	}
}
