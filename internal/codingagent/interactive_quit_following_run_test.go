package codingagent

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// The quit by keys while a run streams, and the agent_settled handler of that run sends a user message (pi.sendUserMessage). The message starts its run once the handlers returned, and the
// quit waits for it: the pinned Pi, quit under tmux in this situation (ctrl+d, ctrl+c twice and /quit, 5 runs each), runs the turn of the message through its own agent_settled and emits
// session_shutdown after that (agent-session.ts _emitAgentSettled awaits the deferred actions before it resolves the idle wait; scenarios 115-quit-settled-send-user-message-*).
func TestQuitByKeysJoinsTheRunOfAMessageTheSettledHandlerSent(t *testing.T) {
	m, ctx, _ := newLifecycleMode(t)
	var mu sync.Mutex
	var order []string
	record := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}
	settled := 0
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "probe", Handlers: map[string][]extension.HandlerFn{
		"agent_settled": {func(...any) (any, error) {
			mu.Lock()
			settled++
			n := settled
			mu.Unlock()
			record("agent_settled")
			if n == 1 {
				if err := m.deliverUserMessage("second", ""); err != nil {
					t.Error(err)
				}
			}
			return nil, nil
		}},
		"session_shutdown": {func(...any) (any, error) { record("session_shutdown"); return nil, nil }},
	}}}, t.TempDir())
	streaming := make(chan struct{})
	m.runTurn(ctx, "", func(run context.Context) ([]agent.AgentMessage, error) {
		close(streaming)
		<-run.Done()
		return nil, nil
	})
	<-streaming
	rt := &quitSettleRuntime{}
	rt.dispose = func(settle func()) {
		if settle != nil {
			settle()
		}
		emitSessionShutdown(m.newRunner, "quit")
	}
	m.runtimeHost = rt
	m.installRuntimeHooks()
	defer m.enterOwnerLoop()()
	if err := m.finishInteractiveShutdown(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := slices.Clone(order)
	mu.Unlock()
	if want := []string{"agent_settled", "agent_settled", "session_shutdown"}; !slices.Equal(got, want) {
		t.Errorf("the quit ran %v, want %v: the run of the message the first agent_settled handler sent settles before session_shutdown", got, want)
	}
	if m.runGen != 2 {
		t.Errorf("runGen = %d, want 2: the sent message started its run", m.runGen)
	}
}
