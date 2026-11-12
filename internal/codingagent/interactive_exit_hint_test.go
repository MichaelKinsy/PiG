package codingagent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func setStdoutTTY(t *testing.T, value bool) {
	t.Helper()
	previous := stdoutIsTTY
	stdoutIsTTY = func() bool { return value }
	t.Cleanup(func() { stdoutIsTTY = previous })
}

func runShutdownInputLoop(t *testing.T, m *InteractiveMode, source io.Reader) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer func() {
		cancel()
		// The tests call the inner loop directly and own its pump lifetime.
		for range m.inputReadCh {
		}
	}()
	return m.inputLoop(ctx, source)
}

// Pi's shutdown stops the renderer and restores cooked mode before writing one
// resume hint. Drive both the key handler and the loop's exit branch: testing
// either alone misses the duplicate Ctrl+D hint.
func TestInteractiveQuitPrintsOneHintAfterTerminalRestore(t *testing.T) {
	// upstream: packages/coding-agent/test/suite/regressions/5080-signal-shutdown-extension-cleanup.test.ts:143
	setStdoutTTY(t, true)
	for _, key := range []string{"\x04", "\x03\x03", "extension"} {
		t.Run(fmt.Sprintf("%q", key), func(t *testing.T) {
			// The faint label follows chalk's stdout level; FORCE_COLOR pins it to 1 whatever the test's stdout is.
			t.Setenv("FORCE_COLOR", "1")
			session, err := tempSessionMgr(t).Create("exit-regression", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.AppendMessage(mkAssistantMsg("finished")); err != nil {
				t.Fatal(err)
			}
			m := &InteractiveMode{
				opts:    InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: session}},
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
				if err := runShutdownInputLoop(t, m, os.Stdin); err != nil {
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

// exitScreenSession draws its exit screen to the terminal when it closes.
type exitScreenSession struct{ *fakeFrontendSession }

func (s exitScreenSession) Close() error {
	fmt.Print("<frontend exit screen>\n")
	return s.fakeFrontendSession.Close()
}

// pig additive (D91): Pi has no frontend member. A frontend that owns the
// screen owns its exit screen, so the host writes no resume hint after it.
func TestFrontendExitWritesNoResumeHint(t *testing.T) {
	setStdoutTTY(t, true)
	t.Setenv("FORCE_COLOR", "1")
	session, err := tempSessionMgr(t).Create("exit-frontend", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(mkAssistantMsg("finished")); err != nil {
		t.Fatal(err)
	}
	fe := &fakeFrontend{session: &fakeFrontendSession{}}
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{
		Frontend: &exitScreenFrontend{fakeFrontend: fe}, TuiMode: "regular", CWD: t.TempDir(),
		SessionHandle: &recordingCompactHandle{inner: session},
	}, io.Discard)
	m.rendererOut = nil
	m.openFrontend()
	if m.surface == nil {
		t.Fatal("no surface renderer")
	}
	m.rawRestore = func() { fmt.Print("<cooked>\n") }
	got := captureStdout(t, func() {
		if err := m.finishInteractiveShutdown(); err != nil {
			t.Fatal(err)
		}
	})
	if want := "<frontend exit screen>\n<cooked>\n"; got != want {
		t.Errorf("terminal output = %q, want only the frontend exit screen then cooked restore %q", got, want)
	}
}

type exitScreenFrontend struct{ *fakeFrontend }

func (f *exitScreenFrontend) Open(env frontend.Env) (frontend.Session, error) {
	s, err := f.fakeFrontend.Open(env)
	if err != nil {
		return nil, err
	}
	return exitScreenSession{s.(*fakeFrontendSession)}, nil
}
