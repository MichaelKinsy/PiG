package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// An extension can inject a custom message while a turn is in flight, e.g.
// pi-chain reporting a finished chain with deliverAs "followUp". Persisting it
// at creation time recorded it between an assistant's tool_use and its
// tool_result. Replaying that session then produced an invalid sequence and the
// provider rejected the whole request:
//
//	github-copilot: HTTP 400: invalid_request_error: messages.338: `tool_use`
//	ids were found without `tool_result` blocks immediately after
//
// Persistence must therefore run when the agent delivers the message, which is
// the same message_end site every other message uses.
func TestPersistMessage_IncludesCustomMessages(t *testing.T) {
	var persisted []AgentMessage
	a := NewAgent(AgentOptions{
		OnMessagePersist: func(m AgentMessage) error {
			persisted = append(persisted, m)
			return nil
		},
	})

	custom := AgentMessage{Custom: map[string]any{
		"role":       RoleCustom,
		"customType": "chain-completion",
		"content":    "chain finished",
	}}
	if err := a.persistMessage(custom); err != nil {
		t.Fatalf("persist custom message: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("custom message not persisted (got %d); a queued custom message would be lost from the session", len(persisted))
	}
	if got, _ := persisted[0].Custom["customType"].(string); got != "chain-completion" {
		t.Errorf("persisted customType = %q, want chain-completion", got)
	}

	// Bash executions keep their own append path and must not double-persist.
	persisted = nil
	if err := a.persistMessage(AgentMessage{}); err != nil {
		t.Fatalf("persist empty message: %v", err)
	}
	if len(persisted) != 0 {
		t.Errorf("empty message persisted %d times, want 0", len(persisted))
	}
}

// The delivered order is what gets recorded: a follow-up queued mid-turn is
// persisted after the tool_result that was already in flight, never between the
// tool_use and its result.
func TestFollowUpCustomMessagePersistsAfterToolResult(t *testing.T) {
	var order []string
	a := NewAgent(AgentOptions{
		OnMessagePersist: func(m AgentMessage) error {
			switch {
			case m.Assistant != nil:
				order = append(order, "assistant")
			case m.ToolResult != nil:
				order = append(order, "toolResult")
			case m.Custom != nil:
				order = append(order, "custom")
			}
			return nil
		},
	})

	// The sequence the agent delivers: assistant tool_use, its result, then the
	// queued custom message drained afterwards.
	if err := a.persistMessage(AgentMessage{Assistant: &AssistantMessage{
		Role:    RoleAssistant,
		Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "bash"}},
	}}); err != nil {
		t.Fatalf("persist assistant message: %v", err)
	}
	if err := a.persistMessage(AgentMessage{ToolResult: &ToolResultMessage{
		Role: RoleToolResult, ToolCallID: "call-1",
	}}); err != nil {
		t.Fatalf("persist tool result: %v", err)
	}
	if err := a.persistMessage(AgentMessage{Custom: map[string]any{
		"role": RoleCustom, "customType": "chain-completion", "content": "done",
	}}); err != nil {
		t.Fatalf("persist custom message: %v", err)
	}

	want := []string{"assistant", "toolResult", "custom"}
	if len(order) != len(want) {
		t.Fatalf("persist order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("persist order = %v, want %v", order, want)
		}
	}
}
