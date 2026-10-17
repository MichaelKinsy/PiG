package extensionconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// ctx.ui.theme.appearance, colors and style for every SDK against the host's own theme (.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/theme/theme.ts:311-367). The host resolves the appearance and the concrete colors with the palette; each SDK renders style over the palette's escape sequences. The expected values are what the host's tui.Theme answers for the same palette, so an SDK that returns a constant, ignores a color kind or rounds differently fails.

// conformanceTheme is the built-in dark theme with the values theme-style.test.ts changes: an OKLCH accent and two tokens set to the terminal default.
func conformanceTheme(t *testing.T, mode tui.TerminalColorMode) *tui.Theme {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(findModuleRoot(t), "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	colors := theme["colors"].(map[string]any)
	colors["accent"] = "oklch(62% 0.1 200)"
	colors["text"] = ""
	colors["userMessageBg"] = ""
	theme["name"] = "conformance"
	encoded, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "conformance.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := tui.LoadThemeFromPath(path, mode)
	if err != nil || loaded == nil {
		t.Fatalf("load theme: %v", err)
	}
	return loaded
}

// themeProbeCases are theme.style options as Pi's JSON carries them: a token or a Color per slot, and attributes.
var themeProbeCases = []map[string]any{
	{"fg": "success", "bg": "toolSuccessBg", "bold": true},
	{},
	{"bold": true, "dim": true, "italic": true, "underline": true, "inverse": true, "strikethrough": true},
	{"fg": "accent", "italic": true},
	{"fg": "text", "bg": "userMessageBg"},
	{"fg": map[string]any{"kind": "rgb", "r": 10, "g": 20, "b": 30}},
	{"fg": map[string]any{"kind": "rgb", "r": 10.5, "g": 20.4, "b": 29.6}},
	{"fg": map[string]any{"kind": "rgb", "r": 128, "g": 128, "b": 130}, "bg": map[string]any{"kind": "rgb", "r": 250, "g": 100, "b": 50}},
	{"fg": map[string]any{"kind": "oklch", "l": 0.62, "c": 0.1, "h": 200}},
	{"bg": map[string]any{"kind": "oklch", "l": 1, "c": 0.3, "h": 150}},
	{"fg": map[string]any{"kind": "oklch", "l": 0.7, "c": 0.3, "h": 150}},
	{"bg": map[string]any{"kind": "oklch", "l": 0.5, "c": 0.4, "h": 30}},
	{"fg": map[string]any{"kind": "oklch", "l": 0.2, "c": 0.05, "h": 30}, "underline": true},
	{"fg": map[string]any{"kind": "indexed", "index": 5}, "bg": map[string]any{"kind": "indexed", "index": 200}},
	{"fg": "notAToken"},
	{"fg": "userMessageBg"},
	{"bg": "success"},
}

var themeProbeTokens = []string{"accent", "text", "success", "userMessageBg", "toolSuccessBg", "thinkingMax", "notAToken"}

// hostColor is the extension wire's JSON for a tui.Color, written here so the host's encoder is not its own oracle.
func hostColor(color tui.Color) map[string]any {
	switch color := color.(type) {
	case tui.IndexedColor:
		return map[string]any{"kind": "indexed", "index": float64(color.Index)}
	case tui.RgbColorValue:
		return map[string]any{"kind": "rgb", "r": color.R, "g": color.G, "b": color.B}
	case tui.OklchColorValue:
		return map[string]any{"kind": "oklch", "l": color.L, "c": color.C, "h": color.H}
	}
	return nil
}

func tuiColorFrom(t *testing.T, value map[string]any) tui.Color {
	t.Helper()
	number := func(key string) float64 {
		switch v := value[key].(type) {
		case int:
			return float64(v)
		case float64:
			return v
		}
		return 0
	}
	switch value["kind"] {
	case "indexed":
		return tui.IndexedColor{Index: int(number("index"))}
	case "rgb":
		return tui.RgbColorValue{R: number("r"), G: number("g"), B: number("b")}
	case "oklch":
		return tui.OklchColorValue{L: number("l"), C: number("c"), H: number("h")}
	}
	t.Fatalf("unknown color %v", value)
	return nil
}

func tuiStyleFrom(t *testing.T, options map[string]any) tui.ThemeStyle {
	t.Helper()
	var style tui.ThemeStyle
	for key, attribute := range map[string]*bool{"bold": &style.Bold, "dim": &style.Dim, "italic": &style.Italic, "underline": &style.Underline, "inverse": &style.Inverse, "strikethrough": &style.Strikethrough} {
		*attribute, _ = options[key].(bool)
	}
	switch value := options["fg"].(type) {
	case string:
		style.FgToken = value
	case map[string]any:
		style.Fg = tuiColorFrom(t, value)
	}
	switch value := options["bg"].(type) {
	case string:
		style.BgToken = value
	case map[string]any:
		style.Bg = tuiColorFrom(t, value)
	}
	return style
}

// Pi: packages/coding-agent/src/modes/interactive/theme/theme.ts:124 (ThemeStyle.fg); packages/coding-agent/src/modes/interactive/theme/theme.ts:125 (ThemeStyle.bg); packages/coding-agent/src/modes/interactive/theme/theme.ts:66 (TextAttributes.dim); packages/tui/src/colors.ts:48 (TextAttributes.underline); packages/tui/src/colors.ts:49 (TextAttributes.inverse); packages/tui/src/colors.ts:50 (TextAttributes.strikethrough).
func TestThemeAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		// theme-style.test.ts:108-117: the terminal's reported colors resolve a token set to the default.
		t.Cleanup(func() { tui.SetTerminalColors(tui.TerminalColors{}) })
		tui.SetTerminalColors(tui.TerminalColors{Foreground: &tui.RgbColor{R: 200, G: 210, B: 220}, Background: &tui.RgbColor{R: 10, G: 20, B: 30}})
		args := map[string]any{"cases": themeProbeCases, "tokens": themeProbeTokens}
		for _, mode := range []tui.TerminalColorMode{tui.TerminalColorModeTrueColor, tui.TerminalColorMode256} {
			theme := conformanceTheme(t, mode)
			r.setTheme(codingagent.ExtensionThemePalette(theme))
			report := r.toolWith("theme_probe", args)

			if want := string(theme.Appearance()); report["appearance"] != want {
				t.Errorf("%s: appearance = %v, want %s", mode, report["appearance"], want)
			}
			wantColors := map[string]any{}
			values := theme.Colors()
			for _, token := range themeProbeTokens {
				if color, ok := values[token]; ok {
					wantColors[token] = hostColor(color)
				} else {
					wantColors[token] = nil
				}
			}
			if !reflect.DeepEqual(report["colors"], wantColors) {
				t.Errorf("%s: colors = %v\nwant %v", mode, report["colors"], wantColors)
			}
			styles, _ := report["styles"].([]any)
			if len(styles) != len(themeProbeCases) {
				t.Fatalf("%s: %d styles for %d cases: %v", mode, len(styles), len(themeProbeCases), report)
			}
			for i, options := range themeProbeCases {
				want := map[string]any{}
				if out, err := theme.Style("x", tuiStyleFrom(t, options)); err != nil {
					want["error"] = err.Error()
				} else {
					want["ok"] = out
				}
				if !reflect.DeepEqual(styles[i], want) {
					t.Errorf("%s: style %v = %#v, want %#v", mode, options, styles[i], want)
				}
			}
		}
	})
}

