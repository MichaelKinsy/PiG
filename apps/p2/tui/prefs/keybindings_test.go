package prefs_test

// pi: apps/p2/test/p2/tui-theme.test.ts "server defaults: whitelisted, no privileged bindings, below the visitor's local preferences".
// Divergence: a keybinding config value is always a key list (pitui.KeybindingsConfig is map[string][]string): `"ctrl+k"` is `["ctrl+k"]`.

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

func ptr[T any](v T) *T { return &v }

func TestServerDefaultsWhitelistedNoPrivilegedBindingsBelowLocalPreferences(t *testing.T) {
	server := prefs.SanitizeServerPreferences(map[string]any{
		"keybindings": map[string]any{"app.clipboard.pasteImage": "ctrl+y", "app.model.select": "ctrl+k", "app.tools.expand": "ctrl+e"},
		"theme":       map[string]any{"accent": float64(35)},
		"settings": map[string]any{
			"hideThinkingBlock": true, "editorPaddingX": float64(9), "externalEditor": "vim", "terminal": map[string]any{"showImages": false},
		},
	})
	want := prefs.TuiServerPreferences{
		Keybindings: map[string][]string{"app.model.select": {"ctrl+k"}, "app.tools.expand": {"ctrl+e"}},
		Settings:    prefs.TuiSettings{HideThinkingBlock: ptr(true), Terminal: &prefs.TerminalSettings{ShowImages: ptr(false)}},
	}
	if !reflect.DeepEqual(server, want) {
		t.Fatalf("server = %+v, want %+v", server, want)
	}

	server.Theme = "light"
	server.Keybindings["app.suspend"] = []string{"ctrl+q"}
	local := prefs.TuiLocalPreferences{
		Keybindings: pitui.KeybindingsConfig{"app.model.select": {"ctrl+j"}},
		Settings:    prefs.TuiSettings{Terminal: &prefs.TerminalSettings{ImageWidthCells: ptr(40)}},
		Themes:      map[string]theme.ThemeJSON{},
	}
	effective := prefs.EffectivePreferences(&server, local)
	if want := (pitui.KeybindingsConfig{"app.model.select": {"ctrl+j"}, "app.tools.expand": {"ctrl+e"}}); !reflect.DeepEqual(effective.Keybindings, want) {
		t.Fatalf("keybindings = %v, want %v", effective.Keybindings, want)
	}
	if effective.Theme != "light" {
		t.Fatalf("theme = %q", effective.Theme)
	}
	wantSettings := prefs.TuiSettings{HideThinkingBlock: ptr(true), Terminal: &prefs.TerminalSettings{ShowImages: ptr(false), ImageWidthCells: ptr(40)}}
	if !reflect.DeepEqual(effective.Settings, wantSettings) {
		t.Fatalf("settings = %+v, want %+v", effective.Settings, wantSettings)
	}
}
