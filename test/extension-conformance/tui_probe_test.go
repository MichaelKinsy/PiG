package extensionconformance

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The host's keybinding table and terminal capabilities reach pi-tui's state in every SDK: getKeybindings() answers the table the host published (every definition with the user's overrides) and not TUI_KEYBINDINGS, getCapabilities() and getTerminalColorMode() answer the host terminal's and not the environment's, and a later publication replaces both. setKeybindings, setCapabilities and setKittyProtocolActive install what they are given, and Key builds the identifiers the table holds.
//
// Pi: packages/tui/src/keybindings.ts (TUI_KEYBINDINGS, KeybindingsManager, getKeybindings, setKeybindings), packages/tui/src/terminal-image.ts (getCapabilities, setCapabilities, detectCapabilities, resetCapabilitiesCache, getTerminalColorMode), packages/tui/src/keys.ts (Key, setKittyProtocolActive).
//
// The values are ones no SDK default produces: the default Enter submit is rebound to ctrl+y, select.confirm's defaults are replaced by ctrl+j, two custom actions claim ctrl+k, and the terminal reports iTerm2 images with hyperlinks, which an environment without iTerm2 never detects.

func tuiProbeTable(submit string, confirm string) map[string]any {
	return map[string]any{
		"definitions": map[string]any{
			"tui.input.submit":   map[string]any{"defaultKeys": []string{"enter"}, "description": "Submit input"},
			"tui.select.confirm": map[string]any{"defaultKeys": []string{confirm}, "description": "Confirm selection (host)"},
			"x.probe.a":          map[string]any{"defaultKeys": "ctrl+a", "description": "Probe A"},
			"x.probe.b":          map[string]any{"defaultKeys": []string{"ctrl+b"}},
		},
		"userBindings": map[string]any{
			"tui.input.submit": []string{submit},
			"x.probe.a":        []string{"ctrl+k"},
			"x.probe.b":        "ctrl+k",
		},
	}
}

var tuiProbeArgs = map[string]any{
	"ids":    []string{"tui.input.submit", "tui.select.confirm", "x.probe.a", "x.probe.b", "x.probe.missing"},
	"inputs": []map[string]any{{"data": "\x19", "id": "tui.input.submit"}, {"data": "\r", "id": "tui.input.submit"}, {"data": "\n", "id": "tui.select.confirm"}, {"data": "\r", "id": "tui.select.confirm"}, {"data": "\x0b", "id": "x.probe.b"}},
}

func TestTuiStateAcrossSDKs(t *testing.T) {
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		// The state the host published first.
		r.setTuiState(tuiProbeTable("ctrl+y", "ctrl+j"), subprocess.TerminalCapabilitiesPayload{Images: "iterm2", TrueColor: true, Hyperlinks: true})
		report := r.toolWith("tui_probe", tuiProbeArgs)
		want := map[string]any{
			"keys": map[string]any{
				"tui.input.submit": []any{"ctrl+y"}, "tui.select.confirm": []any{"ctrl+j"},
				"x.probe.a": []any{"ctrl+k"}, "x.probe.b": []any{"ctrl+k"}, "x.probe.missing": []any{},
			},
			"matches": []any{true, false, true, false, true},
			"definitions": map[string]any{
				"tui.input.submit": "Submit input", "tui.select.confirm": "Confirm selection (host)", "x.probe.a": "Probe A", "x.probe.b": nil, "x.probe.missing": nil,
			},
			"user":        map[string]any{"tui.input.submit": []any{"ctrl+y"}, "x.probe.a": []any{"ctrl+k"}, "x.probe.b": []any{"ctrl+k"}},
			"conflicts":   []any{map[string]any{"key": "ctrl+k", "keybindings": []any{"x.probe.a", "x.probe.b"}}},
			"resolvedIds": []any{"tui.input.submit", "tui.select.confirm", "x.probe.a", "x.probe.b"},
			// TUI_KEYBINDINGS stays Pi's table, not the host's.
			"defaults":     map[string]any{"tui.input.submit": []any{"enter"}, "tui.select.confirm": []any{"enter"}, "count": float64(0)},
			"key":          map[string]any{"ctrl": "ctrl+x", "alt": "alt+enter", "shift": "shift+tab", "escape": "escape", "ctrlShift": "ctrl+shift+p"},
			"capabilities": map[string]any{"images": "iterm2", "trueColor": true, "hyperlinks": true},
			"colorMode":    "truecolor",
		}
		checkTuiProbe(t, report, want)
		if report["installed"] == nil || report["swapped"] == nil {
			t.Fatalf("no install/swap section: %v", report)
		}
		if !reflect.DeepEqual(report["swapped"], map[string]any{
			"capabilities": map[string]any{"images": "kitty", "trueColor": false, "hyperlinks": false}, "colorMode": "256color",
			"overridden": true, "detected": true,
			"restored": map[string]any{"images": "iterm2", "trueColor": true, "hyperlinks": true}, "kitty": []any{false, true, false},
		}) {
			t.Errorf("swapped = %v", report["swapped"])
		}
		if !reflect.DeepEqual(report["installed"], map[string]any{"keys": []any{"ctrl+q"}, "hostKeys": []any{}, "rebound": []any{"ctrl+w"}, "restored": []any{"ctrl+y"}}) {
			t.Errorf("installed = %v", report["installed"])
		}

		// A later publication replaces both.
		r.setTuiState(tuiProbeTable("ctrl+u", "ctrl+n"), subprocess.TerminalCapabilitiesPayload{Images: "kitty", TrueColor: false, Hyperlinks: false})
		second := r.toolWith("tui_probe", tuiProbeArgs)
		keys, _ := second["keys"].(map[string]any)
		if !reflect.DeepEqual(keys["tui.input.submit"], []any{"ctrl+u"}) || !reflect.DeepEqual(keys["tui.select.confirm"], []any{"ctrl+n"}) {
			t.Errorf("second keys = %v", keys)
		}
		if !reflect.DeepEqual(second["matches"], []any{false, false, false, false, true}) {
			t.Errorf("second matches = %v", second["matches"])
		}
		if !reflect.DeepEqual(second["capabilities"], map[string]any{"images": "kitty", "trueColor": false, "hyperlinks": false}) || second["colorMode"] != "256color" {
			t.Errorf("second capabilities = %v / %v", second["capabilities"], second["colorMode"])
		}
	})
}

func checkTuiProbe(t *testing.T, got, want map[string]any) {
	t.Helper()
	for key, expected := range want {
		if key == "defaults" {
			defaults, _ := got["defaults"].(map[string]any)
			if !reflect.DeepEqual(defaults["tui.input.submit"], expected.(map[string]any)["tui.input.submit"]) || !reflect.DeepEqual(defaults["tui.select.confirm"], expected.(map[string]any)["tui.select.confirm"]) {
				t.Errorf("defaults = %v, want %v", defaults, expected)
			}
			if count, _ := defaults["count"].(float64); count < 40 {
				t.Errorf("TUI_KEYBINDINGS has %v entries, want Pi's table", defaults["count"])
			}
			continue
		}
		if !reflect.DeepEqual(got[key], expected) {
			t.Errorf("%s = %v\nwant %v", key, got[key], expected)
		}
	}
}
