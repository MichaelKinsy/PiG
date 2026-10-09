package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

// newMatrixProvider keeps the catalog's API, identity and compatibility metadata, replacing only authentication and the remote endpoint. Responses are delivered through each production provider parser, not injected as completed assistant messages.
func newMatrixProvider(t *testing.T, m *GeneratedModel, url string, oauth bool) Provider {
	t.Helper()
	return newMatrixProviderWithKey(t, m, url, oauth, "matrix-key")
}

func newMatrixProviderWithKey(t *testing.T, m *GeneratedModel, url string, oauth bool, key string) Provider {
	t.Helper()
	switch m.API {
	case APIAnthropicMessages:
		if oauth && m.Provider == "anthropic" && key == "matrix-key" {
			key = "sk-ant-oat01-matrix"
		}
		return NewAnthropicProvider(AnthropicConfig{APIKey: key, Model: m.ID, ProviderID: m.Provider, BaseURL: url, Compat: m.Compat, UseBearerAuth: m.Provider == "github-copilot"})
	case APIOpenAICompletions:
		return NewOpenAIProvider(OpenAIConfig{APIKey: key, Model: m.ID, ProviderID: m.Provider, BaseURL: url, Compat: m.Compat})
	case APIOpenAIResponses:
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: key, Model: m.ID, ProviderID: m.Provider, BaseURL: url, Compat: m.Compat, IsReasoning: m.Reasoning})
	case APIAzureOpenAIResponses:
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: key, Model: m.ID, ProviderID: m.Provider, BaseURL: url, Compat: m.Compat})
	case APIOpenAICodexResponses:
		if key == "matrix-key" {
			key = codexTestToken(t, "matrix-account")
		}
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: key, Model: m.ID, ProviderID: m.Provider, BaseURL: url, Compat: m.Compat})
	case APIGoogleVertex:
		return newGoogleVertexTestProvider(GoogleVertexConfig{APIKey: key, Model: m.ID, ProviderID: m.Provider, BaseURL: url, Project: "matrix-project", Location: "us-central1"}, nil)
	case APIGoogleGenerativeAI:
		return NewGoogleProvider(GoogleConfig{APIKey: key, Model: m.ID, ProviderID: m.Provider, BaseURL: url})
	case APIMistralConversations:
		return newMistralTestProvider(MistralConfig{APIKey: key, Model: m.ID, ProviderID: m.Provider, BaseURL: url, Reasoning: m.Reasoning}, nil)
	case APIBedrockConverseStream:
		return NewBedrockTestProvider(m.ID, m.DisplayName, url)
	default:
		t.Fatalf("unsupported matrix API %s for %s/%s", m.API, m.Provider, m.ID)
		return nil
	}
}

