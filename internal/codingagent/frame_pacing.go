package codingagent

import (
	"os"

	"github.com/MichaelKinsy/PiG/tui"
)

// framePacingEnv selects the frame pacing of one run and wins over the setting: "pi" or "0" is Pi's fixed interval, "adaptive" the adaptive policy, and any other value counts as unset.
const framePacingEnv = "PIG_FRAME_PACING"

const (
	framePacingPi       = "pi"
	framePacingAdaptive = "adaptive"
)

// adaptiveFramePacingSelected reports whether a run with this setting and environment uses the adaptive frame pacing policy.
func adaptiveFramePacingSelected(setting string, getenv func(string) string) bool {
	switch getenv(framePacingEnv) {
	case framePacingPi, "0":
		return false
	case framePacingAdaptive:
		return true
	}
	return setting == framePacingAdaptive
}

// installFramePacing gives the renderer the adaptive frame pacing policy when the run selects it; otherwise the renderer keeps Pi's interval and nothing of the policy exists in the run. The selection is read once, at start.
//
// pig divergence (D625): Pi has one frame interval and no setting for it.
func (m *InteractiveMode) installFramePacing() {
	renderer, ok := m.tuiInst.(interface{ SetFramePacing(*tui.FramePacing) })
	if !ok || m.framePacingInstalled || !adaptiveFramePacingSelected(framePacingAdaptive, os.Getenv) {
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
// pig divergence (D625): frames are paced only then, so an extension's component that counts frames, an idle screen and a compaction see Pi's interval.
func (m *InteractiveMode) replyStreamsUndisturbed() bool {
	return m.runStreaming() && !m.isCompacting && !m.extensionDrawsOnScreen()
}

// extensionDrawsOnScreen reports whether an extension's dialog, overlay, header, footer, editor or widget is mounted, or any overlay is open. Only the owner loop calls it; the two locks are held for a few field reads.
func (m *InteractiveMode) extensionDrawsOnScreen() bool {
	if len(m.extensionDialogs) > 0 || len(m.extensionOverlays) > 0 || m.tuiInst.HasOverlay() {
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
