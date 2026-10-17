package ai_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/anthropic-messages.ts

// Scripted Messages-API SSE responses (text, thinking with signatures, redacted thinking, fragmented tool input, stop reasons, usage in the start and
// delta events, ping and error events, truncated streams) read by the pinned pi-ai and by PiG; the stream events and the final message must be equal.
func TestAnthropicMessagesResponseMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(127))
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	texts := []string{"", "a", "hello ", "wörld", "😀", "line\n", "<&>", " "}
	var probes []responseProbe
	for range 600 {
		var sb strings.Builder
		event := func(name, data string) { fmt.Fprintf(&sb, "event: %s\ndata: %s\n\n", name, data) }
		event("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_%d","type":"message","role":"assistant","content":[],"model":"m","usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d}}}`, rng.Intn(100), rng.Intn(100), rng.Intn(5), rng.Intn(10), rng.Intn(10)))
		blocks := rng.Intn(4)
		for index := range blocks {
			switch rng.Intn(4) {
			case 0:
				event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, index))
				for range rng.Intn(4) {
					event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, index, quote(pick(texts...))))
				}
			case 1:
				event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"thinking","thinking":"","signature":""}}`, index))
				for range rng.Intn(3) {
					event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"thinking_delta","thinking":%s}}`, index, quote(pick(texts...))))
				}
				if rng.Intn(2) == 0 {
					event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"signature_delta","signature":%s}}`, index, quote(pick("sig", "abc=", ""))))
				}
			case 2:
				event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"redacted_thinking","data":%s}}`, index, quote(pick("ENC", "x"))))
			default:
				event("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":"toolu_%d","name":%s,"input":{}}}`, index, rng.Intn(100), quote(pick("read", "bash", "Read"))))
				for _, fragment := range pick(`{"path":"a"}`, `{"a":1,"b":[1,2]}`, `{"x":`, ``, `{}`) {
					_ = fragment
				}
				whole := pick(`{"path":"a"}`, `{"a":1,"b":[1,2]}`, `{"x":`, `{}`, ``)
				for cut := 0; cut < len(whole); cut += 1 + rng.Intn(4) {
					event("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%s}}`, index, quote(whole[cut:min(len(whole), cut+3)])))
				}
			}
			if rng.Intn(8) != 0 {
				event("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, index))
			}
			if rng.Intn(6) == 0 {
				event("ping", `{"type":"ping"}`)
			}
		}
		if rng.Intn(10) == 0 {
			event("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
		} else if rng.Intn(8) != 0 {
			event("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%s,"stop_sequence":null},"usage":{"output_tokens":%d,"input_tokens":%d,"cache_read_input_tokens":%d}}`, pick(`"end_turn"`, `"max_tokens"`, `"tool_use"`, `"stop_sequence"`, `"refusal"`, `"pause_turn"`, `"weird"`, `null`), rng.Intn(100), rng.Intn(100), rng.Intn(10)))
			if rng.Intn(6) != 0 {
				event("message_stop", `{"type":"message_stop"}`)
			}
		}
		probes = append(probes, responseProbe{Provider: "anthropic", Reasoning: rng.Intn(2) == 0, APIKey: "sk-ant-api03-x", SSE: sb.String()})
	}
	checkResponses(t, "anthropic-messages.js", "anthropic-messages", ai.APIAnthropicMessages, ai.StreamAnthropic, probes)
}
