package ai_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/openai-responses.ts, openai-responses-shared.ts processResponsesStream

// Scripted Responses-API SSE streams (reasoning summaries, assistant messages with output text and refusals, function calls with fragmented
// arguments, completed/incomplete/failed responses with usage, error events, truncated streams) read by the pinned pi-ai and by PiG; the stream
// events and the final message must be equal.
func TestOpenAIResponsesResponseMatchesPi(t *testing.T) {
	checkResponsesResponse(t, "openai-responses")
}

// TestOpenAICodexResponsesResponseMatchesPi reads the same streams through openai-codex-responses' SSE transport.
func TestOpenAICodexResponsesResponseMatchesPi(t *testing.T) {
	checkResponsesResponse(t, "openai-codex-responses")
}

func checkResponsesResponse(t *testing.T, apiName string) {
	rng := rand.New(rand.NewSource(131))
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	texts := []string{"", "a", "hello ", "wörld", "😀", "line\n", "<&>", " "}
	var probes []responseProbe
	for range 600 {
		var sb strings.Builder
		event := func(data string) { fmt.Fprintf(&sb, "data: %s\n\n", data) }
		event(fmt.Sprintf(`{"type":"response.created","response":{"id":"resp_%d"}}`, rng.Intn(100)))
		for range rng.Intn(4) {
			id := rng.Intn(1000)
			switch rng.Intn(3) {
			case 0:
				event(fmt.Sprintf(`{"type":"response.output_item.added","item":{"type":"reasoning","id":"rs_%d","summary":[]}}`, id))
				var summary []string
				for range rng.Intn(3) {
					event(`{"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":""}}`)
					text := ""
					for range rng.Intn(3) {
						delta := pick(texts...)
						text += delta
						event(fmt.Sprintf(`{"type":"response.reasoning_summary_text.delta","delta":%s}`, quote(delta)))
					}
					event(`{"type":"response.reasoning_summary_part.done"}`)
					summary = append(summary, fmt.Sprintf(`{"type":"summary_text","text":%s}`, quote(text)))
				}
				if rng.Intn(8) != 0 {
					event(fmt.Sprintf(`{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_%d","summary":[%s]}}`, id, strings.Join(summary, ",")))
				}
			case 1:
				event(fmt.Sprintf(`{"type":"response.output_item.added","item":{"type":"message","id":"msg_%d","role":"assistant","content":[],"status":"in_progress"}}`, id))
				var parts []string
				for range 1 + rng.Intn(2) {
					if rng.Intn(5) == 0 {
						event(`{"type":"response.content_part.added","part":{"type":"refusal","refusal":""}}`)
						delta := pick(texts...)
						event(fmt.Sprintf(`{"type":"response.refusal.delta","delta":%s}`, quote(delta)))
						parts = append(parts, fmt.Sprintf(`{"type":"refusal","refusal":%s}`, quote(delta)))
						continue
					}
					event(`{"type":"response.content_part.added","part":{"type":"output_text","text":"","annotations":[]}}`)
					text := ""
					for range rng.Intn(4) {
						delta := pick(texts...)
						text += delta
						event(fmt.Sprintf(`{"type":"response.output_text.delta","delta":%s}`, quote(delta)))
					}
					event(`{"type":"response.content_part.done"}`)
					parts = append(parts, fmt.Sprintf(`{"type":"output_text","text":%s,"annotations":[]}`, quote(text)))
				}
				if rng.Intn(8) != 0 {
					event(fmt.Sprintf(`{"type":"response.output_item.done","item":{"type":"message","id":"msg_%d","role":"assistant","status":"completed","content":[%s]}}`, id, strings.Join(parts, ",")))
				}
			default:
				name := pick("read", "bash")
				event(fmt.Sprintf(`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_%d","call_id":"call_%d","name":%s,"arguments":""}}`, id, id, quote(name)))
				whole := pick(`{"path":"a"}`, `{"a":1,"b":[1,2]}`, `{"x":`, `{}`, ``)
				for cut := 0; cut < len(whole); cut += 1 + rng.Intn(4) {
					event(fmt.Sprintf(`{"type":"response.function_call_arguments.delta","delta":%s}`, quote(whole[cut:min(len(whole), cut+3)])))
				}
				if rng.Intn(3) != 0 {
					event(fmt.Sprintf(`{"type":"response.function_call_arguments.done","arguments":%s}`, quote(whole)))
				}
				if rng.Intn(8) != 0 {
					event(fmt.Sprintf(`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_%d","call_id":"call_%d","name":%s,"arguments":%s}}`, id, id, quote(name), quote(whole)))
				}
			}
		}
		switch rng.Intn(8) {
		case 0:
			event(`{"type":"error","code":"rate_limit","message":"slow down"}`)
		case 1:
			event(`{"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`)
		case 2: // truncated
		default:
			event(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_x","status":%s,"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d,"input_tokens_details":{"cached_tokens":%d},"output_tokens_details":{"reasoning_tokens":%d}}}}`, pick(`"completed"`, `"incomplete"`, `"cancelled"`, `"failed"`, `"queued"`, `"in_progress"`), 10+rng.Intn(100), rng.Intn(50), 60+rng.Intn(100), rng.Intn(10), rng.Intn(5)))
		}
		probe := responseProbe{Provider: "openai", Reasoning: rng.Intn(2) == 0, SSE: sb.String()}
		if apiName == "openai-codex-responses" {
			probe.Provider, probe.APIKey = "openai-codex", "x."+base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc"}}`))+".y"
			probe.Options = map[string]any{"transport": "sse"}
		}
		probes = append(probes, probe)
	}
	if apiName == "openai-codex-responses" {
		checkResponses(t, "openai-codex-responses.js", apiName, ai.APIOpenAICodexResponses, ai.StreamOpenAICodexResponses, probes)
		return
	}
	checkResponses(t, "openai-responses.js", apiName, ai.APIOpenAIResponses, ai.StreamOpenAIResponses, probes)
}

