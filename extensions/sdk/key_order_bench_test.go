package sdk

import (
	"fmt"
	"strings"
	"testing"
)

// contextArgs is a context event with count messages: a user message, an assistant message with a tool call and a text block, and the tool's result, in turn.
func contextArgs(count int) string {
	var b strings.Builder
	b.WriteString(`{"type":"context","messages":[`)
	for i := range count {
		if i > 0 {
			b.WriteByte(',')
		}
		switch i % 3 {
		case 0:
			fmt.Fprintf(&b, `{"role":"user","content":"question %d about the files in the repository and what they hold","timestamp":%d}`, i, i)
		case 1:
			fmt.Fprintf(&b, `{"role":"assistant","content":[{"type":"text","text":"I will read it"},{"type":"toolCall","id":"c%d","name":"read","arguments":{"path":"src/file%d.go","offset":%d,"limit":200}}],"api":"openai-completions","provider":"faux","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":%d}`, i, i, i, i)
		default:
			fmt.Fprintf(&b, `{"role":"toolResult","toolCallId":"c%d","toolName":"read","content":[{"type":"text","text":"package main\n\nfunc main() {}\n"}],"isError":false,"timestamp":%d}`, i-1, i)
		}
	}
	b.WriteString(`]}`)
	return b.String()
}

// BenchmarkContextEvent is a context event of count messages through decode, handler, result and reply: a handler that returns nothing, one that hands back
// the list without its first message, and one that sets the content of every message in place.
func BenchmarkContextEvent(b *testing.B) {
	handlers := []struct {
		name string
		run  func(data map[string]any) any
	}{
		{"nil-return", func(map[string]any) any { return nil }},
		{"handed-back", func(data map[string]any) any { return map[string]any{"messages": data["messages"].([]any)[1:]} }},
		{"redact-all", func(data map[string]any) any {
			for _, message := range data["messages"].([]any) {
				message.(map[string]any)["content"] = "[redacted]"
			}
			return nil
		}},
	}
	for _, count := range []int{300, 3000, 10000} {
		args := []byte(contextArgs(count))
		for _, handler := range handlers {
			b.Run(fmt.Sprintf("context-%d/%s", count, handler.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(args)))
				for b.Loop() {
					if _, err := runContextEvent(args, handler.run); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
