package tui

import (
	"strings"
	"testing"
)

// Pi packages/tui/src/components/image.ts: the constructor stores the supplied ImageTheme and the fallback line (no terminal image support) is rendered through its fallbackColor.
func TestNewImageUsesTheSuppliedThemeForTheFallbackLine(t *testing.T) {
	SetCapabilities(TerminalCapabilities{})
	defer ResetCapabilitiesCache()
	theme := ImageTheme{FallbackColor: func(s string) string { return "<<" + s + ">>" }}
	image := NewImage("", "image/png", theme, ImageOptions{Filename: "pic.png"}, &ImageDimensions{WidthPx: 10, HeightPx: 10})
	lines := image.Render(80)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "<<[Image: ") || !strings.HasSuffix(lines[0], ">>") {
		t.Fatalf("fallback lines %q", lines)
	}
}

// DefaultImageTheme is the theme-aware ImageTheme callers pass as the constructor's third argument (image.ts: `theme: ImageTheme`).
func TestNewImageDefaultsToTheActiveTheme(t *testing.T) {
	SetCapabilities(TerminalCapabilities{})
	defer ResetCapabilitiesCache()
	image := NewImage("", "image/png", DefaultImageTheme(), ImageOptions{}, &ImageDimensions{WidthPx: 10, HeightPx: 10})
	if got := image.Theme.FallbackColor("x"); got != DefaultImageTheme().FallbackColor("x") {
		t.Fatalf("default theme styles %q", got)
	}
}

// image.ts hosts pass theme.fg("muted", ...) as fallbackColor; DefaultImageTheme is that for the active theme.
func TestDefaultImageThemeMutesTheFallbackLine(t *testing.T) {
	SetCapabilities(TerminalCapabilities{})
	defer ResetCapabilitiesCache()
	image := NewImage("", "image/png", DefaultImageTheme(), ImageOptions{}, &ImageDimensions{WidthPx: 10, HeightPx: 10})
	th := ActiveTheme()
	if th.Muted == "" {
		t.Skip("active theme has no muted colour")
	}
	if lines := image.Render(80); len(lines) != 1 || !strings.HasPrefix(lines[0], th.Muted+"[Image: ") {
		t.Fatalf("fallback lines %q, want the muted colour %q first", lines, th.Muted)
	}
}
