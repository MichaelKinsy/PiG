package ai_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/mistral-conversations.ts

// Scripted Mistral chat-completion streams (string and typed-chunk content, thinking chunks, fragmented tool calls, finish reasons, usage with cached
// tokens, truncated streams) read by the pinned pi-ai and by PiG; the stream events and the final message must be equal.
func TestMistralResponseMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(137))
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	texts := []string{"", "a", "hello ", "wörld", "😀", "line\n", "<&>", " "}
	var probes []responseProbe
	for range 600 {
		var sb strings.Builder
		chunk := func(delta, finish string) {
			fmt.Fprintf(&sb, "data: {\"id\":\"cmpl-%d\",\"choices\":[{\"index\":0,\"delta\":{%s},\"finish_reason\":%s}]}\n\n", rng.Intn(100), delta, finish)
		}
		for range 1 + rng.Intn(8) {
			switch rng.Intn(8) {
			case 0, 1:
				chunk(`"content":`+quote(pick(texts...)), "null")
			case 2:
				chunk(fmt.Sprintf(`"content":[{"type":"text","text":%s}]`, quote(pick(texts...))), "null")
			case 3:
				chunk(fmt.Sprintf(`"content":[{"type":"thinking","thinking":[{"type":"text","text":%s}]}]`, quote(pick(texts...))), "null")
			case 4, 5:
				index := rng.Intn(2)
				call := fmt.Sprintf(`"index":%d,"id":"call%d","type":"function","function":{"name":%s,"arguments":%s}`, index, rng.Intn(100), quote(pick("read", "bash")), pick(quote(`{"path":`), `{"a":1}`, quote(`{"a":1}`)))
				chunk(`"tool_calls":[{`+call+`}]`, "null")
			case 6:
				chunk(``, pick(`"stop"`, `"length"`, `"tool_calls"`, `"model_length"`, `"error"`, `"weird"`))
			default:
				fmt.Fprintf(&sb, "data: {\"id\":\"cmpl-1\",\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d%s}}\n\n", 10+rng.Intn(100), rng.Intn(50), 60+rng.Intn(100), pick("", `,"prompt_tokens_details":{"cached_tokens":4}`, `,"num_cached_tokens":5`))
			}
		}
		if rng.Intn(4) != 0 {
			sb.WriteString("data: [DONE]\n\n")
		}
		probes = append(probes, responseProbe{Provider: "mistral", Reasoning: rng.Intn(2) == 0, SSE: sb.String()})
	}
	checkResponses(t, "mistral-conversations.js", "mistral-conversations", ai.APIMistralConversations, ai.StreamMistral, probes)
}
