//go:build !race

package coding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"runtime/trace"
	"strings"
	"sync/atomic"
	"testing"

	xtrace "golang.org/x/exp/trace"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// sessionHopWakes streams one text response through a Session driven as -p mode drives it (one in-process listener, Events discarded) under runtime/trace. It returns the message_update events the listener saw and the goroutine wake-ups whose waker is Session code: the executor hand-off (rawEvents send, done wait) and the Events channel.
func sessionHopWakes(t *testing.T, chars int) (updates int64, wakes int) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	size := 4
	faux := ai.NewFauxProvider(ai.FauxConfig{TokenSize: &ai.FauxTokenSize{Min: &size, Max: &size}, Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 10_000_000, MaxTokens: 1_000_000}}})
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxAssistantMessage(ai.FauxContentString(strings.Repeat("x", chars)), ai.FauxAssistantMessageOptions{})})
	session, err := NewSession(services, SessionOptions{Model: faux.GetModel(), NoSession: true, ResourceLoader: NoResources, SystemPrompt: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	var seen atomic.Int64
	session.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.MessageUpdateEvent); ok {
			seen.Add(1)
		}
	})
	session.DiscardEvents()
	var buf bytes.Buffer
	if err := trace.Start(&buf); err != nil {
		t.Skipf("runtime/trace unavailable: %v", err)
	}
	ctx := context.Background()
	_, err = session.Send(ctx, "write")
	if err == nil {
		err = session.FlushEvents(ctx)
	}
	trace.Stop()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := xtrace.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for {
		event, err := reader.ReadEvent()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind() != xtrace.EventStateTransition {
			continue
		}
		transition := event.StateTransition()
		if transition.Resource.Kind != xtrace.ResourceGoroutine {
			continue
		}
		if from, to := transition.Goroutine(); from != xtrace.GoWaiting || to != xtrace.GoRunnable {
			continue
		}
		if sessionWaker(event.Stack()) {
			wakes++
		}
	}
	return seen.Load(), wakes
}

// sessionWaker reports whether the first frame outside the runtime and the standard synchronization packages belongs to package coding.
func sessionWaker(stack xtrace.Stack) bool {
	for frame := range stack.Frames() {
		name := frame.Func
		if strings.HasPrefix(name, "runtime.") || strings.HasPrefix(name, "sync.") || strings.HasPrefix(name, "internal/") || strings.HasPrefix(name, "context.") || strings.HasPrefix(name, "time.") {
			continue
		}
		return strings.HasPrefix(name, "github.com/MichaelKinsy/PiG/coding.")
	}
	return false
}

// Pi's _handleAgentEvent runs its listeners synchronously on the one event loop (agent-session.ts:1039-1043, 1098). With only in-process listeners and no Events consumer, every Session event executes on its producer's goroutine: the executor hand-off (rawEvents send, done wait) and the Events channel cost no goroutine wake-up per delta. At 4cfaf24153, with print mode's Events drain, these hops woke about four goroutines per delta.
func TestSessionInProcessDeliveryWakesPerDelta(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const chars = 8_000
	smallUpdates, small := sessionHopWakes(t, chars)
	largeUpdates, large := sessionHopWakes(t, 2*chars)
	deltas := largeUpdates - smallUpdates
	// The faux provider streams about 16 characters per text delta.
	if deltas < chars/32 {
		t.Fatalf("the listener saw %d then %d message_update events; want one per streamed delta", smallUpdates, largeUpdates)
	}
	perDelta := float64(large-small) / float64(deltas)
	t.Logf("session wakes: %d at %d updates, %d at %d updates: %.3f per delta", small, smallUpdates, large, largeUpdates, perDelta)
	if perDelta > 0.05 {
		t.Fatalf("Session hand-offs woke %.3f goroutines per streamed delta, want none", perDelta)
	}
}
