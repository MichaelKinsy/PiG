package sdk

import "testing"

// D189: a call made through a request's context after its handler returned is deferred work, and Pi runs it once its loop turns; the call says so (callMsg.Timer) and the host holds it. A call the handler makes while it runs carries no mark.
func TestCallAfterTheRequestReturnedCarriesTheTimerMark(t *testing.T) {
	_, connection, host, ctx, finish := retainedContextHarness(t)
	defer finish()
	pending, err := ctx.beginHostCall("ui.notify", map[string]any{"message": "inline"})
	if err != nil {
		t.Fatal(err)
	}
	inline := nextLifetimeCall(t, host)
	if inline.Call.Timer || inline.Call.ParentRequestID != "origin" {
		t.Fatalf("a call of the running handler = %+v, want parent origin and no timer mark", inline.Call)
	}
	if err := host.send(envelope{Type: msgCallResult, ID: inline.ID, CallResult: &callResultMsg{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.waitCall(pending); err != nil {
		t.Fatal(err)
	}
	if err := connection.respond("origin", nil, nil); err != nil {
		t.Fatal(err)
	}
	finish()
	deferred, err := ctx.beginHostCall("ui.notify", map[string]any{"message": "deferred"})
	if err != nil {
		t.Fatal(err)
	}
	call := nextLifetimeCall(t, host)
	if !call.Call.Timer || call.Call.ParentRequestID != "" {
		t.Fatalf("a call after the handler returned = %+v, want no parent and a timer mark", call.Call)
	}
	if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.waitCall(deferred); err != nil {
		t.Fatal(err)
	}
}
