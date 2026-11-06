package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts flushCompactionQueue (4879-4956), the restoreQueue branch.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui/widthx"
)

func pendingTexts(m *InteractiveMode) (steering, followUp []string) {
	queuedSteering, queuedFollowUp := m.agent.PendingMessages()
	for _, msg := range queuedSteering {
		steering = append(steering, extractAgentMessageText(msg))
	}
	for _, msg := range queuedFollowUp {
		followUp = append(followUp, extractAgentMessageText(msg))
	}
	return steering, followUp
}

func settleSubmissions(m *InteractiveMode) {
	for range 4 {
		synctest.Wait()
		m.drainMainLoopOnce()
	}
}

// Flushing moves every queued message out of the compaction queue at once, in order, into the run that still drains its queues: a steering message steers and a
// follow-up follows up, whether the run is about to retry (the compaction ended with willRetry) or is still settling. The pending display no longer lists them as
// compaction-queued messages.
func TestFlushCompactionQueueDeliversInOrderToTheActiveRun(t *testing.T) {
	queue := []compactionQueuedMessage{
		{text: "s1", mode: compactionQueueSteer}, {text: "f1", mode: compactionQueueFollowUp},
		{text: "s2", mode: compactionQueueSteer}, {text: "f2", mode: compactionQueueFollowUp},
	}
	for _, tc := range []struct {
		name      string
		willRetry bool
	}{{"retry", true}, {"settling run", false}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, _ := newTickRenderProbe(t, "regular")
				m.turnActive.Store(true)
				m.compactionQueue = slices.Clone(queue)
				m.flushCompactionQueue(t.Context(), tc.willRetry)
				settleSubmissions(m)
				steering, followUp := pendingTexts(m)
				if !slices.Equal(steering, []string{"s1", "s2"}) || !slices.Equal(followUp, []string{"f1", "f2"}) {
					t.Fatalf("steering=%q followUp=%q, want [s1 s2] and [f1 f2] in order", steering, followUp)
				}
				if len(m.compactionQueue) != 0 {
					t.Fatalf("the compaction queue keeps %d messages", len(m.compactionQueue))
				}
				display := widthx.StripAnsi(strings.Join(m.pendingMessagesContainer.Render(100), "\n"))
				if strings.Count(display, "Steering: s1") != 1 || strings.Count(display, "Follow-up: f2") != 1 {
					t.Fatalf("pending display lists each queued message once: %q", display)
				}
			})
		})
	}
}

// An empty compaction queue flushes nothing and leaves the Session's queues alone.
func TestFlushCompactionQueueWithNothingQueuedChangesNothing(t *testing.T) {
	m, _ := newTickRenderProbe(t, "regular")
	m.steerMessageWithImages("already queued", nil)
	m.flushCompactionQueue(t.Context(), false)
	m.flushCompactionQueue(t.Context(), true)
	if steering, followUp := pendingTexts(m); !slices.Equal(steering, []string{"already queued"}) || len(followUp) != 0 {
		t.Fatalf("steering=%q followUp=%q", steering, followUp)
	}
}

// A prompt that the run rejects while the messages queued during compaction are flushed puts the whole queue back, clears the Session's queue, and shows
// "Failed to send queued message(s): <error>" in place of the rejection's own error line; the plural follows the size of the queue.
func TestFlushCompactionQueueRestoresTheQueueWhenThePromptIsRejected(t *testing.T) {
	for _, queued := range [][]compactionQueuedMessage{
		{{text: "only", mode: compactionQueueSteer}},
		{{text: "first", mode: compactionQueueSteer}, {text: "second", mode: compactionQueueFollowUp}},
	} {
		t.Run(queued[0].text, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, _ := newTickRenderProbe(t, "regular")
				m.agent.SetModel(nil)
				m.compactionQueue = append([]compactionQueuedMessage(nil), queued...)
				m.flushCompactionQueue(t.Context(), false)
				// The submission hops between the owner loop and its workers: preflight, then the turn's own goroutine.
				for range 4 {
					synctest.Wait()
					m.drainMainLoopOnce()
				}
				if len(m.compactionQueue) != len(queued) || m.compactionQueue[0].text != queued[0].text {
					t.Fatalf("queue = %v, want the %d messages restored; chat %q", m.compactionQueue, len(queued), widthx.StripAnsi(strings.Join(m.chatContainer.Render(300), "\n")))
				}
				plural := ""
				if len(queued) > 1 {
					plural = "s"
				}
				chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(300), "\n"))
				want := "Error: Failed to send queued message" + plural + ": " + FormatNoModelSelectedMessage()
				if !strings.Contains(strings.Join(strings.Fields(chat), " "), strings.Join(strings.Fields(want), " ")) {
					t.Fatalf("chat = %q, want %q", chat, want)
				}
				if strings.Contains(strings.Join(strings.Fields(chat), " "), "Error: "+strings.Join(strings.Fields(FormatNoModelSelectedMessage()), " ")) {
					t.Fatalf("the rejection's own error line is shown as well: %q", chat)
				}
				if steering, followUps := m.agent.PendingMessages(); len(steering)+len(followUps) != 0 {
					t.Fatalf("the Session queue keeps %d messages", len(steering)+len(followUps))
				}
			})
		})
	}
}
