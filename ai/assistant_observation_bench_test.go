package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func BenchmarkAssistantMessageObservation(b *testing.B) {
	for _, shape := range []string{"empty", "text", "mixed"} {
		b.Run(shape, func(b *testing.B) {
			message := &AssistantMessage{Content: []AssistantContentBlock{}, StopReason: StopReasonPending}
			switch shape {
			case "text":
				message.Content = append(message.Content, TextContent{Text: strings.Repeat("text", 1024)})
			case "mixed":
				for range 8 {
					message.Content = append(message.Content,
						TextContent{Text: strings.Repeat("text", 256)},
						ThinkingContent{Thinking: strings.Repeat("reason", 256)},
						ToolCall{Name: "read", Arguments: JsonObject{"path": "file", "options": map[string]any{"recursive": true}}},
					)
				}
			}
			cell := newAssistantMessageCell(message)
			full := cell.view()
			shallow := full.ShallowCopy()
			b.Run("publish", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					cell.publish(message, assistantMessageReplacements{})
				}
			})
			b.Run("observe-full", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_ = full.Observe()
				}
			})
			b.Run("observe-shallow", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_ = shallow.Observe()
				}
			})
			b.Run("marshal-shallow", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := json.Marshal(shallow); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