// ctx.ui.theme.fg closes a faint token with SGR 22;39 (.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/theme/theme.ts:361-365, 399-402): the palette's opening for a faint token ends in SGR 2, which SGR 39 alone would leave open past the text. Only the system theme has faint tokens, so the row uses it with no terminal colors reported, the case Pi's systemIndexedColors makes its neutral tokens faint for. Every foreground token of the theme is compared with the host's own Theme.FgText, and an unknown token throws `Unknown theme color: <token>` in every SDK, as Pi 1.0.4's theme.ts:361-376 does.
func TestThemeFaintForegroundAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		tui.SetTerminalColors(tui.TerminalColors{})
		theme := tui.NewThemeRegistry().Get(tui.SystemThemeName)
		if theme == nil {
			t.Fatal("no system theme")
		}
		foregrounds, _ := theme.ANSIPalette()
		tokens := []string{"notAToken"}
		faint := 0
		for token, ansi := range foregrounds {
			tokens = append(tokens, token)
			if strings.HasSuffix(ansi, "\x1b[2m") {
				faint++
			}
		}
		slices.Sort(tokens)
		if faint == 0 {
			t.Fatalf("the system theme has no faint foreground token, so the row proves nothing: %v", foregrounds)
		}
		r.setTheme(codingagent.ExtensionThemePalette(theme))
		report := r.toolWith("theme_probe", map[string]any{"fgTokens": tokens})
		got, _ := report["fgs"].(map[string]any)
		for _, token := range tokens {
			want := "throw:Unknown theme color: " + token
			if token != "notAToken" {
				want = theme.Fg(token, "x")
			}
			if got[token] != want {
				t.Errorf("fg(%q) = %q, want the host's %q", token, got[token], want)
			}
		}
	})
}

