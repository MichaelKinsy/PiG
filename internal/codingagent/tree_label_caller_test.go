package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi interactive-mode.ts:5527 persists the label supplied by the shared LabelInput; tree-selector.ts:1406 updates the displayed node first.
func TestTreeLabelInputUsesProductionPersistence(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	DefaultKeybindingsManager().syncToTUI()
	session := upstreamTreeSession(t, "asst-1", treeUser("user-1", "", "hello"), treeAssistant("asst-1", "user-1", "message body"))
	if _, err := session.AppendLabelChange("asst-1", new("checkpoint")); err != nil {
		t.Fatal(err)
	}
	base := tui.NewWithOutput(io.Discard, 120, 40)
	base.SetRenderDispatcher(func(func()) {})
	t.Cleanup(base.CancelPendingRender)
	painted := false
	renderer := &treeCaptureRenderer{TuiMainScreen: base, capture: func() {
		if overlay := base.ActiveOverlay(); overlay != nil && strings.Contains(stripANSITest(strings.Join(overlay.Render(120), "\n")), "[Xcheckpoint]") {
			painted = true
		}
	}}
	m := &InteractiveMode{opts: InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: session}}, tuiInst: renderer, chatContainer: tui.NewContainer(), modalInputCh: make(chan []byte, 4)}
	for _, key := range []string{"L", "X", "\r", "\x1b"} {
		m.modalInputCh <- []byte(key)
	}
	if _, selected := m.buildSlashContext(t.Context()).PickTreeEntry(""); selected {
		t.Fatal("label save and cancel selected a branch")
	}
	if got := session.GetTree()[0].Children[0].Label; got != "Xcheckpoint" {
		t.Fatalf("persisted label=%q, want Xcheckpoint", got)
	}
	if !painted {
		t.Fatal("updated label did not paint before the tree closed")
	}
}
