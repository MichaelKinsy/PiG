package compaction

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi main 15e7bdc1 "omit thinking from compaction summaries" (compaction-serialization.test.ts "should omit thinking"): replayed thinking is often not part of the live context, so serializing it can make the summary request larger than the context it summarizes. Visible text, tool calls and tool results are serialized; a message with only thinking adds nothing.
func TestSerializeConversationOmitsThinking(t *testing.T) {
	messages := []ai.Message{ai.AssistantMessage{Content: []ai.AssistantContentBlock{
		ai.ThinkingContent{Thinking: "private reasoning", ThinkingSignature: "sig"},
		ai.TextContent{Text: "visible reply"},
		ai.ToolCall{ID: "tc1", Name: "read", Arguments: ai.JsonObject{"path": "a.ts"}},
	}}}
	if got, want := SerializeConversation(messages), "[Assistant]: visible reply\n\n[Assistant tool calls]: read(path=\"a.ts\")"; got != want {
		t.Errorf("SerializeConversation = %q, want %q", got, want)
	}
	if got := SerializeConversation([]ai.Message{ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "only thinking"}}}}); got != "" {
		t.Errorf("a message with only thinking serialized as %q", got)
	}
}