// Hand-written edge streams for the branches of processResponsesStream the random streams above never reach: the [DONE] sentinel, raw
// reasoning text, items that only arrive as output_item.done, a done event of another type than the open block, argument completion that does
// and does not extend the streamed prefix, final_answer phases, terminal events without a response or with encrypted reasoning content,
// cache-write usage, incomplete details, failed responses with and without details, and response.done (Codex only).
func TestProcessResponsesStreamEdgeEventsMatchPi(t *testing.T) {
	checkResponsesEdgeEvents(t, "openai-responses")
}

func TestProcessResponsesStreamEdgeEventsMatchPiOnCodex(t *testing.T) {
	checkResponsesEdgeEvents(t, "openai-codex-responses")
}

func checkResponsesEdgeEvents(t *testing.T, apiName string) {
	created := `{"type":"response.created","response":{"id":"resp_1"}}`
	completed := func(response string) string {
		return `{"type":"response.completed","response":{"id":"resp_1",` + response + `}}`
	}
	usage := `"usage":{"input_tokens":100,"output_tokens":7,"total_tokens":107,"input_tokens_details":{"cached_tokens":10,"cache_write_tokens":5},"output_tokens_details":{"reasoning_tokens":3}}`
	reasoningAdded := `{"type":"response.output_item.added","item":{"type":"reasoning","id":"rs_1","summary":[]}}`
	messageAdded := `{"type":"response.output_item.added","item":{"type":"message","id":"msg_1","role":"assistant","content":[],"status":"in_progress"}}`
	callAdded := `{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}`
	streams := [][]string{
		{created, `[DONE]`, completed(`"status":"completed"`)},
		{created, completed(`"status":"completed"`), `[DONE]`},
		{created, reasoningAdded, `{"type":"response.reasoning_text.delta","delta":"raw "}`, `{"type":"response.reasoning_summary_text.delta","delta":"sum"}`,
			`{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"final raw"}]}}`, completed(`"status":"completed"`)},
		{created, reasoningAdded, `{"type":"response.reasoning_summary_text.delta","delta":"streamed"}`,
			`{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","summary":[],"content":[]}}`, completed(`"status":"completed"`)},
		{created, reasoningAdded, `{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"s1"},{"type":"summary_text","text":"s2"}],"encrypted_content":"enc"}}`,
			`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"reasoning","id":"rs_1","encrypted_content":"late"}]}}`},
		{created, reasoningAdded, `{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"s"}]}}`,
			`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"reasoning","id":"rs_1","encrypted_content":"late"},{"type":"message","id":"rs_1","encrypted_content":"no"}]}}`},
		{created, `{"type":"response.output_item.done","item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"only done","annotations":[]},{"type":"refusal","refusal":"no"}]}}`, completed(`"status":"completed"`)},
		{created, `{"type":"response.output_item.added","item":{"type":"message","id":"msg_1","role":"assistant","content":[],"status":"in_progress","phase":"final_answer"}}`,
			`{"type":"response.refusal.delta","delta":"nope"}`, completed(`"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}`)},
		{created, `{"type":"response.output_item.added","item":{"type":"message","id":"msg_1","role":"assistant","content":[],"status":"in_progress","phase":"commentary"}}`,
			`{"type":"response.output_text.delta","delta":"hi"}`, `{"type":"response.output_item.done","item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","phase":"commentary","content":[{"type":"output_text","text":"hi"}]}}`, completed(`"status":"completed"`)},
		{created, messageAdded, `{"type":"response.output_text.delta","delta":"hi"}`, `{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{}"}}`, completed(`"status":"completed"`)},
		{created, callAdded, `{"type":"response.function_call_arguments.delta","delta":"{\"a\":"}`, `{"type":"response.function_call_arguments.done","arguments":"{\"a\":1}"}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}`, completed(`"status":"completed"`)},
		{created, callAdded, `{"type":"response.function_call_arguments.delta","delta":"{\"a\":"}`, `{"type":"response.function_call_arguments.done","arguments":"{\"b\":2}"}`, completed(`"status":"completed"`)},
		{created, callAdded, `{"type":"response.function_call_arguments.delta","delta":"{\"a\":1}"}`, `{"type":"response.function_call_arguments.done","arguments":"{\"a\":1}"}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}`, completed(`"status":"completed"`)},
		{created, callAdded, `{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}`, completed(`"status":"completed"`)},
		{created, `{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"p\":1}"}}`, `{"type":"response.function_call_arguments.delta","delta":"x"}`, completed(`"status":"completed"`)},
		{created, `{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{}","namespace":"ns"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{}","namespace":"ns"}}`, completed(`"status":"completed"`)},
		{created, callAdded, `{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{}"}}`, `{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_2","call_id":"call_2","name":"bash","arguments":"{}"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_2","call_id":"call_2","name":"bash","arguments":"{}"}}`, completed(`"status":"completed"`)},
		{created, callAdded, completed(`"status":"completed"`)},
		{created, reasoningAdded, `{"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"s"}]}}`,
			`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","id":"rs_1","encrypted_content":"no"},{"type":"reasoning","id":"rs_1","encrypted_content":"late"}]}}`},
		{created, completed(`"status":"completed","end_turn":true`)},
		{created, completed(`"status":"completed","end_turn":false`)},
		{created, messageAdded, `{"type":"response.output_text.delta","delta":"a"}`, `{"type":"response.output_item.added","item":{"type":"message","id":"msg_2","role":"assistant","content":[],"status":"in_progress"}}`, `{"type":"response.output_text.delta","delta":"b"}`,
			`{"type":"response.output_item.done","item":{"type":"message","id":"msg_2","role":"assistant","status":"completed","content":[{"type":"output_text","text":"b"}]}}`, completed(`"status":"completed"`)},
		{created, `{"type":"response.completed"}`},
		{created, completed(usage + `,"status":"completed"`)},
		{created, completed(`"status":"completed","usage":{"input_tokens":4,"output_tokens":1,"total_tokens":5}`)},
		{created, completed(`"status":"completed","service_tier":"flex",` + usage)},
		{created, completed(`"status":"incomplete","incomplete_details":{"reason":"content_filter"}`)},
		{created, completed(`"status":"incomplete","incomplete_details":{}`)},
		{created, completed(`"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}`)},
		{created, completed(`"status":"bogus"`)},
		{created, completed(`"status":"queued"`)},
		{created, completed(`"status":"failed"`)},
		{created, `{"type":"response.done","response":{"id":"resp_1","status":"completed"}}`},
		{created, `{"type":"response.done"}`},
		{created, `{"type":"response.failed","response":{"id":"resp_f","status":"failed","error":{"code":"server_error","message":"boom"},` + usage + `}}`},
		{created, `{"type":"response.failed","response":{"id":"resp_f","status":"failed","error":{}}}`},
		{created, `{"type":"response.failed","response":{"id":"resp_f","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`},
		{created, `{"type":"response.failed","response":{"id":"resp_f","status":"failed"}}`},
		{created, `{"type":"response.failed"}`},
		{created, `{"type":"error","code":"rate_limit","message":"slow down"}`},
		{created, `{"type":"error"}`},
		{created, reasoningAdded, `{"type":"error","code":"x","message":"y"}`},
		{created, `{"type":"response.output_text.delta","delta":"orphan"}`, `{"type":"response.reasoning_summary_text.delta","delta":"orphan"}`, completed(`"status":"completed"`)},
		{created, `{"type":"response.output_item.added","item":{"type":"web_search_call","id":"ws_1"}}`, completed(`"status":"completed"`)},
		{created, `{"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_1"}}`, completed(`"status":"completed"`)},
		{created, messageAdded, `{"type":"response.output_item.added","item":{"type":"web_search_call","id":"ws_1"}}`, `{"type":"response.output_text.delta","delta":"still the message"}`, completed(`"status":"completed"`)},
		{created, `{"error":{"message":"inline","code":"c"}}`},
		{created, `{"type":"response.created","response":{"id":"resp_2"}}`, completed(`"status":"completed"`)},
		{created, `{"type":"response.created"}`, completed(`"status":"completed"`)},
		// Members of an unexpected type: mapCodexEvents normalizes a terminal status that is not one of its own to undefined and yields every other
		// event unchanged into processResponsesStream (openai-codex-responses.ts:743-788). (A mistyped delta is carried raw in Pi's delta event,
		// which a Go string cannot hold; messageOnly below compares those streams by their final message.)
		{created, messageAdded, `{"type":"response.output_text.delta","delta":"x"}`, completed(`"status":"completed","usage":5`)},
	}
	if apiName == "openai-codex-responses" {
		// openai-responses keeps a non-string status raw in rawStopReason, which a Go string cannot hold.
		streams = append(streams, []string{created, messageAdded, `{"type":"response.output_text.delta","delta":"x"}`, completed(`"status":5,` + usage)})
	}
	messageOnly := [][]string{
		{created, messageAdded, `{"type":"response.output_text.delta","delta":5}`, `{"type":"response.output_text.delta","delta":null}`, completed(`"status":"completed"`)},
		{created, messageAdded, `{"type":"response.output_text.delta","delta":{"a":1}}`, completed(`"status":"completed",` + usage)},
		{created, callAdded, `{"type":"response.function_call_arguments.delta","delta":7}`, completed(`"status":"completed"`)},
	}
	streams = append(streams, messageOnly...)
	var probes []responseProbe
	for n, events := range streams {
		var sb strings.Builder
		for _, event := range events {
			fmt.Fprintf(&sb, "data: %s\n\n", event)
		}
		for _, reasoning := range []bool{false, true} {
			probe := responseProbe{Provider: "openai", Reasoning: reasoning, SSE: sb.String(), MessageOnly: n >= len(streams)-len(messageOnly)}
			if apiName == "openai-codex-responses" {
				probe.Provider, probe.APIKey = "openai-codex", "x."+base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc"}}`))+".y"
				probe.Options = map[string]any{"transport": "sse"}
			}
			probes = append(probes, probe)
		}
	}
	if apiName == "openai-codex-responses" {
		checkResponsesAllowEmpty(t, "openai-codex-responses.js", apiName, ai.APIOpenAICodexResponses, ai.StreamOpenAICodexResponses, probes)
		return
	}
	checkResponsesAllowEmpty(t, "openai-responses.js", apiName, ai.APIOpenAIResponses, ai.StreamOpenAIResponses, probes)
}
