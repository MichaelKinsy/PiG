package tui

import (
	"testing"
	"time"
)

// Expected values are the output of Pi 1.5.1 provider-brand.ts radiusShimmer/openRouterGlow (with pi-tui mixColors and foregroundAnsi) at each elapsed time,
// computed by running the pinned Pi build.
type brandCase struct {
	elapsed time.Duration
	mode    TerminalColorMode
	dark    bool
	want    string
}

var radiusShimmerCases = []brandCase{
	{0 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;77;154;191mR\x1b[38;2;85;162;194ma\x1b[38;2;104;179;201md\x1b[38;2;123;196;207mi\x1b[38;2;131;204;210mu\x1b[38;2;148;202;191ms\x1b[39m"},
	{0 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;67mR\x1b[38;5;73ma\x1b[38;5;74md\x1b[38;5;116mi\x1b[38;5;116mu\x1b[38;5;115ms\x1b[39m"},
	{130 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;117;152;176mR\x1b[38;2;80;154;190ma\x1b[38;2;81;158;193md\x1b[38;2;98;173;198mi\x1b[38;2;118;192;205mu\x1b[38;2;130;203;210ms\x1b[39m"},
	{130 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;103mR\x1b[38;5;67ma\x1b[38;5;73md\x1b[38;5;74mi\x1b[38;5;110mu\x1b[38;5;116ms\x1b[39m"},
	{700 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;241;183;94mR\x1b[38;2;241;167;109ma\x1b[38;2;240;151;123md\x1b[38;2;240;144;130mi\x1b[38;2;215;146;140mu\x1b[38;2;159;149;161ms\x1b[39m"},
	{700 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;215mR\x1b[38;5;215ma\x1b[38;5;210md\x1b[38;5;210mi\x1b[38;5;174mu\x1b[38;5;139ms\x1b[39m"},
	{1200 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;131;204;210mR\x1b[38;2;148;202;191ma\x1b[38;2;186;197;149md\x1b[38;2;224;192;106mi\x1b[38;2;241;190;87mu\x1b[38;2;241;183;94ms\x1b[39m"},
	{1200 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;116mR\x1b[38;5;115ma\x1b[38;5;150md\x1b[38;5;179mi\x1b[38;5;215mu\x1b[38;5;215ms\x1b[39m"},
	{1800 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;159;149;161mR\x1b[38;2;102;152;181ma\x1b[38;2;77;154;191md\x1b[38;2;85;162;194mi\x1b[38;2;104;179;201mu\x1b[38;2;123;196;207ms\x1b[39m"},
	{1800 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;139mR\x1b[38;5;67ma\x1b[38;5;67md\x1b[38;5;73mi\x1b[38;5;74mu\x1b[38;5;116ms\x1b[39m"},
	{2400 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;241;190;87mR\x1b[38;2;241;183;94ma\x1b[38;2;241;167;109md\x1b[38;2;240;151;123mi\x1b[38;2;240;144;130mu\x1b[38;2;215;146;140ms\x1b[39m"},
	{2400 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;215mR\x1b[38;5;215ma\x1b[38;5;215md\x1b[38;5;210mi\x1b[38;5;210mu\x1b[38;5;174ms\x1b[39m"},
	{5000 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;159;149;161mR\x1b[38;2;102;152;181ma\x1b[38;2;77;154;191md\x1b[38;2;85;162;194mi\x1b[38;2;104;179;201mu\x1b[38;2;123;196;207ms\x1b[39m"},
	{5000 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;139mR\x1b[38;5;67ma\x1b[38;5;67md\x1b[38;5;73mi\x1b[38;5;74mu\x1b[38;5;116ms\x1b[39m"},
}
var openRouterGlowCases = []brandCase{
	{0 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;198;242;67mOpenRouter\x1b[39m"},
	{0 * time.Millisecond, TerminalColorModeTrueColor, false, "\x1b[38;2;93;50;225mOpenRouter\x1b[39m"},
	{0 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;191mOpenRouter\x1b[39m"},
	{0 * time.Millisecond, TerminalColorMode256, false, "\x1b[38;5;62mOpenRouter\x1b[39m"},
	{130 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;199;242;72mOpenRouter\x1b[39m"},
	{130 * time.Millisecond, TerminalColorModeTrueColor, false, "\x1b[38;2;94;52;226mOpenRouter\x1b[39m"},
	{130 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;191mOpenRouter\x1b[39m"},
	{130 * time.Millisecond, TerminalColorMode256, false, "\x1b[38;5;62mOpenRouter\x1b[39m"},
	{700 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;225;250;141mOpenRouter\x1b[39m"},
	{700 * time.Millisecond, TerminalColorModeTrueColor, false, "\x1b[38;2;124;88;244mOpenRouter\x1b[39m"},
	{700 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;192mOpenRouter\x1b[39m"},
	{700 * time.Millisecond, TerminalColorMode256, false, "\x1b[38;5;99mOpenRouter\x1b[39m"},
	{1200 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;239;255;176mOpenRouter\x1b[39m"},
	{1200 * time.Millisecond, TerminalColorModeTrueColor, false, "\x1b[38;2;143;107;255mOpenRouter\x1b[39m"},
	{1200 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;229mOpenRouter\x1b[39m"},
	{1200 * time.Millisecond, TerminalColorMode256, false, "\x1b[38;5;99mOpenRouter\x1b[39m"},
	{1800 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;220;248;128mOpenRouter\x1b[39m"},
	{1800 * time.Millisecond, TerminalColorModeTrueColor, false, "\x1b[38;2;117;81;240mOpenRouter\x1b[39m"},
	{1800 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;192mOpenRouter\x1b[39m"},
	{1800 * time.Millisecond, TerminalColorMode256, false, "\x1b[38;5;99mOpenRouter\x1b[39m"},
	{2400 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;198;242;67mOpenRouter\x1b[39m"},
	{2400 * time.Millisecond, TerminalColorModeTrueColor, false, "\x1b[38;2;93;50;225mOpenRouter\x1b[39m"},
	{2400 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;191mOpenRouter\x1b[39m"},
	{2400 * time.Millisecond, TerminalColorMode256, false, "\x1b[38;5;62mOpenRouter\x1b[39m"},
	{5000 * time.Millisecond, TerminalColorModeTrueColor, true, "\x1b[38;2;201;243;77mOpenRouter\x1b[39m"},
	{5000 * time.Millisecond, TerminalColorModeTrueColor, false, "\x1b[38;2;96;55;227mOpenRouter\x1b[39m"},
	{5000 * time.Millisecond, TerminalColorMode256, true, "\x1b[38;5;191mOpenRouter\x1b[39m"},
	{5000 * time.Millisecond, TerminalColorMode256, false, "\x1b[38;5;62mOpenRouter\x1b[39m"},
}

func TestRadiusShimmerMatchesPi(t *testing.T) {
	for _, c := range radiusShimmerCases {
		if got := RadiusShimmer("Radius", c.elapsed, c.mode); got != c.want {
			t.Errorf("RadiusShimmer at %v in %s = %q, want %q", c.elapsed, c.mode, got, c.want)
		}
	}
}

func TestOpenRouterGlowMatchesPi(t *testing.T) {
	theme := ActiveTheme()
	dark := theme.Appearance() != "light"
	style := GetProviderBrandStyle("openrouter")
	checked := 0
	for _, c := range openRouterGlowCases {
		if c.mode != theme.GetColorMode() || c.dark != dark {
			continue
		}
		checked++
		if got := style("OpenRouter", c.elapsed); got != c.want {
			t.Errorf("glow at %v = %q, want %q", c.elapsed, got, c.want)
		}
	}
	if checked == 0 {
		t.Fatalf("no recorded Pi glow for mode %s dark=%v", theme.GetColorMode(), dark)
	}
}
