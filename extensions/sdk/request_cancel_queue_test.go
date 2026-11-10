package sdk

import "testing"

// unarmedRequests is the number of requests the read loop queued for the main loop and the main loop has not armed.
func unarmedRequests(c *conn) int {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	return len(c.unarmed)
}

// A cancel the read loop keeps for a queued request is dropped when the request is armed, and a cancel of a request that is not queued (finished or unknown) keeps nothing.
func TestQueuedCancelIsDroppedWhenTheRequestIsArmedAndNoneIsKeptForAnUnknownRequest(t *testing.T) {
	h := newWindowHarness(t)
	for _, cancel := range []envelope{
		{Type: msgCancel, ID: "ghost", Cancel: &cancelMsg{RequestID: "ghost", Reason: "late", SignalOnly: true}},
		{Type: msgCancel, ID: "ghost", Cancel: &cancelMsg{RequestID: "ghost", Reason: "late"}},
		{Type: msgCancel, ID: "ghost"},
	} {
		if err := h.host.send(cancel); err != nil {
			t.Fatal(err)
		}
	}
	h.queue(t, envelope{Type: msgCancel, ID: "probe-1", Cancel: &cancelMsg{RequestID: "probe-1", Reason: "tool aborted", SignalOnly: true}})
	if got := unarmedRequests(h.ext.conn); got != 1 {
		t.Fatalf("requests queued and not armed = %d, want the one request", got)
	}
	h.freeLoop()
	<-h.observed
	if got := unarmedRequests(h.ext.conn); got != 0 {
		t.Fatalf("requests queued and not armed after the arming = %d, want none", got)
	}
}

// A signal-only cancel followed by an ordinary one, both read before the arming, leaves the request ended and the signal's reason the first cancel's, as the two frames applied in order do.
func TestSignalOnlyThenOrdinaryCancelReadBeforeTheArmingEndTheRequest(t *testing.T) {
	h := newWindowHarness(t)
	h.queue(t,
		envelope{Type: msgCancel, ID: "probe-1", Cancel: &cancelMsg{RequestID: "probe-1", Reason: "signal reason", SignalOnly: true}},
		envelope{Type: msgCancel, ID: "probe-1", Cancel: &cancelMsg{RequestID: "probe-1", Reason: "request reason"}})
	h.freeLoop()
	got := <-h.observed
	if !got.firstDone || got.reason != "signal reason" {
		t.Errorf("first Done = %v, CancellationReason = %q, want the signal aborted with the first cancel's reason", got.firstDone, got.reason)
	}
	if got.callErr == nil || got.callText != "" {
		t.Errorf("host call = %q, %v, want it refused: the ordinary cancel ended the request", got.callText, got.callErr)
	}
}