// themeModifierTexts are texts the modifiers draw beyond "x": empty, a line break of each kind, and an inner close of bold and of italic.
var themeModifierTexts = []string{"", "a\nb", "a\r\nb", "a\x1b[22mb", "a\x1b[23mb"}

// chalkModifiers is Pi's chalk 6.0.0 (the version pi-coding-agent 1.1.0 pins) over themeModifierTexts with color support, which theme.ts bold, italic, underline, inverse and strikethrough return (theme.ts:378-396). Recorded from `new Chalk({level: 1})` in Pi's own node_modules: chalk draws nothing for an empty string, closes and re-opens the style around each line break (keeping a CR), and re-opens it after an inner close of the same style.
var chalkModifiers = map[string][]string{
	"bold":          {"", "\x1b[1ma\x1b[22m\n\x1b[1mb\x1b[22m", "\x1b[1ma\x1b[22m\r\n\x1b[1mb\x1b[22m", "\x1b[1ma\x1b[22m\x1b[1mb\x1b[22m", "\x1b[1ma\x1b[23mb\x1b[22m"},
	"italic":        {"", "\x1b[3ma\x1b[23m\n\x1b[3mb\x1b[23m", "\x1b[3ma\x1b[23m\r\n\x1b[3mb\x1b[23m", "\x1b[3ma\x1b[22mb\x1b[23m", "\x1b[3ma\x1b[23m\x1b[3mb\x1b[23m"},
	"underline":     {"", "\x1b[4ma\x1b[24m\n\x1b[4mb\x1b[24m", "\x1b[4ma\x1b[24m\r\n\x1b[4mb\x1b[24m", "\x1b[4ma\x1b[22mb\x1b[24m", "\x1b[4ma\x1b[23mb\x1b[24m"},
	"inverse":       {"", "\x1b[7ma\x1b[27m\n\x1b[7mb\x1b[27m", "\x1b[7ma\x1b[27m\r\n\x1b[7mb\x1b[27m", "\x1b[7ma\x1b[22mb\x1b[27m", "\x1b[7ma\x1b[23mb\x1b[27m"},
	"strikethrough": {"", "\x1b[9ma\x1b[29m\n\x1b[9mb\x1b[29m", "\x1b[9ma\x1b[29m\r\n\x1b[9mb\x1b[29m", "\x1b[9ma\x1b[22mb\x1b[29m", "\x1b[9ma\x1b[23mb\x1b[29m"},
}

