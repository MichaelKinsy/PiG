package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream Agent.processEvents awaits every listener for every event, so an
// aborted run still delivers its assistant message_end, turn_end, and
// agent_end. Cancelling the run context must not make a pending event send
// race the cancellation.
func TestAbortedRunDeliversTerminalEvents(t *testing.T) {
	for run := range 200 {
		events := make(chan AgentEvent, 64)
		started := make(chan struct{}, 1)
		ctx, cancel := context.WithCancel(context.Background())
		provider := &scriptedProvider{respond: func(_ int, req scriptedRequest) *ai.AssistantMessageEventStream {
			return abortableStream(req.ctx, started)
		}}
		a := NewAgent(AgentOptions{Model: scriptedModel(provider), EventCh: events})
		go func() {
			<-started
			cancel()
		}()
		_, _ = a.Send(ctx, "abort me")
		close(events)
		assistantEnds, turnEnds, agentEnds := 0, 0, 0
		for ev := range events {
			switch ev := ev.(type) {
			case MessageEndEvent:
				if ev.Message.Assistant != nil {
					assistantEnds++
				}
			case TurnEndEvent:
				turnEnds++
			case AgentEndEvent:
				agentEnds++
			}
		}
		if assistantEnds != 1 || turnEnds != 1 || agentEnds != 1 {
			t.Fatalf("run %d: assistant message_end %d, turn_end %d, agent_end %d; want one each", run, assistantEnds, turnEnds, agentEnds)
		}
	}
}

// EventDone releases a blocked emit once the event consumer has stopped, so
// an owner that stops draining EventCh cannot hang the agent.
func TestEventDoneReleasesBlockedEmit(t *testing.T) {
	done := make(chan struct{})
	close(done)
	provider := &scriptedProvider{respond: replyText("ok")}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider), EventCh: make(chan AgentEvent), EventDone: done})
	if _, err := a.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// cancelledStartProvider fails to start a stream because its request was
// cancelled, as a provider does when the abort lands before the request.
type cancelledStartProvider struct{}

func (cancelledStartProvider) ID() string   { return "cancelled-start" }
func (cancelledStartProvider) Close() error { return nil }
func (cancelledStartProvider) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return nil, ctx.Err()
}

// Upstream providers encode a request aborted before it streams as
// stopReason "aborted", so the run ends as aborted, not as a provider error.
func TestCancelledStreamStartEndsAborted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := NewAgent(AgentOptions{Model: &ai.Model{ID: "m", Provider: cancelledStartProvider{}}})
	messages, err := a.Send(ctx, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error %v, want context.Canceled", err)
	}
	last := messages[len(messages)-1].Assistant
	if last == nil || last.StopReason != ai.StopReasonAborted {
		t.Fatalf("last message %+v, want an aborted assistant message", messages[len(messages)-1])
	}
}
