package coding

import (
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func queueUpdateLabel(update agent.QueueUpdateEvent) string {
	return fmt.Sprintf("%v/%v", update.Steering, update.FollowUp)
}

// Pi agent-session.ts:831-843,1879-1904,2043-2051 invokes queue subscribers directly. A subscriber can synchronously clear or add input, and nested notifications finish before the outer notification resumes.
func TestSessionQueueSubscriberReentrantUpdates(t *testing.T) {
	for _, mutation := range []string{"clear", "steer", "followUp"} {
		t.Run(mutation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newRecoveryHarness(t, harnessOptions{})
				entered, done := make(chan struct{}), make(chan struct{})
				var mu sync.Mutex
				var order []string
				record := func(label string) { mu.Lock(); defer mu.Unlock(); order = append(order, label) }
				nested := false
				h.session.Subscribe(func(event agent.AgentEvent) {
					update, ok := event.(agent.QueueUpdateEvent)
					if !ok {
						return
					}
					record("a:" + queueUpdateLabel(update))
					if nested {
						return
					}
					nested = true
					close(entered)
					switch mutation {
					case "clear":
						steering, followUp := h.session.ClearQueue()
						if !slices.Equal(steering, []string{"outer"}) || len(followUp) != 0 {
							t.Errorf("clear returned steering=%v followUp=%v", steering, followUp)
						}
					case "steer":
						h.session.QueueSteer("inner", nil)
					case "followUp":
						h.session.QueueFollowUp("inner", nil)
					}
					record("nested returned")
				})
				h.session.Subscribe(func(event agent.AgentEvent) {
					if update, ok := event.(agent.QueueUpdateEvent); ok {
						record("b:" + queueUpdateLabel(update))
					}
				})
				go func() { h.session.QueueSteer("outer", nil); close(done) }()
				<-entered
				synctest.Wait()
				select {
				case <-done:
				default:
					t.Error("nested queue mutation blocked its caller on the Session event executor")
					_ = h.session.Close()
					<-done
					return
				}
				steering, followUp := h.session.Agent().PendingMessages()
				texts := func(messages []agent.AgentMessage) []string {
					out := make([]string, 0, len(messages))
					for _, message := range messages {
						out = append(out, extractUserMessageText(message.User.Content))
					}
					return out
				}
				wantSteering, wantFollowUp := []string{"outer"}, []string{}
				switch mutation {
				case "steer":
					wantSteering = []string{"inner", "outer"}
				case "followUp":
					wantFollowUp = []string{"inner"}
				}
				if !slices.Equal(texts(steering), wantSteering) || !slices.Equal(texts(followUp), wantFollowUp) {
					t.Fatalf("Agent queues: steering=%v followUp=%v", texts(steering), texts(followUp))
				}
				inner := map[string]string{"clear": "[]/[]", "steer": "[outer inner]/[]", "followUp": "[outer]/[inner]"}[mutation]
				want := []string{"a:[outer]/[]", "a:" + inner, "b:" + inner, "nested returned", "b:[outer]/[]"}
				mu.Lock()
				defer mu.Unlock()
				if !reflect.DeepEqual(order, want) {
					t.Fatalf("notification order=%v, want=%v", order, want)
				}
			})
		})
	}
}

func TestSessionQueueReentryBeyondEventBuffer(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	// Exceed the actual mailbox capacity rather than assuming the current default.
	count := 2 * cap(h.session.rawEvents)
	started := false
	h.session.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.QueueUpdateEvent); !ok || started {
			return
		}
		started = true
		for i := range count {
			h.session.QueueFollowUp(fmt.Sprintf("nested-%d", i), nil)
		}
	})
	h.session.QueueSteer("outer", nil)
	if err := h.session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var updates []agent.QueueUpdateEvent
	for _, event := range h.events {
		if update, ok := event.(agent.QueueUpdateEvent); ok {
			updates = append(updates, update)
		}
	}
	if len(updates) != count+1 {
		t.Fatalf("wire updates=%d, want %d", len(updates), count+1)
	}
	for i := range count {
		if len(updates[i].FollowUp) != i+1 || updates[i].FollowUp[i] != fmt.Sprintf("nested-%d", i) {
			t.Fatalf("nested wire update[%d]=%v", i, updates[i])
		}
	}
	if last := updates[count]; !slices.Equal(last.Steering, []string{"outer"}) || len(last.FollowUp) != 0 {
		t.Fatalf("outer event snapshot changed during nested notifications: %v", last)
	}
}

func TestSessionMessageSubscriberQueuesInputSynchronously(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
		queued := false
		h.session.Subscribe(func(event agent.AgentEvent) {
			if start, ok := event.(agent.MessageStartEvent); ok && start.Message.User != nil && !queued {
				queued = true
				h.session.QueueSteer("nested input", nil)
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
			t.Error("message subscriber blocked while queueing input")
			_ = h.session.Close()
			<-done
			return
		}
		if !queued || h.session.PendingMessageCount() != 0 {
			t.Fatalf("queued=%v pending=%d", queued, h.session.PendingMessageCount())
		}
		var users []string
		for _, message := range h.session.Messages() {
			if message.User != nil {
				users = append(users, extractUserMessageText(message.User.Content))
			}
		}
		if !slices.Equal(users, []string{"hello", "nested input"}) {
			t.Fatalf("users=%v", users)
		}
	})
}
