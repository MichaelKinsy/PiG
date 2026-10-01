package runtime

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// runtimeAssistantMessage adapts the provider carrier to the agent carrier without filtering content or changing response metadata.
func runtimeAssistantMessage(message *ai.AssistantMessage) agent.AssistantMessage {
	return agent.AssistantMessage{
		Role: "assistant", Content: message.Content, Timestamp: message.Timestamp, Usage: &message.Usage,
		API: message.API, Provider: message.Provider, ModelID: message.Model, ResponseModel: message.ResponseModel, ResponseID: message.ResponseID,
		ProviderThinkingLevel: message.ProviderThinkingLevel, ThinkingLevel: message.ThinkingLevel, Diagnostics: message.Diagnostics, Deferred: message.Deferred, StopReason: message.StopReason,
		ErrorMessage: message.ErrorMessage, RawStopReason: message.RawStopReason, EndTurn: message.EndTurn,
	}
}

// runtimeToolMessage adapts a provider tool result without normalizing its content, details, usage, or error state.
func runtimeToolMessage(message ai.ToolResultMessage) agent.ToolResultMessage {
	return agent.ToolResultMessage{Role: "toolResult", ToolCallID: message.ToolCallID, ToolName: message.ToolName, Content: message.Content, Details: message.Details, Usage: message.Usage, IsError: message.IsError, Timestamp: message.Timestamp}
}
