package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// BenchmarkToolResultMessageStringify appends the JSON.stringify text of a tool result message, as the session writer does for every tool result: one
// without nested calls and one with the record of two calls its tool made.
func BenchmarkToolResultMessageStringify(b *testing.B) {
	duration := int64(12)
	plain := ToolResultMessage{Role: RoleToolResult, ToolCallID: "call_1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n\nfunc main() {}\n"}}, Timestamp: 1700000000000}
	nested := plain
	nested.NestedCalls = &ai.NestedToolCalls{Complete: true, Calls: []ai.NestedToolCallRecord{
		{ID: "call_1/1", Name: "helper", Status: ai.NestedToolCallOK, DurationMs: &duration},
		{ID: "call_1/2", Name: "missing", Status: ai.NestedToolCallError, Error: "Tool missing not found"},
	}}
	for _, tc := range []struct {
		name    string
		message ToolResultMessage
	}{{"plain", plain}, {"nestedCalls", nested}} {
		b.Run(tc.name, func(b *testing.B) {
			var dst []byte
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if dst, err = (AgentMessage{ToolResult: &tc.message}).AppendStringify(dst[:0]); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
