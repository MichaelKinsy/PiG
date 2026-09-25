package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// assistantToolUseSignature returns the ThoughtSignature on the first
// ToolCall block of the first assistant message in msgs.
func assistantToolUseSignature(t *testing.T, msgs []AgentMessage) string {
	t.Helper()
	for _, m := range msgs {
		if m.Assistant == nil {
			continue
		}
		for _, blk := range m.Assistant.Content {
			if tu, ok := blk.(ai.ToolCall); ok {
				return tu.ThoughtSignature
			}
		}
	}
	t.Fatal("no assistant tool_use block in normalized output")
	return ""
}

func toolUseConversation(sig string) []AgentMessage {
	return []AgentMessage{
		{Assistant: &AssistantMessage{
			Role:     "assistant",
			Provider: "static-fake",
			ModelID:  "fake",
			Content: []ai.AssistantContentBlock{
				ai.ToolCall{ID: "t1", Name: "bash", Arguments: ai.JsonObject{"command": "ls"}, ThoughtSignature: sig},
			},
		}},
		{ToolResult: &ToolResultMessage{
			Role: RoleToolResult, ToolCallID: "t1",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}},
		}},
	}
}

// TestNormalizeMessages_StripsCrossModelToolCallSignature pins upstream
// transform-messages.ts:131: a tool-call thoughtSignature from a different
// provider/model is unusable by the target provider and must be dropped before
// the provider converter sees it. Otherwise a signature minted by one provider
// (e.g. an OpenAI reasoning.encrypted blob) leaks into another provider's
// request (e.g. Google), which rejects it.
func TestNormalizeMessages_StripsCrossModelToolCallSignature(t *testing.T) {
	const sig = "c2lnbmF0dXJl" // base64("signature")
	// Target model differs from the message's provider/model → cross-model.
	foreign := fakeTestModel(&staticProvider{})
	foreign.ID = "other-model"
	got := NormalizeMessages(toolUseConversation(sig), foreign)
	if s := assistantToolUseSignature(t, got); s != "" {
		t.Fatalf("cross-model tool-call signature not stripped: %q", s)
	}
}

// TestNormalizeMessages_KeepsSameModelToolCallSignature pins the other side of
// the gate: a same-provider/model tool-call signature is retained for replay.
func TestNormalizeMessages_KeepsSameModelToolCallSignature(t *testing.T) {
	const sig = "c2lnbmF0dXJl"
	// Target model matches the message (Provider.ID()=="static-fake", ID=="fake").
	same := fakeTestModel(&staticProvider{})
	same.ID = "fake"
	got := NormalizeMessages(toolUseConversation(sig), same)
	if s := assistantToolUseSignature(t, got); s != sig {
		t.Fatalf("same-model tool-call signature not retained: got %q want %q", s, sig)
	}
}
