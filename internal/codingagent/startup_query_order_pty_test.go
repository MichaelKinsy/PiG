//go:build linux

package codingagent

import (
	"os"
	"strings"
	"testing"
)

const (
	startupQueryOrderChildEnv = "PIG_TEST_STARTUP_QUERY_ORDER_CHILD"
	startupQueryOrderModeEnv  = "PIG_TEST_STARTUP_QUERY_ORDER_MODE"
)

// Pi 1.2.0 writes the terminal color query before its first frame. TUI.start() only requests the first frame: requestRender()
// defers scheduleRender() through process.nextTick and the render timer (pi-tui tui.js start(), requestRender()). init() continues
// synchronously after ui.start() with isInitialized, programStatus.report(), ensurePngTranscoder() and
// themeController.applyFromSettings(), which writes OSC 10, OSC 11, OSC 4;0..15 and a DA1 sentinel, and the deferred frame runs only
// at `await waitForTerminalColors()` (interactive-mode.js init()). Fullscreen enters the alternate screen in ui.start(), before the
// query. Measured on a pty, Pi writes the query and then the frame's `\x1b[?2026h`; a client that types once the first frame shows
// therefore finds the color round trip already requested. Init runs for real on a pseudo-terminal that answers nothing.
func TestStartupWritesTheTerminalColorQueryBeforeTheFirstFrame(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(startupQueryOrderModeEnv, mode)
			out := string(runInitPTYChild(t, "TestStartupWritesTheTerminalColorQueryBeforeTheFirstFrameChild", startupQueryOrderChildEnv))
			query := strings.Index(out, "\x1b]11;?\x07")
			sentinel := strings.Index(out, "\x1b]4;15;?\x07\x1b[c")
			frame := strings.Index(out, "\x1b[?2026h")
			if query < 0 || sentinel < 0 || frame < 0 {
				t.Fatalf("startup wrote OSC 11 at %d, the DA1 sentinel at %d, the first frame at %d; want all three:\n%q", query, sentinel, frame, out)
			}
			if query > frame || sentinel > frame {
				t.Fatalf("the first frame (byte %d) precedes the terminal color query (OSC 11 at byte %d, DA1 sentinel at byte %d); Pi writes the query first", frame, query, sentinel)
			}
			if mode == "fullscreen" {
				if enter := strings.Index(out, "\x1b[?1049h"); enter < 0 || enter > query {
					t.Fatalf("fullscreen entered the alternate screen at byte %d, want it before the color query at byte %d", enter, query)
				}
			}
		})
	}
}

// TestStartupWritesTheTerminalColorQueryBeforeTheFirstFrameChild is the child half of TestStartupWritesTheTerminalColorQueryBeforeTheFirstFrame.
func TestStartupWritesTheTerminalColorQueryBeforeTheFirstFrameChild(t *testing.T) {
	report := os.Getenv(startupQueryOrderChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestStartupWritesTheTerminalColorQueryBeforeTheFirstFrame")
	}
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true, Settings: Settings{TuiMode: os.Getenv(startupQueryOrderModeEnv)}})
	// Without a Session, Init fails at the Session bind, after the color wait.
	if err := m.Init(t.Context()); err == nil {
		childFatalf(t, "Init without a SessionHandle succeeded")
	}
	if err := m.Stop(); err != nil {
		childFatalf(t, "%v", err)
	}
}
