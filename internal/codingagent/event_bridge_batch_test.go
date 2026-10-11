package codingagent

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// batchHarness builds a runner whose message_update handler blocks until the
// test releases it, so queued updates accumulate while the first dispatch is
// in flight. Delivered events are recorded under a mutex.
type batchHarness struct {
	runner     *inproc.Runner
	release    chan struct{}
	entered    chan struct{}
	mu         sync.Mutex
	deliveries []extension.MessageUpdateEvent
	done       chan struct{} // sent after each delivery
}

func newBatchHarness(t *testing.T) *batchHarness {
	t.Helper()
	h := &batchHarness{
		release: make(chan struct{}),
		entered: make(chan struct{}, 8),
		done:    make(chan struct{}, 64),
	}
	handler := func(args ...any) (any, error) {
		h.entered <- struct{}{}
		<-h.release
		if len(args) > 0 {
			if ev, ok := args[0].(extension.MessageUpdateEvent); ok {
				h.mu.Lock()
				h.deliveries = append(h.deliveries, ev)
				h.mu.Unlock()
			}
		}
		h.done <- struct{}{}
		return nil, nil
	}
	h.runner = inproc.NewRunner([]extension.Extension{{Path: "/tmp/batch.ts", Handlers: map[string][]extension.HandlerFn{EventMessageUpdate: {handler}}}}, ".")
	return h
}

// queueInFlight emits the first update and waits until its dispatch has
// actually entered the handler, so later emits land in the queue behind it.
func (h *batchHarness) queueInFlight(t *testing.T, first agent.MessageUpdateEvent, rest ...agent.MessageUpdateEvent) {
	t.Helper()
	emitMessageUpdateContext(t.Context(), h.runner, first.Message, first.AssistantMessageEvent)
	<-h.entered
	for _, u := range rest {
		emitMessageUpdateContext(t.Context(), h.runner, u.Message, u.AssistantMessageEvent)
	}
}

func (h *batchHarness) delivered() []extension.MessageUpdateEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]extension.MessageUpdateEvent{}, h.deliveries...)
}

func textUpdate(delta string, msgText string) agent.MessageUpdateEvent {
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: msgText}}, StopReason: ai.StopReasonPending}
	return agent.MessageUpdateEvent{
		Message:               agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: partial.Content}},
		AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: delta, Partial: partial},
	}
}

// Consecutive text deltas of one content block merge into a single delivery
// while a dispatch is in flight; the merged delta is the concatenation and the
// message state is the latest one.
func TestMessageUpdateBatcherMergesConsecutiveDeltas(t *testing.T) {
	h := newBatchHarness(t)
	h.queueInFlight(t, textUpdate("a", "a"), textUpdate("b", "ab"), textUpdate("c", "abc"))

	close(h.release) // unblock the in-flight dispatch
	<-h.done         // first delivery recorded
	<-h.done         // merged delivery recorded

	got := h.delivered()
	if len(got) != 2 {
		t.Fatalf("deliveries = %d, want 2 (first + merged)", len(got))
	}
	merged, ok := got[1].AssistantMessageEvent.(ai.TextDeltaEvent)
	if !ok {
		t.Fatalf("merged event type = %T", got[1].AssistantMessageEvent)
	}
	if merged.Delta != "bc" {
		t.Fatalf("merged delta = %q, want %q", merged.Delta, "bc")
	}
}

// Different delta kinds never merge; they deliver in queue order.
func TestMessageUpdateBatcherKeepsDistinctDeltaKinds(t *testing.T) {
	h := newBatchHarness(t)
	text := textUpdate("a", "a")
	thinking := textUpdate("t", "a")
	thinking.AssistantMessageEvent = ai.ThinkingDeltaEvent{ContentIndex: 1, Delta: "t"}
	h.queueInFlight(t, text, thinking)

	close(h.release)
	<-h.done
	<-h.done

	got := h.delivered()
	if len(got) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(got))
	}
	if _, ok := got[0].AssistantMessageEvent.(ai.TextDeltaEvent); !ok {
		t.Fatalf("first delivery = %T, want TextDelta", got[0].AssistantMessageEvent)
	}
	if _, ok := got[1].AssistantMessageEvent.(ai.ThinkingDeltaEvent); !ok {
		t.Fatalf("second delivery = %T, want ThinkingDelta", got[1].AssistantMessageEvent)
	}
}

// DrainMessageUpdates returns only after every queued update is delivered, so
// a boundary event dispatched behind it observes them first.
func TestDrainMessageUpdatesWaitsForQueue(t *testing.T) {
	h := newBatchHarness(t)
	h.queueInFlight(t, textUpdate("a", "a"), textUpdate("b", "ab"), textUpdate("c", "abc"))

	boundarySeen := make(chan struct{})
	go func() {
		DrainMessageUpdates(h.runner)
		close(boundarySeen)
	}()
	// The drain must not complete while the first dispatch is still in flight.
	select {
	case <-boundarySeen:
		t.Fatal("drain completed while a dispatch was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(h.release)
	<-h.done
	<-h.done // "bc" merged delivery
	select {
	case <-boundarySeen:
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not complete after queue emptied")
	}
	if got := h.delivered(); len(got) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(got))
	}
}
