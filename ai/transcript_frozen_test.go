package ai

import (
	"math"
	"testing"
)

func assistantWithCall(args JsonObject) AssistantMessage {
	return AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "t"}, ToolCall{ID: "c", Name: "n", Arguments: args}}, StopReason: StopReasonToolUse}
}

func TestFrozenContextSharesItsMessagesAndAnOrdinaryOneCopiesThem(t *testing.T) {
	args := JsonObject{"n": 1.0}
	messages := []Message{UserMessage{Content: UserText("q")}, assistantWithCall(args)}

	frozen := NormalizeContext(Context{Messages: messages, Frozen: true})
	args["n"] = 2.0
	if got := frozen.At(1).(AssistantMessage).Content[1].(ToolCall).Arguments["n"]; got != 2.0 {
		t.Fatalf("a frozen transcript copied its messages: n = %v", got)
	}
	if frozen.Len() != 2 {
		t.Fatalf("Len = %d, want 2", frozen.Len())
	}

	ordinary := NormalizeContext(Context{Messages: messages})
	args["n"] = 3.0
	if got := ordinary.At(1).(AssistantMessage).Content[1].(ToolCall).Arguments["n"]; got != 2.0 {
		t.Fatalf("an ordinary transcript shares its messages: n = %v", got)
	}
}

func TestFrozenContextStillValidatesAndStillCopiesTheCallersTools(t *testing.T) {
	bad := NormalizeContext(Context{Messages: []Message{assistantWithCall(JsonObject{"n": math.NaN()})}, Frozen: true})
	if bad.Len() != 0 || bad.Messages() != nil {
		t.Fatal("a frozen transcript with an unmarshalable argument was accepted")
	}

	tools := []ToolSchema{{Name: "t", Description: "d", Parameters: map[string]any{"type": "object"}}}
	frozen := NormalizeContext(Context{SystemPrompt: "rules", Tools: tools, Messages: []Message{UserMessage{Content: UserText("q")}}, Frozen: true})
	tools[0].Parameters["type"] = "changed"
	system, ok := frozen.At(0).(SystemMessage)
	if !ok || system.ToolsAdded[0].Parameters["type"] != "object" {
		t.Fatalf("the initial system message shares the caller's tools: %#v", frozen.At(0))
	}
	if frozen.Len() != 2 {
		t.Fatalf("Len = %d, want the system message and the user message", frozen.Len())
	}
}

func TestFrozenContextNormalizesMissingContentWithoutTouchingTheCallersMessages(t *testing.T) {
	messages := []Message{UserMessage{}, AssistantMessage{}}
	frozen := NormalizeContext(Context{Messages: messages, Frozen: true})
	if frozen.At(0).(UserMessage).Content == nil || frozen.At(1).(AssistantMessage).Content == nil {
		t.Fatal("missing content was not normalized")
	}
	if messages[0].(UserMessage).Content != nil {
		t.Fatal("normalization rewrote the caller's slice")
	}
}
