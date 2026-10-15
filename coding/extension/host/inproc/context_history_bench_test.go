package inproc_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// contextHistory is a conversation of exchanges: a user line and an assistant answer with text, thinking and a tool call, then the tool result.
func contextHistory(exchanges int) []extension.AgentMessage {
	messages := make([]extension.AgentMessage, 0, exchanges*3)
	for i := range exchanges {
		messages = append(messages,
			extension.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("question %d about the repository layout and the failing test", i))}},
			extension.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{
				ai.ThinkingContent{Thinking: "let me look at the files first"},
				ai.TextContent{Text: fmt.Sprintf("I will read file %d now.", i)},
				ai.ToolCall{ID: fmt.Sprintf("call_%d", i), Name: "read", Arguments: ai.JsonObject{"path": fmt.Sprintf("src/file%d.go", i)}},
			}}},
			extension.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: fmt.Sprintf("call_%d", i), ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n\nfunc main() {}\n"}}}},
		)
	}
	return messages
}

// BenchmarkEmitContextPerRequest measures what every model request pays for the `context` event when one handler is registered and changes nothing: the runner's deep
// clone of the whole conversation, plus the handler's own copy of the list. Cost grows with the whole history, not with the context window.
func BenchmarkEmitContextPerRequest(b *testing.B) {
	for _, exchanges := range []int{50, 500, 5000} {
		b.Run(fmt.Sprintf("exchanges=%d", exchanges), func(b *testing.B) {
			ext := extension.Extension{Path: "noop.ts", Handlers: map[string][]extension.HandlerFn{"context": {func(...any) (any, error) { return nil, nil }}}}
			runner := inproc.NewRunner([]extension.Extension{ext}, b.TempDir())
			messages := contextHistory(exchanges)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, _, err := runner.EmitContextTracked(b.Context(), messages); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
