package cli

import (
	"slices"
	"testing"
)

// Pi's session.reload() awaits session_shutdown, the loaders, session_start and resources_discover in one chain, and the command that awaited ctx.reload() goes on in the microtasks of its end (agent-session.ts:3815-3861, rpc-mode.ts:345-347), so its loop reads no stdin line inside the chain unless a handler of it waits on a timer or I/O. The gate holds input from the reload's start to the end of its hold, and admits it only while such a handler waits.
// mutation-checked: with rpcSettleGate.openLocked ignoring the reloads the gate holds, input is admitted while a reload runs.
func TestRPCSettleGateHoldsInputThroughAReload(t *testing.T) {
	g := &rpcSettleGate{}
	report := g.tailHandlerReporter()
	release := g.holdReload()
	if gateOpen(g) {
		t.Fatal("input was admitted while a reload ran")
	}
	report(1)
	if !gateOpen(g) {
		t.Fatal("a handler of the reload that waits did not admit input")
	}
	report(0)
	if gateOpen(g) {
		t.Fatal("input was admitted between the return of the reload's handler and the reload's end")
	}
	second := g.holdReload()
	waiter := g.opened()
	release()
	release()
	select {
	case <-waiter:
		t.Fatal("the end of one reload admitted input while another ran")
	default:
	}
	second()
	select {
	case <-waiter:
	default:
		t.Fatal("the end of the last reload did not release the held input")
	}
}

// The hold of a reload ends with the continuation of the command that awaited it: at the handler's next await, or once its response is written (rpc-mode.ts:420-423 preflightResult writes it in the microtasks of the handler's return). A command that has answered holds nothing.
func TestRPCCommandFlightRunsResumptionWorkAtTheNextSuspensionOrTheAnswer(t *testing.T) {
	runs := &rpcCommandRuns{}
	suspended := &rpcCommandFlight{runs: runs}
	ran := 0
	suspended.AfterResumption(func() { ran++ })
	if ran != 0 {
		t.Fatal("the work ran before the handler awaited or answered")
	}
	suspended.Suspended()
	if ran != 1 {
		t.Fatalf("the handler's next await ran the work %d times, want once", ran)
	}
	suspended.Suspended()
	if ran != 1 {
		t.Fatalf("a later await ran the work again: %d", ran)
	}
	answering := &rpcCommandFlight{runs: runs}
	answering.AfterResumption(func() { ran++ })
	runs.answered(answering, nil)
	if ran != 2 {
		t.Fatalf("the answer ran the work %d times in all, want twice", ran)
	}
	answering.AfterResumption(func() { ran++ })
	if ran != 3 {
		t.Fatal("work left with an answered command did not run at once")
	}
}

// An answer read while a reload runs resumes a handler of the reload's chain, and Pi runs that continuation behind the lines of the answer's chunk and the microtasks they queued (deliverRPCDialogAnswer): the response of a prompt in the chunk is written before the handler goes on. Outside a reload the answer is delivered on its line.
// mutation-checked: with deliverRPCDialogAnswer delivering at once during a reload, the handler that asked goes on first.
func TestRPCDialogAnswerReadDuringAReloadIsDeliveredBehindItsBatch(t *testing.T) {
	batch := func(reloading bool) []string {
		var order []string
		turn := &rpcResponseTurn{write: func(any) {}}
		turn.Begin()
		deliverRPCDialogAnswer(turn, reloading, func() { order = append(order, "the handler that asked goes on") })
		// The prompt behind the answer: its handler returns in the batch, and its response is a microtask of that return.
		turn.Microtask(func() {
			order = append(order, "the prompt's handler returned")
			turn.Microtask(func() { order = append(order, "the prompt's response") })
		})
		turn.End()
		return order
	}
	want := []string{"the prompt's handler returned", "the prompt's response", "the handler that asked goes on"}
	if got := batch(true); !slices.Equal(got, want) {
		t.Fatalf("an answer read during a reload: %q, want %q", got, want)
	}
	want = []string{"the handler that asked goes on", "the prompt's handler returned", "the prompt's response"}
	if got := batch(false); !slices.Equal(got, want) {
		t.Fatalf("an answer read outside a reload: %q, want %q", got, want)
	}
}
