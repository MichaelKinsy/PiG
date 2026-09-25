package pico3

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestFrameMetadataUsesSharedAIValues(t *testing.T) {
	turn := JsonObject{}
	frames := []ai.AssistantMessageFrame{
		ai.StartFrame{Partial: ai.AssistantMessage{Content: []ai.AssistantContentBlock{}}},
		ai.TextStartFrame{ContentIndex: 0, Content: ai.TextContent{Text: "partial", TextSignature: "old"}},
		ai.TextEndFrame{ContentIndex: 0, Content: "done"},
		ai.ThinkingStartFrame{ContentIndex: 1, Content: ai.ThinkingContent{Thinking: "partial", Redacted: true}},
		ai.ThinkingEndFrame{ContentIndex: 1, Content: "thought", ThinkingSignature: "signed"},
		ai.ToolCallStartFrame{ContentIndex: 2, ToolCall: ai.ToolCall{ID: "call", Name: "tool", Arguments: ai.JsonObject{}}},
		ai.ToolCallEndFrame{ContentIndex: 2, ID: "call", Name: "tool", Arguments: ai.JsonObject{"value": 1}, ThoughtSignature: "signature", Namespace: "tools"},
	}
	for _, frame := range frames {
		check(t, applyFrame(turn, frame))
	}
	blocks := arr(obj(turn, "message"), "content")
	equal(t, blocks[0], JsonObject{"type": "text", "text": "done"}, "text clears signature")
	equal(t, blocks[1], JsonObject{"type": "thinking", "thinking": "thought", "thinkingSignature": "signed"}, "thinking clears redaction")
	equal(t, blocks[2], JsonObject{"type": "toolCall", "id": "call", "name": "tool", "arguments": JsonObject{"value": 1}, "thoughtSignature": "signature", "namespace": "tools"}, "tool metadata")
}
