package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Ports packages/coding-agent/test/theme-style.test.ts (upstream 0.99.1).

// themeStyleLoad is the test's `loadTheme`: a copy of a built-in theme, modified by edit.
func themeStyleLoad(t *testing.T, base string, edit func(theme map[string]any)) *Theme {
	t.Helper()
	t.Cleanup(func() { SetTerminalColors(TerminalColors{}) })
	data, err := builtinThemes.ReadFile("theme_" + base + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(theme)
	}
	encoded, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), theme["name"].(string)+".json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadThemeFromPath(path, TerminalColorModeTrueColor)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("no theme loaded")
	}
	return loaded
}

func themeStyleVars(theme map[string]any) map[string]any {
	vars, _ := theme["vars"].(map[string]any)
	if vars == nil {
		vars = map[string]any{}
		theme["vars"] = vars
	}
	return vars
}

func TestThemeStylesUpstream(t *testing.T) {
	// theme-style.test.ts:36
	t.Run("renders theme tokens the same as the generic text styler", func(t *testing.T) {
		theme := themeStyleLoad(t, "dark", nil)
		got, err := theme.Style("Ready", ThemeStyle{FgToken: "success", BgToken: "toolSuccessBg", TextAttributes: TextAttributes{Bold: true}})
		if err != nil {
			t.Fatal(err)
		}
		colors := theme.Colors()
		want := StyleText("Ready", TextStyle{TextAttributes: TextAttributes{Bold: true}, Fg: colors["success"], Bg: colors["toolSuccessBg"]}, TerminalColorModeTrueColor)
		if got != want {
			t.Errorf("style = %q, want %q", got, want)
		}
	})

	// theme-style.test.ts:43
	t.Run("rejects unknown tokens and tokens in the wrong slot", func(t *testing.T) {
		theme := themeStyleLoad(t, "dark", nil)
		if _, err := theme.Style("x", ThemeStyle{FgToken: "notAToken"}); err == nil || err.Error() != "Unknown theme color: notAToken" {
			t.Errorf("unknown token error = %v", err)
		}
		// Background tokens are not foreground colors; use theme.colors.userMessageBg.
		if _, err := theme.Style("x", ThemeStyle{FgToken: "userMessageBg"}); err == nil || err.Error() != "Unknown theme color: userMessageBg" {
			t.Errorf("wrong slot error = %v", err)
		}
	})

	// theme-style.test.ts:50
	t.Run("loads OKLCH theme values", func(t *testing.T) {
		theme := themeStyleLoad(t, "dark", func(theme map[string]any) {
			theme["colors"].(map[string]any)["accent"] = "oklch(62% 0.1 200)"
		})
		if got := theme.Colors()["accent"]; got != (OklchColorValue{L: 0.62, C: 0.1, H: 200}) {
			t.Errorf("accent = %+v", got)
		}
	})

	// theme-style.test.ts:57
	t.Run("loads OKHSL theme values, including through variables", func(t *testing.T) {
		theme := themeStyleLoad(t, "dark", func(theme map[string]any) {
			themeStyleVars(theme)["brand"] = "okhsl(250 60% 55%)"
			colors := theme["colors"].(map[string]any)
			colors["accent"] = "brand"
			colors["error"] = "okhsl(20 90% 60%)"
		})
		accent, err := NewOkhslColor(250, 0.6, 0.55)
		if err != nil {
			t.Fatal(err)
		}
		errorColor, err := NewOkhslColor(20, 0.9, 0.6)
		if err != nil {
			t.Fatal(err)
		}
		colors := theme.Colors()
		if got, want := ColorToHex(colors["accent"]), ColorToHex(accent); got != want {
			t.Errorf("accent = %s, want %s", got, want)
		}
		if got, want := ColorToHex(colors["error"]), ColorToHex(errorColor); got != want {
			t.Errorf("error = %s, want %s", got, want)
		}
	})

	// theme-style.test.ts:67
	t.Run("detects the appearance unless it is declared", func(t *testing.T) {
		// The terminal-appearance fallback below reads COLORFGBG when the terminal reported no background; the host must not influence it.
		t.Setenv("COLORFGBG", "")
		if got := themeStyleLoad(t, "dark", nil).Appearance(); got != "dark" {
			t.Errorf("dark = %q", got)
		}
		if got := themeStyleLoad(t, "light", nil).Appearance(); got != "light" {
			t.Errorf("light = %q", got)
		}
		// Without a declaration, the appearance is detected from the theme's own colors.
		for _, base := range []string{"dark", "light"} {
			if got := themeStyleLoad(t, base, func(theme map[string]any) { delete(theme, "appearance") }).Appearance(); string(got) != base {
				t.Errorf("undeclared %s = %q", base, got)
			}
		}
		if got := themeStyleLoad(t, "dark", func(theme map[string]any) { theme["appearance"] = "light" }).Appearance(); got != "light" {
			t.Errorf("declared light = %q", got)
		}

		// Palette colors 0-15 follow the terminal palette, so such themes follow the terminal background.
		paletteOnly := themeStyleLoad(t, "dark", func(theme map[string]any) {
			delete(theme, "appearance")
			colors := theme["colors"].(map[string]any)
			for key := range colors {
				if len(key) >= 2 && key[len(key)-2:] == "Bg" {
					colors[key] = 0
				} else {
					colors[key] = 7
				}
			}
		})
		if got := paletteOnly.Appearance(); got != "dark" {
			t.Errorf("palette-only = %q, want dark", got)
		}
		SetTerminalColors(TerminalColors{Background: &RgbColor{R: 250, G: 250, B: 250}})
		if got := paletteOnly.Appearance(); got != "light" {
			t.Errorf("palette-only on a light terminal = %q, want light", got)
		}
	})

	// theme-style.test.ts:90
	t.Run("renders empty tokens as terminal defaults and reports concrete colors for them", func(t *testing.T) {
		theme := themeStyleLoad(t, "dark", func(theme map[string]any) {
			colors := theme["colors"].(map[string]any)
			colors["text"] = ""
			colors["userMessageBg"] = ""
		})
		if got := theme.Fg("text", "x"); got != "\x1b[39mx\x1b[39m" {
			t.Errorf("fg = %q", got)
		}
		if got := theme.Bg("userMessageBg", "x"); got != "\x1b[49mx\x1b[49m" {
			t.Errorf("bg = %q", got)
		}
		if got := ColorToHex(theme.Colors()["text"]); got != "#e5e5e7" {
			t.Errorf("text = %s", got)
		}
		if got := ColorToHex(theme.Colors()["userMessageBg"]); got != "#000000" {
			t.Errorf("userMessageBg = %s", got)
		}

		SetTerminalColors(TerminalColors{Foreground: &RgbColor{R: 200, G: 210, B: 220}, Background: &RgbColor{R: 10, G: 20, B: 30}})
		if got := ColorToHex(theme.Colors()["text"]); got != "#c8d2dc" {
			t.Errorf("text = %s", got)
		}
		if got := ColorToHex(theme.Colors()["userMessageBg"]); got != "#0a141e" {
			t.Errorf("userMessageBg = %s", got)
		}
	})
}

// upstream 0.99.1 theme.ts fg: a dim token is `${ansi}\x1b[2m${text}\x1b[22;39m`, every other token closes with `\x1b[39m`. FgClose derives the closer from the opening prefix, for helpers that receive a prefix from Theme.Fg.
func TestFgCloseClosesADimForegroundWithTheFaintReset(t *testing.T) {
	th := NewThemeRegistry().Get(SystemThemeName)
	if th == nil {
		t.Fatal("no system theme")
	}
	for _, token := range []string{"dim", "muted", "accent", "text"} {
		prefix := th.GetFgAnsi(token)
		want := th.Fg(token, "x")
		if got := prefix + "x" + FgClose(prefix); got != want {
			t.Errorf("token %q: prefix + text + FgClose = %q, want Fg %q", token, got, want)
		}
		if got := fg(prefix, "x"); got != want {
			t.Errorf("token %q: select fg helper = %q, want %q", token, got, want)
		}
	}
}
