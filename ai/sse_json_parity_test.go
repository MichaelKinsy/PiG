package ai

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestProvidersRejectMalformedSSEJSON(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		result, _ := runAnthropicEvents(t, `event: message_start
data: {"message":{"usage":{}}}

event: content_block_delta
data: {bad json

event: message_delta
data: {"delta":{"stop_reason":"end_turn"},"usage":{}}

event: message_stop
data: {}

`)
		assertSSEErrorContains(t, result, "Could not parse Anthropic SSE event content_block_delta")
	})

	t.Run("anthropic-message-stop", func(t *testing.T) {
		result, _ := runAnthropicEvents(t, `event: message_start
data: {"message":{"usage":{}}}

event: message_delta
data: {"delta":{"stop_reason":"end_turn"},"usage":{}}

event: message_stop
data: {bad json

`)
		assertSSEErrorContains(t, result, "Could not parse Anthropic SSE event message_stop")
	})

	t.Run("openai-completions", func(t *testing.T) {
		result := runOpenAICompletionsSSE(t, "data: {bad json\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		assertSSEErrorContains(t, result, "invalid")
	})

	t.Run("openai-responses", func(t *testing.T) {
		result, _ := collectResponsesEvents(t, "data: {bad json\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		assertSSEErrorContains(t, result, "invalid")
	})

	t.Run("google", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIGoogleGenerativeAI, "google", "model")
		provider := &googleProvider{}
		provider.parseGeminiSSE(context.Background(), strings.NewReader("data: {bad json\n\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"), builder)
		assertSSEErrorContains(t, builder.stream.Result(), "invalid")
	})

	t.Run("mistral", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
		provider := &mistralProvider{}
		provider.consumeStream(context.Background(), io.NopCloser(strings.NewReader("data: {bad json\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")), builder)
		assertSSEErrorContains(t, builder.stream.Result(), "invalid")
	})
}

func TestParseJSONWithRepairEscapesInvalidBackslashes(t *testing.T) {
	var payload struct {
		Text string `json:"text"`
	}
	if err := unmarshalJSONWithRepair(`{"text":"a\qb"}`, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Text != `a\qb` {
		t.Fatalf("text = %q", payload.Text)
	}
}

func TestParseJSONWithRepairKeepsInvalidUnicodeEscapesInvalid(t *testing.T) {
	input := `{"text":"a\uZZ"}`
	if repaired := repairJSON(input); repaired != input {
		t.Fatalf("repairJSON(%s) = %s", input, repaired)
	}
	var payload any
	if err := unmarshalJSONWithRepair(input, &payload); err == nil {
		t.Fatalf("invalid unicode escape parsed as %#v", payload)
	}
}

func TestAnthropicSSERepairsRawControlCharacters(t *testing.T) {
	result, _ := runAnthropicEvents(t, "event: message_start\ndata: {\"message\":{\"usage\":{}}}\n\nevent: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\nevent: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"a\tb\"}}\n\nevent: content_block_stop\ndata: {\"index\":0}\n\nevent: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{}}\n\nevent: message_stop\ndata: {}\n\n")
	assertSSETextResult(t, result, "a\tb")
}
