package coding

import (
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:1879-1904 — queue metadata is published synchronously before Agent admission.
func TestSessionQueueUpdatePrecedesAgentAdmission(t *testing.T) {
	for _, followUp := range []bool{false, true} {
		name := "steer"
		if followUp {
			name = "followUp"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true})
				entered, release := make(chan struct{}), make(chan struct{})
				h.session.Subscribe(func(event agent.AgentEvent) {
					if update, ok := event.(agent.QueueUpdateEvent); ok && len(update.Steering)+len(update.FollowUp) == 1 {
						close(entered)
						<-release
					}
				})
				done := make(chan error, 1)
				go func() {
					var err error
					if followUp {
						_, err = h.session.FollowUp(t.Context(), "queued", nil, nil)
					} else {
						_, err = h.session.Steer(t.Context(), "queued", nil, nil)
					}
					done <- err
				}()
				synctest.Wait()
				select {
				case <-entered:
				default:
					t.Error("queue_update subscriber was not invoked")
				}
				if pending := h.session.PendingMessageCount(); pending != 1 {
					t.Errorf("pending=%d want1", pending)
				}
				if h.session.Agent().HasQueuedMessages() {
					t.Error("Agent admission overtook queue_update subscriber completion")
				}
				close(release)
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

// upstream: packages/coding-agent/src/core/agent-session.ts:896-919 — nonempty queued text is removed before extension and public message_start handlers.
func TestSessionQueueRemovalPrecedesExtensionMessageStart(t *testing.T) {
	var h *recoveryHarness
	var mu sync.Mutex
	var observed []string
	h = newRecoveryHarness(t, harnessOptions{emptySessionManager: true, extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"message_start": {func(args ...any) (any, error) {
			message := args[0].(extension.MessageStartEvent).Message
			if message.User != nil && extractUserMessageText(message.User.Content) == "queued" {
				mu.Lock()
				defer mu.Unlock()
				if h.session.PendingMessageCount() != 0 {
					t.Error("extension saw queued text still pending")
				}
				observed = append(observed, "extension")
			}
			return nil, nil
		}},
	}}}, fauxReply("done", ai.StopReasonStop, 0))
	h.session.Subscribe(func(event agent.AgentEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch event := event.(type) {
		case agent.QueueUpdateEvent:
			if len(event.Steering)+len(event.FollowUp) == 0 {
				observed = append(observed, "removed")
			}
		case agent.MessageStartEvent:
			if event.Message.User != nil && extractUserMessageText(event.Message.User.Content) == "queued" {
				observed = append(observed, "public")
			}
		}
	})
	if _, err := h.session.Steer(t.Context(), "queued", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.session.Send(t.Context(), "start"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"removed", "extension", "public"}; !reflect.DeepEqual(observed, want) {
		t.Fatalf("order=%v want=%v", observed, want)
	}
}

// upstream: packages/coding-agent/src/core/agent-session.ts:896-909 — the truthy contentText guard deliberately retains an empty queued string in pending metadata.
func TestSessionEmptyQueuedTextRetainsUpstreamPendingState(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true}, fauxReply("done", ai.StopReasonStop, 0))
	if _, err := h.session.Steer(t.Context(), "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.session.Send(t.Context(), "start"); err != nil {
		t.Fatal(err)
	}
	if pending := h.session.PendingMessageCount(); pending != 1 {
		t.Fatalf("empty pending count=%d want1", pending)
	}
	found := false
	for _, message := range h.session.Messages() {
		if message.User == nil || extractUserMessageText(message.User.Content) != "" {
			continue
		}
		found = true
		blocks, ok := message.User.Content.(ai.UserContentBlocks)
		if !ok || len(blocks) != 1 || blocks[0] != (ai.TextContent{Text: ""}) {
			t.Fatalf("empty user content=%#v", message.User.Content)
		}
	}
	if !found {
		t.Fatal("empty queued message was lost")
	}
}
