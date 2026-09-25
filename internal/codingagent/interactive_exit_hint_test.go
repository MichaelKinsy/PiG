package codingagent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's shutdown stops the renderer and restores cooked mode before writing one
// resume hint. Drive both the key handler and the loop's exit branch: testing
// either alone misses the duplicate Ctrl+D hint.
func TestInteractiveQuitPrintsOneHintAfterTerminalRestore(t *testing.T) {
	for _, key := range []string{"\x04", "\x03\x03", "extension"} {
		t.Run(fmt.Sprintf("%q", key), func(t *testing.T) {
			session, err := tempSessionMgr(t).Create("exit-regression", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.AppendMessage(mkAssistantMsg("finished")); err != nil {
				t.Fatal(err)
			}
			m := &InteractiveMode{
				opts:    InteractiveOptions{SessionHandle: &recordingCompactHandle{inner: session}},
				tuiInst: tui.NewWithOutput(io.Discard, 80, 24),
				editor:  tui.NewEditor(), keybindings: DefaultKeybindingsManager(), isIdle: true,
			}
			input, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = input.Close() }()
			previous := os.Stdin
			os.Stdin = input
			defer func() { os.Stdin = previous }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			restores := 0
			got := captureStdout(t, func() {
				m.rawRestore = func() {
					restores++
					fmt.Print("<cooked>\n")
				}
				if key == "extension" {
					m.requestShutdown()
				} else {
					for _, ch := range key {
						if err := m.dispatchKey(ctx, string(ch)); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := m.inputLoop(ctx, os.Stdin); err != nil {
					t.Fatal(err)
				}
				m.stopInteractiveTui() // Run's defer must be a no-op.
			})
			if count := strings.Count(got, "To resume this session:"); count != 1 {
				t.Errorf("resume hints = %d, want 1: %q", count, got)
			}
			if restores != 1 || !strings.HasPrefix(got, "<cooked>\n") {
				t.Errorf("hint precedes cooked-mode restore (restores=%d): %q", restores, got)
			}
			want := "<cooked>\n\x1b[2mTo resume this session:\x1b[22m pig --session exit-regression\n"
			if got != want {
				t.Errorf("exit hint bytes = %q, want Pi's terminal-faint label %q", got, want)
			}
			if m.rawRestore != nil {
				t.Error("raw restore remains armed after shutdown")
			}
		})
	}
}
