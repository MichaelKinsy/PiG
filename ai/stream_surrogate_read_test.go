package ai_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// streamUnitMarks finds the marked texts of a message as JSON.stringify writes it: T[..] of a text block, H[..] of a thinking block and A[..] of a
// tool call's argument.
var streamUnitMarks = regexp.MustCompile(`\b[THA]\[[^\]]*\]`)

// streamSurrogateReplies are the event streams of each API kind whose text, thinking and tool-argument deltas end with the escape of a high
// surrogate; the next delta starts with second: the escape of the low surrogate (a server that escapes everything outside ASCII splits one
// character over two deltas this way) or nothing (the unit stays alone).
func streamSurrogateReplies(second string) map[string]string {
	fill := func(records ...string) []string {
		for i := range records {
			records[i] = strings.ReplaceAll(records[i], "§", second)
		}
		return records
	}
	anthropic := surrogateEvents(true, fill(
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m1","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"H[\ud83d"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"§]"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"T[\ud83d"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"§]"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"N","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"p\":\"A[\ud83d"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"§]\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`)...)
	chunk := func(delta, finish string) string {
		return `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + finish + `}]}`
	}
	completions := surrogateEvents(false, fill(
		chunk(`{"role":"assistant","reasoning_content":"H[\ud83d"}`, "null"),
		chunk(`{"reasoning_content":"§]"}`, "null"),
		chunk(`{"content":"T[\ud83d"}`, "null"),
		chunk(`{"content":"§]"}`, "null"),
		chunk(`{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"N","arguments":"{\"p\":\"A[\ud83d"}}]}`, "null"),
		chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"§]\"}"}}]}`, "null"),
		chunk(`{}`, `"tool_calls"`))...) + "data: [DONE]\n\n"
	responses := surrogateEvents(true, fill(
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp_1","object":"response","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.content_part.added","sequence_number":2,"item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","sequence_number":3,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"T[\ud83d"}`,
		`{"type":"response.output_text.delta","sequence_number":4,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"§]"}`,
		`{"type":"response.output_item.done","sequence_number":5,"output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"T[\ud83d§]","annotations":[]}]}}`,
		`{"type":"response.output_item.added","sequence_number":6,"output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"N","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":7,"item_id":"fc_1","output_index":1,"delta":"{\"p\":\"A[\ud83d"}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":8,"item_id":"fc_1","output_index":1,"delta":"§]\"}"}`,
		`{"type":"response.function_call_arguments.done","sequence_number":9,"item_id":"fc_1","output_index":1,"arguments":"{\"p\":\"A[\ud83d§]\"}"}`,
		`{"type":"response.output_item.done","sequence_number":10,"output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"N","arguments":"{\"p\":\"A[\ud83d§]\"}","status":"completed"}}`,
		`{"type":"response.completed","sequence_number":11,"response":{"id":"resp_1","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`)...)
	part := func(parts, rest string) string {
		return `{"candidates":[{"content":{"role":"model","parts":[` + parts + `]},"index":0` + rest + `}]}`
	}
	google := surrogateEvents(false, fill(
		part(`{"text":"H[\ud83d","thought":true}`, ""),
		part(`{"text":"§]","thought":true}`, ""),
		part(`{"text":"T[\ud83d"}`, ""),
		part(`{"text":"§]"}`, ""),
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"N","args":{"p":"A[\ud83d§]"}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)...)
	mistral := surrogateEvents(false, fill(
		chunk(`{"role":"assistant","content":"T[\ud83d"}`, "null"),
		chunk(`{"content":"§]"}`, "null"),
		chunk(`{"content":[{"type":"text","text":" T[\ud83d§]"}]}`, "null"),
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{"content":"","tool_calls":[{"id":"call00001","type":"function","function":{"name":"N","arguments":"{\"p\":\"A[\ud83d§]\"}"},"index":0}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)...)
	return map[string]string{"anthropic-messages": anthropic, "openai-completions": completions, "openai-responses": responses, "azure-openai-responses": responses,
		"google-generative-ai": google, "mistral-conversations": mistral}
}

// A provider's stream is JSON, and JSON.parse reads the escape of a UTF-16 surrogate as that unit. Two rows for the stream of every API kind,
// each with the unit in a text delta, a thinking delta and a tool-argument delta:
//   - a character a server split over two deltas (`\ud83d` ends one, `\ude00` starts the next) is one character again in the message, as the
//     JavaScript `+=` that joins the deltas makes it;
//   - a unit that stays alone is in the message as the unit.
//
// The message PiG ends with holds the marked texts with the bytes of the message Pi ends with, as each is written into a session line. Mistral's
// reader removes a lone unit from each delta (mistral-conversations.ts:640), so both rows end with neither half there.
func TestStreamDeltasKeepSurrogatesAsPi(t *testing.T) {
	context := json.RawMessage(`{"systemPrompt":"s","tools":[{"name":"N","description":"d","parameters":{"type":"object","properties":{"p":{"type":"string"}}}}],"messages":[{"role":"user","content":"go","timestamp":1}]}`)
	modes := []struct{ name, second, text string }{
		{"a character split over two deltas", `\ude00`, "T[\U0001F600]"},
		{"a unit that stays alone", "", `T[\ud83d]`},
	}
	var rows []toolSurrogateStreamRow
	var holds []string
	for _, mode := range modes {
		replies := streamSurrogateReplies(mode.second)
		for _, shape := range toolSurrogateShapes() {
			shape.Context = context
			shape.Name += ": " + mode.name
			rows = append(rows, toolSurrogateStreamRow{toolSurrogateRow: shape, Reply: replies[shape.API]})
			holds = append(holds, mode.text)
		}
	}
	want := piPayloads(t, "testdata/tool_surrogate_stream.mjs", rows)
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			piMarks := strings.Join(streamUnitMarks.FindAllString(want[i], -1), " ")
			if !strings.Contains(piMarks, "A[") || !strings.Contains(piMarks, "T[") {
				t.Fatalf("Pi's message does not hold the text and the argument of the stream: %s", want[i])
			}
			if row.API != "mistral-conversations" && !strings.Contains(piMarks, holds[i]) {
				t.Fatalf("Pi's message does not hold %s: %s", holds[i], want[i])
			}
			message := toolSurrogateResult(t, row.toolSurrogateRow, sseReply(row.Reply))
			encoded, err := jsstring.MarshalJSON(message)
			if err != nil {
				t.Fatal(err)
			}
			// A character joined from two deltas is held in its four bytes, the form every reader of the message takes (the next request, the
			// screen), not as two halves side by side.
			for _, block := range message.Content {
				text := ""
				switch block := block.(type) {
				case ai.TextContent:
					text = block.Text
				case ai.ThinkingContent:
					text = block.Thinking
				}
				if jsstring.HasAdjacentSurrogates(text) {
					t.Errorf("the block holds the two halves of a character side by side instead of the character: %q", text)
				}
			}
			if marks := strings.Join(streamUnitMarks.FindAllString(string(encoded), -1), " "); marks != piMarks {
				t.Errorf("the message holds %q, Pi's holds %q\nPiG: %s\nPi:  %s", marks, piMarks, encoded, want[i])
			}
		})
	}
}
