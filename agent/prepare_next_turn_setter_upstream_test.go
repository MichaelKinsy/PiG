package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/test/agent-loop.test.ts:1503 "should use prepareNextTurn snapshot before continuing", and agent.ts:211
// (`public prepareNextTurn`, assigned after construction): a hook installed with SetPrepareNextTurn runs after the first turn's tool results,
// and its update's messages are in the second provider request only.
func TestSetPrepareNextTurnMessagesReachTheNextRequest(t *testing.T) {
	var requests [][]ai.Message
	provider := &scriptedProvider{respond: func(call int, req scriptedRequest) *ai.AssistantMessageEventStream {
		requests = append(requests, req.transcript.Messages())
		if call == 1 {
			return doneStream(toolUseMessage(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"})))
		}
		return doneStream(textMessage("done"))
	}}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(string) {})}})
	calls := 0
	a.SetPrepareNextTurn(func(context.Context) (*AgentLoopTurnUpdate, error) {
		calls++
		if calls > 1 {
			return nil, nil
		}
		return &AgentLoopTurnUpdate{Messages: []AgentMessage{{System: &ai.SystemMessage{Content: ai.SystemText("updated guidance"), Timestamp: 1}}}}, nil
	})
	mustSend(t, a, "echo something")
	if len(requests) != 2 {
		t.Fatalf("provider saw %d requests, want 2", len(requests))
	}
	hasGuidance := func(messages []ai.Message) bool {
		return slices.ContainsFunc(messages, func(m ai.Message) bool {
			system, ok := m.(ai.SystemMessage)
			return ok && system.Content == ai.SystemText("updated guidance")
		})
	}
	if hasGuidance(requests[0]) || !hasGuidance(requests[1]) {
		t.Fatalf("guidance must reach only the second request: first=%v second=%v", requests[0], requests[1])
	}
	if calls != 1 {
		t.Fatalf("prepareNextTurn ran %d times, want once: it runs only before a turn that follows tool results", calls)
	}
}
