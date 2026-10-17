package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// anthHistory is a conversation of turns tool calls and replies, of the small messages a coding session sends.
func anthHistory(turns int) anthRequest {
	request := anthRequest{Model: "m", MaxTokens: 100, Stream: true, System: []anthSystemBlock{{Type: "text", Text: "system"}}}
	for i := range turns {
		id := fmt.Sprintf("toolu_%016d", i)
		request.Messages = append(request.Messages,
			anthMessage{Role: "user", Content: []anthContentBlock{{Type: "text", Text: fmt.Sprintf("hello %d", i)}}},
			anthMessage{Role: "assistant", Content: []anthContentBlock{{Type: "text", Text: "ok"}, {Type: "tool_use", ID: id, Name: "read", Input: json.RawMessage(fmt.Sprintf(`{"path":"/tmp/x","n":%d}`, i))}}},
			anthMessage{Role: "user", Content: []anthContentBlock{{Type: "tool_result", ToolUseID: id, Content: strings.Repeat("result line\n", 10)}}})
	}
	return request
}

func oaiHistory(turns int) oaiRequest {
	request := oaiRequest{Model: "m", Stream: true, Messages: []oaiMessage{{Role: "system", Content: "system"}}}
	for i := range turns {
		id := fmt.Sprintf("call_%016d", i)
		request.Messages = append(request.Messages,
			oaiMessage{Role: "user", Content: fmt.Sprintf("hello %d", i)},
			oaiMessage{Role: "assistant", Content: "ok", ToolCalls: []oaiRequestToolCall{{ID: id, Type: "function", Function: &oaiRequestToolCallFunction{Name: "read", Arguments: fmt.Sprintf(`{"path":"/tmp/x","n":%d}`, i)}}}},
			oaiMessage{Role: "tool", ToolCallID: id, Content: strings.Repeat("result line\n", 10)})
	}
	return request
}

func benchmarkRequest(b *testing.B, request any, cached bool) {
	cache := newQuotedStringCache(1 << 30)
	if !cached {
		cache.messages = nil
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := marshalSpliced(request, cache, marshalRequestBody); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnthropicRequest200Turns(b *testing.B) {
	b.Run("messages-cached", func(b *testing.B) { benchmarkRequest(b, anthHistory(200), true) })
	b.Run("uncached", func(b *testing.B) { benchmarkRequest(b, anthHistory(200), false) })
}

func BenchmarkCompletionsRequest200Turns(b *testing.B) {
	b.Run("messages-cached", func(b *testing.B) { benchmarkRequest(b, oaiHistory(200), true) })
	b.Run("uncached", func(b *testing.B) { benchmarkRequest(b, oaiHistory(200), false) })
}
