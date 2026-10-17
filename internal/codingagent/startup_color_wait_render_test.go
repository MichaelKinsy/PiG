package codingagent

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi starts the TUI before it awaits waitForTerminalColors (interactive-mode.ts init(): ui.start(), applyFromSettings(), await
// waitForTerminalColors()), so a key typed while a silent terminal holds startup for up to 100 ms reaches the editor and is painted at
// once: the editor's change requests a render, which runs while init() awaits. PiG starts the renderer before that wait too, so its wait
// loop must paint early input as well, not keep it off screen until the colors arrive.
func TestEarlyInputIsPaintedWhileStartupWaitsForTheTerminalColors(t *testing.T) {
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	var out bytes.Buffer
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{CWD: t.TempDir(), Model: model, AgentDir: t.TempDir(), Settings: Settings{TuiMode: "regular"}}, &out)
	m.mountInteractiveTui(true)
	if strings.Contains(out.String(), "zq") {
		t.Fatal("the first paint already shows the text typed later")
	}
	colors := make(chan struct{})
	m.themeState.colorQuery = colors
	m.inputReadCh = make(chan inputChunk)
	m.uiTaskCh = make(chan func())
	painted := make(chan bool, 1)
	go func() {
		m.inputReadCh <- inputChunk{data: []byte("zq")}
		// The loop takes this task only after it handled the input.
		m.uiTaskCh <- func() {
			painted <- strings.Contains(out.String(), "zq")
			close(colors)
		}
	}()
	if err := m.waitForTerminalColors(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.editor.Text(); got != "zq" {
		t.Fatalf("editor = %q, want the early text", got)
	}
	if !<-painted {
		t.Fatal("text typed while startup waited for the terminal's colors was not painted until the colors arrived")
	}
}

// Pi awaits waitForTerminalColors() on its event loop (interactive-mode.js init()), so a render requested while startup waits for the
// terminal's colors runs during the wait: Pi's deferred first frame (tui.js start() requests it) and a throttled frame after a change
// alike. A terminal that answers nothing holds the wait for terminalColorQueryTimeout; the requested frame must paint before that ends.
func TestRenderRequestedWhileStartupWaitsForTheTerminalColorsPaints(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, term := newTickRenderProbe(t, mode)
				time.Sleep(terminalColorQueryTimeout)
				synctest.Wait()
				m.drainMainLoopOnce()
				term.take()
				m.editor.SetText("zq")
				m.tuiInst.RequestRender()
				colors := make(chan struct{})
				m.themeState.colorQuery = colors
				painted := make(chan bool, 1)
				go func() {
					time.Sleep(terminalColorQueryTimeout)
					m.uiTaskCh <- func() {
						painted <- strings.Contains(widthx.StripAnsi(term.take()), "zq")
						close(colors)
					}
				}()
				if err := m.waitForTerminalColors(t.Context()); err != nil {
					t.Fatal(err)
				}
				if !<-painted {
					t.Fatal("a render requested while startup waited for the terminal's colors did not paint until the colors arrived")
				}
			})
		})
	}
}

// TUI.start requests the first frame, and Pi's event loop runs it while init() awaits the terminal colors (tui.js start(), requestRender()).
// A terminal that answers at once can end PiG's wait before the owner loop ran that frame. The wait must still paint it before it returns:
// Init does not serve the owner loop again until it returns, so the first frame would otherwise wait for the rest of startup.
func TestStartupColorWaitPaintsTheRequestedFirstFrameBeforeItReturns(t *testing.T) {
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			var out bytes.Buffer
			m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{CWD: t.TempDir(), Model: model, AgentDir: t.TempDir(), Settings: Settings{TuiMode: mode}}, &out)
			t.Cleanup(m.teardownCurrentTui)
			m.mountInteractiveTui(false)
			m.startInteractiveTui()
			colors := make(chan struct{})
			close(colors)
			m.themeState.colorQuery = colors
			if err := m.waitForTerminalColors(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "\x1b[?2026h") {
				t.Fatalf("the startup color wait returned before the first frame TUI.start requested was painted:\n%q", out.String())
			}
		})
	}
}
