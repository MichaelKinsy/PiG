package codingagent

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// Feed parsed terminal chunks through the real modal route, rather than calling
// dispatchKey from a UI task. The navigation must receive editor input and Esc
// while its Session call is still waiting, without a second stdin reader.
func TestTreeSummaryReceivesParsedTerminalInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := statusBorderMode(t, true)
		m.keybindings = DefaultKeybindingsManager()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		m.runCtx = ctx
		h := &lifecycleNavigationHandle{events: make(chan agent.AgentEvent, 1)}
		m.opts.SessionHandle = h
		m.eventCh = h.events
		h.navigate = func(navCtx context.Context) (NavigateTreeResult, error) {
			// inputLoop is suspended in the slash handler, so only the modal
			// route can accept these chunks from the process input pump.
			mainInput := make(chan inputChunk)
			m.routeInputChunk(ctx, []byte("x"), mainInput)
			m.routeInputChunk(ctx, []byte("\x1b"), mainInput)
			select {
			case <-navCtx.Done():
				return NavigateTreeResult{Aborted: true}, nil
			case <-ctx.Done():
				return NavigateTreeResult{}, errors.New("navigation stranded terminal input on the main loop")
			}
		}
		result, err := m.buildSlashContext(ctx).NavigateTreeFull(context.Background(), "target", true, "")
		if err != nil || !result.Aborted {
			t.Fatalf("terminal Esc result = %+v, %v", result, err)
		}
		if h.aborts != 1 || m.editor.Text() != "x" {
			t.Fatalf("terminal input: abort calls=%d editor=%q", h.aborts, m.editor.Text())
		}
		if route, _ := m.modalRoute(); route != nil {
			t.Fatal("completed navigation retained the modal input route")
		}
	})
}
