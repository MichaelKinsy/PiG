package ai_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The transcript hot path: a tool result and an assistant message encoded as JSON.stringify (a session entry, an RPC event)
// and decoded again.

func benchToolResult() ai.ToolResultMessage {
	text := "output line with <angle> & ampersand, and some more text to give the string a realistic length\n"
	return ai.ToolResultMessage{ToolCallID: "call_0123456789", ToolName: "bash", Timestamp: 1700000000000,
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text + text + text}},
		Details: map[string]any{"exitCode": float64(0), "truncated": false, "fullOutputPath": "/tmp/out.txt", "calls": []any{map[string]any{"id": float64(1), "name": "read", "status": "ok"}}}}
}

func benchAssistant() ai.AssistantMessage {
	return ai.AssistantMessage{API: "faux", Provider: "faux", Model: "m", StopReason: "toolUse", Timestamp: 1700000000000,
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: "I will read the file."},
			ai.ToolCall{ID: "call_1", Name: "read", Arguments: ai.JsonObject{"path": "/tmp/x.go", "offset": float64(1), "limit": float64(200)}}}}
}

func BenchmarkToolResultMessageEncode(b *testing.B) {
	message := benchToolResult()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sdkjson.MarshalStringify(message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkToolResultMessageDecode(b *testing.B) {
	message := benchToolResult()
	data, err := sdkjson.MarshalStringify(message)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		var decoded ai.ToolResultMessage
		if err := sdkjson.Unmarshal(data, &decoded); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAssistantMessageEncode(b *testing.B) {
	message := benchAssistant()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := sdkjson.MarshalStringify(message); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAssistantMessageDecode(b *testing.B) {
	message := benchAssistant()
	data, err := sdkjson.MarshalStringify(message)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		var decoded ai.AssistantMessage
		if err := sdkjson.Unmarshal(data, &decoded); err != nil {
			b.Fatal(err)
		}
	}
}
