//go:build linux

package codingagent

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

const initStateChildEnv = "PIG_TEST_INIT_STATE_CHILD"

// interactive-mode.ts:943-1100 init(): everything before the Session bind runs in order and leaves the interactive state the later steps read: the startup marks in
// order, the TUI with its raw mode and hardware-cursor settings, the keybindings, the header/footer/chat containers, the editor focused in its container, the
// footer with the working directory and the auto-compact flag, the slash registry, and the extension context bound to this run's abort signal. Without a
// Session the bind fails after those steps, which is how this check reaches them in a unit test.
func TestInitBuildsTheInteractiveStateBeforeTheSessionBind(t *testing.T) {
	runInitPTYChild(t, "TestInitBuildsTheInteractiveStateBeforeTheSessionBindChild", initStateChildEnv)
}

func TestInitBuildsTheInteractiveStateBeforeTheSessionBindChild(t *testing.T) {
	report := os.Getenv(initStateChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestInitBuildsTheInteractiveStateBeforeTheSessionBind")
	}
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	cwd := t.TempDir()
	on, off := true, false
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: cwd, AgentDir: t.TempDir(), NoThemes: true, Settings: Settings{ShowHardwareCursor: &on, ClearOnShrink: &on, Compaction: &CompactionSettingsJSON{Enabled: &off}}})
	m.runEnded.Store(true)
	var marks []string
	var headerPendingAtTuiStart bool
	var textAtTuiStart string
	m.opts.StartupMark = func(label string) {
		marks = append(marks, label)
		if label == "tui-started" {
			headerPendingAtTuiStart = m.startupHeaderPending
			// Until startup completes the editor takes text but a submission only reports that startup is in progress (handleStartupSubmit).
			m.editor.OnSubmit("typed early")
			textAtTuiStart = m.editor.Text()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := m.Init(ctx)
	if err == nil {
		childFatalf(t, "Init without a SessionHandle succeeded")
	}
	wantMarks := []string{"theme-detected", "pre-raw-mode", "raw-mode-entered", "tui-layout-built", "tui-started", "terminal-colors-ready"}
	if !slices.Equal(marks, wantMarks) {
		childFatalf(t, "startup marks = %q, want %q", marks, wantMarks)
	}
	if !m.isInitialized || m.runCtx != ctx || m.backgroundCtx == nil || m.backgroundCtx.Err() != nil {
		childFatalf(t, "run state: initialized %v, runCtx is ctx %v, backgroundCtx %v", m.isInitialized, m.runCtx == ctx, m.backgroundCtx)
	}
	if m.runEnded.Load() {
		childFatalf(t, "Init left runEnded set from the previous run")
	}
	if m.tuiInst == nil || m.rawRestore == nil || m.rawDrain == nil || m.keybindings == nil {
		childFatalf(t, "terminal state: tui %v rawRestore %v rawDrain %v keybindings %v", m.tuiInst != nil, m.rawRestore != nil, m.rawDrain != nil, m.keybindings != nil)
	}
	if !m.tuiInst.GetShowHardwareCursor() || !m.tuiInst.GetClearOnShrink() {
		childFatalf(t, "TUI settings: hardware cursor %v, clear on shrink %v, want both from the settings", m.tuiInst.GetShowHardwareCursor(), m.tuiInst.GetClearOnShrink())
	}
	if !headerPendingAtTuiStart {
		childFatalf(t, "the started TUI carried the startup header; Pi mounts it after the terminal colors")
	}
	if textAtTuiStart != "typed early" {
		childFatalf(t, "a submission during startup left the editor text %q, want it kept", textAtTuiStart)
	}
	if m.editor.OnSubmit == nil {
		childFatalf(t, "the editor has no submit handler once startup completed")
	}
	m.editor.SetText("")
	m.editor.OnSubmit("   ")
	if got := m.editor.Text(); got == "   " {
		childFatalf(t, "the startup submit handler is still installed after startup completed (editor text %q)", got)
	}
	if m.tuiInst.GetFocusedComponent() != tui.Component(m.editor) {
		childFatalf(t, "focus is on %T, want the editor", m.tuiInst.GetFocusedComponent())
	}
	if m.statusLine.autoCompactEnabled || m.statusLine.cwd != cwd {
		childFatalf(t, "footer: autoCompact %v cwd %q, want false from the settings and %q", m.statusLine.autoCompactEnabled, m.statusLine.cwd, cwd)
	}
	if m.startupHeaderPending {
		childFatalf(t, "the startup header is still pending after the terminal colors were awaited")
	}
	if m.extHeader == nil || m.extFooter == nil || m.loadedResourcesContainer == nil || m.chatContainer == nil || m.statusContainer == nil ||
		m.pendingMessagesContainer == nil || m.widgetContainer == nil || m.editorContainer == nil || m.editor == nil || m.statusLine == nil {
		childFatalf(t, "a layout piece was not built")
	}
	if !slices.Contains(m.editorContainer.Children(), tui.Component(m.editor)) {
		childFatalf(t, "the editor is not in the editor container")
	}
	// interactive-mode.ts:995 ui.setFocus(this.editor).
	if !m.editor.Focused {
		childFatalf(t, "the editor does not hold the TUI focus")
	}
	// The footer shows the working directory and the auto-compact flag (interactive-mode.ts:657-658); the settings turn compaction off, against the footer's default.
	if m.statusLine.cwd != cwd || m.statusLine.autoCompactEnabled {
		childFatalf(t, "footer: cwd %q auto-compact %v, want %q and false", m.statusLine.cwd, m.statusLine.autoCompactEnabled, cwd)
	}
	if m.slashRegistry == nil || m.abortCtx == nil || m.abortFn == nil || m.extCtx == nil {
		childFatalf(t, "extension state: registry %v abort %v/%v ctx %v", m.slashRegistry != nil, m.abortCtx != nil, m.abortFn != nil, m.extCtx != nil)
	}
	if !m.extCtx.HasUI || m.extCtx.CWD != cwd || m.extCtx.AbortSignal != m.abortCtx || m.extCtx.IsIdle == nil || m.extCtx.IsProjectTrusted == nil {
		childFatalf(t, "extension context = %+v, want a UI context for %s bound to the run's abort signal", m.extCtx, cwd)
	}
	m.abortFn()
	if m.extCtx.AbortSignal.Err() == nil {
		childFatalf(t, "the context's abort function does not cancel its signal")
	}
	background := m.backgroundCtx
	if err := m.Stop(); err != nil {
		childFatalf(t, "%v", err)
	}
	if background.Err() == nil {
		childFatalf(t, "Stop did not cancel the background context")
	}
	if m.isInitialized || m.backgroundCtx != nil {
		childFatalf(t, "Stop left initialized %v backgroundCtx %v", m.isInitialized, m.backgroundCtx)
	}
}
