package ai

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

// anthropicStreamBodies returns seeded Anthropic Messages event streams: mostly well-formed block sequences (text, thinking, redacted thinking, tool use with partial JSON) with stop reasons and usage, some truncated, reordered or carrying unknown events, split at arbitrary bytes.
func anthropicStreamBodies() [][][]byte {
	random := rand.New(rand.NewSource(20261105))
	pick := func(options ...string) string { return options[random.Intn(len(options))] }
	var bodies [][][]byte
	for range 3000 {
		var body strings.Builder
		emit := func(event string, data any) {
			encoded, _ := json.Marshal(data)
			eol := pick("\n", "\n", "\n", "\r\n")
			body.WriteString("event: " + event + eol + "data: " + string(encoded) + eol + eol)
		}
		usage := func() map[string]any {
			u := map[string]any{"input_tokens": random.Intn(50), "output_tokens": random.Intn(50)}
			if random.Intn(2) == 0 {
				u["cache_read_input_tokens"] = random.Intn(10)
				u["cache_creation_input_tokens"] = random.Intn(10)
			}
			return u
		}
		if random.Intn(12) != 0 {
			emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": pick("msg_1", "msg_2"), "usage": usage()}})
		}
		for index := range random.Intn(4) {
			switch random.Intn(5) {
			case 0, 1:
				emit("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}})
				for range random.Intn(3) {
					emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": pick("hi", " there", "é𝄞", "")}})
				}
			case 2:
				emit("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
				for range random.Intn(3) {
					emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "thinking_delta", "thinking": pick("hm", " so", "")}})
				}
				if random.Intn(2) == 0 {
					emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "signature_delta", "signature": pick("c2ln", "YWJj")}})
				}
			case 3:
				emit("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": pick("toolu_1", "toolu_2"), "name": pick("read", "bash"), "input": map[string]any{}}})
				for _, part := range []string{`{"path"`, `:"a.t`, `xt"}`}[:random.Intn(4)] {
					emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": part}})
				}
			default:
				emit("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "redacted_thinking", "data": pick("cmVk", "eA==")}})
			}
			if random.Intn(8) != 0 {
				emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
			}
			if random.Intn(10) == 0 {
				emit("ping", map[string]any{"type": "ping"})
			}
		}
		if random.Intn(6) != 0 {
			delta := map[string]any{"stop_reason": pick("end_turn", "max_tokens", "tool_use", "stop_sequence", "refusal", "pause_turn", "sensitive")}
			emit("message_delta", map[string]any{"type": "message_delta", "delta": delta, "usage": usage()})
		}
		switch random.Intn(10) {
		case 0:
			emit("error", map[string]any{"type": "error", "error": map[string]any{"type": "overloaded_error", "message": "Overloaded"}})
		case 1:
		default:
			emit("message_stop", map[string]any{"type": "message_stop"})
		}
		raw := []byte(body.String())
		var chunks [][]byte
		for len(raw) > 0 {
			size := 1 + random.Intn(len(raw))
			if random.Intn(3) == 0 {
				size = 1 + random.Intn(6)
			}
			size = min(size, len(raw))
			// D121: Pig holds a chunk-final CR until the next chunk arrives, Pi reads it as a line break at once; the differential never splits a CRLF.
			if raw[size-1] == '\r' && size < len(raw) {
				size++
			}
			chunks = append(chunks, raw[:size])
			raw = raw[size:]
		}
		bodies = append(bodies, chunks)
	}
	return bodies
}

// TestAnthropicProviderStreamMatchesPi compares Pi's anthropic-messages provider with Pig's over seeded Anthropic event streams.
func TestAnthropicProviderStreamMatchesPi(t *testing.T) {
	providerStreamOracle(t, [4]string{"anthropic-messages", "anthropicMessagesApi", "anthropic-messages", "anthropic"}, true, anthropicStreamBodies(), func(baseURL string) Provider {
		return NewAnthropicProvider(AnthropicConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	})
}
