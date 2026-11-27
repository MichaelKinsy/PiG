package codingagent

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// Subprocess extension host calls (ui.setEditorText, ui.pasteToEditor) arrive
// on a host-call lane goroutine (coding/extension/host/subprocess/host_calls.go),
// while the interactive owner loop edits and renders the same tui.Editor
// (which has no mutex). Routing both methods through the owner loop prevents
// concurrent unsynchronized access to the editor and terminal renderer.
func TestExtUIEditorCallsFromHostCallGoroutine_NoRace(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir()})
	m.runCtx = ctx
	m.editor = tui.NewEditor()
	m.chatContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.tuiInst.Add(m.chatContainer)
	m.tuiInst.Add(m.editor)
	t.Cleanup(m.tuiInst.CancelPendingRender)

	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: m})

	const iters = 500
	done := make(chan struct{})
	var ownerWG sync.WaitGroup

	// Owner loop goroutine: drains posted UI tasks and handles typing + renders.
	ownerWG.Go(func() {
		typed := 0
		for {
			select {
			case fn := <-m.uiTaskCh:
				fn()
			case <-done:
				// Drain remaining tasks posted before shutdown.
				for {
					select {
					case fn := <-m.uiTaskCh:
						fn()
					default:
						return
					}
				}
			default:
				if typed%7 == 0 {
					m.editor.SetText("")
				}
				m.editor.HandleInput("x")
				m.tuiInst.Render()
				typed++
			}
		}
	})

	// Host-call lane goroutine: delivers ui.setEditorText and ui.pasteToEditor
	// through UIBridge.HandleCall, matching subprocess/host_calls.go drain().
	var callerWG sync.WaitGroup
	callerWG.Go(func() {
		setArgs, _ := json.Marshal(map[string]string{"text": "SET TEXT FROM TIMER"})
		pasteArgs, _ := json.Marshal(map[string]string{"text": "Hello from extension"})
		for i := range iters {
			method, args := "ui.setEditorText", setArgs
			if i%2 == 1 {
				method, args = "ui.pasteToEditor", pasteArgs
			}
			res, err := bridge.HandleCall("ext", &subprocess.CallPayload{
				Method: method,
				Args:   args,
			})
			if err != nil {
				t.Errorf("HandleCall(%s): %v", method, err)
				return
			}
			if res != nil && res.Error != nil {
				t.Errorf("HandleCall(%s) error: %+v", method, res.Error)
				return
			}
		}
	})

	callerWG.Wait()
	close(done)
	ownerWG.Wait()
}
