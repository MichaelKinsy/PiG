package agent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A bashExecution message with excludeFromContext=false becomes a "Ran `cmd`"
// user message in LLM context; with excludeFromContext=true it is dropped.
// Mirrors upstream bashExecutionToText (messages.ts:90-108) and the !!cmd
// semantics surfaced via RPC bash excludeFromContext.
func TestConvertToLLM_BashExecutionExcludeFromContext(t *testing.T) {
	bash := func(exclude bool) AgentMessage {
		return AgentMessage{Custom: map[string]any{
			"role":               RoleBashExecution,
			"command":            "ls -la",
			"output":             "total 0",
			"exitCode":           float64(0),
			"cancelled":          false,
			"truncated":          false,
			"excludeFromContext": exclude,
		}}
	}

	included := convertToLLM([]AgentMessage{bash(false)}, nil)
	if len(included) != 1 {
		t.Fatalf("expected 1 LLM message for included bash, got %d", len(included))
	}
	if _, ok := included[0].(ai.UserMessage); !ok {
		t.Fatalf("bash context message = %T, want ai.UserMessage", included[0])
	}
	text := messageText(t, included[0])
	if !strings.Contains(text, "Ran `ls -la`") || !strings.Contains(text, "total 0") {
		t.Fatalf("bash context text missing command/output: %q", text)
	}

	excluded := convertToLLM([]AgentMessage{bash(true)}, nil)
	if len(excluded) != 0 {
		t.Fatalf("excludeFromContext bash must be dropped from LLM context, got %d messages", len(excluded))
	}
}

func TestConvertToLLM_SummaryRolesAddProviderContextWrappers(t *testing.T) {
	messages := []AgentMessage{
		{Custom: map[string]any{"role": RoleCompactionSummary, "summary": "compact body"}},
		{Custom: map[string]any{"role": RoleBranchSummary, "summary": "branch body"}},
	}
	converted := convertToLLM(messages, nil)
	if len(converted) != 2 {
		t.Fatalf("converted message count = %d, want 2", len(converted))
	}
	compaction := messageText(t, converted[0])
	if !strings.Contains(compaction, "The conversation history before this point was compacted") || !strings.Contains(compaction, "<summary>\ncompact body\n</summary>") {
		t.Fatalf("compaction context = %q", compaction)
	}
	branch := messageText(t, converted[1])
	if !strings.Contains(branch, "The following is a summary of a branch") || !strings.Contains(branch, "<summary>\nbranch body</summary>") {
		t.Fatalf("branch context = %q", branch)
	}
}

func messageText(t *testing.T, message ai.Message) string {
	t.Helper()
	user, ok := message.(ai.UserMessage)
	if !ok {
		t.Fatalf("message = %T, want ai.UserMessage", message)
	}
	switch content := user.Content.(type) {
	case ai.UserText:
		return string(content)
	case ai.UserContentBlocks:
		var text strings.Builder
		for _, block := range content {
			if block, ok := block.(ai.TextContent); ok {
				text.WriteString(block.Text)
			}
		}
		return text.String()
	default:
		t.Fatalf("user content = %T", user.Content)
		return ""
	}
}

// TestConvertToLLM_ToolResultCarriesToolName pins that the neutral->provider
// conversion preserves ToolName on the tool_result content block. This is the
// live request path; Gemini's functionResponse.name reads it. Dropping it (as
// pig did) makes every Google tool-result request diverge from pi 0.84.0.
func TestConvertToLLM_ToolResultCarriesToolName(t *testing.T) {
	msgs := []AgentMessage{
		{Assistant: &AssistantMessage{
			Role: "assistant",
			Content: []ai.AssistantContentBlock{
				ai.ToolCall{ID: "call-1", Name: "search", Arguments: ai.JsonObject{"q": "X"}},
			},
			StopReason: "toolUse",
		}},
		{ToolResult: &ToolResultMessage{
			Role:       RoleToolResult,
			ToolCallID: "call-1",
			ToolName:   "search",
			Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: "found it"}},
		}},
	}
	out := convertToLLM(msgs, nil)
	var result ai.ToolResultMessage
	found := false
	for _, message := range out {
		candidate, ok := message.(ai.ToolResultMessage)
		if !ok {
			continue
		}
		result = candidate
		found = true
		break
	}
	if !found {
		t.Fatalf("no tool-result message in %d converted messages", len(out))
	}
	if result.ToolName != "search" {
		t.Errorf("ToolName = %q, want %q", result.ToolName, "search")
	}
}
