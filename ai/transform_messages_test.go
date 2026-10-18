package ai

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestCompletionsToolOnlyContentUsesCompatNullability(t *testing.T) {
	for _, required := range []bool{false, true} {
		provider := &openAIProvider{cfg: OpenAIConfig{Compat: &OpenAICompat{RequiresAssistantAfterToolResult: new(required)}}}
		messages, err := provider.convertMessagesWithCompat([]Message{AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "call", Name: "tool", Arguments: JsonObject{}}}}}, nil, "system", false, false)
		if err != nil || len(messages) != 1 {
			t.Fatalf("messages=%#v err=%v", messages, err)
		}
		if required {
			if messages[0].Content != "" {
				t.Fatalf("required empty content=%#v", messages[0].Content)
			}
		} else if messages[0].Content != nil {
			t.Fatalf("default content=%#v, want null", messages[0].Content)
		}
	}
}

func BenchmarkTransformMessagesHistory(b *testing.B) {
	model := replayTarget()
	messages := make([]Message, 0, 4096)
	for range 1024 {
		messages = append(messages, UserMessage{Content: UserText("continue")}, AssistantMessage{Provider: "openai", API: APIOpenAIResponses, Model: "other", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ThinkingContent{Thinking: "reasoning", ThinkingSignature: "old"}, ToolCall{ID: "call", Name: "tool", Arguments: JsonObject{"value": 21}}}}, SystemMessage{Content: SystemText("update")}, ToolResultMessage{ToolCallID: "call", Content: []ToolResultMessageContent{TextContent{Text: "42"}}})
	}
	b.ReportAllocs()
	for b.Loop() {
		if result := TransformMessages(messages, model, nil); len(result) != len(messages) {
			b.Fatal(len(result))
		}
	}
}

func TestTransformMessagesUsesJavaScriptThinkingWhitespace(t *testing.T) {
	result := TransformMessages([]Message{AssistantMessage{Provider: "openai", API: APIOpenAIResponses, Model: "target", Content: []AssistantContentBlock{ThinkingContent{Thinking: "\ufeff"}, ThinkingContent{Thinking: "\u0085"}}}}, replayTarget(), nil)
	want := []AssistantContentBlock{ThinkingContent{Thinking: "\u0085"}}
	if got := result[0].(AssistantMessage).Content; !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks=%#v want=%#v", got, want)
	}
}

func replayTarget() *Model {
	return &Model{ID: "target", ProviderMeta: ProviderMetadata{ProviderID: "openai", API: APIOpenAIResponses}, Input: []string{"text", "image"}}
}
func TestTransformMessagesThinkingIdentityAndIncompleteHistory(t *testing.T) {
	model := replayTarget()
	for _, status := range []StopReason{StopReasonStop, StopReasonError, StopReasonAborted} {
		for _, same := range []bool{true, false} {
			t.Run(string(status)+"/"+map[bool]string{true: "same", false: "foreign"}[same], func(t *testing.T) {
				source := AssistantMessage{Provider: "openai", API: APIOpenAIResponses, Model: "target", StopReason: status, Content: []AssistantContentBlock{ThinkingContent{Thinking: "opaque", ThinkingSignature: "encrypted", Redacted: true}, ThinkingContent{ThinkingSignature: "signed-empty"}, ThinkingContent{Thinking: " \t"}, ThinkingContent{Thinking: "useful"}, TextContent{Text: "answer", TextSignature: "text-sig"}, ToolCall{ID: "call", Name: "tool", ThoughtSignature: "thought-sig", Arguments: JsonObject{}}}}
				if !same {
					source.Model = "other"
				}
				before := source.cloneMessage()
				messages := TransformMessages([]Message{source, ToolResultMessage{ToolCallID: "call", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}}}, model, nil)
				if !reflect.DeepEqual(source, before) {
					t.Fatal("input changed")
				}
				if status == StopReasonError || status == StopReasonAborted {
					if len(messages) != 1 {
						t.Fatalf("failed turn retained: %#v", messages)
					}
					if _, ok := messages[0].(ToolResultMessage); !ok {
						t.Fatalf("raw upstream keeps orphan result: %#v", messages)
					}
					return
				}
				got := messages[0].(AssistantMessage).Content
				if same {
					if len(got) != 5 {
						t.Fatalf("same-model blocks=%#v", got)
					}
					if got[0].(ThinkingContent).ThinkingSignature != "encrypted" || got[1].(ThinkingContent).ThinkingSignature != "signed-empty" || got[4].(ToolCall).ThoughtSignature != "thought-sig" {
						t.Fatal(got)
					}
				} else {
					want := []AssistantContentBlock{TextContent{Text: "answer"}, ToolCall{ID: "call", Name: "tool", Arguments: JsonObject{}}}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("foreign blocks=%#v want=%#v", got, want)
					}
				}
			})
		}
	}
}
func TestTransformMessagesPairsIDsAndHoldsSystemMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		model := replayTarget()
		callbackCalls := 0
		source := AssistantMessage{Provider: "foreign", Model: "old", Content: []AssistantContentBlock{ToolCall{ID: "original", Name: "tool", Arguments: JsonObject{}}, ToolCall{ID: "missing", Name: "other", Arguments: JsonObject{}}}, StopReason: StopReasonToolUse}
		messages := []Message{source, SystemMessage{Content: SystemText("update")}, ToolResultMessage{ToolCallID: "original", Content: []ToolResultMessageContent{TextContent{Text: "real"}}}, UserMessage{Content: UserText("next")}}
		result := TransformMessages(messages, model, func(id string, target *Model, assistant AssistantMessage) string {
			callbackCalls++
			if target != model || assistant.Model != "old" {
				t.Fatal("wrong callback context")
			}
			return "normalized_" + id
		})
		if callbackCalls != 2 || len(result) != 5 {
			t.Fatalf("calls=%d result=%#v", callbackCalls, result)
		}
		if result[0].(AssistantMessage).Content[0].(ToolCall).ID != "normalized_original" || result[1].(ToolResultMessage).ToolCallID != "normalized_original" {
			t.Fatal(result)
		}
		synthetic := result[2].(ToolResultMessage)
		if synthetic.ToolCallID != "normalized_missing" || synthetic.ToolName != "other" || !synthetic.IsError || synthetic.Timestamp != time.Now().UnixMilli() || !reflect.DeepEqual(synthetic.Content, []ToolResultMessageContent{TextContent{Text: "No result provided"}}) {
			t.Fatal(synthetic)
		}
		if result[3].(SystemMessage).Content != SystemText("update") || result[4].(UserMessage).Content != UserText("next") {
			t.Fatal(result)
		}
		if messages[0].(AssistantMessage).Content[0].(ToolCall).ID != "original" || messages[2].(ToolResultMessage).ToolCallID != "original" {
			t.Fatal("ID transform mutated history")
		}
	})
}
func TestTransformMessagesImageDowngradeAndNilContent(t *testing.T) {
	model := replayTarget()
	model.Input = []string{"text"}
	image := ImageContent{Data: "data", MimeType: "image/png"}
	result := TransformMessages([]Message{UserMessage{Content: UserContentBlocks{image, image, TextContent{Text: "text"}, image}}, ToolResultMessage{Content: []ToolResultMessageContent{TextContent{Text: "(tool image omitted: model does not support images)"}, image, image}}, UserMessage{}, ToolResultMessage{}, AssistantMessage{}, SystemMessage{}}, model, nil)
	want := UserContentBlocks{TextContent{Text: "(image omitted: model does not support images)"}, TextContent{Text: "text"}, TextContent{Text: "(image omitted: model does not support images)"}}
	if !reflect.DeepEqual(result[0].(UserMessage).Content, want) || len(result[1].(ToolResultMessage).Content) != 1 {
		t.Fatal(result)
	}
	if result[2].(UserMessage).Content == nil || result[3].(ToolResultMessage).Content == nil || result[4].(AssistantMessage).Content == nil || result[5].(SystemMessage).Content == nil {
		t.Fatal("nil content survived")
	}
}

