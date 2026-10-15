//go:build !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// mistralStreamBodies returns seeded Mistral chat completion chunk streams: string and typed-array content (text and thinking parts), tool calls with string or object arguments, finish reasons, usage, [DONE], truncation.
func mistralStreamBodies() [][][]byte {
	random := rand.New(rand.NewSource(20261108))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	var bodies [][][]byte
	for range 3000 {
		var body strings.Builder
		emit := func(data any) {
			encoded, _ := json.Marshal(data)
			body.WriteString("data: " + string(encoded) + pick("\n\n", "\n\n", "\r\n\r\n"))
		}
		usage := func() map[string]any {
			u := map[string]any{"prompt_tokens": random.Intn(50), "completion_tokens": random.Intn(50), "total_tokens": random.Intn(100)}
			if random.Intn(3) == 0 {
				u["prompt_tokens_details"] = map[string]any{"cached_tokens": random.Intn(10)}
			}
			return u
		}
		for range random.Intn(6) {
			delta := map[string]any{}
			switch random.Intn(6) {
			case 0, 1:
				delta["content"] = pick("hi", " there", "é𝄞", "")
			case 2:
				delta["content"] = []any{map[string]any{"type": "text", "text": pick("a", "b", "")}}
			case 3:
				delta["content"] = []any{map[string]any{"type": "thinking", "thinking": []any{map[string]any{"type": "text", "text": pick("hm", " so")}}}}
			case 4:
				call := map[string]any{"id": pick("abc123xyz", "def456uvw"), "index": random.Intn(2), "function": map[string]any{"name": pick("read", "bash")}}
				if random.Intn(3) == 0 {
					call["function"].(map[string]any)["arguments"] = map[string]any{"path": "a"}
				} else {
					call["function"].(map[string]any)["arguments"] = pick(`{"path"`, `:"a"}`, "")
				}
				delta["tool_calls"] = []any{call}
			default:
				delta["role"] = "assistant"
			}
			choice := map[string]any{"index": 0, "delta": delta}
			if random.Intn(6) == 0 {
				choice["finish_reason"] = pick("stop", "length", "tool_calls", "model_length", "error")
			}
			chunk := map[string]any{"id": pick("m1", "m2"), "choices": []any{choice}}
			if random.Intn(6) == 0 {
				chunk["usage"] = usage()
			}
			emit(chunk)
		}
		if random.Intn(2) == 0 {
			emit(map[string]any{"id": "m1", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": pick("stop", "length", "tool_calls")}}, "usage": usage()})
		}
		if random.Intn(5) != 0 {
			body.WriteString("data: [DONE]\n\n")
		}
		bodies = append(bodies, splitSeededChunks(random, []byte(body.String())))
	}
	return bodies
}

// TestMistralProviderStreamMatchesPi compares Pi's mistral-conversations provider with Pig's over seeded Mistral chunk streams.
func TestMistralProviderStreamMatchesPi(t *testing.T) {
	providerStreamOracle(t, [4]string{"mistral-conversations", "mistralConversationsApi", "mistral-conversations", "mistral"}, true, mistralStreamBodies(), func(baseURL string) Provider {
		return NewMistralProvider(MistralConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	})
}
