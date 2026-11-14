package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// BenchmarkCallToolResultToLLMContent decodes one tool result a server sent and converts it for the model: a text block with no lone surrogate.
func BenchmarkCallToolResultToLLMContent(b *testing.B) {
	raw := []byte(`{"content":[{"type":"text","text":"` + strings.Repeat(`line of output\n`, 60) + `"}]}`)
	b.ReportAllocs()
	for b.Loop() {
		var result CallToolResult
		if err := json.Unmarshal(raw, &result); err != nil {
			b.Fatal(err)
		}
		if content := ToLLMContent(result); len(content) != 1 {
			b.Fatal("the result has no content")
		}
	}
}
