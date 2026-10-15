//go:build !pig_strip_mistral_conversations

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
data: {"type":"message_start","message":{"usage":{}}}

event: content_block_delta
data: {bad json

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{}}

event: message_stop
data: {"type":"message_stop"}

`)
		assertSSEErrorContains(t, result, "Could not parse Anthropic SSE event content_block_delta")
	})

	t.Run("anthropic-message-stop", func(t *testing.T) {
		result, _ := runAnthropicEvents(t, `event: message_start
data: {"type":"message_start","message":{"usage":{}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{}}

event: message_stop
data: {bad json

`)
		assertSSEErrorContains(t, result, "Could not parse Anthropic SSE event message_stop")
	})

	t.Run("openai-completions", func(t *testing.T) {
		result := runOpenAICompletionsSSE(t, "data: {bad json\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		assertSSEErrorContains(t, result, "Error reading response: malformed server-sent event JSON.")
	})

	t.Run("openai-responses", func(t *testing.T) {
		result, _ := collectResponsesEvents(t, "data: {bad json\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
		assertSSEErrorContains(t, result, "Error reading response: malformed server-sent event JSON.")
	})

	t.Run("google", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIGoogleGenerativeAI, "google", "model")
		provider := &googleProvider{}
		provider.parseGeminiSSE(context.Background(), strings.NewReader("data: {bad json\n\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"), builder)
		// @google/genai reads each chunk with Response.json(), whose rejection is JSON.parse's SyntaxError (probed: Pi 1.1.0).
		if got, want := builder.stream.Result().ErrorMessage, "Expected property name or '}' in JSON at position 1 (line 1 column 2)"; got != want {
			t.Fatalf("error = %q, want %q", got, want)
		}
	})

	t.Run("mistral", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
		provider := &mistralProvider{}
		provider.consumeStream(context.Background(), io.NopCloser(strings.NewReader("data: {bad json\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")), builder)
		// readMistralEvents lets JSON.parse's SyntaxError escape (mistral-conversations.ts:498-506), so the message is V8's.
		result := builder.stream.Result()
		if result.StopReason != StopReasonError || result.ErrorMessage != "Expected property name or '}' in JSON at position 1 (line 1 column 2)" {
			t.Fatalf("result = %#v", result)
		}
	})
}

func TestParseJSONWithRepairEscapesInvalidBackslashes(t *testing.T) {
	type textPayload struct {
		Text string `json:"text"`
	}
	payload, err := ParseJSONWithRepair[textPayload](`{"text":"a\qb"}`)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Text != `a\qb` {
		t.Fatalf("text = %q", payload.Text)
	}
}

// Pi 4b994d5e (#10868): a \u not followed by four hex digits is an invalid escape, kept as literal text like \q.
func TestParseJSONWithRepairKeepsInvalidUnicodeEscapesLiteral(t *testing.T) {
	input := `{"text":"a\uZZ"}`
	if repaired, want := repairJSON(input), `{"text":"a\\uZZ"}`; repaired != want {
		t.Fatalf("repairJSON(%s) = %s, want %s", input, repaired, want)
	}
	payload, err := ParseJSONWithRepair[struct {
		Text string `json:"text"`
	}](input)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Text != `a\uZZ` {
		t.Fatalf("text = %q", payload.Text)
	}
}

func TestAnthropicSSERepairsRawControlCharacters(t *testing.T) {
	result, _ := runAnthropicEvents(t, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"a\tb\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	assertSSETextResult(t, result, "a\tb")
}
