package sdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// The vectors are what pi-coding-agent's own formatKeyText, keyText, keyDisplayText, keyHint and rawKeyHint return
// (test/parity/sdk-bulk/gen-coding-agent.mjs), under the host's theme palette and keybinding table.
func TestKeyHintsMatchPi(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "parity", "sdk-bulk", "pi-coding-agent-helpers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Hints []struct {
			Platform   string
			Key        string
			Capitalize bool
			Result     string
		}
		Defs map[string]struct {
			DefaultKeys json.RawMessage
			Description string
		}
		Themes []struct {
			Palette  json.RawMessage
			KeyHints []struct {
				Platform string
				User     map[string]json.RawMessage
				IDs      map[string]struct{ KeyText, KeyDisplayText, KeyHint string }
				Raw      map[string]string
			}
		}
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	goos := map[string]string{"darwin": "darwin", "linux": "linux", "win32": "windows"}
	for _, h := range v.Hints {
		if got := formatKeyText(goos[h.Platform], h.Key, h.Capitalize); got != h.Result {
			t.Errorf("formatKeyText(%s, %q, %v) = %q, Pi %q", h.Platform, h.Key, h.Capitalize, got, h.Result)
		}
	}
	for _, theme := range v.Themes {
		palette, ok := decodeUITheme(theme.Palette)
		if !ok {
			t.Fatal("palette does not decode")
		}
		setHostTheme(palette, theme.Palette)
		for _, row := range theme.KeyHints {
			defs := tui.KeybindingDefinitions{}
			for id, d := range v.Defs {
				defs[id] = tui.KeybindingDefinition{DefaultKeys: wireKeys(d.DefaultKeys), Description: d.Description}
			}
			user := tui.KeybindingsConfig{}
			for id, keys := range row.User {
				user[id] = wireKeys(keys)
			}
			tui.SetKeybindings(tui.NewKeybindingsManager(defs, user))
			for id, want := range row.IDs {
				keys := tui.GetKeybindings().GetKeys(id)
				if got := formatKeys(goos[row.Platform], keys, false); got != want.KeyText {
					t.Errorf("%s keyText(%q) = %q, Pi %q", row.Platform, id, got, want.KeyText)
				}
				if got := formatKeys(goos[row.Platform], keys, true); got != want.KeyDisplayText {
					t.Errorf("%s keyDisplayText(%q) = %q, Pi %q", row.Platform, id, got, want.KeyDisplayText)
				}
				if got := palette.Fg("dim", formatKeys(goos[row.Platform], keys, false)) + palette.Fg("muted", " to go"); got != want.KeyHint {
					t.Errorf("%s keyHint(%q) = %q, Pi %q", row.Platform, id, got, want.KeyHint)
				}
			}
			for key, want := range row.Raw {
				if got := palette.Fg("dim", formatKeyText(goos[row.Platform], key, false)) + palette.Fg("muted", " desc"); got != want {
					t.Errorf("%s rawKeyHint(%q) = %q, Pi %q", row.Platform, key, got, want)
				}
			}
		}
	}
}
