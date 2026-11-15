package codingagent

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The quit by keys while a run's agent_settled handlers are in flight. Pi 1.5.2: _emitAgentSettled sets _isAgentRunActive to false before it emits agent_settled
// (agent-session.ts:1139-1161), so isIdle is true in the settle tail and `await this.session.abort()` of the dispose (agent-session.ts:2601-2611, waitForIdle returns at
// once for an idle session) does not await the handlers: session_shutdown follows at once, and Pi's process exits as soon as the dispose returns (Pi under tmux with an
// agent_settled handler that waits on a 3 s timer: 94-99 ms; pig before this change: 2383 ms). A quit while the run is still active does await agent_settled: the idle
// wait it registers resolves after the emit (TestQuitByKeysWhileARunStreamsServesTheOwnerLoopUntilTheRunSettles).
func TestQuitByKeysInTheSettleTailDoesNotAwaitAgentSettledHandlers(t *testing.T) {
	budget := testbudget.Wait(t)
	m, ctx, _ := newLifecycleMode(t)
	var mu sync.Mutex
	var order []string
	record := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}
	handlerStarted, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "probe", Handlers: map[string][]extension.HandlerFn{
		"agent_settled": {func(...any) (any, error) {
			record("agent_settled starts")
			close(handlerStarted)
			select {
			case <-release:
			case <-time.After(budget):
			}
			record("agent_settled ends")
			return nil, nil
		}},
		"session_shutdown": {func(...any) (any, error) { record("session_shutdown"); return nil, nil }},
	}}}, t.TempDir())
	// The run ends at once; its settle tail holds the quit's agent_settled handler.
	m.runTurn(ctx, "", func(context.Context) ([]agent.AgentMessage, error) { return nil, nil })
	<-handlerStarted
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
	if want := []string{"agent_settled starts", "session_shutdown"}; !slices.Equal(got, want) {
		t.Errorf("the quit ran %v, want %v: Pi's dispose does not await the agent_settled handlers of a run that is already idle, so session_shutdown follows at once", got, want)
	}
}
