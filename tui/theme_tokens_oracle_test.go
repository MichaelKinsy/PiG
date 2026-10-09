package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// theme.ts Theme.fg/bg/getFgAnsi/getBgAnsi, the text attributes and the thinking and bash border colours for every
// token of the built-in dark and light themes in truecolor and 256-color mode, against the pinned Pi.
func TestBuiltinThemeTokensMatchPi(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "node", "testdata/theme_tokens.mjs", pigversion.UpstreamVersion)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected map[string]struct {
		Mode     string            `json:"mode"`
		Fg       map[string]string `json:"fg"`
		Bg       map[string]string `json:"bg"`
		FgAnsi   map[string]string `json:"fgAnsi"`
		BgAnsi   map[string]string `json:"bgAnsi"`
		Styles   []string          `json:"styles"`
		Thinking map[string]string `json:"thinking"`
		Bash     string            `json:"bash"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != 4 {
		t.Fatalf("oracle answered %d theme/mode pairs", len(expected))
	}
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	for key, want := range expected {
		name, mode := key[:slices.Index([]byte(key), '/')], want.Mode
		t.Run(key, func(t *testing.T) {
			SetCapabilities(TerminalCapabilities{TrueColor: mode == "truecolor"})
			theme := ThemeByName(name)
			if theme == nil {
				t.Fatalf("no built-in theme %q", name)
			}
			theme = theme.WithColorMode(TerminalColorMode(mode))
			if string(theme.GetColorMode()) != mode {
				t.Fatalf("color mode %q, want %q", theme.GetColorMode(), mode)
			}
			palette := func(kind string, tokens map[string]string, text func(string) string, ansi func(string) string, wantAnsi map[string]string) {
				if len(tokens) == 0 {
					t.Fatalf("%s tokens missing from the oracle", kind)
				}
				for token, wantText := range tokens {
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Errorf("%s %q: Pig panics (%v), Pi defines it", kind, token, r)
							}
						}()
						if got := text(token); got != wantText {
							t.Errorf("%s %q: Pig %q, Pi %q", kind, token, got, wantText)
						}
						if got := ansi(token); got != wantAnsi[token] {
							t.Errorf("%s %q opening: Pig %q, Pi %q", kind, token, got, wantAnsi[token])
						}
					}()
				}
			}
			palette("fg", want.Fg, func(token string) string { return theme.Fg(token, "x") }, theme.GetFgAnsi, want.FgAnsi)
			palette("bg", want.Bg, func(token string) string { return theme.Bg(token, "x") }, theme.GetBgAnsi, want.BgAnsi)
			fgPalette, bgPalette := theme.ANSIPalette()
			if len(fgPalette) != len(want.Fg) || len(bgPalette) != len(want.Bg) {
				t.Errorf("Pig defines %d fg and %d bg tokens, Pi %d and %d", len(fgPalette), len(bgPalette), len(want.Fg), len(want.Bg))
			}
			styles := []string{theme.Bold("x"), theme.Italic("x"), theme.Underline("x"), theme.Inverse("x"), theme.Strikethrough("x")}
			if !slices.Equal(styles, want.Styles) {
				t.Errorf("attributes: Pig %q, Pi %q", styles, want.Styles)
			}
			for level, wantText := range want.Thinking {
				if got := theme.GetThinkingBorderColor(level)("x"); got != wantText {
					t.Errorf("thinking border %q: Pig %q, Pi %q", level, got, wantText)
				}
			}
			if got := theme.GetBashModeBorderColor()("x"); got != want.Bash {
				t.Errorf("bash border: Pig %q, Pi %q", got, want.Bash)
			}
		})
	}
}
