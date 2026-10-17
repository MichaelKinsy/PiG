//go:build !race

package codingagent

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// largeAssistantMessage is an assistant turn whose text and tool-call argument each carry half of payload bytes, with a decoded form that is proven.
func largeAssistantMessage(payload, i int) agent.AgentMessage {
	half := payload / 2
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "faux", ModelID: "faux-1",
		Usage: &ai.Usage{Input: 1200, Output: 80, TotalTokens: 1280}, StopReason: ai.StopReasonToolUse, Timestamp: int64(i),
		Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: strings.Repeat("a", half)},
			ai.ToolCall{ID: "call_1", Name: "write", Arguments: map[string]any{"path": "big.txt", "content": strings.Repeat("b", half)}},
		}}}
}

// ceilingAppendBytesPerPayloadByte bounds the heap one append allocates per payload byte. On 4cfaf24153 an append allocates 9.85x its payload (the nested
// encoders' copies of the record and the proven decoded form's tool-call argument reorder); decoding the record just written and deep-comparing it with the
// appended message raises that to 13.07x. 11x sits between the two, so a decode on the proven-decoded-form path fails.
const ceilingAppendBytesPerPayloadByte = 11.0

// Appending a message whose decoded form is proven must not decode the record it just encoded: the bytes it allocates stay a small multiple of the payload.
func TestAppendMessageEntryDoesNotDecodeItsRecord(t *testing.T) {
	const payload = 1 << 20
	session := NewSession("append-cost", "/project")
	messages := make([]agent.AgentMessage, 8)
	for i := range messages {
		messages[i] = largeAssistantMessage(payload, i)
		if _, ok := messages[i].DecodedForm(); !ok {
			t.Fatal("test message must have a proven decoded form")
		}
	}
	if _, err := session.AppendMessage(largeAssistantMessage(payload, -1)); err != nil {
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
	if ratio > ceilingAppendBytesPerPayloadByte {
		t.Fatalf("appending a %d-byte message allocates %.0f bytes (%.2fx), want at most %.1fx: the record is decoded again", payload, perAppend, ratio, ceilingAppendBytesPerPayloadByte)
	}
}

func BenchmarkAppendLargeMessageEntry(b *testing.B) {
	for _, payload := range []int{1 << 10, 64 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dKiB", payload>>10), func(b *testing.B) {
			session := NewSession("bench", "/project")
			message := largeAssistantMessage(payload, 1)
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
