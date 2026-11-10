package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Pi's abort of a session_before_compact / session_before_tree event fires event.signal and nothing else: the handler's host calls are in-process and never fail with it. A signal-only cancel frame therefore aborts the context's Done, Err and CancellationReason while the request's host calls, pending and later, keep their answers, and the reader applies it before it routes a result that follows it on the wire.
func TestSignalOnlyCancelAbortsTheSignalAndLeavesHostCallsRunning(t *testing.T) {
	_, connection, host, ctx, finish := retainedContextHarness(t)
	defer finish()
	type observed struct {
		text   string
		err    error
		done   bool
		ctxErr error
		reason string
	}
	result := make(chan observed, 1)
	go func() {
		got, err := ctx.callHost("ui.getEditorText", nil)
		out := observed{err: err, ctxErr: ctx.Err(), reason: ctx.CancellationReason()}
		select {
		case <-ctx.Done():
			out.done = true
		default:
		}
		if got != nil {
			out.text = string(got.Result)
		}
		result <- out
	}()
	call := nextLifetimeCall(t, host)
	if err := host.send(envelope{Type: msgCancel, ID: "origin", Cancel: &cancelMsg{RequestID: "origin", Reason: "context canceled", SignalOnly: true}}); err != nil {
		t.Fatal(err)
	}
	// The result follows the cancel frame on the wire: the handler that wakes with it finds its signal aborted.
	if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`"answered"`)}}); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil || got.text != `"answered"` {
		t.Fatalf("the call pending at the signal-only cancel = %q, %v, want the host's answer", got.text, got.err)
	}
	if !got.done || !errors.Is(got.ctxErr, context.Canceled) || got.reason != "context canceled" {
		t.Fatalf("after the answer: Done closed = %v, Err = %v, CancellationReason = %q, want the signal aborted by the cancel", got.done, got.ctxErr, got.reason)
	}
	// A call made after the abort reaches the host and is answered.
	later := make(chan error, 1)
	var laterText string
	go func() {
		reply, err := ctx.callHost("ui.getEditorText", nil)
		if reply != nil {
			laterText = string(reply.Result)
		}
		later <- err
	}()
	call = nextLifetimeCall(t, host)
	if call.Call.ParentRequestID != "origin" {
		t.Fatalf("a call after the abort names parent %q, want its request", call.Call.ParentRequestID)
	}
	if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`"later"`)}}); err != nil {
		t.Fatal(err)
	}
	if err := <-later; err != nil || laterText != `"later"` {
		t.Fatalf("a call after the abort = %q, %v, want the host's answer", laterText, err)
	}
	if err := connection.respond("origin", nil, nil); err != nil {
		t.Fatal(err)
	}
}

// A signal-only cancel that reaches the main loop (the request was not armed when the reader read it) aborts the signal alone as well, and an ordinary cancel still ends the request and its calls.
func TestCancelRequestSignalOnlyKeepsTheRequestAndOrdinaryCancelEndsIt(t *testing.T) {
	ext, _, host, ctx, finish := retainedContextHarness(t)
	defer finish()
	result := make(chan error, 1)
	go func() { _, err := ctx.callHost("ui.getEditorText", nil); result <- err }()
	call := nextLifetimeCall(t, host)
	ext.cancelRequest(envelope{Type: msgCancel, ID: "origin", Cancel: &cancelMsg{RequestID: "origin", Reason: "aborted", SignalOnly: true}})
	if ctx.Err() == nil || ctx.CancellationReason() != "aborted" {
		t.Fatalf("signal-only cancel: Err = %v, reason %q, want the signal aborted", ctx.Err(), ctx.CancellationReason())
	}
	select {
	case err := <-result:
		t.Fatalf("the pending call ended with the signal: %v", err)
	default:
	}
	if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`"x"`)}}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatalf("the call pending at the signal-only cancel = %v, want its answer", err)
	}
	// An ordinary cancel of the same request ends its calls.
	ended := make(chan error, 1)
	go func() { _, err := ctx.callHost("ui.getEditorText", nil); ended <- err }()
	nextLifetimeCall(t, host)
	ext.cancelRequest(envelope{Type: msgCancel, ID: "origin", Cancel: &cancelMsg{RequestID: "origin", Reason: "ordinary"}})
	if err := <-ended; err == nil {
		t.Fatal("an ordinary cancel left the pending call running")
	}
}
