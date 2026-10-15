package ai

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// comb-audit row 100: an openai-completions chunk that is valid JSON but holds a member of an unexpected type. Pi's JavaScript ignores it, coerces it with `+=` or throws
// depending on the expression that reads it (openai-completions.ts:558-700). The rows run Pi's own stream over each body and compare the final message with PiG's.
func TestOpenAICompletionsMistypedMembersMatchPi(t *testing.T) {
	const head = `{"id":"c","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`
	const stop = `{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	tool := func(arguments string) []string {
		return []string{
			`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"a\":"}}]}}]}`,
			`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":` + arguments + `}}]}}]}`,
			`{"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`,
		}
	}
	type row struct {
		Name   string   `json:"name"`
		Chunks []string `json:"chunks"`
	}
	rows := []row{
		{"content number", []string{head, `{"choices":[{"delta":{"content":5},"finish_reason":"stop"}]}`}},
		{"content true", []string{head, `{"choices":[{"delta":{"content":true},"finish_reason":"stop"}]}`}},
		{"content empty array", []string{head, `{"choices":[{"delta":{"content":[]},"finish_reason":"stop"}]}`}},
		{"content object", []string{head, `{"choices":[{"delta":{"content":{"a":1}},"finish_reason":"stop"}]}`}},
		{"reasoning_content number then reasoning", []string{head, `{"choices":[{"delta":{"reasoning_content":5,"reasoning":"think"},"finish_reason":"stop"}]}`}},
		{"reasoning object", []string{head, `{"choices":[{"delta":{"reasoning":{"a":1}},"finish_reason":"stop"}]}`}},
		{"tool_calls number", []string{head, `{"choices":[{"delta":{"content":"lo","tool_calls":5}}]}`, stop}},
		{"tool_calls true", []string{head, `{"choices":[{"delta":{"tool_calls":true},"finish_reason":"stop"}]}`}},
		{"tool_calls object", []string{head, `{"choices":[{"delta":{"tool_calls":{"a":1}}}]}`, stop}},
		{"tool_calls zero", []string{head, `{"choices":[{"delta":{"tool_calls":0},"finish_reason":"stop"}]}`}},
		{"tool_calls empty string", []string{head, `{"choices":[{"delta":{"tool_calls":""},"finish_reason":"stop"}]}`}},
		{"arguments number", tool(`5`)},
		{"arguments true", tool(`true`)},
		{"arguments array", tool(`[1,"x",null]`)},
		{"arguments object", tool(`{"b":1}`)},
		{"arguments zero", tool(`0`)},
		{"arguments false", tool(`false`)},
		{"choices number", []string{head, `{"id":"c","choices":5}`, stop}},
		{"choices string", []string{head, `{"id":"c","choices":"x"}`, stop}},
		{"choices object", []string{head, `{"id":"c","choices":{"0":{"delta":{"content":"no"}}}}`, stop}},
		{"choices null element", []string{head, `{"id":"c","choices":[null]}`, stop}},
		{"choices number element", []string{head, `{"id":"c","choices":[5]}`, stop}},
		{"choices string element", []string{head, `{"id":"c","choices":["x"]}`, stop}},
		{"delta number", []string{head, `{"choices":[{"delta":5,"finish_reason":"stop"}]}`}},
		{"delta string", []string{head, `{"choices":[{"delta":"x","finish_reason":"stop"}]}`}},
		{"delta array", []string{head, `{"choices":[{"delta":[],"finish_reason":"stop"}]}`}},
		{"delta zero", []string{head, `{"choices":[{"delta":0,"finish_reason":"stop"}]}`}},
		{"finish_reason zero", []string{head, `{"choices":[{"delta":{},"finish_reason":0}]}`, stop}},
		{"index string", []string{head, `{"choices":[{"index":"0","delta":{"content":"lo"},"finish_reason":"stop"}]}`}},
		{"tool call index string", []string{
			`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":"{\"a\":"}}]}}]}`,
			`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":"0","id":"call_1","function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`}},
		{"tool call function string", []string{`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":"x"}]},"finish_reason":"stop"}]}`}},
		{"reasoning_details object", []string{head, `{"choices":[{"delta":{"reasoning_details":{"a":1}},"finish_reason":"stop"}]}`}},
		{"usage zero", []string{head, `{"usage":0,"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"model number", []string{head, `{"model":5,"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"chunk array", []string{head, `[1,2]`, stop}},
		{"chunk number", []string{head, `5`, stop}},
		{"chunk string", []string{head, `"x"`, stop}},
		{"delta role number", []string{head, `{"choices":[{"delta":{"role":5,"content":"lo"},"finish_reason":"stop"}]}`}},
		{"tool call type number", []string{`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":5,"function":{"name":"read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`}},
		{"tool call custom zero", []string{`{"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","custom":0,"function":{"name":"read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`}},
		{"tool call element number", []string{head, `{"choices":[{"delta":{"tool_calls":[5]},"finish_reason":"stop"}]}`}},
		{"tool call element string and array", []string{head, `{"choices":[{"delta":{"tool_calls":["x",[1]]},"finish_reason":"stop"}]}`}},
		{"finish_reason number", []string{head, `{"choices":[{"delta":{"content":"lo"},"finish_reason":5}]}`}},
		{"finish_reason true", []string{head, `{"choices":[{"delta":{},"finish_reason":true}]}`}},
		{"finish_reason object", []string{head, `{"choices":[{"delta":{},"finish_reason":{"a":1}}]}`}},
		{"finish_reason array of stop", []string{head, `{"choices":[{"delta":{},"finish_reason":["stop"]}]}`}},
		{"finish_reason empty array", []string{head, `{"choices":[{"delta":{},"finish_reason":[]}]}`}},
		{"finish_reason number then stop", []string{head, `{"choices":[{"delta":{},"finish_reason":5}]}`, stop}},
		{"content array", []string{head, `{"choices":[{"delta":{"content":["a",1,null]},"finish_reason":"stop"}]}`}},
		{"content object with length", []string{head, `{"choices":[{"delta":{"content":{"length":3}},"finish_reason":"stop"}]}`}},
		{"content object with zero length", []string{head, `{"choices":[{"delta":{"content":{"length":"0"}},"finish_reason":"stop"}]}`}},
		{"tool_calls string", []string{head, `{"choices":[{"delta":{"tool_calls":"ab"},"finish_reason":"stop"}]}`}},
		{"tool_calls astral string", []string{head, `{"choices":[{"delta":{"tool_calls":"\ud83d\ude00x"},"finish_reason":"stop"}]}`}},
		{"usage number", []string{head, `{"usage":{"prompt_tokens":4,"completion_tokens":2},"choices":[{"delta":{}}]}`, `{"usage":5,"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage string", []string{head, `{"usage":"x","choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage false then choice usage", []string{head, `{"usage":false,"choices":[{"delta":{},"usage":{"prompt_tokens":4,"completion_tokens":2},"finish_reason":"stop"}]}`}},
		{"choice usage array", []string{head, `{"choices":[{"delta":{},"usage":[1],"finish_reason":"stop"}]}`}},
		// The JS read takes the record after the integer-valued floats are rewritten (77790f284b): 12.0 is 12 for JSON.parse, beside a mistyped member.
		{"integer float beside mistyped member", []string{head, `{"usage":{"prompt_tokens":12.0,"completion_tokens":2E0},"choices":[{"delta":{"content":5},"finish_reason":"stop"}]}`}},
		{"usage prompt string", []string{head, `{"usage":{"prompt_tokens":"9","completion_tokens":2,"prompt_tokens_details":{"cached_tokens":3}},"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage prompt true", []string{head, `{"usage":{"prompt_tokens":true,"completion_tokens":2},"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage prompt false", []string{head, `{"usage":{"prompt_tokens":false,"completion_tokens":2},"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage completion empty string", []string{head, `{"usage":{"prompt_tokens":4,"completion_tokens":""},"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage details number", []string{head, `{"usage":{"prompt_tokens":4,"completion_tokens":2,"prompt_tokens_details":5,"prompt_cache_hit_tokens":1},"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage details cache_write false", []string{head, `{"usage":{"prompt_tokens":4,"completion_tokens":2,"prompt_tokens_details":{"cache_write_tokens":false}},"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage cached chain unread", []string{head, `{"usage":{"prompt_tokens":4,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1},"prompt_cache_hit_tokens":"x","cached_tokens":{}},"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage completion details string", []string{head, `{"usage":{"prompt_tokens":4,"completion_tokens":2,"completion_tokens_details":"x"},"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"usage reasoning zero string", []string{head, `{"usage":{"prompt_tokens":4,"completion_tokens":2,"completion_tokens_details":{"reasoning_tokens":""}},"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		// Pi 63035734 moved reasoning_details before the tool calls, so the pinned oracle (1.2.0) no longer describes a chunk whose tool_calls throws: see TestParseSSE_ReasoningDetailsApplyBeforeNonIterableToolCalls.
	}
	type result struct {
		Stop  string `json:"stop"`
		Error string `json:"error"`
		Text  string `json:"text"`
		Tools string `json:"tools"`
		Kinds string `json:"kinds"`
		Usage [6]int `json:"usage"`
	}
	var want []result
	const script = `(async () => {
const [root, rowsJSON] = process.argv.slice(1);
const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
const { createServer } = require("node:http");
const oai = await load("node_modules/@earendil-works/pi-ai/dist/api/openai-completions.js");
const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
const out = [];
for (const row of JSON.parse(rowsJSON)) {
	const body = row.chunks.map((c) => "data: " + c + "\n\n").join("") + "data: [DONE]\n\n";
	const server = createServer((req, res) => { req.resume(); req.on("end", () => { res.writeHead(200, { "content-type": "text/event-stream" }); res.end(body); }); });
	await new Promise((r) => server.listen(0, "127.0.0.1", r));
	const model = { id: "gpt-4o", name: "gpt-4o", api: "openai-completions", provider: "openai", baseUrl: "http://127.0.0.1:" + server.address().port + "/v1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 };
	const r = await oai.stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: "k" }).result();
	out.push({
		stop: r.stopReason,
		error: r.errorMessage ?? "",
		text: r.content.filter((b) => b.type === "text").map((b) => b.text).join(""),
		tools: JSON.stringify(r.content.filter((b) => b.type === "toolCall").map((b) => [b.name, b.arguments])),
		kinds: r.content.map((b) => b.type).join(","),
		usage: [r.usage.input, r.usage.output, r.usage.cacheRead, r.usage.cacheWrite, r.usage.totalTokens, r.usage.reasoning ?? 0],
	});
	server.close();
}
console.log(JSON.stringify(out));
process.exit(0);
})()`
	encodedRows, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), string(encodedRows)).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("undecodable Pi output %q: %v", output, err)
	}
	for i, test := range rows {
		t.Run(test.Name, func(t *testing.T) {
			var body strings.Builder
			for _, chunk := range test.Chunks {
				body.WriteString("data: " + chunk + "\n\n")
			}
			body.WriteString("data: [DONE]\n\n")
			message := runOpenAICompletionsSSE(t, body.String())
			var text strings.Builder
			var tools []any
			var kinds []string
			for _, block := range message.Content {
				kinds = append(kinds, block.contentType())
				switch block := block.(type) {
				case TextContent:
					text.WriteString(block.Text)
				case ToolCall:
					tools = append(tools, []any{block.Name, block.Arguments})
				}
			}
			encoded, _ := json.Marshal(tools)
			usage := message.Usage
			reasoning := 0
			if usage.Reasoning != nil {
				reasoning = *usage.Reasoning
			}
			got := result{Stop: string(message.StopReason), Error: message.ErrorMessage, Text: text.String(), Tools: string(encoded), Kinds: strings.Join(kinds, ","),
				Usage: [6]int{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.TotalTokens, reasoning}}
			if want[i].Tools == "" || want[i].Tools == "[]" {
				want[i].Tools = "null"
			}
			if got != want[i] {
				t.Fatalf("PiG %+v\n Pi %+v", got, want[i])
			}
		})
	}
}

// The mistyped members whose JS result Go's types cannot hold (a count Pi stores raw or concatenates, a prompt count whose Number() is not an integer, an id
// or name stored as a non-string) still end the turn with the decode error rather than guess; they are the residual differences listed in handoffs/owner-signoff.md.
func TestOpenAICompletionsUnmodeledMistypedMembersStillEndTheTurn(t *testing.T) {
	for name, chunk := range map[string]string{
		"completion count string":   `{"usage":{"prompt_tokens":3,"completion_tokens":"1"},"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"prompt count not a number": `{"usage":{"prompt_tokens":"x","completion_tokens":1},"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"prompt count fraction":     `{"usage":{"prompt_tokens":"2.5","completion_tokens":1},"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"cached count false":        `{"usage":{"prompt_tokens":3,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":false}},"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"id number":                 `{"id":5,"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"tool call name number":     `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":5}}]},"finish_reason":"stop"}]}`,
		"tool call id number":       `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":7}]},"finish_reason":"stop"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			message := runOpenAICompletionsSSE(t, "data: "+chunk+"\n\ndata: [DONE]\n\n")
			if message.StopReason != StopReasonError || !strings.HasPrefix(message.ErrorMessage, "openai-completions: invalid SSE JSON: ") {
				t.Fatalf("stop %q error %q, want the decode error", message.StopReason, message.ErrorMessage)
			}
		})
	}
}
