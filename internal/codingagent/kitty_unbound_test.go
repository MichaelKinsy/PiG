package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// keybindingsManagerFor builds a default manager for one platform column, so
// a test asserts that column's keys on any host.
func keybindingsManagerFor(platform tui.KeybindingPlatform) *KeybindingsManager {
	km := DefaultKeybindingsManager()
	km.platform = platform
	km.definitions = appKeybindingDefinitionsFor(platform)
	km.rebuild()
	return km
}

// otherColumnKeys is the non-Windows default column the encoding tables assert.
func otherColumnKeys() *KeybindingsManager {
	return keybindingsManagerFor(tui.KeybindingPlatformLinux)
}

// Upstream CustomEditor.handleInput runs an app action only when
// keybindings.matches(data, action) is true; an unbound key goes to the
// editor. Kitty CSI-u and xterm modifyOtherKeys encodings of a key must
// follow the same bindings, so moving an action to another key (as the
// Windows and WSL columns do, or as a user can) unbinds the old key in every
// encoding.
func TestKittyEncodedKeysFollowAppBindings(t *testing.T) {
	km := DefaultKeybindingsManager()
	km.SetUserBindings(map[string][]KeyID{
		"app.message.followUp":     {"ctrl+q"},
		"app.message.dequeue":      {"alt+q"},
		"app.model.cycleBackward":  {"alt+p"},
		"app.clipboard.pasteImage": {"alt+v"},
		"app.suspend":              {},
		"app.tools.expand":         {"ctrl+g"},
		"app.editor.external":      {"ctrl+x"},
	})
	unbound := []struct {
		name string
		in   string
	}{
		{"kitty alt+enter", "\x1b[13;3u"},
		{"xterm alt+enter", "\x1b[27;3;13~"},
		{"kitty alt+up", "\x1b[1;3A"},
		{"kitty shift+ctrl+p", "\x1b[112;6u"},
		{"xterm shift+ctrl+p", "\x1b[27;6;112~"},
		{"kitty ctrl+v", "\x1b[118;5u"},
		{"kitty ctrl+z", "\x1b[122;5u"},
		{"kitty ctrl+o", "\x1b[111;5u"},
	}
	for _, tc := range unbound {
		if got := classifyKeyWithBindings(tc.in, km); got != actionInsert {
			t.Errorf("%s (%q) = %d, want actionInsert: the action was bound to another key", tc.name, tc.in, got)
		}
	}
	bound := []struct {
		name string
		in   string
		want keyAction
	}{
		{"ctrl+q follow-up", "\x11", actionFollowUp},
		{"kitty alt+p cycle backward", "\x1b[112;3u", actionCycleModelBackward},
		{"kitty ctrl+g tools", "\x1b[103;5u", actionToggleTools},
		{"kitty ctrl+x external editor", "\x1b[120;5u", actionExternalEditor},
	}
	for _, tc := range bound {
		if got := classifyKeyWithBindings(tc.in, km); got != tc.want {
			t.Errorf("%s (%q) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

// Each platform column binds its own keys, and only those keys, in every
// encoding: the Windows and WSL columns move follow-up to ctrl+q, dequeue to
// alt+q, cycle-backward to alt+p, and paste-image to alt+v, and native
// Windows leaves suspend unbound (core/keybindings.ts KEYBINDINGS).
func TestKittyEncodedAppKeysFollowEachPlatformColumn(t *testing.T) {
	for _, platform := range tui.KeybindingPlatforms {
		km := keybindingsManagerFor(platform)
		windows := platform.UsesWindowsKeybindings()
		pick := func(other, win keyAction) keyAction {
			if windows {
				return win
			}
			return other
		}
		suspend := actionSuspend
		if platform == tui.KeybindingPlatformWin32 {
			suspend = actionInsert
		}
		cases := []struct {
			name string
			in   string
			want keyAction
		}{
			{"kitty alt+enter", "\x1b[13;3u", pick(actionFollowUp, actionInsert)},
			{"kitty ctrl+q", "\x1b[113;5u", pick(actionInsert, actionFollowUp)},
			{"kitty alt+up", "\x1b[1;3A", pick(actionDequeue, actionInsert)},
			{"kitty alt+q", "\x1b[113;3u", pick(actionInsert, actionDequeue)},
			{"kitty shift+ctrl+p", "\x1b[112;6u", pick(actionCycleModelBackward, actionInsert)},
			{"kitty alt+p", "\x1b[112;3u", pick(actionInsert, actionCycleModelBackward)},
			{"kitty Cyrillic shift+ctrl+p", "\x1b[1079:1047:112;6u", pick(actionCycleModelBackward, actionInsert)},
			{"kitty Cyrillic alt+p", "\x1b[1079::112;3u", pick(actionInsert, actionCycleModelBackward)},
			{"kitty ctrl+v", "\x1b[118;5u", pick(actionPasteImage, actionInsert)},
			{"kitty alt+v", "\x1b[118;3u", pick(actionInsert, actionPasteImage)},
			{"kitty ctrl+z", "\x1b[122;5u", suspend},
			{"kitty ctrl+c", "\x1b[99;5u", actionClearEditor},
		}
		for _, tc := range cases {
			if got := classifyKeyWithBindings(tc.in, km); got != tc.want {
				t.Errorf("%s: %s (%q) = %d, want %d", platform, tc.name, tc.in, got, tc.want)
			}
		}
	}
}
