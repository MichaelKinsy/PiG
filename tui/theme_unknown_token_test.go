package tui

import "testing"

// theme.ts:361-376, 399-407: fg, bg, getFgAnsi and getBgAnsi throw `Unknown theme color: <token>` for a token the theme does not define.
func TestThemeTokenLookupsPanicForAnUnknownToken(t *testing.T) {
	theme := ActiveTheme()
	for name, call := range map[string]func(){
		"Fg":        func() { _ = theme.Fg("nope", "x") },
		"Bg":        func() { _ = theme.Bg("nope", "x") },
		"GetFgAnsi": func() { _ = theme.GetFgAnsi("nope") },
		"GetBgAnsi": func() { _ = theme.GetBgAnsi("nope") },
	} {
		func() {
			defer func() {
				got := recover()
				err, ok := got.(error)
				if !ok || err.Error() != "Unknown theme color: nope" {
					t.Errorf("%s: recovered %v, want an error \"Unknown theme color: nope\"", name, got)
				}
			}()
			call()
		}()
	}
}

// theme.ts:378-396 bold, italic, underline and strikethrough wrap text in chalk's SGR pairs.
func TestThemeTextAttributeMethodsWrapInChalkPairs(t *testing.T) {
	theme := ActiveTheme()
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"Bold", theme.Bold("x"), "\x1b[1mx\x1b[22m"},
		{"Italic", theme.Italic("x"), "\x1b[3mx\x1b[23m"},
		{"Underline", theme.Underline("x"), "\x1b[4mx\x1b[24m"},
		{"Strikethrough", theme.Strikethrough("x"), "\x1b[9mx\x1b[29m"},
		{"Bold of empty text", theme.Bold(""), ""},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
