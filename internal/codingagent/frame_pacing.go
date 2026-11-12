package codingagent

import (
	"os"
	"reflect"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// framePacingEnv selects the frame pacing of one run and wins over the terminal.framePacing setting: "pi" or "0" is Pi's fixed interval, "adaptive" the adaptive policy, and any other value, the empty one included, counts as unset.
const framePacingEnv = "PIG_FRAME_PACING"

// adaptiveFramePacingSelected reports whether a run with this terminal.framePacing setting (Settings.GetTerminalFramePacing) and environment uses the adaptive frame pacing policy.
func adaptiveFramePacingSelected(setting string, getenv func(string) string) bool {
	switch getenv(framePacingEnv) {
	case TerminalFramePacingPi, "0":
		return false
	case TerminalFramePacingAdaptive:
		return true
	}
	return setting == TerminalFramePacingAdaptive
}

// installFramePacing gives the renderer the adaptive frame pacing policy when the run selects it (PIG_FRAME_PACING, else terminal.framePacing of the global settings file, else terminalFramePacingDefault, Pi's interval); otherwise the renderer keeps Pi's interval and nothing of the policy exists in the run. The selection is read once, at start: a settings change or a reload acts at the next start.
//
// pig additive (D625): Pi has one frame interval and no setting for it; a run that does not select the adaptive policy has Pi's.
func (m *InteractiveMode) installFramePacing() {
	renderer, ok := m.tuiInst.(interface{ SetFramePacing(*tui.FramePacing) })
	if !ok || m.framePacingInstalled || !adaptiveFramePacingSelected(m.opts.Settings.GetTerminalFramePacing(), os.Getenv) {
		return
	}
	m.framePacingInstalled = true
	renderer.SetFramePacing(tui.NewAdaptiveFramePacing(&m.framePacingAllowed))
}

// allowFramePacing opens or closes the adaptive policy's gate before a throttled frame is drawn, so the gate is never older than one frame. The owner loop calls it.
func (m *InteractiveMode) allowFramePacing() {
	if m.framePacingInstalled {
		m.framePacingAllowed.Store(m.replyStreamsUndisturbed())
	}
}

// replyStreamsUndisturbed reports whether an agent run is active, from its start to its end (runStreaming: a run that ends by an abort, an error or an empty queue is no longer active), no compaction is running, and no extension draws on the screen. Only the owner loop calls it.
//
// pig additive (D625): frames are paced only then, so an extension's component that counts frames, an idle screen and a compaction see Pi's interval.
func (m *InteractiveMode) replyStreamsUndisturbed() bool {
	return m.runStreaming() && !m.isCompacting && !m.extensionDrawsOnScreen()
}

// extensionDrawsOnScreen reports whether an extension's dialog, overlay, header, footer, editor or widget is mounted, any overlay is open, or the transcript holds a tool card, a message or an entry that an extension's renderer draws (extensionRendersTranscript). Only the owner loop calls it; the two locks are held for a few field reads.
func (m *InteractiveMode) extensionDrawsOnScreen() bool {
	if m.extensionRendersTranscript || len(m.extensionDialogs) > 0 || len(m.extensionOverlays) > 0 || m.tuiInst.HasOverlay() {
		return true
	}
	m.extComponentMu.Lock()
	mounted := m.extHeaderComponent != nil || m.extFooterComponent != nil || m.editorFactory != nil
	m.extComponentMu.Unlock()
	if mounted {
		return true
	}
	m.widgetMu.Lock()
	mounted = len(m.bridgeWidgets) > 0 || len(m.inprocessWidgets) > 0
	m.widgetMu.Unlock()
	return mounted
}

// extensionRenderersMounted notes that the transcript now holds something an extension's renderer draws: a tool card, a custom message or a custom entry. The gate stays closed from the next frame until the transcript is emptied (transcriptEmptied), which is when the last such component leaves the screen. Only the owner loop calls it.
//
// pig additive (D625): an extension's renderer runs once a frame, so it sees Pi's interval.
func (m *InteractiveMode) extensionRenderersMounted() {
	m.extensionRendersTranscript = true
}

// transcriptEmptied notes that the transcript holds nothing any more, so nothing an extension's renderer draws. Every site that empties the chat container calls it; what is rendered again afterwards is noted again. Only the owner loop calls it.
func (m *InteractiveMode) transcriptEmptied() {
	m.extensionRendersTranscript = false
}

// toolRenderersOfAnExtension reports whether resolved, the renderers a tool card is drawn with, come from an extension: the tool's own definition carries a call or result renderer (registered), or an extension's resolver answered with renderers that are not, or are no longer, the ones the base resolution returned (fromBase, and plain, its value at that moment).
func toolRenderersOfAnExtension(resolved, fromBase *extension.ToolRenderers, plain extension.ToolRenderers, registered bool) bool {
	if resolved == nil {
		return false
	}
	if registered || resolved != fromBase {
		return true
	}
	same := func(a, b any) bool { return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer() }
	return resolved.RenderShell != plain.RenderShell || !same(resolved.RenderCall, plain.RenderCall) || !same(resolved.RenderResult, plain.RenderResult)
}
