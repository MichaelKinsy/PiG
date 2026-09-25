package coding

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestSessionQueueOperationsPreserveModesAndEmitUpdates(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()

	if err := sess.SetSteeringMode(agent.QueueModeAll); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetFollowUpMode(agent.QueueModeOneAtATime); err != nil {
		t.Fatal(err)
	}
	if sess.Agent().SteeringMode() != agent.QueueModeAll || sess.Agent().FollowUpMode() != agent.QueueModeOneAtATime {
		t.Fatalf("queue modes = %q/%q", sess.Agent().SteeringMode(), sess.Agent().FollowUpMode())
	}

	sess.Steer("redirect", []ai.ImageContent{{MimeType: "image/png", Data: "aW1n"}})
	sess.FollowUp("later", nil)
	if got := sess.PendingMessageCount(); got != 2 {
		t.Fatalf("pending messages = %d, want 2", got)
	}
	steering, followUp := sess.ClearQueue()
	if len(steering) != 1 || steering[0] != "redirect" {
		t.Fatalf("cleared steering = %#v", steering)
	}
	if len(followUp) != 1 || followUp[0] != "later" {
		t.Fatalf("cleared follow-up = %#v", followUp)
	}
	if got := sess.PendingMessageCount(); got != 0 {
		t.Fatalf("pending messages after clear = %d", got)
	}

	var updates []agent.QueueUpdateEvent
	deadline := time.After(time.Second)
	for len(updates) < 3 {
		select {
		case event := <-sess.Events():
			if update, ok := event.(agent.QueueUpdateEvent); ok {
				updates = append(updates, update)
			}
		case <-deadline:
			t.Fatalf("queue updates = %#v, want three", updates)
		}
	}
	if got := updates[2]; len(got.Steering) != 0 || len(got.FollowUp) != 0 {
		t.Fatalf("final queue update = %#v, want empty", got)
	}
}

func TestSessionThinkingLevelUsesModelCapabilities(t *testing.T) {
	model := fakeModel()
	model.Capabilities.MaxThinking = ai.ThinkingHigh
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()

	levels := sess.AvailableThinkingLevels()
	if len(levels) != 5 || levels[0] != ai.ThinkingOff || levels[4] != ai.ThinkingHigh {
		t.Fatalf("available thinking levels = %#v", levels)
	}
	if err := sess.SetThinkingLevel(ai.ThinkingMax); err != nil {
		t.Fatal(err)
	}
	if got := sess.ThinkingLevel(); got != ai.ThinkingHigh {
		t.Fatalf("thinking level = %q, want high", got)
	}
}

type retryUntilCancelledProvider struct {
	calls chan struct{}
}

func (p *retryUntilCancelledProvider) ID() string   { return "retry-cancel" }
func (p *retryUntilCancelledProvider) Close() error { return nil }
func (p *retryUntilCancelledProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	select {
	case p.calls <- struct{}{}:
	default:
	}
	message := sessionTestMessage(p.ID(), "", ai.StopReasonError, "please retry your request")
	return newSessionTestStream(
		ai.StartEvent{Partial: message},
		ai.ErrorEvent{Reason: ai.StopReasonError, Error: message},
	), nil
}

func TestSessionAbortRetryCancelsDelay(t *testing.T) {
	services := newTestServices(t)
	enabled := true
	if err := services.SettingsManager().UpdateGlobal(func(settings *icodingagent.Settings) {
		settings.Retry = &icodingagent.RetrySettingsJSON{
			Enabled:     &enabled,
			MaxRetries:  new(3),
			BaseDelayMs: new(10_000),
		}
	}); err != nil {
		t.Fatal(err)
	}
	provider := &retryUntilCancelledProvider{calls: make(chan struct{}, 1)}
	model := &ai.Model{ID: "retry-cancel", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	sess, err := NewSession(services, SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()

	done := make(chan error, 1)
	go func() {
		_, err := sess.Send(context.Background(), "retry")
		done <- err
	}()
	select {
	case <-provider.calls:
	case <-time.After(time.Second):
		t.Fatal("provider was not called")
	}

	deadline := time.Now().Add(time.Second)
	for {
		sess.retryMu.Lock()
		waiting := sess.retryCancel != nil
		sess.retryMu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry delay did not start")
		}
		time.Sleep(time.Millisecond)
	}
	sess.AbortRetry()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("AbortRetry did not cancel the retry delay")
	}
}
