package ai

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// splitSeededChunks cuts raw at random byte offsets, never leaving a CR at a chunk end (D121).
func splitSeededChunks(random *rand.Rand, raw []byte) [][]byte {
	var chunks [][]byte
	for len(raw) > 0 {
		size := 1 + random.Intn(len(raw))
		if random.Intn(3) == 0 {
			size = 1 + random.Intn(6)
		}
		size = min(size, len(raw))
		if raw[size-1] == '\r' && size < len(raw) {
			size++
		}
		chunks = append(chunks, raw[:size])
		raw = raw[size:]
	}
	return chunks
}

// openAICompletionsStreamBodies returns seeded chat.completions chunk streams: content, reasoning and tool-call deltas, finish reasons, usage chunks, [DONE], truncation.
func openAICompletionsStreamBodies() [][][]byte {
	random := rand.New(rand.NewSource(20261106))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	var bodies [][][]byte
	for range 3000 {
		var body strings.Builder
		emit := func(data any) {
			encoded, _ := json.Marshal(data)
			body.WriteString(pick("data: ", "data:") + string(encoded) + pick("\n\n", "\n\n", "\r\n\r\n"))
		}
		toolIndex := 0
		for range random.Intn(6) {
			delta := map[string]any{}
			switch random.Intn(5) {
			case 0, 1:
				delta["content"] = pick("hi", " there", "é𝄞", "")
			case 2:
				delta[pick("reasoning_content", "reasoning", "reasoning_text")] = pick("hm", " so", "")
			case 3:
				call := map[string]any{"index": toolIndex, "function": map[string]any{"arguments": pick(`{"path"`, `:"a"}`, "")}}
				if random.Intn(2) == 0 {
					call["id"] = pick("call_1", "call_2")
					call["function"].(map[string]any)["name"] = pick("read", "bash")
				}
				if random.Intn(3) == 0 {
					toolIndex++
				}
				delta["tool_calls"] = []any{call}
			default:
				delta["role"] = "assistant"
			}
			choice := map[string]any{"index": 0, "delta": delta}
			if random.Intn(6) == 0 {
				choice["finish_reason"] = pick("stop", "length", "tool_calls", "content_filter", "function_call", "network_error")
			}
			chunk := map[string]any{"id": pick("c1", "c2"), "object": "chat.completion.chunk", "choices": []any{choice}}
			if random.Intn(8) == 0 {
				chunk["usage"] = map[string]any{"prompt_tokens": random.Intn(50), "completion_tokens": random.Intn(50), "total_tokens": random.Intn(100), "prompt_tokens_details": map[string]any{"cached_tokens": random.Intn(10)}, "completion_tokens_details": map[string]any{"reasoning_tokens": random.Intn(10)}}
			}
			emit(chunk)
		}
		if random.Intn(2) == 0 {
			emit(map[string]any{"id": "c1", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": pick("stop", "length", "tool_calls")}}})
		}
		if random.Intn(3) == 0 {
			emit(map[string]any{"id": "c1", "object": "chat.completion.chunk", "choices": []any{}, "usage": map[string]any{"prompt_tokens": random.Intn(50), "completion_tokens": random.Intn(50), "total_tokens": random.Intn(100)}})
		}
		if random.Intn(5) != 0 {
			body.WriteString("data: [DONE]\n\n")
		}
		bodies = append(bodies, splitSeededChunks(random, []byte(body.String())))
	}
	return bodies
}

// TestOpenAICompletionsProviderStreamMatchesPi compares Pi's openai-completions provider with Pig's over seeded chunk streams.
func TestOpenAICompletionsProviderStreamMatchesPi(t *testing.T) {
	providerStreamOracle(t, [4]string{"openai-completions", "openAICompletionsApi", "openai-completions", "openai"}, true, openAICompletionsStreamBodies(), func(baseURL string) Provider {
		return NewOpenAIProvider(OpenAIConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	})
}
