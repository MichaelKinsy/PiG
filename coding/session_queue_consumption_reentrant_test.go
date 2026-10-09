package coding

import (
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent-session.ts:_handleAgentEvent removes queued text and emits queue_update before message_start handlers. _emit calls listeners directly, so a guarded nested queue mutation finishes before delivery resumes.
func TestSessionConsumedQueueSubscriberReentrantUpdates(t *testing.T) {
	for _, mutation := range []string{"clear", "steer", "followUp"} {
		t.Run(mutation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
				h.session.QueueSteer("outer", nil)
				var order []string
				nested, observing := false, true
				h.session.Subscribe(func(event agent.AgentEvent) {
					if !observing {
						return
					}
					switch event := event.(type) {
					case agent.QueueUpdateEvent:
						order = append(order, "a:"+queueUpdateLabel(event))
						if nested {
							return
						}
						nested = true
						switch mutation {
						case "clear":
							steering, followUp := h.session.ClearQueue()
							if len(steering)+len(followUp) != 0 {
								t.Errorf("consumed text still pending: steering=%v followUp=%v", steering, followUp)
							}
						case "steer":
							h.session.QueueSteer("inner", nil)
						case "followUp":
							h.session.QueueFollowUp("inner", nil)
						}
						order = append(order, "nested returned")
					case agent.MessageStartEvent:
						if event.Message.User != nil && extractUserMessageText(event.Message.User.Content) == "outer" {
							order = append(order, fmt.Sprintf("message_start:pending=%d", h.session.PendingMessageCount()))
							observing = false
						}
					}
				})
				h.session.Subscribe(func(event agent.AgentEvent) {
					if update, ok := event.(agent.QueueUpdateEvent); ok && observing {
						order = append(order, "b:"+queueUpdateLabel(update))
					}
				})
				done := make(chan error, 1)
				go func() { err := h.session.Prompt(t.Context(), "hello"); done <- err }()
				synctest.Wait()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				default:
					t.Error("consumption queue_update callback blocked its nested mutation")
					_ = h.session.Close()
					<-done
					return
				}
				inner := map[string]string{"clear": "[]/[]", "steer": "[inner]/[]", "followUp": "[]/[inner]"}[mutation]
				pending := 1
				if mutation == "clear" {
					pending = 0
				}
				want := []string{"a:[]/[]", "a:" + inner, "b:" + inner, "nested returned", "b:[]/[]", fmt.Sprintf("message_start:pending=%d", pending)}
				if !reflect.DeepEqual(order, want) {
					t.Errorf("notification order=%v, want=%v", order, want)
				}
				if h.session.PendingMessageCount() != 0 || h.session.Agent().HasQueuedMessages() {
					t.Error("completed run retains consumed queue input")
				}
			})
		})
	}
}
