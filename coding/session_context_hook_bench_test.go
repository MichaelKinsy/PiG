package coding

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// BenchmarkSameContextMessagesPerRequest measures the check that follows a `context` handler that changed nothing. A subprocess handler returns decoded JSON, so the
// returned messages never DeepEqual the session's own and every message takes the marshal-both-sides path. Cost grows with the whole history, once per model request.
func BenchmarkSameContextMessagesPerRequest(b *testing.B) {
	for _, exchanges := range []int{50, 500, 5000} {
		b.Run(fmt.Sprintf("exchanges=%d", exchanges), func(b *testing.B) {
			visible := make([]extension.AgentMessage, 0, exchanges*3)
			for i := range exchanges {
				visible = append(visible,
					extension.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("question %d about the repository layout", i))}},
					extension.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{
						ai.ThinkingContent{Thinking: "let me look at the files first"},
						ai.TextContent{Text: fmt.Sprintf("I will read file %d now.", i)},
						ai.ToolCall{ID: fmt.Sprintf("call_%d", i), Name: "read", Arguments: ai.JsonObject{"path": fmt.Sprintf("src/file%d.go", i)}},
					}}},
					extension.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: fmt.Sprintf("call_%d", i), ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n\nfunc main() {}\n"}}}},
				)
			}
			encoded, err := json.Marshal(visible)
			if err != nil {
				b.Fatal(err)
			}
			var returned []extension.AgentMessage
			if err := json.Unmarshal(encoded, &returned); err != nil {
				b.Fatal(err)
			}
			if !sameContextMessages(returned, visible) {
				b.Fatal("a round-tripped conversation must compare equal")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				sameContextMessages(returned, visible)
			}
		})
	}
}
