package agent

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// toolHistory builds turns of one user prompt, an assistant message with three tool calls, and their three results.
func toolHistory(turns int) []AgentMessage {
	messages := make([]AgentMessage, 0, turns*5)
	for turn := range turns {
		messages = append(messages, AgentMessage{User: &UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("prompt %d", turn))}})
		calls := make([]ai.AssistantContentBlock, 0, 3)
		for call := range 3 {
			calls = append(calls, ai.ToolCall{ID: fmt.Sprintf("call_%d_%d", turn, call), Name: "read", Arguments: ai.JsonObject{"path": "a"}})
		}
		messages = append(messages, AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Content: calls, StopReason: ai.StopReasonToolUse}})
		for call := range 3 {
			messages = append(messages, AgentMessage{ToolResult: &ToolResultMessage{
				Role: RoleToolResult, ToolCallID: fmt.Sprintf("call_%d_%d", turn, call), ToolName: "read",
				Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result"}},
			}})
		}
	}
	return messages
}

// BenchmarkNormalizeAndConvertScaling shows the cost of the pre-send message pipeline (NormalizeMessages, ConvertToLLM, ai.TransformMessages) as the history grows. The time per turn must stay flat: a rise means a quadratic pass.
func BenchmarkNormalizeAndConvertScaling(b *testing.B) {
	model := &ai.Model{ID: "m", Input: []string{"text", "image"}}
	for _, turns := range []int{50, 500, 5000} {
		messages := toolHistory(turns)
		b.Run(fmt.Sprintf("turns=%d", turns), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				llm := ConvertToLLM(NormalizeMessages(messages, model))
				_ = ai.TransformMessages(llm, model, nil)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(turns), "ns/turn")
		})
	}
}
