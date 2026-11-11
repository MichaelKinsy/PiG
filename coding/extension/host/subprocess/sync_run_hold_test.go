package subprocess

import (
	"encoding/json"
	"testing"
)

// Pi's registerProvider and addAutocompleteProvider return nothing (types.ts registerProvider, addAutocompleteProvider), so the handler that calls
// them has not awaited: they run off their lane (startsAsync) but must not end the hold the handler's triggering send set.
func TestSyncRunHoldEndsOnlyAtACallPiAwaits(t *testing.T) {
	for _, method := range []string{"registerProvider", "ui.addAutocompleteProvider"} {
		if !startsAsync(method) {
			t.Fatalf("%s no longer runs off its lane, so this test no longer separates returnsPromiseInPi from startsAsync", method)
		}
	}
	conn := &Conn{requestStates: map[string]requestStateSink{"req-1": {event: "session_start"}}}
	h := &Host{}
	args, _ := json.Marshal(map[string]any{"message": map[string]any{"customType": "x"}, "options": map[string]any{"triggerTurn": true}})
	h.holdAfterSend(conn, &CallPayload{Method: "sendMessage", ParentRequestID: "req-1", Args: args})
	if !h.syncRuns.busy() {
		t.Fatal("a triggering send from a handler in flight did not hold")
	}
	for _, method := range []string{"registerProvider", "ui.addAutocompleteProvider", "setSessionName", "ui.notify"} {
		h.endSyncRunHold(conn, &CallPayload{Method: method, ParentRequestID: "req-1"})
		if !h.syncRuns.busy() {
			t.Fatalf("%s ended the hold, but Pi's API behind it returns nothing", method)
		}
	}
	h.endSyncRunHold(conn, &CallPayload{Method: "waitForIdle", ParentRequestID: "req-2"})
	if !h.syncRuns.busy() {
		t.Fatal("a call of another handler ended the hold")
	}
	h.endSyncRunHold(conn, &CallPayload{Method: "waitForIdle", ParentRequestID: "req-1"})
	if h.syncRuns.busy() {
		t.Fatal("waitForIdle, which Pi awaits, did not end the hold")
	}
}

// A send the host fails holds nothing and a message Pi holds back, or queues for the next turn, does not hold: only the send that marks the Session busy.
func TestSyncRunHoldIsSetOnlyByASendThatMarksTheSessionBusy(t *testing.T) {
	conn := &Conn{requestStates: map[string]requestStateSink{"req-1": {event: "session_start"}, "req-settled": {event: "agent_settled"}}}
	send := func(parent, options string) bool {
		h := &Host{}
		args := json.RawMessage(`{"message":{"customType":"x"},"options":` + options + `}`)
		h.holdAfterSend(conn, &CallPayload{Method: "sendMessage", ParentRequestID: parent, Args: args})
		return h.syncRuns.busy()
	}
	for _, tc := range []struct {
		name, parent, options string
		want                  bool
	}{
		{"triggering send from a handler", "req-1", `{"triggerTurn":true}`, true},
		{"no trigger", "req-1", `{}`, false},
		{"trigger false", "req-1", `{"triggerTurn":false}`, false},
		{"next turn", "req-1", `{"triggerTurn":true,"deliverAs":"nextTurn"}`, false},
		{"sent from agent_settled", "req-settled", `{"triggerTurn":true}`, false},
		{"from a thread after its request returned", "req-gone", `{"triggerTurn":true}`, false},
	} {
		if got := send(tc.parent, tc.options); got != tc.want {
			t.Errorf("%s: held = %t, want %t", tc.name, got, tc.want)
		}
	}
	// A runtime with an event loop names the send: its callback holds with no request in flight, a handler in agent_settled still does not.
	h := &Host{}
	args := json.RawMessage(`{"message":{"customType":"x"},"options":{"triggerTurn":true},"yield_token":"y1"}`)
	h.holdAfterSend(conn, &CallPayload{Method: "sendMessage", Args: args})
	if !h.syncRuns.busy() {
		t.Fatal("a send with a yield token and no request did not hold")
	}
	h.endSyncRunHoldOf(conn, json.RawMessage(`{"token":"other"}`))
	if !h.syncRuns.busy() {
		t.Fatal("another send's report ended the hold")
	}
	h.endSyncRunHoldOf(conn, json.RawMessage(`{"token":"y1"}`))
	if h.syncRuns.busy() {
		t.Fatal("the report of the send's token did not end its hold")
	}
	h.holdAfterSend(conn, &CallPayload{Method: "sendMessage", Args: args})
	h.syncRuns.closed(conn)
	if h.syncRuns.busy() {
		t.Fatal("a closed connection's hold stayed")
	}
	h.holdAfterSend(conn, &CallPayload{Method: "sendMessage", ParentRequestID: "req-settled", Args: args})
	if h.syncRuns.busy() {
		t.Fatal("a send from agent_settled held, where the Session holds the message back and stays idle")
	}
}

// The yield helper of a thread-based SDK (CallYield) stands for Pi's first `await`: it ends the hold of its own request and no other.
func TestSyncRunHoldEndsAtTheYieldCall(t *testing.T) {
	conn := &Conn{requestStates: map[string]requestStateSink{"req-1": {event: "session_start"}}}
	h := &Host{}
	args, _ := json.Marshal(map[string]any{"message": map[string]any{"customType": "x"}, "options": map[string]any{"triggerTurn": true}})
	h.holdAfterSend(conn, &CallPayload{Method: "sendMessage", ParentRequestID: "req-1", Args: args})
	h.endSyncRunHold(conn, &CallPayload{Method: CallYield, ParentRequestID: "req-2"})
	if !h.syncRuns.busy() {
		t.Fatal("another request's yield ended the hold")
	}
	h.endSyncRunHold(conn, &CallPayload{Method: CallYield, ParentRequestID: "req-1"})
	if h.syncRuns.busy() {
		t.Fatal("the handler's yield did not end its hold")
	}
}
