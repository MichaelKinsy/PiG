package ai

import (
	"context"
	"io"
	"strings"
	"testing"
)

// Pi's SSE consumers follow the SSE field grammar: the single space after a
// colon is optional, repeated data fields are joined with a newline, and the
// final event is dispatched at EOF. Keep every HTTP streaming provider on that
// contract so gateways can use any valid framing.
func TestProvidersAcceptSpecCompliantSSEFraming(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		result, _ := runAnthropicEvents(t, strings.Join([]string{
			"event:message_start",
			`data:{"message":{"id":"message-1",`,
			`data:"usage":{}}}`,
			"",
			"event:content_block_start",
			`data:{"index":0,"content_block":{"type":"text"}}`,
			"",
			"event:content_block_delta",
			`data:{"index":0,"delta":{"type":"text_delta",`,
			`data:"text":"hi"}}`,
			"",
			"event:content_block_stop",
			`data:{"index":0}`,
			"",
			"event:message_delta",
			`data:{"delta":{"stop_reason":"end_turn"},"usage":{}}`,
			"",
			"event:message_stop",
			`data:{}`,
		}, "\n"))
		assertSSETextResult(t, result, "hi")
	})

	t.Run("openai-completions", func(t *testing.T) {
		result := runOpenAICompletionsSSE(t, strings.Join([]string{
			`data:{"choices":[{"delta":{"content":`,
			`data:"hi"},"finish_reason":"stop"}]}`,
			"",
			"data:[DONE]",
		}, "\n"))
		assertSSETextResult(t, result, "hi")
	})

	t.Run("openai-responses", func(t *testing.T) {
		result, _ := collectResponsesEvents(t, strings.Join([]string{
			`data:{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"message-1","content":[]}}`,
			"",
			`data:{"type":"response.output_text.delta","output_index":0,`,
			`data:"delta":"hi"}`,
			"",
			`data:{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"message-1","content":[{"type":"output_text","text":"hi"}]}}`,
			"",
			`data:{"type":"response.completed","response":{"status":`,
			`data:"completed"}}`,
		}, "\n"))
		assertSSETextResult(t, result, "hi")
	})

	t.Run("google", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIGoogleGenerativeAI, "google", "model")
		provider := &googleProvider{}
		provider.parseGeminiSSE(context.Background(), strings.NewReader(strings.Join([]string{
			`data:{"candidates":[{"content":{"parts":[{"text":`,
			`data:"hi"}]},"finishReason":"STOP"}]}`,
		}, "\n")), builder)
		assertSSETextResult(t, builder.stream.Result(), "hi")
	})

	t.Run("mistral", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
		provider := &mistralProvider{}
		provider.consumeStream(context.Background(), io.NopCloser(strings.NewReader(strings.Join([]string{
			`data:{"choices":[{"delta":{"content":`,
			`data:"hi"},"finish_reason":"stop"}]}`,
			"",
			"data:[DONE]",
		}, "\n"))), builder)
		assertSSETextResult(t, builder.stream.Result(), "hi")
	})
}

func TestSSEDecoderAcceptsSSELineEndingsAndDispatchesAtEOF(t *testing.T) {
	decoder := newSSEDecoder(strings.NewReader(": keepalive\rid: ignored\rdata:first\r\ndata: second\n\nevent:done\rdata:last"))
	if !decoder.Next() {
		t.Fatalf("first event missing: %v", decoder.Err())
	}
	first := decoder.Event()
	if first.Event != "" || first.Data != "first\nsecond" {
		t.Fatalf("first event = %#v", first)
	}
	if !decoder.Next() {
		t.Fatalf("EOF event missing: %v", decoder.Err())
	}
	second := decoder.Event()
	if second.Event != "done" || second.Data != "last" {
		t.Fatalf("second event = %#v", second)
	}
	next := decoder.Next()
	if next || decoder.Err() != nil {
		t.Fatalf("decoder terminal state: next=%t err=%v", next, decoder.Err())
	}
}

func assertSSETextResult(t *testing.T, result *AssistantMessage, want string) {
	t.Helper()
	if result.StopReason != StopReasonStop || len(result.Content) != 1 {
		t.Fatalf("result = %#v", result)
	}
	text, ok := result.Content[0].(TextContent)
	if !ok || text.Text != want {
		t.Fatalf("content = %#v, want text %q", result.Content, want)
	}
}

func assertSSEErrorContains(t *testing.T, result *AssistantMessage, want string) {
	t.Helper()
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, want) {
		t.Fatalf("result = %#v, want error containing %q", result, want)
	}
}
