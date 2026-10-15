package ai_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/openai-completions.ts

// Scripted chat-completion SSE responses (text, reasoning in each field name, fragmented and parallel tool calls, finish reasons, usage chunks, error
// chunks) read by the pinned pi-ai (through options.fetch) and by PiG (through a local server). The stream events and the final assistant message must
// be equal as parsed JSON, partial snapshots and the timestamp aside.
func TestOpenAICompletionsResponseMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(113))
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	texts := []string{"", "a", "hello ", "wörld", "😀", "line\n", "<&>", " "}
	var probes []responseProbe
	for range 600 {
		var lines []string
		id := rng.Intn(2) == 0
		chunk := func(delta string, finish string) string {
			member := fmt.Sprintf(`"choices":[{"index":0,"delta":{%s},"finish_reason":%s}]`, delta, finish)
			if id {
				return fmt.Sprintf(`{"id":"chatcmpl-%d",%s}`, rng.Intn(100), member)
			}
			return "{" + member + "}"
		}
		toolIDs := map[int]bool{}
		for range 1 + rng.Intn(10) {
			switch rng.Intn(11) {
			case 0, 1, 2:
				lines = append(lines, chunk(`"content":`+quote(pick(texts...)), "null"))
			case 3:
				lines = append(lines, chunk(fmt.Sprintf(`%q:%s`, pick("reasoning_content", "reasoning", "reasoning_text"), quote(pick(texts...))), "null"))
			case 4, 5:
				index := rng.Intn(3)
				// Arguments that are JSON but not an object (1, "x") are left out: Pi keeps the parsed value, PiG's ToolCall.Arguments is an object.
				call := fmt.Sprintf(`"index":%d`, index)
				if !toolIDs[index] || rng.Intn(5) == 0 {
					toolIDs[index] = true
					call += fmt.Sprintf(`,"id":"call_%d","type":"function","function":{"name":%s,"arguments":%s}`, rng.Intn(100), quote(pick("read", "bash")), quote(pick(`{"path":`, `{"a":1}`, `{"p"`)))
				} else {
					call += `,"function":{"arguments":` + quote(pick(`{}`, `,"b":2}`, ``)) + `}`
				}
				lines = append(lines, chunk(`"tool_calls":[{`+call+`}]`, "null"))
			case 6:
				lines = append(lines, chunk(``, pick(`"stop"`, `"length"`, `"tool_calls"`, `"content_filter"`, `"function_call"`, `"network_error"`, `"weird"`)))
			case 7:
				usage := fmt.Sprintf(`"prompt_tokens":%d,"completion_tokens":%d,"prompt_tokens_details":{"cached_tokens":%d%s},"completion_tokens_details":{"reasoning_tokens":%d}`, 10+rng.Intn(100), rng.Intn(50), rng.Intn(10), pick("", `,"cache_write_tokens":3`, `,"cache_write_tokens":0`), rng.Intn(5))
				switch rng.Intn(5) {
				case 0: // usage inside the choice (some providers)
					lines = append(lines, fmt.Sprintf(`{"choices":[{"index":0,"delta":{},"finish_reason":null,"usage":{%s}}]}`, usage))
				case 1: // Moonshot reports cached tokens at the top level
					lines = append(lines, fmt.Sprintf(`{"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"cached_tokens":%d}}`, 10+rng.Intn(100), rng.Intn(50), rng.Intn(10)))
				default:
					lines = append(lines, fmt.Sprintf(`{"choices":[],"usage":{%s}}`, usage))
				}
			case 9:
				details := pick(`{"type":"reasoning.text","text":"think ","id":"r1","index":0}`, `{"type":"reasoning.summary","summary":"sum","id":"r2","index":1}`, `{"type":"reasoning.encrypted","id":"call_3","data":"enc"}`)
				lines = append(lines, chunk(`"reasoning_details":[`+details+`]`, "null"))
			case 10:
				lines = append(lines, fmt.Sprintf(`{"model":%s,"choices":[]}`, quote(pick("m", "other-model"))))
			default:
				if rng.Intn(6) == 0 {
					lines = append(lines, `{"error":{"message":"boom","code":"x"}}`)
				} else {
					lines = append(lines, chunk(`"role":"assistant"`, "null"))
				}
			}
		}
		sse := ""
		for _, line := range lines {
			sse += "data: " + line + "\n\n"
		}
		if rng.Intn(4) != 0 {
			sse += "data: [DONE]\n\n"
		}
		probes = append(probes, responseProbe{Provider: pick("custom", "openrouter", "zai", "deepseek", "moonshotai", "xai"), Reasoning: rng.Intn(2) == 0, SSE: sse})
	}
	checkResponses(t, "openai-completions.js", "openai-completions", ai.APIOpenAICompletions, ai.StreamOpenAICompletions, probes)
}