// A model without image input leaves text-only content as it is, and still turns a typed nil block slice into an empty one, as the placeholder pass does.
func TestTransformMessagesTextOnlyContentForTextModel(t *testing.T) {
	model := replayTarget()
	model.Input = []string{"text"}
	user := UserContentBlocks{TextContent{Text: "a"}, TextContent{Text: "(image omitted: model does not support images)"}, TextContent{Text: "b"}}
	tool := []ToolResultMessageContent{TextContent{Text: "r"}}
	result := TransformMessages([]Message{UserMessage{Content: user}, UserMessage{Content: UserContentBlocks(nil)}, UserMessage{Content: UserContentBlocks{}}, ToolResultMessage{ToolCallID: "x", Content: tool}}, model, nil)
	if got := result[0].(UserMessage).Content; !reflect.DeepEqual(got, user) {
		t.Fatalf("text-only user content = %#v", got)
	}
	for _, i := range []int{1, 2} {
		if got, ok := result[i].(UserMessage).Content.(UserContentBlocks); !ok || got == nil || len(got) != 0 {
			t.Fatalf("message %d content = %#v, want an empty block slice", i, result[i].(UserMessage).Content)
		}
	}
	if got := result[3].(ToolResultMessage).Content; !reflect.DeepEqual(got, tool) {
		t.Fatalf("text-only tool result content = %#v", got)
	}
}
func TestTransformMessagesClosesPriorCallsBeforeDroppedAssistantAndAtEOF(t *testing.T) {
	model := replayTarget()
	call := AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "one", Name: "tool", Arguments: JsonObject{}}}, StopReason: StopReasonToolUse}
	for _, tail := range [][]Message{nil, {AssistantMessage{StopReason: StopReasonAborted}}, {UserMessage{Content: UserText("next")}}} {
		input := append([]Message{call}, tail...)
		result := TransformMessages(input, model, nil)
		if len(result) < 2 || result[1].(ToolResultMessage).ToolCallID != "one" {
			t.Fatalf("result=%#v", result)
		}
	}
}

// transform-messages.ts: a message is "the same model" only when provider, api and model id all match; the same provider and model id over another API (for example one catalog entry served by two endpoints) replays as a foreign message.
func TestTransformMessagesTreatsADifferentAPIAsAForeignModel(t *testing.T) {
	model := replayTarget()
	source := AssistantMessage{Provider: "openai", API: APIOpenAICompletions, Model: "target", StopReason: StopReasonStop, Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "plan", ThinkingSignature: "encrypted"},
		TextContent{Text: "answer", TextSignature: "text-sig"},
		ToolCall{ID: "call", Name: "tool", ThoughtSignature: "thought-sig", Arguments: JsonObject{}},
	}}
	result := TransformMessages([]Message{source, ToolResultMessage{ToolCallID: "call", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}}}, model, nil)
	want := []AssistantContentBlock{TextContent{Text: "answer"}, ToolCall{ID: "call", Name: "tool", Arguments: JsonObject{}}}
	if got := result[0].(AssistantMessage).Content; !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks = %#v, want the foreign replay %#v", got, want)
	}
}
