package cli

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
)

func benchAssistant() *agent.AssistantMessage {
	return &agent.AssistantMessage{Role: "assistant", API: "openai-completions", Provider: "openai", ModelID: "gpt-bench", StopReason: ai.StopReasonToolUse, Timestamp: 1700000000000,
		Usage: &ai.Usage{Input: 1200, Output: 80, TotalTokens: 1280},
		Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: strings.Repeat("A paragraph of assistant prose with some words. ", 12)},
			ai.ToolCall{ID: "call_1", Name: "write", Arguments: ai.JsonObject{"path": "main.go", "content": strings.Repeat("package main\n\nfunc main() {}\n", 20)}},
		}}
}

// benchRPCEvent is one event through the conversion and the JSON line writer, as subscribeRPCEvents and writeJSONLine do per event.
func benchRPCEvent(b *testing.B, event agent.AgentEvent) {
	b.ReportAllocs()
	var bytes int
	for b.Loop() {
		frames, err := rpcAgentEvent(event)
		if err != nil {
			b.Fatal(err)
		}
		for _, frame := range frames {
			line, err := rpcclient.SerializeJsonLine(frame)
			if err != nil {
				b.Fatal(err)
			}
			bytes += len(line)
		}
	}
	b.SetBytes(int64(bytes / max(b.N, 1)))
}

func BenchmarkRPCEventMessageEnd(b *testing.B) {
	benchRPCEvent(b, agent.MessageEndEvent{Message: agent.AgentMessage{Assistant: benchAssistant()}})
}

func BenchmarkRPCEventTurnEnd(b *testing.B) {
	result := agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call_1", ToolName: "write", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Wrote 600 bytes to main.go"}}, Timestamp: 1700000000001}
	benchRPCEvent(b, agent.TurnEndEvent{TurnIndex: 3, Message: agent.AgentMessage{Assistant: benchAssistant()}, ToolResults: []agent.ToolResultMessage{result}})
}

func BenchmarkRPCEventToolExecutionEnd(b *testing.B) {
	benchRPCEvent(b, agent.ToolExecutionEndEvent{ToolCallID: "call_1", ToolName: "write", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Wrote 600 bytes to main.go"}}, Details: map[string]any{"bytes": 600, "path": "main.go"}}})
}
