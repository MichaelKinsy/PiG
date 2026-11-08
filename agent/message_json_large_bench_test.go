package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// BenchmarkAgentMessageUnmarshalJSONLarge decodes the messages a long session holds most of: a tool result carrying a file of 20 KB and an assistant answer of 6 KB.
func BenchmarkAgentMessageUnmarshalJSONLarge(b *testing.B) {
	file := strings.Repeat("func example(a, b int) int { return a + b } // line of a source file\n", 290)
	answer := strings.Repeat("A paragraph of the answer, with `code` and **emphasis**, as the model writes it.\n\n", 75)
	messages := []AgentMessage{
		{ToolResult: &ToolResultMessage{
			Role: RoleToolResult, ToolCallID: "call-1", ToolName: "read", Timestamp: 1_700_000_000_000,
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: file}},
		}},
		{Assistant: &AssistantMessage{
			Role: RoleAssistant, API: ai.API("openai-responses"), Provider: "openai", ModelID: "gpt-5", Timestamp: 1_700_000_000_000,
			Content:    []ai.AssistantContentBlock{ai.TextContent{Text: answer}},
			Usage:      &ai.Usage{Input: 1000, Output: 50, TotalTokens: 1050},
			StopReason: "stop",
		}},
	}
	encoded := make([][]byte, len(messages))
	for i, message := range messages {
		var err error
		if encoded[i], err = json.Marshal(message); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, data := range encoded {
			var message AgentMessage
			if err := json.Unmarshal(data, &message); err != nil {
				b.Fatal(err)
			}
		}
	}
}
