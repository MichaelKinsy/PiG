package agent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream holds deliverAs "nextTurn" messages apart from the steering and
// follow-up queues and injects them beside the user message before the first
// model call (agent-session.ts:1226). Pig routed them into FollowUp, which
// continues the running turn instead, so a message meant as context for the
// next prompt reached the model a call too late: and on an idle agent sat in a
// queue with no goroutine to drain it.
func TestNextTurnMessagesArriveWithTheUserMessage(t *testing.T) {
	agent := newTestAgentForNextTurn(t)

	agent.QueueNextTurn(AgentMessage{Custom: map[string]any{
		"role": RoleCustom, "customType": "chain_state", "content": "step 2 of 3",
	}})
	if len(agent.pendingNextTurn) != 1 {
		t.Fatalf("QueueNextTurn deferred %d messages, want 1", len(agent.pendingNextTurn))
	}

	if _, err := agent.Send(context.Background(), "continue"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The deferred message must sit immediately after the user message, so the
	// first model call of the turn already carries it.
	userAt := -1
	for i, m := range agent.messages {
		if m.User != nil {
			userAt = i
		}
	}
	if userAt < 0 {
		t.Fatal("no user message in history")
	}
	if userAt+1 >= len(agent.messages) {
		t.Fatal("the deferred message was never injected")
	}
	next := agent.messages[userAt+1]
	if next.Custom == nil || next.Custom["customType"] != "chain_state" {
		t.Errorf("message after the user message = %+v; want the deferred chain_state", next)
	}
	if len(agent.pendingNextTurn) != 0 {
		t.Error("the deferred message was injected but not cleared")
	}
}

// A deferred message must not be delivered as a follow-up continuation.
func TestNextTurnDoesNotEnterTheFollowUpQueue(t *testing.T) {
	agent := newTestAgentForNextTurn(t)
	agent.QueueNextTurn(AgentMessage{Custom: map[string]any{"role": RoleCustom, "customType": "x"}})

	if agent.HasQueuedMessages() {
		t.Error("a nextTurn message entered the steering or follow-up queue")
	}
}

func newTestAgentForNextTurn(t *testing.T) *Agent {
	t.Helper()
	provider := &recordingProvider{seqs: [][]ai.AssistantMessageEvent{textSeq("")}}
	return NewAgent(AgentOptions{Model: &ai.Model{ID: "model", Provider: provider}})
}
