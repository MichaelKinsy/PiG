package ai

import (
	"runtime"
	"strings"
	"testing"
)

// A session keeps the message long after its stream ended. The fast chunk decode hands out substrings of the record, and a record is a substring of
// the line buffer it arrived in, so a kept substring would hold that whole buffer. Each record here carries a megabyte the decode ignores; what eight
// streams leave behind in the kept value must hold no such record.
func TestOpenAICompletionsMessageKeepsNoStreamRecord(t *testing.T) {
	const padding, streams = 1 << 20, 8
	chunks := []string{
		`"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]`,
		`"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{}"}}]}}]`,
		`"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]`,
	}
	var sse strings.Builder
	for _, members := range chunks {
		record := `{"id":"chatcmpl-1","system_fingerprint":"` + strings.Repeat("x", padding) + `",` + members + `}`
		if _, fast := parseOpenAIChunkFast(record); !fast {
			t.Fatalf("the fast decode does not read %s; the test needs records whose strings are substrings", members)
		}
		sse.WriteString("data: " + record + "\n\n")
	}
	provider := &openAIProvider{cfg: OpenAIConfig{Model: "test-model"}}
	result := func() *AssistantMessage {
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "openai", "test-model")
		provider.parseSSE(t.Context(), strings.NewReader(sse.String()), builder, nil)
		message := builder.stream.Result()
		if len(message.Content) != 2 || message.ResponseID != "chatcmpl-1" || message.RawStopReason != "tool_calls" {
			t.Fatalf("message = %#v", message)
		}
		if call, ok := message.Content[1].(ToolCall); !ok || call.ID != "call_1" || call.Name != "read" {
			t.Fatalf("content = %#v", message.Content)
		}
		return message
	}
	liveHeap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	for _, part := range []struct {
		name string
		keep func(*AssistantMessage) any
	}{
		{"response id", func(message *AssistantMessage) any { return message.ResponseID }},
		{"raw stop reason", func(message *AssistantMessage) any { return message.RawStopReason }},
		{"tool call id", func(message *AssistantMessage) any { return message.Content[1].(ToolCall).ID }},
		{"tool call name", func(message *AssistantMessage) any { return message.Content[1].(ToolCall).Name }},
		{"message", func(message *AssistantMessage) any { return message }},
	} {
		t.Run(part.name, func(t *testing.T) {
			result()
			kept := make([]any, 0, streams)
			before := liveHeap()
			for range streams {
				kept = append(kept, part.keep(result()))
			}
			after := liveHeap()
			// One held record is over a megabyte; everything a message owns is a few hundred bytes.
			if after > before+padding {
				t.Fatalf("the %s of %d streams keeps %d bytes of the heap alive, want under %d: it holds its record's line buffer", part.name, streams, after-before, padding)
			}
			runtime.KeepAlive(kept)
		})
	}
}
