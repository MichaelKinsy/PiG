package ai

import (
	"context"
	"strings"
	"testing"
)

func emptyTranscript() TranscriptContext {
	return NormalizeContext(Context{})
}

func TestFauxProvider_TextResponse(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{
		Content: []FauxContentBlock{FauxText("Hello, world!")}, StopReason: "stop",
	})})
	stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var gotDone bool
	for event := range stream.Events(context.Background()) {
		switch event := event.(type) {
		case TextDeltaEvent:
			text.WriteString(event.Delta)
		case DoneEvent:
			gotDone = true
		}
	}
	if text.String() != "Hello, world!" {
		t.Errorf("got text %q, want %q", text.String(), "Hello, world!")
	}
	if !gotDone {
		t.Error("missing done event")
	}
}

func TestTestFauxPorterActivationProbe(t *testing.T) {
	messages := []Message{UserMessage{Content: UserText("/skill:pig-porter verify model-resolver-selector PORTER_HEADLESS_VERIFY")}}
	kind, text, _ := classifyTestFauxRequest(messages)
	if kind != "text" || text != "pig-porter-headless-ok" {
		t.Fatalf("probe = %q/%q", kind, text)
	}
}

func TestTestFauxToolFollowUpUsesLatestUserTurn(t *testing.T) {
	messages := []Message{
		UserMessage{Content: UserText("Run: expr 20 + 22")},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "42"}}},
		UserMessage{Content: UserText("Run: bash long output")},
		ToolResultMessage{Content: []ToolResultMessageContent{TextContent{Text: "1\n2\n3"}}},
	}
	kind, text, _ := classifyTestFauxRequest(messages)
	if kind != "text" || text != "ran" {
		t.Fatalf("follow-up = %q/%q, want text/ran", kind, text)
	}
}

func TestFauxProvider_ToolCallResponse(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{
		Content: []FauxContentBlock{FauxToolCall("bash", map[string]any{"command": "ls"}, "tc-1")}, StopReason: "toolUse",
	})})
	stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gotToolCall bool
	for event := range stream.Events(context.Background()) {
		delta, ok := event.(ToolCallDeltaEvent)
		if !ok {
			continue
		}
		gotToolCall = true
		tool, ok := delta.Partial.Content[delta.ContentIndex].(ToolCall)
		if !ok {
			t.Fatalf("tool content = %#v", delta.Partial.Content[delta.ContentIndex])
		}
		if tool.Name != "bash" || tool.ID != "tc-1" {
			t.Errorf("tool = %#v, want bash/tc-1", tool)
		}
	}
	if !gotToolCall {
		t.Error("missing tool call event")
	}
}

func TestFauxProvider_ErrorResponse(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{StopReason: "error", ErrorMessage: "rate limited"})})
	stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gotError bool
	for event := range stream.Events(context.Background()) {
		if _, ok := event.(ErrorEvent); ok {
			gotError = true
		}
	}
	if !gotError || stream.Result().ErrorMessage != "rate limited" {
		t.Fatalf("error event=%t result=%#v", gotError, stream.Result())
	}
}

func TestFauxProvider_NoResponsesQueued(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gotError bool
	for event := range stream.Events(context.Background()) {
		if _, ok := event.(ErrorEvent); ok {
			gotError = true
		}
	}
	if !gotError {
		t.Error("expected error when no responses queued")
	}
}

func TestFauxProvider_Factory(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	provider.SetResponses([]FauxResponseStep{FauxFactoryStep(func(_ TranscriptContext, _ StreamOptions, _ int) FauxResponse {
		return FauxResponse{Content: []FauxContentBlock{FauxText("dynamic response")}, StopReason: "stop"}
	})})
	stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for event := range stream.Events(context.Background()) {
		if event, ok := event.(TextDeltaEvent); ok {
			text.WriteString(event.Delta)
		}
	}
	if text.String() != "dynamic response" {
		t.Errorf("got %q, want dynamic response", text.String())
	}
}

func TestFauxProvider_CallCount(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	provider.SetResponses([]FauxResponseStep{
		FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("a")}, StopReason: "stop"}),
		FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("b")}, StopReason: "stop"}),
	})
	for range 2 {
		stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(context.Background()) {
		}
	}
	if provider.CallCount() != 2 || provider.PendingResponseCount() != 0 {
		t.Fatalf("call count=%d pending=%d", provider.CallCount(), provider.PendingResponseCount())
	}
}

func TestFauxProvider_ThinkingResponse(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{
		Content: []FauxContentBlock{FauxThinking("let me think..."), FauxText("answer")}, StopReason: "stop",
	})})
	stream, err := provider.Stream(context.Background(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var thinking, text string
	for event := range stream.Events(context.Background()) {
		switch event := event.(type) {
		case ThinkingDeltaEvent:
			thinking += event.Delta
		case TextDeltaEvent:
			text += event.Delta
		}
	}
	if thinking != "let me think..." || text != "answer" {
		t.Fatalf("thinking=%q text=%q", thinking, text)
	}
}

func TestFauxProvider_Cancellation(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{TokensPerSecond: 1})
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{
		Content: []FauxContentBlock{FauxText("a very long response that should be interrupted")}, StopReason: "stop",
	})})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := provider.Stream(ctx, emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(ctx) {
		cancel()
		break
	}
	result := stream.Result()
	if result.StopReason != StopReasonAborted {
		t.Fatalf("stop reason = %q, want %q", result.StopReason, StopReasonAborted)
	}
}

func TestSplitByTokenSize(t *testing.T) {
	chunks := splitByTokenSize("hello", 1, 1)
	var total strings.Builder
	for _, chunk := range chunks {
		total.WriteString(chunk)
	}
	if total.String() != "hello" {
		t.Errorf("reassembled = %q, want hello", total.String())
	}
	empty := splitByTokenSize("", 3, 5)
	if len(empty) != 1 || empty[0] != "" {
		t.Errorf("empty input: got %v", empty)
	}
}
