package codingagent

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// quitFlushBarrier stands for the marker that Session.FlushEvents puts on Events: the run waits until the consumer of Events, the owner loop, acknowledges it.
type quitFlushBarrier struct {
	agent.AgentEvent
	done chan struct{}
}

func (b *quitFlushBarrier) AcknowledgeEvent() { close(b.done) }

// quitSettleRuntime is a runtime host whose dispose after the TUI's stop does what coding.Runtime's does: it runs the settle the mode installed, then goes on to session_shutdown.
type quitSettleRuntime struct {
	InteractiveRuntime
	settle  func()
	dispose func(settle func())
}

func (h *quitSettleRuntime) SetBeforeSessionReplacement(func(context.Context) error) {}

func (h *quitSettleRuntime) SetBeforeSessionInvalidate(func()) {}

func (h *quitSettleRuntime) SetRebindSession(func(context.Context, InteractiveSessionHandle) error) {
}

func (h *quitSettleRuntime) TakeUnreportedDiagnostics() []AgentSessionRuntimeDiagnostic { return nil }

func (h *quitSettleRuntime) SetBeforeQuitDispose(settle func()) { h.settle = settle }

func (h *quitSettleRuntime) EmitQuitShutdownAfterStop() { h.dispose(h.settle) }

// The quit by keys while a run streams. Pi's shutdown() stops the TUI, which unsubscribes the mode from its Session (interactive-mode.ts stop: this.unsubscribe()), and then
// awaits runtimeHost.dispose(), whose first step is `await this.session.abort()` (agent-session-runtime.ts dispose). Pi's loop turns under that await: the aborted run
// publishes its last events to the extensions and the session file, emits agent_settled, and only then is the Session idle (agent-session.ts _emitAgentSettled resolves
// the idle wait); session_shutdown follows.
//
// Here the run's end needs the owner loop: Session.RunAgentPrompt flushes its events and waits until the loop acknowledges the flush. The dispose ran on that loop and
// awaited the run there, so each waited for the other and pig never exited. The mode therefore installs the settle of its run for that dispose: it aborts the run and
// serves the loop until the run's goroutine has returned.
func TestQuitByKeysWhileARunStreamsServesTheOwnerLoopUntilTheRunSettles(t *testing.T) {
	m, ctx, _ := newLifecycleMode(t)
	var mu sync.Mutex
	var order []string
	record := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "probe", Handlers: map[string][]extension.HandlerFn{
		"agent_settled":    {func(...any) (any, error) { record("agent_settled"); return nil, nil }},
		"session_shutdown": {func(...any) (any, error) { record("session_shutdown"); return nil, nil }},
	}}}, t.TempDir())
	events := make(chan agent.AgentEvent)
	m.eventCh = events
	// A message that streams when the user quits: agent_end removes its block when the mode applies the event.
	streamingBlock := &tui.AssistantMessageComponent{}
	m.evCurrentBlock = streamingBlock
	streaming, flushing, ended := make(chan struct{}), make(chan struct{}), make(chan struct{})
	// The run streams until its abort. Then, as Session.RunAgentPrompt does, it publishes its last event and flushes: it returns once the consumer of Events acknowledged.
	m.runTurn(ctx, "", func(run context.Context) ([]agent.AgentMessage, error) {
		defer close(ended)
		close(streaming)
		<-run.Done()
		barrier := &quitFlushBarrier{done: make(chan struct{})}
		close(flushing)
		for _, ev := range []agent.AgentEvent{agent.AgentEndEvent{}, barrier} {
			select {
			case events <- ev:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		select {
		case <-barrier.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		record("the run's flush acknowledged")
		return nil, nil
	})
	<-streaming
	rt := &quitSettleRuntime{}
	rt.dispose = func(settle func()) {
		if settle != nil {
			settle()
		} else {
			// Without a settle of the mode the dispose aborts the run and awaits it where it stands: on the owner loop.
			m.abortRun(m.runCtx)
		}
		select {
		case <-ended:
		default:
			// The run is not over. It reaches its flush, and nothing can acknowledge it: this goroutine is the loop.
			<-flushing
			t.Fatal("the quit's dispose awaits the run on the owner loop while the run's flush waits for that loop: neither can go on, and pig never exits")
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
	if want := []string{"the run's flush acknowledged", "agent_settled", "session_shutdown"}; !slices.Equal(got, want) {
		t.Errorf("the quit ran %v, want %v: Pi's abort resolves after agent_settled, and session_shutdown follows it", got, want)
	}
	if m.evCurrentBlock != streamingBlock {
		t.Error("the mode applied an event of the run after its TUI stopped; Pi's stop() unsubscribes the mode from the Session, so nothing of the run is drawn after it")
	}
}
