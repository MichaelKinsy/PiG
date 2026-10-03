package codingagent

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// The palette an extension receives for ctx.ui.theme carries what Theme.appearance and Theme.colors answer, resolved by the host (.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/theme/theme.ts:311-336): the declared or detected appearance, and a concrete color for every token, with a token set to the terminal default resolved from the terminal's reported colors and the theme's color mode as its own.
func TestExtensionThemePaletteCarriesAppearanceColorsAndMode(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	colors := source["colors"].(map[string]any)
	colors["accent"] = "oklch(62% 0.1 200)"
	colors["text"] = ""
	source["name"] = "palette-test"
	source["appearance"] = "light"
	encoded, _ := json.Marshal(source)
	path := filepath.Join(t.TempDir(), "palette-test.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tui.SetTerminalColors(tui.TerminalColors{}) })
	tui.SetTerminalColors(tui.TerminalColors{Foreground: &tui.RgbColor{R: 200, G: 210, B: 220}, Background: &tui.RgbColor{R: 10, G: 20, B: 30}})

	for _, mode := range []tui.TerminalColorMode{tui.TerminalColorModeTrueColor, tui.TerminalColorMode256} {
		theme, err := tui.LoadThemeFromPath(path, mode)
		if err != nil || theme == nil {
			t.Fatalf("load theme: %v", err)
		}
		palette, ok := ExtensionThemePalette(theme).(map[string]any)
		if !ok {
			t.Fatalf("palette is %T", ExtensionThemePalette(theme))
		}
		if palette["appearance"] != "light" {
			t.Errorf("%s: appearance = %v, want the declared light", mode, palette["appearance"])
		}
		if palette["mode"] != string(mode) {
			t.Errorf("%s: mode = %v, want the theme's own mode (its escape sequences are built for it)", mode, palette["mode"])
		}
		got, ok := palette["colors"].(map[string]any)
		if !ok {
			t.Fatalf("%s: colors is %T", mode, palette["colors"])
		}
		values := theme.ColorValues()
		if len(got) != len(values) {
			t.Errorf("%s: %d colors, want one per token (%d)", mode, len(got), len(values))
		}
		want := map[string]any{
			"accent": map[string]any{"kind": "oklch", "l": 0.62, "c": 0.1, "h": float64(200)},
			"text":   map[string]any{"kind": "rgb", "r": float64(200), "g": float64(210), "b": float64(220)},
		}
		for token, color := range want {
			var round any
			raw, _ := json.Marshal(got[token])
			_ = json.Unmarshal(raw, &round)
			if !reflect.DeepEqual(round, color) {
				t.Errorf("%s: colors[%s] = %v, want %v", mode, token, round, color)
			}
		}
		if indexed, ok := values["thinkingMax"]; !ok || got["thinkingMax"] == nil {
			t.Errorf("%s: thinkingMax = %v (theme has %v)", mode, got["thinkingMax"], indexed)
		}
	}
}

// ctx.ui.getTheme(name) is Pi's getThemeByName, which creates a built-in or custom theme in the terminal's color mode (.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/theme/theme.ts:599-600,631-645 createTheme mode ?? getTerminalColorMode()). The palette's mode and its escape sequences follow the terminal, not the mode the registry stored the theme in.
func TestExtensionGetThemeFollowsTheTerminalColorMode(t *testing.T) {
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	t.Cleanup(tui.ResetCapabilitiesCache)
	ui := &ExtUIContext{}
	for _, tc := range []struct {
		trueColor bool
		mode      string
		prefix    string
	}{
		{false, "256color", "\x1b[38;5;"},
		{true, "truecolor", "\x1b[38;2;"},
	} {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: tc.trueColor})
		for _, name := range []string{"dark", "light"} {
			theme, err := ui.GetTheme(name)
			palette, ok := theme.(map[string]any)
			if err != nil || !ok {
				t.Fatalf("GetTheme(%s) = %v, %v", name, theme, err)
			}
			if palette["mode"] != tc.mode {
				t.Errorf("trueColor=%v: GetTheme(%s) mode = %v, want %s", tc.trueColor, name, palette["mode"], tc.mode)
			}
			foregrounds, _ := palette["foregrounds"].(map[string]string)
			if accent := foregrounds["accent"]; !strings.HasPrefix(accent, tc.prefix) {
				t.Errorf("trueColor=%v: GetTheme(%s) accent = %q, want a %q sequence", tc.trueColor, name, accent, tc.prefix)
			}
		}
	}
}

// A terminal can report a default color whose channel is not finite: Pi's parseOscHexChannel divides Infinity by Infinity for a channel of more than 256 hex digits (.upstream/v0.99.2/packages/tui/src/terminal-colors.ts:27-36). Pi's Theme.colors then has no Color for the token (rgbColor rejects a non-finite channel, colors.ts:58-79), and JSON cannot carry one, so the palette leaves the color out instead of failing the whole state push.
func TestExtensionThemePaletteOmitsNonFiniteColors(t *testing.T) {
	restoreStartupTheme(t)
	long := "rgb:" + strings.Repeat("f", 300) + "/00/00"
	reply, ok := tui.ParseOscColorResponse("\x1b]10;" + long + "\x07")
	if !ok || reply.RGB == nil || !math.IsNaN(reply.RGB.R) {
		t.Fatalf("OSC 10 %q = %+v, %v; want a NaN red channel as Pi parses it", long, reply.RGB, ok)
	}
	tui.SetTerminalColors(tui.TerminalColors{Foreground: reply.RGB, Background: &tui.RgbColor{R: 10, G: 20, B: 30}})
	data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	source["colors"].(map[string]any)["text"] = ""
	source["name"] = "nonfinite-test"
	encoded, _ := json.Marshal(source)
	path := filepath.Join(t.TempDir(), "nonfinite-test.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	theme, err := tui.LoadThemeFromPath(path, tui.TerminalColorModeTrueColor)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ExtensionThemePalette(theme))
	if err != nil {
		t.Fatalf("the palette does not serialize: %v", err)
	}
	var palette struct {
		Colors map[string]any `json:"colors"`
	}
	if err := json.Unmarshal(raw, &palette); err != nil {
		t.Fatal(err)
	}
	if _, ok := palette.Colors["text"]; ok {
		t.Errorf("colors[text] = %v, want it left out", palette.Colors["text"])
	}
	if _, ok := palette.Colors["accent"]; !ok {
		t.Errorf("colors lost the finite accent: %v", palette.Colors)
	}
}
