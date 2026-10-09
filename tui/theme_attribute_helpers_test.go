package tui

import "testing"

// theme.ts bold/italic/underline/strikethrough wrap text in the chalk attribute and its scoped close; chalk returns an empty string unchanged.
func TestThemeTextAttributeHelpers(t *testing.T) {
	theme, err := LoadBuiltinTheme("dark")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		got  func(string) string
		want string
	}{
		{"bold", theme.Bold, "\x1b[1mx\x1b[22m"},
		{"italic", theme.Italic, "\x1b[3mx\x1b[23m"},
		{"underline", theme.Underline, "\x1b[4mx\x1b[24m"},
		{"strikethrough", theme.Strikethrough, "\x1b[9mx\x1b[29m"},
	}
	for _, c := range cases {
		if got := c.got("x"); got != c.want {
			t.Errorf("%s(x) = %q, want %q", c.name, got, c.want)
		}
		if got := c.got(""); got != "" {
			t.Errorf("%s(\"\") = %q, want empty", c.name, got)
		}
	}
}

// theme.ts getThinkingBorderColor maps every thinking level to its own token and any other level to thinkingOff; getBashModeBorderColor uses bashMode.
// The distinct-color check needs truecolor: the 256-color palette (colors.ts rgbToAnsi256) maps thinkingOff and thinkingMinimal to the same index, as in Pi, so the theme is resolved in truecolor whatever terminal runs the test.
// Pi source: packages/tui/src/terminal-colors.ts
// mutation-checked: zeroing the results of Theme.GetBashModeBorderColor fails it
// Pi: packages/coding-agent/src/modes/interactive/theme/theme.ts:412 (Theme.getThinkingBorderColor); packages/coding-agent/src/modes/interactive/theme/theme.ts:434 (Theme.getBashModeBorderColor).
func TestThemeBorderColorFunctions(t *testing.T) {
	theme, err := loadBuiltinThemeWithMode("dark", TerminalColorModeTrueColor)
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{
		"off": "thinkingOff", "minimal": "thinkingMinimal", "low": "thinkingLow", "medium": "thinkingMedium",
		"high": "thinkingHigh", "xhigh": "thinkingXhigh", "max": "thinkingMax", "unknown": "thinkingOff", "": "thinkingOff",
	}
	seen := map[string]string{}
	for level, token := range tokens {
		got := theme.GetThinkingBorderColor(level)("x")
		if want := theme.Fg(token, "x"); got != want {
			t.Errorf("GetThinkingBorderColor(%q)(x) = %q, want %q", level, got, want)
		}
		if level != "unknown" && level != "" {
			if prior, dup := seen[got]; dup {
				t.Errorf("levels %q and %q share the border color %q", prior, level, got)
			}
			seen[got] = level
		}
	}
	if got, want := theme.GetBashModeBorderColor()("x"), theme.Fg("bashMode", "x"); got != want {
		t.Errorf("GetBashModeBorderColor()(x) = %q, want %q", got, want)
	}
}
