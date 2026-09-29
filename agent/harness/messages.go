package harness

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ConvertToLlm converts harness messages without normalizing failed assistants or synthesizing tool results. Provider normalization belongs to the request boundary.
// Ports packages/agent/src/harness/messages.ts (convertToLlm).
func ConvertToLlm(messages []agent.AgentMessage) []ai.Message {
	result := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.System != nil:
			result = append(result, *message.System)
		case message.User != nil:
			result = append(result, message.User.LLMMessage())
		case message.Assistant != nil:
			result = append(result, message.Assistant.LLMMessage())
		case message.ToolResult != nil:
			tool := message.ToolResult
			result = append(result, ai.ToolResultMessage{ToolCallID: tool.ToolCallID, ToolName: tool.ToolName, Content: tool.Content, Details: tool.Details, Usage: tool.Usage, IsError: tool.IsError, Timestamp: tool.Timestamp})
		default:
			result = append(result, agent.ConvertToLLM([]agent.AgentMessage{message}, nil)...)
		}
	}
	return result
}
