package codingagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// A frame requested before an awaited session operation is painted before the operation begins (Pi: the key press that closes the picker asks for a frame that runs on the
// next tick, tui.ts:1033-1041, inside the awaited runtime call). The recording terminal shows the order for a swap of 0 ms and of 50 ms: the frame with the old transcript
// and the selector gone is written before the session_before_fork handler runs, and the swap's own frames after it.
func TestFrameRequestedBeforeAwaitedSwapIsPaintedFirst(t *testing.T) {
	for _, swap := range []time.Duration{0, 50 * time.Millisecond} {
		t.Run(swap.String(), func(t *testing.T) {
			m, terminal := scrollbackMode(t)
			const selectorRow = "SELECTOR ROW OF THE FORK PICKER"
			// The picker took the editor slot and a frame showed it; closing it restores the editor and requests a frame, which the test's terminal does not throttle-paint.
			m.editorContainer.SetChildren(tui.NewText(selectorRow))
			m.tuiInst.Render()
			m.editorContainer.SetChildren(m.editor)
			m.tuiInst.RequestRender()
			terminal.Reset()

			var atHook string
			handle := m.opts.SessionHandle.(*beforeUIHandle)
			handle.runner = inproc.NewRunner([]extension.Extension{{Path: "slow", Handlers: map[string][]extension.HandlerFn{
				EventSessionBeforeFork: {func(...any) (any, error) {
					atHook = terminal.String()
					time.Sleep(swap)
					return nil, nil
				}},
			}}}, m.opts.CWD)

			sc := m.buildSlashContext(context.Background())
			for _, entry := range m.currentSession().GetEntries() {
				if entry.Base().Type == "message" {
					sc.Args = entry.Base().ID
					break
				}
			}
			if err := forkHandler(sc); err != nil {
				t.Fatal(err)
			}
			m.tuiInst.Render()
			if atHook == "" {
				t.Fatal("no frame was written before the session_before_fork handler ran: the requested frame waited for the swap")
			}
			if strings.Contains(atHook, selectorRow) || strings.Contains(atHook, "Forked to new session") {
				t.Errorf("the frame before the swap is not the old transcript with the selector gone:\n%q", atHook)
			}
			after := strings.TrimPrefix(terminal.String(), atHook)
			if !strings.Contains(after, "Forked to new session") {
				t.Errorf("the swap's frames did not follow the first frame:\n%q", after)
			}
		})
	}
}
