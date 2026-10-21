package sdk

import "testing"

// D5: a call made through a request's context after its handler returned is deferred work, and Pi runs it once its loop turns; the call says so (callMsg.Timer) and the host holds it. A call the handler makes while it runs carries no mark.
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

// D5: Pi's loop turns while a sibling tool still awaits, so deferred work that fires then runs right after the tool that queued it ends: the call carries Tick besides Timer. With no tool running, or only the finished one, it carries Timer alone.
func TestDeferredCallWhileASiblingToolRunsCarriesTheTickMark(t *testing.T) {
	ext, connection, host, ctx, finish := retainedContextHarness(t)
	defer finish()
	sibling, finishSibling := ext.armRequest("sibling", &requestMsg{Method: "tool_call"})
	defer finishSibling()
	if err := connection.respond("origin", nil, nil); err != nil {
		t.Fatal(err)
	}
	finish()
	deferred, err := ctx.beginHostCall("ui.notify", map[string]any{"message": "while-sibling-runs"})
	if err != nil {
		t.Fatal(err)
	}
	call := nextLifetimeCall(t, host)
	if !call.Call.Timer || !call.Call.Tick {
		t.Fatalf("a deferred call while a sibling tool runs = %+v, want timer and tick", call.Call)
	}
	if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.waitCall(deferred); err != nil {
		t.Fatal(err)
	}
	if err := connection.respond(sibling.requestID, nil, nil); err != nil {
		t.Fatal(err)
	}
	later, err := ctx.beginHostCall("ui.notify", map[string]any{"message": "after-sibling-ended"})
	if err != nil {
		t.Fatal(err)
	}
	call = nextLifetimeCall(t, host)
	if !call.Call.Timer || call.Call.Tick {
		t.Fatalf("a deferred call once no tool runs = %+v, want timer alone", call.Call)
	}
	if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.waitCall(later); err != nil {
		t.Fatal(err)
	}
}
