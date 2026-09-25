package codingagent

import (
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestWindowsAltVPasteImageReachable guards the byte->keyID resolution fix:
// paste-image is bound to alt+v on Windows (upstream keybindings.ts:112,
// process.platform === "win32" ? "alt+v" : "ctrl+v"), and the legacy alt+v
// encoding \x1bv must resolve to it. Before the union in Matches, the static
// keyIDInputs table had no generic "alt+printable = ESC+key" rule and no alt+v
// entry, so \x1bv resolved to nothing and paste-image was keyboard-unreachable
// on Windows. This test binds paste-image to alt+v directly (so it runs on any
// GOOS) and asserts every alt+v wire form classifies as the paste action.
func TestWindowsAltVPasteImageReachable(t *testing.T) {
	km := DefaultKeybindingsManager()
	km.SetUserBindings(map[string][]KeyID{"app.clipboard.pasteImage": {"alt+v"}})
	for _, enc := range []string{"\x1bv", "\x1b[118;3u", "\x1b[118;3:1u"} {
		if got := classifyKeyWithBindings(enc, km); got != actionPasteImage {
			t.Errorf("alt+v bound to paste-image: classifyKeyWithBindings(%q) = %d, want actionPasteImage (%d)", enc, got, actionPasteImage)
		}
	}
	// Guard against over-matching: ctrl+v must NOT resolve to paste-image when
	// only alt+v is bound (this is the Windows arrangement).
	if got := classifyKeyWithBindings("\x16", km); got == actionPasteImage {
		t.Errorf("ctrl+v (\\x16) must not resolve to paste-image when only alt+v is bound; got actionPasteImage")
	}
}

// TestPlatformDivergentControlKeys asserts the platform-correct classification
// of the keys whose bindings differ on Windows, so the Windows column is
// covered rather than assumed. Mirrors keybindings.ts: paste-image is alt+v
// with Windows keybindings (win32 and WSL; ctrl+v elsewhere), and suspend is
// unbound only on native win32 (no SIGTSTP).
func TestPlatformDivergentControlKeys(t *testing.T) {
	ctrlV, ctrlZ, altV := actionPasteImage, actionSuspend, actionInsert
	platform := tui.HostKeybindingPlatform()
	if platform.UsesWindowsKeybindings() {
		ctrlV, altV = actionInsert, actionPasteImage
	}
	if platform == tui.KeybindingPlatformWin32 {
		ctrlZ = actionInsert
	}
	cases := []struct {
		name string
		enc  string
		want keyAction
	}{
		{"ctrl+v", "\x16", ctrlV},
		{"ctrl+z", "\x1a", ctrlZ},
		{"alt+v", "\x1bv", altV},
	}
	// Upstream's Windows keybindings follow up with ctrl+q and cycle the
	// model backward with alt+p; elsewhere alt+enter and ctrl+shift+p.
	if platform.UsesWindowsKeybindings() {
		cases = append(cases,
			struct {
				name string
				enc  string
				want keyAction
			}{"ctrl+q follow-up", "\x11", actionFollowUp},
			struct {
				name string
				enc  string
				want keyAction
			}{"alt+p cycle model backward", "\x1bp", actionCycleModelBackward},
		)
	} else {
		cases = append(cases,
			struct {
				name string
				enc  string
				want keyAction
			}{"alt+enter legacy ESC CR follow-up", "\x1b\r", actionFollowUp},
			struct {
				name string
				enc  string
				want keyAction
			}{"ctrl+shift+p kitty cycle model backward", "\x1b[112;6u", actionCycleModelBackward},
			struct {
				name string
				enc  string
				want keyAction
			}{"ctrl+shift+p xterm cycle model backward", "\x1b[27;6;112~", actionCycleModelBackward},
		)
	}
	for _, c := range cases {
		if got := classifyKey(c.enc); got != c.want {
			t.Errorf("%s (%q) on %s: classifyKey = %d, want %d", c.name, c.enc, runtime.GOOS, got, c.want)
		}
	}
}

// Ports "Windows keybinding defaults › applies the detected defaults
// consistently" (coding-agent test/keybindings.test.ts) over every platform
// column instead of only the host's.
func TestKeybindingDefinitionsApplyPlatformDefaults(t *testing.T) {
	for _, platform := range tui.KeybindingPlatforms {
		defs := keybindingDefinitionsFor(platform)
		windows := platform.UsesWindowsKeybindings()
		pick := func(windowsKey, otherKey string) []string {
			if windows {
				return []string{windowsKey}
			}
			return []string{otherKey}
		}
		undo := []string{"ctrl+-"}
		switch platform {
		case tui.KeybindingPlatformWin32:
			undo = []string{"ctrl+z"}
		case tui.KeybindingPlatformLinuxWSL:
			undo = []string{"alt+z"}
		}
		for id, want := range map[string][]string{
			"app.clipboard.pasteImage": pick("alt+v", "ctrl+v"),
			"app.message.followUp":     pick("ctrl+q", "alt+enter"),
			"app.model.cycleBackward":  pick("alt+p", "shift+ctrl+p"),
			"app.message.dequeue":      pick("alt+q", "alt+up"),
			tui.KBEditorUndo:           undo,
		} {
			if got := defs[id].DefaultKeys; !slices.Equal(got, want) {
				t.Errorf("%s %s defaults = %v, want %v", platform, id, got, want)
			}
		}
	}
}

// The TUI registry receives the merged table, so a tui-package component can
// resolve an app action and its user override without the coding-agent
// manager.
func TestKeybindingsManagerInstallsAppDefinitionsInTUIRegistry(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })

	km := DefaultKeybindingsManager()
	if got := tui.GetTUIKeybindings().GetKeys("app.model.select"); !slices.Equal(got, []string{"ctrl+l"}) {
		t.Fatalf("TUI registry app.model.select = %v, want [ctrl+l]", got)
	}
	km.SetUserBindings(map[string][]KeyID{"app.model.select": {"ctrl+k"}, tui.KBEditorUndo: {"ctrl+z"}})
	km.syncToTUI()
	kb := tui.GetTUIKeybindings()
	if !kb.Matches("\x0b", "app.model.select") || kb.Matches("\x0c", "app.model.select") {
		t.Fatalf("TUI registry ignores the app.model.select override: %v", kb.GetKeys("app.model.select"))
	}
	if got := kb.GetKeys(tui.KBEditorUndo); !slices.Equal(got, []string{"ctrl+z"}) {
		t.Fatalf("TUI registry undo = %v, want [ctrl+z]", got)
	}
}
