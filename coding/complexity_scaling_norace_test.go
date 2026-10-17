//go:build !race

package coding

import (
	"context"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Complexity gates for the per-turn and per-event Session paths.

type scalingSession struct {
	session *Session
	drained chan struct{}
}

func newScalingSession(t *testing.T, services *AgentSessionServices, history, deltas int) scalingSession {
	model := &ai.Model{ID: "scaling-model", DisplayName: "scaling", Provider: scalingDeltaProvider{deltas: deltas}, Capabilities: ai.ModelCapabilities{ContextWindow: 10_000_000}}
	s, err := NewSession(services, SessionOptions{Model: model, SessionManager: scalingSessionManager(t, history), ResourceLoader: NoResources, SkipBuiltinTools: true, SystemPrompt: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	s.Subscribe(func(agent.AgentEvent) {})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for event := range s.Events() {
			AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		<-drained
	})
	return scalingSession{session: s, drained: drained}
}

func TestScalingSessionTurn(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	send := func(s *Session) {
		if _, err := s.Send(context.Background(), "hello"); err != nil {
			panic(err)
		}
	}
	// The first turn after opening a session converts the whole history once, as Pi's does (agent-session.ts prompt, buildSessionContext, convertToLlm).
	testscaling.Check(t, testscaling.Case{Name: "first session turn (50 deltas) at history n", Size: 50, Bound: testscaling.Linear, Prepare: func(n int) func() {
		s := newScalingSession(t, services, n, 50).session
		return func() { send(s) }
	}})
	// Every later turn allocates a fixed number of objects whatever the history length: the session projection extends on append, and the provider messages of the history are remembered from the previous request. Its bytes stay linear in the history, because a request copies the history as a slice of one header per message.
	testscaling.Check(t, testscaling.Case{Name: "later session turn (50 deltas) at history n", Size: 50, Bound: testscaling.Constant, BytesBound: testscaling.Linear, Prepare: func(n int) func() {
		s := newScalingSession(t, services, n, 50).session
		send(s)
		return func() { send(s) }
	}})
	// Each streamed delta costs O(1) in the history: with n deltas at history n a per-delta history walk would make the turn quadratic.
	testscaling.Check(t, testscaling.Case{Name: "session turn with n deltas at history n", Size: 50, Bound: testscaling.Linear, Prepare: func(n int) func() {
		s := newScalingSession(t, services, n, n).session
		return func() { send(s) }
	}})
	// getContextUsage projects the branch once (agent-session.ts getContextUsage).
	testscaling.Check(t, testscaling.Case{Name: "session getContextUsage at history n", Size: 50, Bound: testscaling.Linear, Prepare: func(n int) func() {
		s := newScalingSession(t, services, n, 1).session
		return func() { s.ContextUsage() }
	}})
	// Opening a Session over an existing log builds the context once.
	testscaling.Check(t, testscaling.Case{Name: "session open over a log of history n", Size: 50, Bound: testscaling.Linear, Prepare: func(n int) func() {
		manager := scalingSessionManager(t, n)
		model := &ai.Model{ID: "scaling-model", DisplayName: "scaling", Provider: scalingDeltaProvider{deltas: 1}, Capabilities: ai.ModelCapabilities{ContextWindow: 10_000_000}}
		return func() {
			s, err := NewSession(services, SessionOptions{Model: model, SessionManager: manager, ResourceLoader: NoResources, SkipBuiltinTools: true, SystemPrompt: "Test"})
			if err != nil {
				panic(err)
			}
			if err := s.Close(); err != nil {
				panic(err)
			}
		}
	}})
}

// A later turn of a Session at history N, 10N and 100N exchanges (4 entries each): the allocation count is independent of the history and the bytes are linear, the copies of the history the request makes (one 16-byte header per message in the provider request, one 48-byte message per copy of the run context).
func TestSessionTurnScalingExponents(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	perfgate.CheckScaling(t, "coding.session_turn", [3]int{20, 200, 2000}, func(history int) func() {
		s := newScalingSession(t, services, history, 5).session
		send := func() {
			if _, err := s.Send(context.Background(), "hello"); err != nil {
				panic(err)
			}
		}
		send()
		return send
	})
}

// The heap a Session keeps per history entry once it has run a turn: the entries with their records, the kept projection, the agent's transcript, which shares the projection's messages, and the provider messages the next request reuses. The difference between a history of 200 and one of 2,000 exchanges (4 entries each) cancels the Session's fixed cost.
func TestSessionRetainedBytesPerEntry(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	live := func() float64 {
		runtime.GC()
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return float64(stats.HeapAlloc)
	}
	retained := func(exchanges int) float64 {
		before := live()
		s := newScalingSession(t, services, exchanges, 5).session
		if _, err := s.Send(context.Background(), "hello"); err != nil {
			t.Fatal(err)
		}
		after := live()
		runtime.KeepAlive(s)
		return after - before
	}
	small, large := retained(200), retained(2000)
	perfgate.Check(t, "coding.session_retained.bytes_per_entry", (large-small)/float64(4*1800))
}
