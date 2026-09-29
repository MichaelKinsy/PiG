package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// .upstream/v0.87.1/packages/ai/test/openrouter-cache-write-repro.test.ts:14 — regression: preserves cache_write_tokens on openai-completions stream path.
// Cache creation at the remote model is live-only; this fixture proves the marker reaches the endpoint and reported write tokens survive both complete requests.
func TestOpenRouterCacheWriteReproUpstream(t *testing.T) {
	nonce := fmt.Sprintf("%d-%g", time.Now().UnixMilli(), rand.Float64())
	paragraph := "Prompt-caching probe content. Keep this exact text stable across requests so the provider can reuse prefix tokens and report cache read and cache write usage."
	system := "You are a concise assistant.\nCache nonce: " + nonce + "\n\n" + strings.TrimSuffix(strings.Repeat(paragraph+"\n\n", 80), "\n\n")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		var payload struct {
			Messages []struct {
				Role    string
				Content json.RawMessage
			}
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Error(err)
		}
		if len(payload.Messages) < 2 {
			t.Error("missing system/user context")
		} else {
			var prompt string
			if err := json.Unmarshal(payload.Messages[0].Content, &prompt); err != nil || prompt != system {
				t.Error("system prefix/nonce changed")
			}
			var blocks []map[string]any
			if err := json.Unmarshal(payload.Messages[len(payload.Messages)-1].Content, &blocks); err != nil || len(blocks) != 1 {
				t.Error("missing marked user content")
			} else if blocks[0]["text"] != "Reply with exactly: OK" || blocks[0]["cache_control"].(map[string]any)["type"] != "ephemeral" {
				t.Error("cache marker changed")
			}
		}
		writeTokens := 0
		if calls == 1 {
			writeTokens = 7
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cache_write_tokens\":%d}}}\n\ndata: [DONE]\n\n", writeTokens)
	}))
	t.Cleanup(server.Close)
	model, ok := LookupModelExact("openrouter/google/gemini-2.5-flash")
	if !ok {
		t.Fatal("missing upstream model")
	}
	provider := NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", ProviderID: model.Provider, Model: model.ID, BaseURL: server.URL, Compat: model.Compat})
	transcript := NormalizeContext(Context{SystemPrompt: system, Messages: []Message{UserMessage{Content: UserText("Reply with exactly: OK")}}})
	options := StreamOptions{MaxTokens: 32, Temperature: 0, TemperatureSet: true, OnPayload: func(value any, _ *Model) (any, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		messages := payload["messages"].([]any)
		for _, message := range slices.Backward(messages) {
			message := message.(map[string]any)
			if message["role"] != "user" {
				continue
			}
			if text, ok := message["content"].(string); ok {
				message["content"] = []any{map[string]any{"type": "text", "text": text, "cache_control": map[string]any{"type": "ephemeral"}}}
				break
			}
			if blocks, ok := message["content"].([]any); ok {
				for _, block := range slices.Backward(blocks) {
					block := block.(map[string]any)
					if block["type"] == "text" {
						block["cache_control"] = map[string]any{"type": "ephemeral"}
						break
					}
				}
			}
			break
		}
		return payload, nil
	}}
	var results []*AssistantMessage
	for range 2 {
		stream, err := provider.Stream(t.Context(), transcript, options)
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if result.StopReason != StopReasonStop {
			t.Fatal(result.ErrorMessage)
		}
		results = append(results, result)
	}
	if results[0].Usage.CacheWrite <= 0 && results[1].Usage.CacheWrite <= 0 {
		t.Fatal("reported cache_write_tokens lost")
	}
	if results[0].Usage.CacheWrite != 7 || results[1].Usage.CacheWrite != 0 {
		t.Fatalf("write tokens=%d/%d", results[0].Usage.CacheWrite, results[1].Usage.CacheWrite)
	}
}
