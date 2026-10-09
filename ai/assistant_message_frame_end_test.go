package ai

import "testing"

// .upstream/current/packages/ai/src/utils/assistant-message-frame.ts:178-190 and 209-222: text_end and thinking_end frames carry
// the event's content (authoritative), while the signature and redaction come from the partial's block and are omitted when the
// block has none. Pi's test "uses authoritative text end content and signature" keeps the event content equal to the partial's
// text, so it cannot tell the two sources apart; these cases make them differ.
func TestAssistantMessageFrameEndFramesTakeContentFromTheEventAndMetadataFromThePartial(t *testing.T) {
	partial := frameSeed()
	encoder := NewAssistantMessageFrameEncoder()
	mustFrame(t, encoder, StartEvent{Partial: partial})
	partial.Content = append(partial.Content, TextContent{Text: "partial text"}, ThinkingContent{Thinking: "partial thought", ThinkingSignature: "sig-think", Redacted: true})
	mustFrame(t, encoder, TextStartEvent{ContentIndex: 0, Partial: partial})
	mustFrame(t, encoder, ThinkingStartEvent{ContentIndex: 1, Partial: partial})

	assertJSONEqual(t, mustFrame(t, encoder, TextEndEvent{ContentIndex: 0, Content: "event text", Partial: partial}),
		map[string]any{"type": "text_end", "contentIndex": 0, "content": "event text"})
	assertJSONEqual(t, mustFrame(t, encoder, ThinkingEndEvent{ContentIndex: 1, Content: "event thought", Partial: partial}),
		map[string]any{"type": "thinking_end", "contentIndex": 1, "content": "event thought", "thinkingSignature": "sig-think", "redacted": true})
}

// assistant-message-frame.ts:263-283 and 465-477: toolcall_end takes id, name, thoughtSignature and namespace from the event's
// tool call, and reduction replaces them on the block, deleting a thoughtSignature or namespace the end frame omits.
func TestAssistantMessageFrameToolCallEndTakesIdentityFromTheEventAndClearsStaleMetadata(t *testing.T) {
	partial := frameSeed()
	encoder := NewAssistantMessageFrameEncoder()
	frames := []AssistantMessageFrame{mustFrame(t, encoder, StartEvent{Partial: partial})}
	partial.Content = append(partial.Content, ToolCall{ID: "partial-id", Name: "partial-name", Arguments: JsonObject{}, ThoughtSignature: "stale-sig", Namespace: "stale-ns"})
	frames = append(frames, mustFrame(t, encoder, ToolCallStartEvent{ContentIndex: 0, Partial: partial}))
	end := mustFrame(t, encoder, ToolCallEndEvent{ContentIndex: 0, ToolCall: ToolCall{ID: "event-id", Name: "event-name", Arguments: JsonObject{"a": float64(1)}}, Partial: partial})
	frames = append(frames, end)

	assertJSONEqual(t, end, map[string]any{"type": "toolcall_end", "contentIndex": 0, "id": "event-id", "name": "event-name", "arguments": map[string]any{"a": 1}})
	assertJSONEqual(t, mustReduce(t, frames).Content, []any{map[string]any{"type": "toolCall", "id": "event-id", "name": "event-name", "arguments": map[string]any{"a": 1}}})

	withMeta := frameSeed()
	encoder = NewAssistantMessageFrameEncoder()
	mustFrame(t, encoder, StartEvent{Partial: withMeta})
	withMeta.Content = append(withMeta.Content, ToolCall{ID: "i", Name: "n", Arguments: JsonObject{}})
	mustFrame(t, encoder, ToolCallStartEvent{ContentIndex: 0, Partial: withMeta})
	assertJSONEqual(t, mustFrame(t, encoder, ToolCallEndEvent{ContentIndex: 0, ToolCall: ToolCall{ID: "i", Name: "n", Arguments: JsonObject{}, ThoughtSignature: "ts", Namespace: "ns"}, Partial: withMeta}),
		map[string]any{"type": "toolcall_end", "contentIndex": 0, "id": "i", "name": "n", "arguments": map[string]any{}, "thoughtSignature": "ts", "namespace": "ns"})
}