// The rest of ctx.ui.theme through every SDK against the host's own theme (theme.ts:353-437): the modifiers, getFgAnsi, getBgAnsi and bg for every token of the palette and one unknown token, getColorMode, getThinkingBorderColor for every level and one unknown level, getBashModeBorderColor, and sourcePath (set for a theme loaded from a file, absent for a built-in one). An unknown token throws `Unknown theme color: <token>` in every SDK. A token that is a foreground but not a background throws from getBgAnsi and bg.
func TestThemeQueriesAcrossSDKs(t *testing.T) {
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh", "max", "bogus"}
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		t.Cleanup(func() { tui.SetTerminalColors(tui.TerminalColors{}) })
		tui.SetTerminalColors(tui.TerminalColors{})
		custom := conformanceTheme(t, tui.TerminalColorMode256)
		themes := map[string]*tui.Theme{"custom file theme, 256 colors": custom, "built-in system theme": tui.NewThemeRegistry().Get(tui.SystemThemeName)}
		// The palette's modifiers flag is the host's chalk level (theme.bold draws nothing at level 0); the row sets it so both outcomes are compared whatever the test's stdout is.
		modifiers := map[string]bool{"custom file theme, 256 colors": true, "built-in system theme": false}
		for name, theme := range themes {
			if theme == nil {
				t.Fatalf("%s: no theme", name)
			}
			foregrounds, backgrounds := theme.ANSIPalette()
			tokens := []string{"notAToken"}
			for token := range foregrounds {
				tokens = append(tokens, token)
			}
			for token := range backgrounds {
				if _, dup := foregrounds[token]; !dup {
					tokens = append(tokens, token)
				}
			}
			slices.Sort(tokens)
			if len(foregrounds) == 0 || len(backgrounds) == 0 {
				t.Fatalf("%s: the palette has %d foregrounds and %d backgrounds, so the row proves nothing", name, len(foregrounds), len(backgrounds))
			}
			palette := codingagent.ExtensionThemePalette(theme).(map[string]any)
			palette["modifiers"] = modifiers[name]
			r.setTheme(palette)
			report := r.toolWith("theme_probe", map[string]any{"queries": true, "ansiTokens": tokens, "levels": levels, "modifierTexts": themeModifierTexts})
			got, _ := report["queries"].(map[string]any)
			if got == nil {
				t.Fatalf("%s: no queries in %v", name, report)
			}
			unknown := func(token string) string { return "throw:Unknown theme color: " + token }
			wantModifiers := map[string]any{"bold": "x", "italic": "x", "underline": "x", "inverse": "x", "strikethrough": "x"}
			if modifiers[name] {
				wantModifiers = map[string]any{"bold": "\x1b[1mx\x1b[22m", "italic": "\x1b[3mx\x1b[23m", "underline": "\x1b[4mx\x1b[24m", "inverse": "\x1b[7mx\x1b[27m", "strikethrough": "\x1b[9mx\x1b[29m"}
			}
			if !reflect.DeepEqual(got["modifiers"], wantModifiers) {
				t.Errorf("%s: modifiers = %#v, want %#v", name, got["modifiers"], wantModifiers)
			}
			wantTexts := map[string]any{}
			for modifier, drawn := range chalkModifiers {
				texts := []any{}
				for i, text := range themeModifierTexts {
					if modifiers[name] {
						text = drawn[i]
					}
					texts = append(texts, text)
				}
				wantTexts[modifier] = texts
			}
			if !reflect.DeepEqual(got["modifierTexts"], wantTexts) {
				t.Errorf("%s: modifier texts = %#v\nwant %#v", name, got["modifierTexts"], wantTexts)
			}
			wantFg, wantBg, wantBgs := map[string]any{}, map[string]any{}, map[string]any{}
			for _, token := range tokens {
				wantFg[token], wantBg[token], wantBgs[token] = unknown(token), unknown(token), unknown(token)
				if _, ok := foregrounds[token]; ok {
					wantFg[token] = theme.GetFgAnsi(token)
				}
				if _, ok := backgrounds[token]; ok {
					wantBg[token] = theme.GetBgAnsi(token)
					wantBgs[token] = theme.Bg(token, "x")
				}
			}
			for key, want := range map[string]map[string]any{"fgAnsi": wantFg, "bgAnsi": wantBg, "bgs": wantBgs} {
				if !reflect.DeepEqual(got[key], want) {
					t.Errorf("%s: %s = %#v\nwant %#v", name, key, got[key], want)
				}
			}
			if want := string(theme.GetColorMode()); got["mode"] != want {
				t.Errorf("%s: mode = %v, want %s", name, got["mode"], want)
			}
			wantThinking := map[string]any{}
			for _, level := range levels {
				wantThinking[level] = theme.GetThinkingBorderColor(level)("x")
			}
			if !reflect.DeepEqual(got["thinking"], wantThinking) {
				t.Errorf("%s: thinking = %#v\nwant %#v", name, got["thinking"], wantThinking)
			}
			if want := theme.GetBashModeBorderColor()("x"); got["bash"] != want {
				t.Errorf("%s: bash = %v, want %q", name, got["bash"], want)
			}
			var wantSource any
			if theme.SourcePath != "" {
				wantSource = theme.SourcePath
			}
			if got["sourcePath"] != wantSource {
				t.Errorf("%s: sourcePath = %v, want %v", name, got["sourcePath"], wantSource)
			}
			if name == "custom file theme, 256 colors" && wantSource == nil {
				t.Errorf("the custom theme has no source path, so the row proves nothing")
			}
		}
	})
}
