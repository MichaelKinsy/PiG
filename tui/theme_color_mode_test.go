package tui

import (
	"strings"
	"testing"
)

func withTrueColor(t *testing.T, trueColor bool) {
	t.Helper()
	previous := ActiveTheme()
	SetCapabilities(TerminalCapabilities{TrueColor: trueColor})
	t.Cleanup(func() {
		ResetCapabilitiesCache()
		activeTheme.Store(previous)
	})
}

// Mirrors theme.ts createTheme: a theme loaded while truecolor is off uses
// 256color mode for every token, including the precomputed fields and
// dynamic Fg/Bg lookups.
func TestSetThemeFollowsTrueColorCapability(t *testing.T) {
	withTrueColor(t, false)
	for _, name := range []string{"dark", "light"} {
		SetTheme(name)
		th := ActiveTheme()
		if th.GetColorMode() != TerminalColorMode256 {
			t.Fatalf("%s mode = %q, want 256color", name, th.GetColorMode())
		}
		for token, got := range map[string]string{"accent": th.Accent, "userMessageBg": th.UserMessageBg, "muted": th.GetFgAnsi("muted"), "selectedBg": th.GetBgAnsi("selectedBg")} {
			if strings.Contains(got, "38;2;") || strings.Contains(got, "48;2;") || got == "" {
				t.Errorf("%s %s = %q, want a 256-color escape", name, token, got)
			}
		}
	}
	SetThemeByName("dark")
	if ActiveTheme().GetColorMode() != TerminalColorMode256 || ActiveTheme().Accent != "\x1b[38;5;140m" {
		t.Fatalf("SetThemeByName dark = %q (%s)", ActiveTheme().Accent, ActiveTheme().GetColorMode())
	}

	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	if ActiveTheme().GetColorMode() != TerminalColorModeTrueColor || ActiveTheme().Accent != "\x1b[38;2;167;152;215m" {
		t.Fatalf("truecolor dark accent = %q (%s)", ActiveTheme().Accent, ActiveTheme().GetColorMode())
	}
}

// Components that paint fixed theme colors must follow the active theme's
// color mode rather than always emitting 24-bit escapes.
func TestFixedComponentColorsFollowColorMode(t *testing.T) {
	withTrueColor(t, false)
	SetTheme("dark")
	rendered := strings.Join([]string{
		ThemeHexFg("#b5bd68"),
		ThemeHexBg("#2d2838"),
		thinkingBorderSGR("low"),
		strings.Join(NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "summary", TokensBefore: 10}, nil, 1).Render(40), "\n"),
	}, "\n")
	if strings.Contains(rendered, "38;2;") || strings.Contains(rendered, "48;2;") {
		t.Fatalf("256-color render still contains 24-bit escapes: %q", rendered)
	}
	if got := ThemeHexFg("#b5bd68"); got != "\x1b[38;5;143m" {
		t.Fatalf("ThemeHexFg = %q", got)
	}
}
