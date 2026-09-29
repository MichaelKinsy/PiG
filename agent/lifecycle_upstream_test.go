package agent

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// .upstream/v0.87.1/packages/agent/test/agent.test.ts:683
func TestAgent_HandlesAbortController(t *testing.T) {
	a := NewAgent(AgentOptions{})
	a.Abort()
}

// .upstream/v0.87.1/packages/agent/test/agent.test.ts:414
func TestAgent_WaitForIdleWaitsForAsyncSubscribers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		barrier := make(chan struct{})
		idleResolved, promptResolved := false, false
		a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("ok")})})
		a.Subscribe(func(_ context.Context, ev AgentEvent) error {
			if end, ok := ev.(MessageEndEvent); ok && end.Message.Assistant != nil {
				<-barrier
			}
			return nil
		})
		go func() { mustSend(t, a, "hello"); promptResolved = true }()
		synctest.Wait()
		go func() { a.WaitForIdle(); idleResolved = true }()
		synctest.Wait()
		if idleResolved || promptResolved || !a.IsStreaming() {
			t.Fatalf("idle=%v prompt=%v streaming=%v before barrier", idleResolved, promptResolved, a.IsStreaming())
		}
		close(barrier)
		synctest.Wait()
		if !idleResolved || !promptResolved || a.IsStreaming() {
			t.Fatalf("idle=%v prompt=%v streaming=%v after barrier", idleResolved, promptResolved, a.IsStreaming())
		}
	})
}

// .upstream/v0.87.1/packages/agent/test/agent.test.ts:449
func TestAgent_PassesActiveAbortSignalToSubscribers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{}, 1)
		var received, providerContext context.Context
		a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: func(_ int, req scriptedRequest) *ai.AssistantMessageEventStream {
			providerContext = req.ctx
			return abortableStream(req.ctx, started)
		}})})
		a.Subscribe(func(ctx context.Context, ev AgentEvent) error {
			if _, ok := ev.(AgentStartEvent); ok {
				received = ctx
			}
			return nil
		})
		done := sendAsync(t, a, "hello")
		<-started
		synctest.Wait()
		if received == nil || received.Err() != nil || received != providerContext || received != a.Signal() {
			t.Fatalf("subscriber=%v provider=%v", received, providerContext)
		}
		a.Abort()
		synctest.Wait()
		<-done
		if received.Err() == nil {
			t.Fatal("subscriber signal was not aborted")
		}
		if a.Signal() != nil {
			t.Fatal("active signal retained after settlement")
		}
	})
}

func BenchmarkAgentLifecycleTextRun(b *testing.B) {
	provider := &scriptedProvider{respond: replyText(strings.Repeat("response ", 128))}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider)})
	a.Subscribe(func(context.Context, AgentEvent) error { return nil })
	b.ReportAllocs()
	for b.Loop() {
		if _, err := a.Send(b.Context(), "hello"); err != nil {
			b.Fatal(err)
		}
		if err := a.Reset(); err != nil {
			b.Fatal(err)
		}
		// The fixture's request recorder is not retained conversation state.
		provider.requests = nil
	}
}

func TestAgentSuccessfulRunDetachesCancellationWithoutAbortingSignal(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("ok")})})
	var signal context.Context
	a.Subscribe(func(ctx context.Context, _ AgentEvent) error { signal = ctx; return nil })
	if _, err := a.Send(parent, "hello"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if signal.Err() != nil {
		t.Fatal("a successful run's retained signal became aborted")
	}
	a.WaitForIdle()
}
