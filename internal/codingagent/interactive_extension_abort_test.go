package codingagent

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's ctx.abort() in interactive mode is `void this.session.abort()` (interactive-mode.ts:2399-2401, :4935-4937): it starts the abort and returns,
// and nothing awaits the end of the run. A handler the agent loop awaits can therefore call it. The host action waited for the run to settle, which
// waits for that handler: an extension that called ctx.abort() from a message_update handler hung the run and every later prompt.
//
// The handler here does not sleep. It counts round trips of the owner loop, which runs the abort as one of its tasks: a call that has not returned
// after extensionAbortReturnBound of them is waiting for the run, and the run is waiting for this handler.
func TestExtensionAbortReturnsToAHandlerTheRunAwaits(t *testing.T) {
	const extensionAbortReturnBound = 1000
	m, ctx, release := newStreamingRoutingMode(t)
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	abort := bridge.actions["abort"].(func())
	waitForIdle := bridge.actions["waitForIdle"].(func(context.Context) error)

	returned := make(chan struct{})
	roundTrips := make(chan int, 1)
	var once sync.Once
	// The agent awaits this listener as it awaits an extension's handler.
	unsubscribe := m.agent.Subscribe(func(context.Context, agent.AgentEvent) error {
		once.Do(func() {
			go func() {
				abort()
				close(returned)
			}()
			for trip := 1; trip <= extensionAbortReturnBound; trip++ {
				onLoop(m, ctx, func() {})
				select {
				case <-returned:
					roundTrips <- trip
					return
				default:
				}
			}
			roundTrips <- 0
		})
		return nil
	})
	defer unsubscribe()
	release()
	select {
	case trip := <-roundTrips:
		if trip == 0 {
			t.Fatalf("abort did not return to the handler the run awaits within %d round trips of the owner loop", extensionAbortReturnBound)
		}
	case <-testbudget.Context(t).Done():
		t.Fatal("the run delivered no event to the handler")
	}
	if err := waitForIdle(ctx); err != nil {
		t.Fatalf("waitForIdle after abort = %v, want nil", err)
	}
	if m.turnActive.Load() {
		t.Fatal("the run was still active after it settled")
	}
}
