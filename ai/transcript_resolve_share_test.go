package ai

import (
	"errors"
	"reflect"
	"strconv"
	"testing"
)

// resolveFixture is a normalized transcript of n rounds with every message shape the resolve step handles: a leading system
// message with tools, mid-conversation system updates with sections, user text and images, thinking, tool calls with
// arguments, tool results with details, errored and aborted assistant turns, and content left nil for normalization.
func resolveFixture(n int) TranscriptContext {
	section := "rules"
	var messages []Message
	for i := range n {
		s := strconv.Itoa(i)
		messages = append(messages,
			UserMessage{Content: UserContentBlocks{TextContent{Text: "question " + s}, ImageContent{Data: "aGk=", MimeType: "image/png"}}, Timestamp: int64(i)},
			AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "think " + s}, ToolCall{ID: "call_" + s, Name: "read", Arguments: JsonObject{"path": "f" + s}}}, StopReason: StopReasonToolUse, Timestamp: int64(i)},
			ToolResultMessage{ToolCallID: "call_" + s, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "result " + s}}, Details: map[string]any{"lines": float64(i)}, Timestamp: int64(i)},
			SystemMessage{Content: SystemText("update " + s), Sections: OrderedSections{{Name: section, Value: &s}}, Timestamp: int64(i)},
			AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "partial " + s}}, StopReason: StopReasonError, ErrorMessage: "boom", Timestamp: int64(i)},
			AssistantMessage{Content: nil, StopReason: StopReasonAborted, Timestamp: int64(i)},
			UserMessage{Content: UserText("again " + s), Timestamp: int64(i)},
			AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "answer " + s}}, StopReason: StopReasonStop, Timestamp: int64(i)},
		)
	}
	tools := []ToolSchema{{Name: "read", Description: "read a file", Parameters: JsonObject{"type": "object"}}}
	return NormalizeContext(Context{SystemPrompt: "system", Tools: tools, Messages: messages})
}

// copyingResolve is the resolve step that deep-copied the transcript twice: through Messages() and again in the
// constructor. Its result is the reference the shared resolve step must equal.
func copyingResolve(context TranscriptContext, supportsMidConversationSystemMessages bool) TranscriptContext {
	if supportsMidConversationSystemMessages {
		return newTranscriptContext(context.Messages())
	}
	messages := context.Messages()
	head := GetCurrentSystemMessage(messages)
	out := make([]Message, 0, len(messages))
	if head != nil {
		out = append(out, *head)
	}
	for _, message := range messages {
		if _, system := message.(SystemMessage); !system {
			out = append(out, message)
		}
	}
	return newTranscriptContext(out)
}

func TestResolveTranscriptEqualsTheCopyingResolve(t *testing.T) {
	for name, context := range map[string]TranscriptContext{
		"rich":   resolveFixture(3),
		"empty":  NormalizeContext(Context{}),
		"failed": {err: errors.New("normalization failed")},
	} {
		for _, mid := range []bool{true, false} {
			got, want := ResolveTranscript(context, mid), copyingResolve(context, mid)
			if !reflect.DeepEqual(got.Messages(), want.Messages()) || got.Len() != want.Len() || (got.err == nil) != (want.err == nil) {
				t.Fatalf("%s mid=%v: got %d messages (err %v), want %d (err %v)\n got %#v\nwant %#v", name, mid, got.Len(), got.err, want.Len(), want.err, got.Messages(), want.Messages())
			}
		}
	}
}

// The resolve step copies nothing per message: a TranscriptContext is immutable (only Messages hands out copies), so the
// resolved transcript shares the normalized messages, and the provider's own Messages call is the one copy a request makes.
// Collapsing still merges every system message (getCurrentSystemMessage, as Pi does), so that merge is not counted.
func TestResolveTranscriptDoesNotCopyTheMessages(t *testing.T) {
	small, large := resolveFixture(25), resolveFixture(250) // 200 and 2,000 messages
	merge := func(context TranscriptContext, mid bool) float64 {
		if mid {
			return 0
		}
		return testing.AllocsPerRun(20, func() { GetCurrentSystemMessage(context.messages) })
	}
	for _, mid := range []bool{true, false} {
		a := testing.AllocsPerRun(20, func() { ResolveTranscript(small, mid) }) - merge(small, mid)
		b := testing.AllocsPerRun(20, func() { ResolveTranscript(large, mid) }) - merge(large, mid)
		if b > a+2 {
			t.Fatalf("mid=%v: ResolveTranscript allocates %.0f times for 200 messages and %.0f for 2,000: it copies per message", mid, a, b)
		}
	}
}

// A provider modifies what Messages returns (TransformMessages rewrites ids, drops blocks, fills content): that must not reach
// the resolved transcript or the transcript it was resolved from.
func TestResolvedTranscriptSurvivesItsCallerModifyingTheCopy(t *testing.T) {
	for _, mid := range []bool{true, false} {
		context := resolveFixture(2)
		before := cloneMessages(context.messages)
		resolved := ResolveTranscript(context, mid)
		resolvedBefore := cloneMessages(resolved.messages)
		copied := TransformMessages(resolved.Messages(), &Model{Input: []string{"text"}}, func(id string, _ *Model, _ AssistantMessage) string { return "x" + id })
		for i, message := range copied {
			switch m := message.(type) {
			case UserMessage:
				if blocks, ok := m.Content.(UserContentBlocks); ok && len(blocks) > 0 {
					blocks[0] = TextContent{Text: "changed"}
				}
			case AssistantMessage:
				for j := range m.Content {
					m.Content[j] = TextContent{Text: "changed"}
				}
			case ToolResultMessage:
				if len(m.Content) > 0 {
					m.Content[0] = TextContent{Text: "changed"}
				}
			case SystemMessage:
				for j := range m.Sections {
					changed := "changed"
					m.Sections[j].Value = &changed
				}
			}
			copied[i] = message
		}
		if !reflect.DeepEqual(context.messages, before) {
			t.Fatalf("mid=%v: the source transcript changed", mid)
		}
		if !reflect.DeepEqual(resolved.messages, resolvedBefore) {
			t.Fatalf("mid=%v: the resolved transcript changed", mid)
		}
	}
}
