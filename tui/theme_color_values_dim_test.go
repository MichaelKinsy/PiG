package tui

import "testing"

// TestThemeColorValuesMixFaintTokensTowardTheBackground: Pi's `get colors()` (packages/coding-agent/src/modes/interactive/theme/theme.ts:333-335)
// replaces each dim token's color with mixColors(color, background, 0.4), where background is the terminal's reported background or
// the appearance guess. Without a reported background the system theme renders muted as faint text (system-theme.test.ts:91), so its
// concrete value is the terminal-default foreground mixed 0.4 toward the guessed background.
func TestThemeColorValuesMixFaintTokensTowardTheBackground(t *testing.T) {
	t.Setenv("COLORFGBG", "")
	SetTerminalColors(TerminalColors{})
	t.Cleanup(func() { SetTerminalColors(TerminalColors{}) })
	theme := createSystemTheme(currentColorMode())
	if !theme.dimTokens["muted"] {
		t.Fatalf("muted is not a dim token of the background-less system theme: %v", theme.dimTokens)
	}
	foreground, background := guessedDefaultColors(theme.Appearance())
	want, err := MixColors(foreground, background, 0.4, ColorMixSpaceOklch)
	if err != nil {
		t.Fatal(err)
	}
	got := theme.Colors()["muted"]
	if ColorToHex(got) != ColorToHex(want) {
		t.Errorf("muted = %s, want %s (the foreground %s mixed 0.4 toward %s)", ColorToHex(got), ColorToHex(want), ColorToHex(foreground), ColorToHex(background))
	}
	if ColorToHex(got) == ColorToHex(foreground) {
		t.Errorf("muted = %s, the unmixed foreground", ColorToHex(got))
	}
}
