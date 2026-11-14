package codingagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi's ctx.abort() in interactive mode is `void this.session.abort()` (interactive-mode.ts:2399-2401, :4935-4937): it starts the abort and returns,
// and nothing awaits the end of the run. A handler the agent loop awaits can therefore call it. The host action waited for the run to settle, which
// waits for that handler: an extension that called ctx.abort() from a message_update handler hung the run, the next prompt and the quit.
func TestExtensionAbortReturnsToAHandlerTheRunAwaits(t *testing.T) {
	m, ctx, release := newStreamingRoutingMode(t)
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	abort := bridge.actions["abort"].(func())
	waitForIdle := bridge.actions["waitForIdle"].(func(context.Context) error)

	returned := make(chan struct{})
	var once sync.Once
	// The agent awaits this listener as it awaits an extension's handler.
	unsubscribe := m.agent.Subscribe(func(context.Context, agent.AgentEvent) error {
		once.Do(func() {
			abort()
			close(returned)
		})
		return nil
	})
	defer unsubscribe()
	release()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("abort did not return to the handler the run awaits")
	}
	if err := waitForIdle(ctx); err != nil {
		t.Fatalf("waitForIdle after abort = %v, want nil", err)
	}
	if m.turnActive.Load() {
		t.Fatal("the run was still active after it settled")
	}
}
