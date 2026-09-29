package codingagent

import (
	"os"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func BenchmarkActiveKeyDiagnostics(b *testing.B) {
	b.Setenv("PIG_DEBUG", "") // Exercise classification without writing the global diagnostic file.
	previous := tui.GetTUIKeybindings()
	b.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	bindings := DefaultKeybindingsManager()
	bindings.SetUserBindings(map[string][]KeyID{tui.KBInputSubmit: {"ctrl+s"}})
	b.ReportAllocs()
	for b.Loop() {
		for _, key := range []string{"/", "l", "o", "g", "i", "n", "\x13", "\x1b[115;5u"} {
			debugLog("key %q -> %d", key, classifyKeyWithBindings(key, bindings))
		}
	}
}

// Pi CustomEditor.handleInput delegates ordinary input to Editor, whose configured tui.input.submit binding invokes onSubmit. Diagnostics must not replace that installed manager.
func TestTerminalInputPreservesRemappedSubmitBindings(t *testing.T) {
	t.Setenv("PIG_DEBUG", "")
	for _, debug := range []string{"", "1"} {
		t.Run("debug="+debug, func(t *testing.T) {
			t.Setenv("PIG_DEBUG_KEYS", debug)
			previous := tui.GetTUIKeybindings()
			t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
			q := newInputQueueMode(t)
			if err := os.WriteFile(KeybindingsFile(q.opts.AgentDir), []byte(`{"tui.input.submit":["ctrl+s"]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			q.keybindings = NewKeybindingsManager(q.opts.AgentDir)
			if keys := tui.GetTUIKeybindings().GetKeys(tui.KBInputSubmit); !slices.Equal(keys, []string{"ctrl+s"}) {
				t.Fatalf("fixture did not load submit override: %q", keys)
			}
			q.editor.SetAutocomplete(tui.NewSlashOnlyProvider([]tui.SlashCommand{{Name: "login", Description: "Configure provider authentication"}}))
			submitted := make(chan string, 2)
			q.editor.OnSubmit = func(text string) { submitted <- text }
			seen := watchTerminalInput(q.InteractiveMode)
			q.start(t)
			q.typeKeys(t, "/login\x13~")
			for _, key := range "/login\x13~" {
				expectSeen(t, seen, string(key))
			}
			keys := runOnQueueLoop(t, q.InteractiveMode, func() []string {
				return tui.GetTUIKeybindings().GetKeys(tui.KBInputSubmit)
			})
			if !slices.Equal(keys, []string{"ctrl+s"}) {
				t.Errorf("input replaced the configured submit binding: %q", keys)
			}
			select {
			case text := <-submitted:
				if text != "/login" {
					t.Fatalf("submitted %q, want /login", text)
				}
			default:
				t.Fatal("raw 0x13 did not submit /login through the actual input loop")
			}
			select {
			case extra := <-submitted:
				t.Fatalf("raw 0x13 submitted more than once: %q", extra)
			default:
			}
		})
	}
}
