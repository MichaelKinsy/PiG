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

// Regression for #10497: large new inputs need more room than chars/4 allows.
func TestEstimateContextTokensReserves35CharactersPerTokenForNewText(t *testing.T) {
	context := NormalizeContext(Context{Messages: []Message{
		estimateAssistant(100, 2_000),
		UserMessage{Content: UserText(strings.Repeat("x", 3_500)), Timestamp: 200},
	}})
	got := EstimateContextTokens(context.Messages())
	want := ContextUsageEstimate{Tokens: 3_000, UsageTokens: 2_000, TrailingTokens: 1_000, LastUsageIndex: 0}
	if got != want {
		t.Fatalf("estimate = %+v, want %+v", got, want)
	}
	model := &Model{ID: "test-model", Capabilities: ModelCapabilities{ContextWindow: 10_000, MaxOutputTokens: 8_000}}
	if got := ClampMaxTokensToContext(model, context, model.Capabilities.MaxOutputTokens); got != 2_904 {
		t.Fatalf("max tokens = %d, want 2904", got)
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
	want := ContextUsageEstimate{Tokens: 1_149, TrailingTokens: 1_149, LastUsageIndex: -1}
	if got != want {
		t.Fatalf("estimate = %+v, want %+v", got, want)
	}
	model := &Model{ID: "test-model", Capabilities: ModelCapabilities{ContextWindow: 10_000, MaxOutputTokens: 8_000}}
	if got := ClampMaxTokensToContext(model, context, model.Capabilities.MaxOutputTokens); got != 4_755 {
		t.Fatalf("max tokens = %d, want 4755", got)
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
	want := ContextUsageEstimate{Tokens: 2_002, UsageTokens: 2_000, TrailingTokens: 2, LastUsageIndex: 3}
	if got != want {
		t.Fatalf("estimate = %+v, want %+v", got, want)
	}
}

func TestEstimateMessageTokensCountsSystemToolsImagesAndUTF16(t *testing.T) {
	system := SystemMessage{Content: SystemText(strings.Repeat("s", 40)), Sections: OrderedSections{{Name: "a", Value: new("")}, {Name: "b", Value: new("bb")}}}
	// "ssss…" + "\n\n" + "bb": the empty section is skipped.
	if got := EstimateMessageTokens(system); got != 13 {
		t.Fatalf("system tokens = %d, want 13", got)
	}
	tools := SystemMessage{Content: SystemText(""), ToolsAdded: []ToolSchema{{Name: "read", Description: "d", Parameters: map[string]any{}}}}
	// [{"name":"read","description":"d","parameters":{}}] is 51 characters.
	if got := EstimateMessageTokens(tools); got != 15 {
		t.Fatalf("tool declaration tokens = %d, want 15", got)
	}
	image := UserMessage{Content: UserContentBlocks{TextContent{Text: "abcd"}, ImageContent{Data: "x", MimeType: "image/png"}}}
	if got := EstimateMessageTokens(image); got != 1373 {
		t.Fatalf("image tokens = %d, want 1373", got)
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

// packages/ai/src/utils/estimate.ts estimateContextTokens accepts `TranscriptContext | readonly Message[]` and reads `context.messages` of a transcript (context-estimate.test.ts passes the transcript itself).
func TestEstimateContextTokensAcceptsATranscriptOrItsMessages(t *testing.T) {
	transcript := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("question"), Timestamp: 100},
		estimateAssistant(200, 500),
		UserMessage{Content: UserText(strings.Repeat("y", 400)), Timestamp: 300},
	}})
	fromTranscript := EstimateContextTokens(transcript)
	fromMessages := EstimateContextTokens(transcript.Messages())
	if fromTranscript != fromMessages || fromTranscript.UsageTokens == 0 || fromTranscript.TrailingTokens == 0 {
		t.Errorf("transcript %+v, messages %+v", fromTranscript, fromMessages)
	}
}
