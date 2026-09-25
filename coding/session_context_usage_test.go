package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// agent-session.ts::getContextUsage requires projected post-compaction valid
// assistant usage; errored responses and retained old usage do not restore it.
func TestSessionContextUsageCompactionUnknownUntilValidProjectedUsage(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	if usage := h.session.ContextUsage(); usage == nil || usage.Tokens == nil || *usage.Tokens != 0 {
		t.Fatalf("fresh usage %#v", usage)
	}
	old := &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "old"}}, StopReason: ai.StopReasonStop, Usage: &ai.Usage{Input: 10000, TotalTokens: 10001}}
	id, err := h.session.Inner().AppendMessage(agent.AgentMessage{Assistant: old})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.session.Inner().AppendCompaction("summary", id, 10001, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []ai.StopReason{ai.StopReasonError, ai.StopReasonAborted} {
		message := *old
		message.StopReason = reason
		if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{Assistant: &message}); err != nil {
			t.Fatal(err)
		}
		if usage := h.session.ContextUsage(); usage == nil || usage.Tokens != nil || usage.Percent != nil {
			t.Fatalf("%s restored usage %#v", reason, usage)
		}
	}
	good := *old
	good.Usage = &ai.Usage{Input: 70, Output: 7, TotalTokens: 77}
	if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{Assistant: &good}); err != nil {
		t.Fatal(err)
	}
	if usage := h.session.ContextUsage(); usage == nil || usage.Tokens == nil || *usage.Tokens != 77 {
		t.Fatalf("new usage %#v", usage)
	}
	if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "abcd"}}}}); err != nil {
		t.Fatal(err)
	}
	if usage := h.session.ContextUsage(); usage == nil || usage.Tokens == nil || *usage.Tokens != 78 {
		t.Fatalf("trailing context usage %#v", usage)
	}
}