func decodeMatrixRequest(r *http.Request) (any, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if r.Header.Get("Content-Encoding") == "zstd" {
		data, err = decodeZstdRawFrameForTest(data)
		if err != nil {
			return nil, err
		}
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func writeMatrixUsageResponse(t *testing.T, w http.ResponseWriter, api API, text string, cached bool, thoughts ...string) {
	t.Helper()
	thinking := ""
	if len(thoughts) > 0 {
		thinking = thoughts[0]
	}
	input, cacheRead, cacheWrite := 80, 0, 20
	if cached {
		input, cacheRead, cacheWrite = 60, 40, 0
	}
	write := func(format string, args ...any) {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			t.Error(err)
		}
	}
	sse := func(value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Error(err)
			return
		}
		write("data: %s\n\n", raw)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	switch api {
	case APIAnthropicMessages:
		events := []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "msg_matrix", "type": "message", "role": "assistant", "content": []any{}, "usage": map[string]any{"input_tokens": input, "output_tokens": 0, "cache_read_input_tokens": cacheRead, "cache_creation_input_tokens": cacheWrite}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 6}},
			{"type": "message_stop"},
		}
		if thinking != "" {
			for _, event := range events[1:4] {
				event["index"] = 1
			}
			extra := []map[string]any{
				{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}},
				{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": thinking}},
				{"type": "content_block_stop", "index": 0},
			}
			events = append(append([]map[string]any{events[0]}, extra...), events[1:]...)
		}
		for _, event := range events {
			write("event: %s\n", event["type"])
			sse(event)
		}
	case APIOpenAICompletions, APIMistralConversations:
		if thinking != "" {
			delta := map[string]any{"reasoning_content": thinking}
			if api == APIMistralConversations {
				delta = map[string]any{"content": []any{map[string]any{"type": "thinking", "thinking": []any{map[string]any{"type": "text", "text": thinking}}}}}
			}
			sse(map[string]any{"id": "chatcmpl_matrix", "choices": []any{map[string]any{"index": 0, "delta": delta}}})
		}
		nativeCached := 0
		if cached {
			nativeCached = 40
		}
		sse(map[string]any{"id": "chatcmpl_matrix", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": nil}}})
		sse(map[string]any{"id": "chatcmpl_matrix", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 6, "total_tokens": 106, "prompt_tokens_details": map[string]any{"cached_tokens": nativeCached}}})
		write("data: [DONE]\n\n")
	case APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses:
		if thinking != "" {
			sse(map[string]any{"type": "response.output_item.added", "output_index": 1, "item": map[string]any{"type": "reasoning", "id": "rs_matrix", "summary": []any{}}})
			sse(map[string]any{"type": "response.reasoning_summary_text.delta", "output_index": 1, "delta": thinking})
			sse(map[string]any{"type": "response.output_item.done", "output_index": 1, "item": map[string]any{"type": "reasoning", "id": "rs_matrix", "summary": []any{map[string]any{"text": thinking}}}})
		}
		nativeCached := 0
		if cached {
			nativeCached = 40
		}
		sse(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_matrix", "content": []any{}}})
		sse(map[string]any{"type": "response.output_text.delta", "output_index": 0, "delta": text})
		item := map[string]any{"type": "message", "id": "msg_matrix", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text}}}
		sse(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		sse(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_matrix", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 6, "total_tokens": 106, "input_tokens_details": map[string]any{"cached_tokens": nativeCached}}}})
	case APIGoogleGenerativeAI, APIGoogleVertex:
		nativeCached := 0
		if cached {
			nativeCached = 40
		}
		parts := []any{map[string]any{"text": text}}
		if thinking != "" {
			parts = append([]any{map[string]any{"text": thinking, "thought": true}}, parts...)
		}
		sse(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "finishReason": "STOP"}}, "usageMetadata": map[string]any{"promptTokenCount": 100, "candidatesTokenCount": 6, "cachedContentTokenCount": nativeCached, "totalTokenCount": 106}})
	case APIBedrockConverseStream:
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		encoder := eventstream.NewEncoder()
		emit := func(kind string, value any) {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Error(err)
				return
			}
			headers := eventstream.Headers{}
			headers.Set(":message-type", eventstream.StringValue("event"))
			headers.Set(":event-type", eventstream.StringValue(kind))
			headers.Set(":content-type", eventstream.StringValue("application/json"))
			if err := encoder.Encode(w, eventstream.Message{Headers: headers, Payload: raw}); err != nil {
				t.Error(err)
			}
		}
		emit("messageStart", map[string]any{"role": "assistant"})
		index := 0
		if thinking != "" {
			emit("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"reasoningContent": map[string]any{"text": thinking}}})
			emit("contentBlockStop", map[string]any{"contentBlockIndex": 0})
			index = 1
		}
		emit("contentBlockDelta", map[string]any{"contentBlockIndex": index, "delta": map[string]any{"text": text}})
		emit("contentBlockStop", map[string]any{"contentBlockIndex": index})
		emit("messageStop", map[string]any{"stopReason": "end_turn"})
		emit("metadata", map[string]any{"usage": map[string]any{"inputTokens": input, "outputTokens": 6, "cacheReadInputTokens": cacheRead, "cacheWriteInputTokens": cacheWrite, "totalTokens": 106}})
	default:
		if _, err := io.WriteString(w, "unsupported API"); err != nil {
			t.Error(err)
		}
	}
}
