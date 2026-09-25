package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// toolPairingIsValid reports the first tool_use id in msgs that is not followed
// immediately by its tool_result. This is the rule Anthropic-family providers
// enforce and the one the live 400 named:
//
//	`tool_use` ids were found without `tool_result` blocks immediately after:
//	chatcmpl-tool-a5d0340fab6acaff
func firstUnpairedToolUse(msgs []agent.AgentMessage) string {
	for i, m := range msgs {
		if m.Assistant == nil {
			continue
		}
		want := map[string]bool{}
		var order []string
		for _, blk := range m.Assistant.Content {
			if tu, ok := blk.(ai.ToolCall); ok && tu.ID != "" {
				want[tu.ID] = true
				order = append(order, tu.ID)
			}
		}
		if len(want) == 0 {
			continue
		}
		// Parallel tool calls are answered by the immediately following run of
		// tool-result messages, which must cover exactly this set.
		for n := i + 1; n < len(msgs) && msgs[n].ToolResult != nil; n++ {
			delete(want, msgs[n].ToolResult.ToolCallID)
		}
		for _, id := range order {
			if want[id] {
				return id
			}
		}
	}
	return ""
}

// A custom message persisted between an assistant's tool_use and its
// tool_result makes every later replay of that session invalid, because
// convertToLLM renders it as a user message and splits the pair. Reproduces the
// exact shape recorded in a live session (entries 373-376: assistant toolCall,
// custom_message "chain-completion", toolResult).
func TestSessionReplay_CustomMessageDoesNotSplitToolPair(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-custom-order", "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	assistantID := mustAppend(t, sess, agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:       agent.RoleAssistant,
		StopReason: "toolUse",
		Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "running it"},
			ai.ToolCall{ID: "call-1", Name: "bash"},
		},
	}})
	_ = assistantID

	// The agent delivers the tool result, then drains the queued custom
	// message. Persisting in delivery order is what keeps the replay valid.
	mustAppend(t, sess, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role:       agent.RoleToolResult,
		ToolCallID: "call-1",
		Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}},
	}})
	mustAppend(t, sess, agent.AgentMessage{Custom: map[string]any{
		"role":       agent.RoleCustom,
		"customType": "chain-completion",
		"content":    "❌ Chain plan-build-review finished (324s).",
		"display":    true,
	}})

	replayed := sess.BuildContext(nil)
	if id := firstUnpairedToolUse(replayed); id != "" {
		t.Fatalf("replayed session splits tool pair %q; the provider rejects this request", id)
	}

	// The custom message must survive the round-trip, not be dropped to make
	// the pairing valid.
	var sawCustom bool
	for _, m := range replayed {
		if m.Custom != nil {
			if got, _ := m.Custom["customType"].(string); got == "chain-completion" {
				sawCustom = true
			}
		}
	}
	if !sawCustom {
		t.Error("custom message lost on replay; deferring persistence must not drop it")
	}

	// It must be recorded as a "custom_message" entry: the transcript, /tree and
	// the session selectors all key on that type, and it is the shape upstream
	// writes. A generic "message" entry round-trips but is invisible to them.
	flushSession(t, sess)
	var customEntries int
	for _, line := range readJSONLLines(t, sess.Path()) {
		if line["type"] == "custom_message" {
			customEntries++
			if line["customType"] != "chain-completion" {
				t.Errorf("custom entry customType = %v, want chain-completion", line["customType"])
			}
		}
	}
	if customEntries != 1 {
		t.Errorf("session holds %d custom_message entries, want 1", customEntries)
	}
}

// mustAppend persists one agent message through the same entry point the
// agent's OnMessagePersist hook uses, so the test drives production code.
func mustAppend(t *testing.T, s *Session, msg agent.AgentMessage) string {
	t.Helper()
	id, err := s.AppendMessage(msg)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	return id
}

// Negative control: the ordering the old code produced must be detected as
// invalid, otherwise the guard above proves nothing. This is the exact shape
// found in the live session file: the custom message written at creation time,
// landing between the tool_use and its result.
func TestSessionReplay_InterleavedCustomMessageIsDetected(t *testing.T) {
	sm := tempSessionMgr(t)
	sess, err := sm.Create("sess-custom-interleaved", "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mustAppend(t, sess, agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:       agent.RoleAssistant,
		StopReason: "toolUse",
		Content:    []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "bash"}},
	}})
	// The pre-fix eager append: recorded before the result arrives.
	mustAppend(t, sess, agent.AgentMessage{Custom: map[string]any{
		"role": agent.RoleCustom, "customType": "chain-completion",
		"content": "chain finished", "display": true,
	}})
	mustAppend(t, sess, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: "call-1",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}},
	}})

	if id := firstUnpairedToolUse(sess.BuildContext(nil)); id != "call-1" {
		t.Fatalf("interleaved custom message not detected as splitting the pair (got %q); "+
			"the ordering guard would pass on a session the provider rejects", id)
	}
}
