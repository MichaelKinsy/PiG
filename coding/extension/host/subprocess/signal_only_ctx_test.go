package subprocess

import (
	"context"
	"testing"
	"time"
)

type signalOnlyCtxValueKey struct{}

// The awaited-event context hides the parent's cancellation and deadline, answers the parent's values (the request path reads the per-call values through it) and the parent as the request's signal.
func TestSignalOnlyCtxHidesCancellationAndKeepsValues(t *testing.T) {
	parent, cancel := context.WithDeadline(context.WithValue(context.Background(), signalOnlyCtxValueKey{}, "call"), time.Now().Add(time.Hour))
	ctx := &signalOnlyCtx{Context: parent, signal: parent}
	cancel()
	if ctx.Done() != nil || ctx.Err() != nil {
		t.Fatalf("Done = %v, Err = %v after the parent was cancelled; want nil, nil", ctx.Done(), ctx.Err())
	}
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("the context reports the parent's deadline")
	}
	if got := ctx.Value(signalOnlyCtxValueKey{}); got != "call" {
		t.Fatalf("parent value = %v, want call", got)
	}
	signal := requestSignal(ctx)
	if signal != parent || signal.Err() == nil {
		t.Fatalf("requestSignal = %v (err %v), want the cancelled parent", signal, signal.Err())
	}
	if requestSignal(parent) != nil {
		t.Fatal("the parent itself answers a signal")
	}
}

func TestSignalOnlyCtxKeepsTheRequestPathValues(t *testing.T) {
	started := func() {}
	parent, cancel := context.WithCancel(withRequestIssued(context.Background(), started))
	defer cancel()
	ctx := &signalOnlyCtx{Context: parent, signal: parent}
	if requestIssued(ctx) == nil {
		t.Fatal("the issued callback of the call is not found through the context")
	}
}

func TestSignalOnlyCtxAllocatesOnce(t *testing.T) {
	parent := t.Context()
	var sink context.Context
	if n := testing.AllocsPerRun(100, func() { sink = &signalOnlyCtx{Context: parent, signal: parent} }); n != 1 {
		t.Fatalf("allocations = %v, want 1", n)
	}
	_ = sink
}
