// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent.ts runWithLifecycle/handleRunFailure turns a rejected preparation into one failed assistant lifecycle and never starts another request.
func TestPrepareNextTurnRejectionFinalizesFailedAssistant(t *testing.T) {
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return doneStream(textMessage("first")) }}
	failure := errors.New("invalid compaction setting")
	rejecting := false
	var phases []string
	var endMessages []AgentMessage
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), PrepareNextTurnWithContext: func(context.Context, PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
		rejecting = true
		return nil, failure
	}, OnEvent: func(event AgentEvent) {
		if !rejecting {
			return
		}
		switch ev := event.(type) {
		case MessageStartEvent:
			phases = append(phases, "message_start")
		case MessageEndEvent:
			phases = append(phases, "message_end")
		case TurnEndEvent:
			phases = append(phases, "turn_end")
		case AgentEndEvent:
			phases = append(phases, "agent_end")
			endMessages = ev.Messages
		}
	}})
	a.FollowUp(userMessage("next"))
	messages, err := a.Send(t.Context(), "start")
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls() != 1 {
		t.Fatalf("provider requests=%d", provider.calls())
	}
	if !reflect.DeepEqual(phases, []string{"message_start", "message_end", "turn_end", "agent_end"}) {
		t.Fatal(phases)
	}
	if len(endMessages) != 1 || endMessages[0].Assistant == nil {
		t.Fatal(endMessages)
	}
	assistant := endMessages[0].Assistant
	if assistant.ErrorMessage != failure.Error() || assistant.StopReason != ai.StopReasonError || assistant.Usage == nil || *assistant.Usage != (ai.Usage{}) {
		t.Fatalf("failure=%+v", assistant)
	}
	if len(messages) == 0 || messages[len(messages)-1].Assistant == nil || messages[len(messages)-1].Assistant.ErrorMessage != failure.Error() {
		t.Fatal(messages)
	}
}
