package ai

import (
	"strings"
	"testing"
)

// Ports packages/ai/test/context-estimate.test.ts.

func estimateAssistant(timestamp int64, totalTokens int) AssistantMessage {
	return AssistantMessage{
		Content: []AssistantContentBlock{TextContent{Text: "kept"}}, API: "openai-responses", Provider: "openai", Model: "test-model",
		Usage: Usage{Input: totalTokens, TotalTokens: totalTokens}, StopReason: StopReasonStop, Timestamp: timestamp,
	}
}

func TestEstimateContextTokensIgnoresStaleUsageBeforeInsertedMessage(t *testing.T) {
	context := NormalizeContext(Context{
		SystemPrompt: "system",
		Messages: []Message{
			UserMessage{Content: UserText("summary"), Timestamp: 200},
			estimateAssistant(100, 9_500),
			UserMessage{Content: UserText(strings.Repeat("x", 4_000)), Timestamp: 300},
		},
	})
	got := EstimateContextTokens(context.Messages())
	want := ContextUsageEstimate{Tokens: 1_005, TrailingTokens: 1_005, LastUsageIndex: -1}
	if got != want {
		t.Fatalf("estimate = %+v, want %+v", got, want)
	}
	model := &Model{ID: "test-model", Capabilities: ModelCapabilities{ContextWindow: 10_000, MaxOutputTokens: 8_000}}
	if got := ClampMaxTokensToContext(model, context, model.Capabilities.MaxOutputTokens); got != 4_899 {
		t.Fatalf("max tokens = %d, want 4899", got)
	}
}

func TestEstimateContextTokensUsesUsageAfterResponseToInsertedContext(t *testing.T) {
	context := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("summary"), Timestamp: 200},
		estimateAssistant(100, 9_500),
		UserMessage{Content: UserText("new prompt"), Timestamp: 300},
		estimateAssistant(400, 2_000),
		UserMessage{Content: UserText("tail"), Timestamp: 500},
	}})
	got := EstimateContextTokens(context.Messages())
	want := ContextUsageEstimate{Tokens: 2_001, UsageTokens: 2_000, TrailingTokens: 1, LastUsageIndex: 3}
	if got != want {
		t.Fatalf("estimate = %+v, want %+v", got, want)
	}
}

func TestEstimateMessageTokensCountsSystemToolsImagesAndUTF16(t *testing.T) {
	system := SystemMessage{Content: SystemText(strings.Repeat("s", 40)), Sections: OrderedSections{{Name: "a", Value: new("")}, {Name: "b", Value: new("bb")}}}
	// "ssss…" + "\n\n" + "bb": the empty section is skipped.
	if got := EstimateMessageTokens(system); got != 11 {
		t.Fatalf("system tokens = %d, want 11", got)
	}
	tools := SystemMessage{Content: SystemText(""), ToolsAdded: []ToolSchema{{Name: "read", Description: "d", Parameters: map[string]any{}}}}
	// [{"name":"read","description":"d","parameters":{}}] is 51 characters.
	if got := EstimateMessageTokens(tools); got != 13 {
		t.Fatalf("tool declaration tokens = %d, want 13", got)
	}
	image := UserMessage{Content: UserContentBlocks{TextContent{Text: "abcd"}, ImageContent{Data: "x", MimeType: "image/png"}}}
	if got := EstimateMessageTokens(image); got != 1201 {
		t.Fatalf("image tokens = %d, want 1201", got)
	}
	// Each CJK character is one UTF-16 unit; an astral emoji is two.
	if got := EstimateMessageTokens(UserMessage{Content: UserText("漢字漢字😀")}); got != 2 {
		t.Fatalf("UTF-16 tokens = %d, want 2", got)
	}
}

func TestClampMaxTokensToContext(t *testing.T) {
	model := func(window int) *Model { return &Model{Capabilities: ModelCapabilities{ContextWindow: window}} }
	empty := NormalizeContext(Context{})
	if got := ClampMaxTokensToContext(model(128_000), empty, 4096); got != 4096 {
		t.Errorf("room to spare: got %d, want 4096", got)
	}
	if got := ClampMaxTokensToContext(model(8192), empty, 32_000); got != 8192-contextSafetyTokens {
		t.Errorf("clamped to window: got %d, want %d", got, 8192-contextSafetyTokens)
	}
	if got := ClampMaxTokensToContext(model(1000), empty, 32_000); got != minMaxTokens {
		t.Errorf("window smaller than the margin: got %d, want %d", got, minMaxTokens)
	}
	if got := ClampMaxTokensToContext(model(0), empty, 500); got != 500 {
		t.Errorf("unknown window: got %d, want 500", got)
	}
}
