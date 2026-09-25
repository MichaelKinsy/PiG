package agent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func benchmarkAgentMessage() AgentMessage {
	return AgentMessage{Assistant: &AssistantMessage{
		Role: RoleAssistant, API: ai.API("openai-responses"), Provider: "openai", ModelID: "gpt-5",
		Content: []ai.AssistantContentBlock{
			ai.ThinkingContent{Thinking: "considering the next operation", ThinkingSignature: "sig"},
			ai.TextContent{Text: "I will inspect the requested files."},
			ai.ToolCall{ID: "call-1", Name: "read", Arguments: ai.JsonObject{"path": "README.md"}},
		},
		Usage:      &ai.Usage{Input: 1000, Output: 50, CacheRead: 900, TotalTokens: 1950},
		StopReason: "toolUse", Timestamp: 1_700_000_000_000,
	}}
}

func BenchmarkAgentMessageMarshalJSON(b *testing.B) {
	message := benchmarkAgentMessage()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAgentMessageUnmarshalJSON(b *testing.B) {
	encoded, err := json.Marshal(benchmarkAgentMessage())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		var message AgentMessage
		if err := json.Unmarshal(encoded, &message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAgentMessageClone(b *testing.B) {
	message := benchmarkAgentMessage()
	b.ReportAllocs()
	for b.Loop() {
		_ = message.Clone()
	}
}

// benchmarkLegacyWireMessage models the duplicate coding-agent-only message
// DTO removed by the canonical AgentMessage JSON boundary. It remains private
// benchmark evidence for the serialization cost of the former path.
type benchmarkLegacyWireMessage struct {
	Role       string                `json:"role"`
	Content    ai.ContentBlocks      `json:"content"`
	Provider   string                `json:"provider,omitempty"`
	Model      string                `json:"model,omitempty"`
	Timestamp  int64                 `json:"timestamp,omitempty"`
	StopReason string                `json:"stopReason,omitempty"`
	Usage      *benchmarkLegacyUsage `json:"usage,omitempty"`
}

type benchmarkLegacyUsage struct {
	Input, Output, CacheRead, CacheWrite, TotalTokens int
}

func benchmarkLegacyMessage() benchmarkLegacyWireMessage {
	message := benchmarkAgentMessage().Assistant
	content := make(ai.ContentBlocks, len(message.Content))
	for i, block := range message.Content {
		content[i] = block
	}
	return benchmarkLegacyWireMessage{
		Role: message.Role, Content: content, Provider: message.Provider,
		Model: message.ModelID, Timestamp: message.Timestamp, StopReason: string(message.StopReason),
		Usage: &benchmarkLegacyUsage{
			Input: message.Usage.Input, Output: message.Usage.Output,
			CacheRead: message.Usage.CacheRead, CacheWrite: message.Usage.CacheWrite,
			TotalTokens: message.Usage.TotalTokens,
		},
	}
}

func BenchmarkLegacyWireMessageMarshalJSON(b *testing.B) {
	message := benchmarkLegacyMessage()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLegacyWireMessageUnmarshalJSON(b *testing.B) {
	encoded, err := json.Marshal(benchmarkLegacyMessage())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		var message benchmarkLegacyWireMessage
		if err := json.Unmarshal(encoded, &message); err != nil {
			b.Fatal(err)
		}
	}
}
