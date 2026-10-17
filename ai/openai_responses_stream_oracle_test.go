package ai

import (
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// openAIResponsesStreamBodies returns seeded Responses API event streams: message, reasoning and function-call items with their deltas and done events, completed/incomplete/failed responses, usage, truncation.
func openAIResponsesStreamBodies() [][][]byte {
	random := rand.New(rand.NewSource(20261107))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	var bodies [][][]byte
	for range 3000 {
		var body strings.Builder
		emit := func(typ string, data map[string]any) {
			data["type"] = typ
			encoded, _ := json.Marshal(data)
			body.WriteString("event: " + typ + "\ndata: " + string(encoded) + pick("\n\n", "\n\n", "\r\n\r\n"))
		}
		final := random.Intn(8)
		if random.Intn(10) != 0 {
			emit("response.created", map[string]any{"response": map[string]any{"id": pick("resp_1", "resp_2")}})
		}
		for index := range random.Intn(4) {
			id := pick("msg_1", "rs_1", "fc_1")
			switch random.Intn(3) {
			case 0:
				emit("response.output_item.added", map[string]any{"output_index": index, "item": map[string]any{"type": "message", "id": id, "role": "assistant", "content": []any{}}})
				var text string
				for range random.Intn(3) {
					delta := pick("hi", " there", "é𝄞", "")
					text += delta
					emit("response.output_text.delta", map[string]any{"output_index": index, "content_index": 0, "item_id": id, "delta": delta})
				}
				if random.Intn(8) != 0 {
					emit("response.output_item.done", map[string]any{"output_index": index, "item": map[string]any{"type": "message", "id": id, "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text}}}})
				}
			case 1:
				emit("response.output_item.added", map[string]any{"output_index": index, "item": map[string]any{"type": "reasoning", "id": id, "summary": []any{}}})
				var text string
				for range random.Intn(3) {
					delta := pick("hm", " so", "")
					text += delta
					emit("response.reasoning_summary_text.delta", map[string]any{"output_index": index, "summary_index": 0, "item_id": id, "delta": delta})
				}
				if random.Intn(2) == 0 {
					emit("response.reasoning_summary_part.done", map[string]any{"output_index": index, "summary_index": 0, "item_id": id})
				}
				if random.Intn(8) != 0 {
					emit("response.output_item.done", map[string]any{"output_index": index, "item": map[string]any{"type": "reasoning", "id": id, "summary": []any{map[string]any{"type": "summary_text", "text": text}}}})
				}
			default:
				emit("response.output_item.added", map[string]any{"output_index": index, "item": map[string]any{"type": "function_call", "id": id, "call_id": pick("call_1", "call_2"), "name": pick("read", "bash"), "arguments": ""}})
				parts := []string{`{"path"`, `:"a.t`, `xt"}`}[:random.Intn(4)]
				for _, part := range parts {
					emit("response.function_call_arguments.delta", map[string]any{"output_index": index, "item_id": id, "delta": part})
				}
				arguments := strings.Join(parts, "")
				if random.Intn(2) == 0 {
					emit("response.function_call_arguments.done", map[string]any{"output_index": index, "item_id": id, "arguments": arguments})
				}
				// Pi leaves a call whose output_item.done never arrived open in a length-truncated message (partialJson in the block, no toolcall_end); Pig closes it. That difference is recorded in the commit message and excluded here.
				if random.Intn(8) != 0 || final == 1 {
					emit("response.output_item.done", map[string]any{"output_index": index, "item": map[string]any{"type": "function_call", "id": id, "call_id": pick("call_1", "call_2"), "name": pick("read", "bash"), "arguments": arguments}})
				}
			}
		}
		usage := map[string]any{"input_tokens": random.Intn(50), "output_tokens": random.Intn(50), "total_tokens": random.Intn(100), "input_tokens_details": map[string]any{"cached_tokens": random.Intn(10)}, "output_tokens_details": map[string]any{"reasoning_tokens": random.Intn(10)}}
		switch final {
		case 0:
		case 1:
			emit("response.incomplete", map[string]any{"response": map[string]any{"id": "resp_1", "status": "incomplete", "incomplete_details": map[string]any{"reason": pick("max_output_tokens", "content_filter")}, "usage": usage}})
		case 2:
			emit("response.failed", map[string]any{"response": map[string]any{"id": "resp_1", "status": "failed", "error": map[string]any{"code": "server_error", "message": "boom"}}})
		case 3:
			emit("error", map[string]any{"code": "rate_limit", "message": "slow down"})
		default:
			emit("response.completed", map[string]any{"response": map[string]any{"id": "resp_1", "status": pick("completed", "completed", "cancelled", "queued", "in_progress"), "usage": usage}})
		}
		bodies = append(bodies, splitSeededChunks(random, []byte(body.String())))
	}
	return bodies
}

// TestOpenAIResponsesProviderStreamMatchesPi compares Pi's openai-responses provider with Pig's over seeded Responses event streams.
func TestOpenAIResponsesProviderStreamMatchesPi(t *testing.T) {
	providerStreamOracle(t, [4]string{"openai-responses", "openAIResponsesApi", "openai-responses", "openai"}, true, openAIResponsesStreamBodies(), func(baseURL string) Provider {
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	})
}

// TestAzureOpenAIResponsesProviderStreamMatchesPi compares Pi's azure-openai-responses provider with Pig's over the same Responses event streams.
func TestAzureOpenAIResponsesProviderStreamMatchesPi(t *testing.T) {
	providerStreamOracle(t, [4]string{"azure-openai-responses", "azureOpenAIResponsesApi", "azure-openai-responses", "azure-openai-responses"}, true, openAIResponsesStreamBodies(), func(baseURL string) Provider {
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	})
}

// TestOpenAICodexResponsesProviderStreamMatchesPi compares Pi's openai-codex-responses provider (SSE transport) with Pig's over the same Responses event streams.
func TestOpenAICodexResponsesProviderStreamMatchesPi(t *testing.T) {
	// Codex reads the account id from the access token's JWT payload.
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc"}}`)) + ".sig"
	t.Setenv("PI_ORACLE_API_KEY", token)
	t.Setenv("PI_ORACLE_TRANSPORT", "sse")
	providerOracleTransport = TransportSSE
	t.Cleanup(func() { providerOracleTransport = "" })
	providerStreamOracle(t, [4]string{"openai-codex-responses", "openAICodexResponsesApi", "openai-codex-responses", "openai-codex"}, true, openAIResponsesStreamBodies(), func(baseURL string) Provider {
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: token, Model: "m", BaseURL: baseURL})
	})
}
