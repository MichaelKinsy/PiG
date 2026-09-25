package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestCancelledStreamFinalizesAbortedTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		if err := stream.Push(ai.StartEvent{Partial: agentTestAssistant(nil, ai.StopReasonPending)}); err != nil {
			t.Fatal(err)
		}
		return stream
	}}
	var finished []AgentTurnContext
	var persisted []AgentMessage
	a := NewAgent(AgentOptions{Model: scriptedModel(provider),
		FinishTurn: func(_ context.Context, turn AgentTurnContext) *AgentTurnDecision {
			finished = append(finished, turn)
			return &AgentTurnDecision{Action: AgentTurnContinue}
		},
		OnMessagePersist: func(message AgentMessage) error {
			persisted = append(persisted, message)
			return nil
		},
	})
	messages, err := a.Send(ctx, "cancel me")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error %v", err)
	}
	if len(finished) != 1 {
		t.Fatalf("FinishTurn calls %d, want 1", len(finished))
	}
	if len(messages) != 2 || messages[1].Assistant == nil || messages[1].Assistant.StopReason != ai.StopReasonAborted {
		t.Fatalf("aborted transcript %+v", messages)
	}
	if len(finished[0].Context) != 2 || len(finished[0].NewMessages) != 2 || finished[0].Message.StopReason != ai.StopReasonAborted {
		t.Fatalf("aborted turn context %+v", finished[0])
	}
	if len(persisted) != 2 || persisted[1].Assistant.StopReason != ai.StopReasonAborted {
		t.Fatalf("persisted messages %+v", persisted)
	}
	if provider.calls() != 1 || a.IsStreaming() {
		t.Fatalf("calls %d streaming %v", provider.calls(), a.IsStreaming())
	}
}
