package coding

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// The listener runs on the Session executor. Its publications must not wait for that same executor, even when the burst exceeds the bounded raw-event mailbox.
func TestSessionMessageSubscriberQueueBurstPreservesPublicationOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
		count := 2 * cap(h.session.rawEvents)
		queued := false
		h.session.Subscribe(func(event agent.AgentEvent) {
			if start, ok := event.(agent.MessageStartEvent); ok && start.Message.User != nil && !queued {
				queued = true
				for i := range count {
					h.session.QueueFollowUp(fmt.Sprintf("burst-%d", i), nil)
				}
			}
		})
		done := make(chan error, 1)
		go func() { _, err := h.session.Prompt(t.Context(), "hello"); done <- err }()
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Error("queue publication blocks the message subscriber at the raw event capacity")
			_ = h.session.Close()
			<-done
			return
		}
		if err := h.session.FlushEvents(t.Context()); err != nil {
			t.Fatal(err)
		}
		var users []string
		for _, message := range h.session.Messages() {
			if message.User != nil {
				users = append(users, extractUserMessageText(message.User.Content))
			}
		}
		wantUsers := []string{"hello"}
		for i := range count {
			wantUsers = append(wantUsers, fmt.Sprintf("burst-%d", i))
		}
		if !slices.Equal(users, wantUsers) {
			t.Fatalf("users=%v, want=%v", users, wantUsers)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		updates := 0
		for _, event := range h.events {
			if start, ok := event.(agent.MessageStartEvent); ok && start.Message.User != nil {
				break
			}
			if update, ok := event.(agent.QueueUpdateEvent); ok {
				updates++
				if len(update.FollowUp) != updates {
					t.Fatalf("queue update %d has %d messages", updates, len(update.FollowUp))
				}
			}
		}
		if updates != count {
			t.Fatalf("queue updates before parent message_start=%d, want=%d", updates, count)
		}
		h.session.queueEvents.mu.Lock()
		defer h.session.queueEvents.mu.Unlock()
		if h.session.queueEvents.overflow != nil || h.session.queueEvents.prefix != 0 {
			t.Fatal("settled run retains queue publication overflow")
		}
	})
}

func TestSessionQueuePublicationOverflowIsReleasedOnClose(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	h.session.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.AgentStartEvent); !ok {
			return
		}
		for i := range 2 * cap(h.session.rawEvents) {
			h.session.QueueSteer(fmt.Sprintf("queued-%d", i), nil)
		}
		if err := h.session.Close(); err != nil {
			t.Error(err)
		}
	})
	h.session.emitOrderedEventSync(agent.AgentStartEvent{})
	<-h.done
	h.session.queueEvents.mu.Lock()
	defer h.session.queueEvents.mu.Unlock()
	if h.session.queueEvents.overflow != nil || h.session.queueEvents.notifying {
		t.Fatal("closed Session retains queue publication state")
	}
}
