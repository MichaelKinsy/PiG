package coding

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// eventPolicyTestEvents scripts a turn that emits 300 text-delta events —
// well past the 2×64 default event buffers — the same shape
// TestSendDeadlocksWithoutEventDrainThenUnblocks uses.
func eventPolicyTestEvents() []ai.AssistantMessageEvent {
	partial := sessionTestMessage("fake", "", ai.StopReasonPending, "")
	evs := []ai.AssistantMessageEvent{ai.StartEvent{Partial: partial}}
	for index := range 300 {
		partial = sessionTestMessage("fake", strings.Repeat("x", index+1), ai.StopReasonPending, "")
		evs = append(evs, ai.TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: partial})
	}
	final := sessionTestMessage("fake", strings.Repeat("x", 300), ai.StopReasonStop, "")
	evs = append(evs, ai.DoneEvent{Reason: ai.StopReasonStop, Message: final})
	return evs
}

// TestSessionEventDropPolicyNeverBlocksSend proves the EventDrop policy: a
// turn that emits more events than the buffers hold completes when nothing
// reads Events(), instead of blocking the agent loop forever. The default
// EventBlock policy keeps the documented blocking behavior — pinned by
// TestSendDeadlocksWithoutEventDrainThenUnblocks.
func TestSessionEventDropPolicyNeverBlocksSend(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{
		Model:       fakeModelWithProvider(staticProviderForSessionTests{events: eventPolicyTestEvents()}),
		EventPolicy: EventDrop,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	done := make(chan error, 1)
	go func() {
		_, e := sess.Send(t.Context(), "hi")
		done <- e
	}()

	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("Send returned error: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send blocked with EventPolicy: EventDrop and no Events() consumer")
	}

	// Dropping live events loses nothing durable: the transcript holds the
	// complete assistant message.
	msgs := sess.Messages()
	last := msgs[len(msgs)-1]
	if last.Assistant == nil {
		t.Fatalf("transcript has no assistant message: %v", msgs)
	}
	var text string
	for _, block := range last.Assistant.Content {
		if tc, ok := block.(ai.TextContent); ok {
			text += tc.Text
		}
	}
	if text != strings.Repeat("x", 300) {
		t.Fatalf("transcript lost the turn: got %d chars, want 300", len(text))
	}
}
