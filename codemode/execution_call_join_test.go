package codemode

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
)

// Execute returns after every goroutine it started has exited, nested tool calls included. A call message from the VM that arrives as
// the execution finishes either finds it finished and starts nothing, or is counted in e.calls before finish can be observed, so the
// Wait that follows finish waits for it. startCall checked finished under the lock and then added to e.calls after releasing it: a
// finish and a Wait in that window returned with the call's goroutine still to start, which the race detector reports as
// sync.WaitGroup reuse (Add from zero racing Wait; nightly 42368266926, TestEveryTerminalKindClosesItsVMWithoutHaltingTheInstance,
// "abort during a nested call") and which let a tool run after Execute had returned.
func TestACallMessageRacingTheFinishIsJoinedBeforeTheWaitReturns(t *testing.T) {
	const rounds = 3000
	var late atomic.Int64
	for range rounds {
		var waited atomic.Bool
		e := newExecution(&Sandbox{}, "", 0, nil, []Tool{{Name: "t", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			if waited.Load() {
				late.Add(1)
			}
			return nil, nil
		}}}, nil)
		e.callCtx = context.Background()
		var racers sync.WaitGroup
		racers.Go(func() {
			e.receive(vmMessage{kind: vmMessageCall, id: 1, isTool: true, name: "t"})
		})
		racers.Go(func() {
			e.finish(&Error{Kind: ErrorAborted, Message: "abort"}, nil, StoreWrites{})
			e.calls.Wait()
			waited.Store(true)
		})
		racers.Wait()
		e.calls.Wait()
	}
	if n := late.Load(); n != 0 {
		t.Fatalf("%d tool calls began after the Wait that follows finish had returned", n)
	}
}
