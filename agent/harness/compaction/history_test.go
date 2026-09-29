package compaction

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Harness convertToLlm preserves assistant history for summarization; provider normalization must not erase failed attempts or synthesize tool results inside the quoted conversation.
func TestHarnessSummaryKeepsUnnormalizedHistory(t *testing.T) {
	failed := assistant("failed attempt")
	failed.Assistant.StopReason = ai.StopReasonError
	call := assistant("")
	call.Assistant.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "orphan", Name: "read", Arguments: ai.JsonObject{"path": "file.ts"}}}
	models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("summary", ai.StopReasonStop, "")}}
	_, err := GenerateSummary(t.Context(), []agent.AgentMessage{user("task"), failed, call}, models, summaryModel(false, 8192), 2000, "", "", "", nil, ai.RetryCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	prompt := requestUserText(t, models)
	if !strings.Contains(prompt, "[Assistant]: failed attempt") || !strings.Contains(prompt, `[Assistant tool calls]: read(path="file.ts")`) || strings.Contains(prompt, "[Tool result]") {
		t.Fatal(prompt)
	}
}
