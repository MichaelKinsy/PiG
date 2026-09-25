package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// maxTokensRequested sends one prompt from messages and returns the
// max_tokens of the provider request.
func maxTokensRequested(t *testing.T, contextWindow, maxOutput int, messages []AgentMessage) int {
	t.Helper()
	provider := &scriptedProvider{respond: replyText("ok")}
	model := &ai.Model{ID: "mock", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: contextWindow, MaxOutputTokens: maxOutput}}
	a := NewAgent(AgentOptions{Model: model})
	a.SetMessages(messages)
	if _, err := a.Send(context.Background(), "new prompt"); err != nil {
		t.Fatal(err)
	}
	return provider.request(1).opts.MaxTokens
}

// Upstream estimateContextTokens ignores assistant usage older than a later
// prefix message: after compaction, the kept pre-compaction response's usage
// describes the old context, not the summary that now precedes it.
func TestClampIgnoresUsageOlderThanPrefix(t *testing.T) {
	now := time.Now().UnixMilli()
	kept := &AssistantMessage{
		Role: RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "kept"}},
		Usage: &ai.Usage{Input: 190_000, TotalTokens: 190_000}, StopReason: ai.StopReasonStop, Timestamp: now - 2000,
	}
	messages := []AgentMessage{
		{Custom: map[string]any{"role": RoleCompactionSummary, "summary": "summary", "timestamp": now - 1000}},
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "kept question"}}, Timestamp: now - 3000}},
		{Assistant: kept},
	}
	if got := maxTokensRequested(t, 200_000, 64_000, messages); got != 64_000 {
		t.Fatalf("max_tokens = %d, want the model's 64000", got)
	}
}

// Upstream estimateMessageTokens counts system text and tool declarations.
func TestClampCountsSystemAndTools(t *testing.T) {
	system := &ai.SystemMessage{Content: ai.SystemText(strings.Repeat("s", 40_000))}
	// 40,000 system chars are 10,000 tokens; the prompt adds 3.
	want := 16_000 - 10_000 - 3 - 4096
	if got := maxTokensRequested(t, 16_000, 64_000, []AgentMessage{{System: system}}); got != want {
		t.Fatalf("max_tokens = %d, want %d", got, want)
	}
}
