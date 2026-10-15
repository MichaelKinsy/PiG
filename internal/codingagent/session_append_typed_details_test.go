//go:build !race

package codingagent

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// largeReadToolResult is a truncated read of payload bytes as the read tool returns it: the text once in the content and once, shared, in its typed
// ReadDetails truncation.
func largeReadToolResult(payload, i int) agent.AgentMessage {
	text := strings.Repeat("package main // line\n", payload/21+1)[:payload]
	details := &tools.ReadDetails{Path: "big.go", Truncation: &tools.TruncationResult{Content: text, Truncated: true, TruncatedBy: "bytes",
		TotalLines: 100000, TotalBytes: 4 * payload, OutputLines: payload / 21, OutputBytes: payload, MaxLines: 2000, MaxBytes: payload}}
	return agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: fmt.Sprintf("call_%d", i), ToolName: "read",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: details, Timestamp: int64(i)}}
}

// ceilingTypedDetailsAppendBytesPerPayloadByte bounds the heap one append of a truncated read allocates per payload byte. On integrate-042
// f22240330e, which decodes the record just encoded (both copies of the text, and the details canonicalized), it is 13.45x; without that decode
// 7.14x, because the record's buffer, sized for the content's text alone, is regrown for the details' copy; an append whose buffer is sized for
// the details but not for the text's escapes regrows it too, 4.52x. Encoding it once into a buffer sized for both and their escapes is 2.26x.
const ceilingTypedDetailsAppendBytesPerPayloadByte = 3.0

// Appending a tool result whose only obstacle to a proven decoded form is its typed details (the read tool's ReadDetails) must not decode the record
// it just encoded, nor regrow the buffer it encodes it into: Pi's _persist does no JSON parse here, and the session keeps the appended object
// either way.
func TestAppendToolResultWithTypedDetailsDoesNotDecodeItsRecord(t *testing.T) {
	const payload = 1 << 20
	session := NewSession("append-typed-details", "/project")
	messages := make([]agent.AgentMessage, 8)
	for i := range messages {
		messages[i] = largeReadToolResult(payload, i)
	}
	if _, err := session.AppendMessage(largeReadToolResult(payload, -1)); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for _, message := range messages {
		if _, err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	perAppend := float64(after.TotalAlloc-before.TotalAlloc) / float64(len(messages))
	ratio := perAppend / payload
	t.Logf("bytes allocated per append: %.0f (%.2fx payload)", perAppend, ratio)
	if ratio > ceilingTypedDetailsAppendBytesPerPayloadByte {
		t.Fatalf("appending a %d-byte read result allocates %.0f bytes (%.2fx), want at most %.1fx: the record is decoded again", payload, perAppend, ratio,
			ceilingTypedDetailsAppendBytesPerPayloadByte)
	}
}

func BenchmarkAppendTypedReadToolResult(b *testing.B) {
	for _, payload := range []int{1 << 10, 64 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dKiB", payload>>10), func(b *testing.B) {
			session := NewSession("bench", "/project")
			message := largeReadToolResult(payload, 1)
			b.SetBytes(int64(payload))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := session.AppendMessage(message); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
