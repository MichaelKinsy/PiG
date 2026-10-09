package codingagent

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts:3107 (setupKeyHandlers; each renderer the driver creates carries it, as the switch at :920 does) `this.ui.onDebug = () => this.handleDebugCommand()` (setupKeyHandlers) and tui.ts:1076 handleTerminalInput: Shift+Ctrl+D is the
// TUI's global debug key. It reaches handleDebugCommand whatever has focus and is consumed before the focused component sees it.
func TestShiftCtrlDWritesTheDebugLogThroughTheTuisDebugKey(t *testing.T) {
	// Each start mode builds its own renderer in createInteractiveTui, and each one carries the callback.
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			m, ctx := newCustomEditorDispatchMode(t)
			m.opts.AgentDir = t.TempDir()
			m.opts.TuiMode = mode
			m.agent = mustNewAgent(agent.AgentOptions{})
			m.uiTaskCh = make(chan func(), 64)
			m.rendererOut = io.Discard
			m.createInteractiveTui(ctx)
			t.Cleanup(m.teardownCurrentTui)
			if got := m.tuiInst.Mode(); string(got) != mode {
				t.Fatalf("renderer mode = %q, want %q", got, mode)
			}

			const shiftCtrlD = "\x1b[100;6u"
			if err := m.dispatchKey(ctx, shiftCtrlD); err != nil {
				t.Fatal(err)
			}
			runPostedTasks(t, m)
			log, err := os.ReadFile(filepath.Join(m.opts.AgentDir, AppName+"-debug.log"))
			if err != nil || !strings.Contains(string(log), "=== All rendered lines with visible widths ===") {
				t.Fatalf("debug log %q, %v; want Shift+Ctrl+D to write it", log, err)
			}
			if m.editor.Text() != "" {
				t.Fatalf("the focused editor received the debug key: %q", m.editor.Text())
			}
		})
	}
}

// A frontend session replaces the renderer createInteractiveTui built before the first paint (openFrontend). Pi's renderer replacement carries the outgoing
// renderer's onDebug to the incoming one (interactive-mode.ts:900 `const onDebug = previousUi.onDebug`, :920 `nextUi.onDebug = onDebug`), so the debug key keeps
// working on the surface. setupKeyHandlers does not register it, so nothing else sets it there.
func TestShiftCtrlDWritesTheDebugLogUnderAFrontendSession(t *testing.T) {
	session := &fakeFrontendSession{}
	m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, "regular")
	if m.surface == nil || m.tuiInst != tui.TUI(m.surface) {
		t.Fatalf("renderer = %T, want the surface renderer", m.tuiInst)
	}
	m.opts.AgentDir = t.TempDir()
	m.agent = mustNewAgent(agent.AgentOptions{})
	m.keybindings = DefaultKeybindingsManager()
	m.defaultCustomEditor()

	const shiftCtrlD = "\x1b[100;6u"
	if !m.tuiInst.ConsumeDebugKey(shiftCtrlD) {
		t.Fatal("the frontend surface did not consume Shift+Ctrl+D: its onDebug is unset")
	}
	log, err := os.ReadFile(filepath.Join(m.opts.AgentDir, AppName+"-debug.log"))
	if err != nil || !strings.Contains(string(log), "=== All rendered lines with visible widths ===") {
		t.Fatalf("debug log %q, %v; want Shift+Ctrl+D to write it", log, err)
	}
}
