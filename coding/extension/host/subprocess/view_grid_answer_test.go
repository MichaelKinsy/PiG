package subprocess

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// pig additive (D614): a ui.view.mount or ui.view.update whose grid the record refuses, made by an intent's handler
// inside its answer, leaves nothing of the intent's ticket behind. handleViewCall reads the ticket of the answering
// intent (answeringTicket) and then checks the grids: the read stores nothing, and a refused call never reaches the
// registry. So the table of unanswered intents holds the one intent while its handler runs and nothing once it has
// answered, the refused calls change no mount, and a legal update the same handler makes after them still belongs to the
// answer: the intent ends done, not stale.
func TestRefusedGridInsideAnAnswerLeavesNoTicketBehind(t *testing.T) {
	b := NewUIBridge(func() {})
	t.Cleanup(b.StopFrontendPushes)
	ab := uiview.ClientAbilities{Slots: []uiview.Slot{uiview.SlotPanel}, Nodes: []string{"text", "grid"},
		Limits: uiview.Limits{Nodes: 8, Depth: 4, SnapshotBytes: 4096, TextBytes: 256, Mounts: 4, IntentArgBytes: 16},
		Input:  uiview.Input{Keyboard: true}}
	if err := b.SetClientAbilities(&ab); err != nil {
		t.Fatal(err)
	}
	conn, peer := newResponsePeer(t, false)
	b.RegisterExtConn("ext", conn, false)
	tickets := func() (n int) {
		b.viewAnswers.Range(func(any, any) bool { n++; return true })
		return n
	}
	viewCall := func(method, parent, args string) *CallResultPayload {
		res, err := b.HandleCall("ext", &CallPayload{Method: method, ParentRequestID: parent, Args: json.RawMessage(args)})
		if err != nil {
			return &CallResultPayload{Error: &ErrorInfo{Code: "call failed", Message: err.Error()}}
		}
		return res
	}
	// One column past the record's sixteen.
	const badGrid = `{"kind":"grid","id":"g","columns":17}`
	res := viewCall(CallUIViewMount, "", `{"key":"k","slot":"panel","view":{"root":{"kind":"text","text":"x"}},"intents":[{"name":"ext.go","arg":{"type":"integer"},"answer":{"type":"integer"}}]}`)
	if res.Error != nil {
		t.Fatalf("mount = %+v", res.Error)
	}
	var st uiview.Status
	if err := json.Unmarshal(res.Result, &st); err != nil {
		t.Fatal(err)
	}
	ref, err := json.Marshal(st.Mount)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	type handled struct {
		during                      int
		badUpdate, badMount, redraw *CallResultPayload
	}
	done := make(chan handled, 1)
	// The extension's side: its handler of the intent makes three view calls inside the request, then answers it.
	go func() {
		var h handled
		var request struct {
			ID      string `json:"id"`
			Request struct {
				Method string `json:"method"`
			} `json:"request"`
		}
		for request.Request.Method != RequestUIViewIntent {
			select {
			case frame := <-peer.requests:
				_ = json.Unmarshal(frame, &request)
			case <-ctx.Done():
				done <- h
				return
			}
		}
		h.badUpdate = viewCall(CallUIViewUpdate, request.ID, `{"mount":`+string(ref)+`,"view":{"root":`+badGrid+`}}`)
		h.badMount = viewCall(CallUIViewMount, request.ID, `{"key":"k","slot":"panel","view":{"root":`+badGrid+`}}`)
		h.during = tickets()
		h.redraw = viewCall(CallUIViewUpdate, request.ID, `{"mount":`+string(ref)+`,"view":{"root":{"kind":"text","text":"redrawn"}}}`)
		done <- h
		peer.writeFrame(sdkFrame(request.ID, `{"outcome":"done","answer":1}`))
	}()
	result := b.InvokeViewIntent(ctx, "ext", st.Mount, "", "ext.go", json.RawMessage(`1`))
	h := <-done
	if h.redraw == nil {
		t.Fatalf("the intent's request did not reach the extension: %+v", result)
	}
	for name, refused := range map[string]*CallResultPayload{"update": h.badUpdate, "mount": h.badMount} {
		if refused.Error == nil || refused.Error.Code != string(uiview.CodeInvalid) {
			t.Errorf("a %s with a grid of 17 columns, inside the answer = %+v, want an error of code invalid", name, refused)
		}
	}
	if h.during != 1 {
		t.Errorf("unanswered intents after the refusals, while the handler ran = %d, want the one intent", h.during)
	}
	if h.redraw.Error != nil {
		t.Fatalf("the handler's legal update after the refusals = %+v", h.redraw.Error)
	}
	if result.Outcome != uiview.OutcomeDone || string(result.Answer) != "1" {
		t.Errorf("the intent = %+v, want done: the update its handler made after the refusals belongs to the answer", result)
	}
	if n := tickets(); n != 0 {
		t.Errorf("unanswered intents after the answer = %d, want none", n)
	}
	snaps := b.Views().Snapshots()
	if len(snaps) != 1 || snaps[0].Mount.Rev != st.Mount.Rev+1 || !strings.Contains(string(snaps[0].View), "redrawn") {
		t.Errorf("mounts after the answer = %+v, want the one mount, redrawn once", snaps)
	}
}
