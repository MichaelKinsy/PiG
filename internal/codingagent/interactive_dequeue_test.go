package codingagent_test

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Alt+Up restores queued messages to the editor and removes them from the
// queue (upstream handleDequeue → restoreQueuedMessagesToEditor → clearAllQueues),
// so the queued message is neither shown as pending nor sent when the turn ends.
func TestInteractiveAltUpDequeuesQueuedMessages(t *testing.T) {
	for _, queue := range []struct{ name, submit string }{{"steer", "\r"}, {"followUp", "\x1b[13;3u"}} {
		for _, key := range []string{"\x1b[1;3A", "\x1bp"} {
			t.Run(queue.name+strings.NewReplacer("\x1b", "ESC").Replace(key), func(t *testing.T) {
				requested := make(chan struct{})
				release := make(chan struct{})
				pair := newSessionPair(t, `{"retry":{"enabled":false}}`, 100000, nil,
					func() *ai.AssistantMessage {
						close(requested)
						<-release
						return reply("first answer", ai.Usage{Input: 10, Output: 10})()
					},
					reply("next answer", ai.Usage{Input: 10, Output: 10}),
				)
				pair.harness.Do(func() { pair.harness.Enter("start") })
				<-requested
				pair.harness.Do(func() {
					pair.harness.SetEditorText("also update the docs")
					pair.harness.Key(queue.submit)
				})
				if steering, followUp := pair.harness.QueuedMessages(); steering+followUp != 1 {
					t.Fatalf("queued before Alt+Up: steering=%d followUp=%d, want exactly one", steering, followUp)
				}
				if !strings.Contains(pair.harness.PendingDisplay(), "also update the docs") {
					t.Fatalf("pending display before Alt+Up lacks the queued message:\n%s", pair.harness.PendingDisplay())
				}
				pair.harness.Do(func() { pair.harness.Key(key) })
				if got := pair.harness.PendingDisplay(); strings.TrimSpace(got) != "" {
					t.Fatalf("pending display after Alt+Up = %q, want empty", got)
				}
				if got := pair.harness.EditorText(); got != "also update the docs" {
					t.Fatalf("editor after Alt+Up = %q, want the queued message restored", got)
				}
				if steering, followUp := pair.harness.QueuedMessages(); steering != 0 || followUp != 0 {
					t.Fatalf("queues after Alt+Up: steering=%d followUp=%d, want empty", steering, followUp)
				}
				close(release)
				pair.harness.WaitIdle(t, 10*time.Second)
				time.Sleep(200 * time.Millisecond)
				if n := len(pair.provider.requestLog()); n != 1 {
					t.Fatalf("model requests = %d, want 1: the dequeued message must not be sent", n)
				}
			})
		}
	}
}
