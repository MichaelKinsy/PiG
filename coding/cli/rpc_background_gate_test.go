package cli

import (
	"context"
	"testing"
)

// The gate is closed from the rebind that takes a replacement's hold until the command's response ends it, and a context of no command replacement holds nothing.
func TestRPCBackgroundGateClosesForAReplacementUntilItsResponse(t *testing.T) {
	var gate rpcBackgroundGate
	if gate.wait() != nil {
		t.Fatal("a gate nobody holds is closed")
	}
	acquireRPCReplacementHold(context.Background())
	if gate.wait() != nil {
		t.Fatal("a context of no command replacement closed the gate")
	}
	hold := &rpcReplacementHold{gate: &gate}
	ctx := withRPCReplacementHold(context.Background(), hold)
	acquireRPCReplacementHold(ctx)
	acquireRPCReplacementHold(ctx)
	open := gate.wait()
	if open == nil {
		t.Fatal("the replacement's rebind did not close the gate")
	}
	select {
	case <-open:
		t.Fatal("the gate opened before the response")
	default:
	}
	hold.end()
	select {
	case <-open:
	default:
		t.Fatal("the gate stayed closed after the response")
	}
	if gate.wait() != nil {
		t.Fatal("the gate is closed after its only hold ended")
	}
	// A replacement whose response came before any rebind never closes the gate.
	late := &rpcReplacementHold{gate: &gate}
	late.end()
	acquireRPCReplacementHold(withRPCReplacementHold(context.Background(), late))
	if gate.wait() != nil {
		t.Fatal("a hold acquired after its response closed the gate")
	}
}

// Two replacements in flight keep the gate closed until the later one answers.
func TestRPCBackgroundGateStaysClosedUntilEveryHoldEnds(t *testing.T) {
	var gate rpcBackgroundGate
	first, second := gate.hold(), gate.hold()
	open := gate.wait()
	first()
	first()
	select {
	case <-open:
		t.Fatal("the gate opened while a second hold remained")
	default:
	}
	second()
	select {
	case <-open:
	default:
		t.Fatal("the gate stayed closed after every hold ended")
	}
}

// A replacement an extension command requested closes the gate until every in-flight command answered, not until the replacement ends.
func TestRPCBackgroundGateHoldsForCommandsUntilTheyAnswer(t *testing.T) {
	var gate rpcBackgroundGate
	gate.holdForCommand()
	gate.holdForCommand()
	open := gate.wait()
	if open == nil {
		t.Fatal("a replacement an extension command requested did not close the gate")
	}
	gate.endCommandHolds()
	select {
	case <-open:
	default:
		t.Fatal("the gate stayed closed after the commands answered")
	}
	gate.endCommandHolds()
	if gate.wait() != nil {
		t.Fatal("ending the command holds twice left the gate closed")
	}
}
