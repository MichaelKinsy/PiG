package ai

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// comb-audit row 100: an Anthropic content_block_delta that is valid JSON but holds a member of an unexpected type. Pi's `blocks.findIndex((b) => b.index === event.index)`
// finds no block for an index that is not a number, no `event.delta.type` case matches a type that is not a string, and the delta members are appended with `+=`
// (anthropic-messages.ts:737-790). The rows run Pi's own stream over each body and compare the final message with PiG's.
func TestAnthropicDeltaMistypedMembersMatchPi(t *testing.T) {
	event := func(kind, data string) string { return "event: " + kind + "\ndata: " + data + "\n\n" }
	start := event("message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-5","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}`)
	end := event("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`) + event("message_stop", `{"type":"message_stop"}`)
	textBlock := start + event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`)
	thinkingBlock := start + event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`) +
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Th"}}`)
	toolBlock := start + event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"read","input":{}}}`) +
		event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`)
	delta := func(index, body string) string {
		return event("content_block_delta", `{"type":"content_block_delta","index":`+index+`,"delta":`+body+`}`)
	}
	type row struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	rows := []row{
		{"text number", textBlock + delta("0", `{"type":"text_delta","text":5}`) + end},
		{"text null", textBlock + delta("0", `{"type":"text_delta","text":null}`) + end},
		{"text missing", textBlock + delta("0", `{"type":"text_delta"}`) + end},
		{"text empty", textBlock + delta("0", `{"type":"text_delta","text":""}`) + end},
		{"thinking null", thinkingBlock + delta("0", `{"type":"thinking_delta","thinking":null}`) + end},
		{"signature missing", thinkingBlock + delta("0", `{"type":"signature_delta"}`) + end},
		{"partial_json null", toolBlock + delta("0", `{"type":"input_json_delta","partial_json":null}`) + delta("0", `{"type":"input_json_delta","partial_json":"}"}`) + end},
		{"text true", textBlock + delta("0", `{"type":"text_delta","text":true}`) + end},
		{"text array", textBlock + delta("0", `{"type":"text_delta","text":[1,"x",null]}`) + end},
		{"text object", textBlock + delta("0", `{"type":"text_delta","text":{"a":1}}`) + end},
		{"text float", textBlock + delta("0", `{"type":"text_delta","text":1.5e21}`) + end},
		{"index exponent spelling", textBlock + delta(`0e0`, `{"type":"text_delta","text":"lo"}`) + end},
		{"index fraction", textBlock + delta(`0.5`, `{"type":"text_delta","text":"lo"}`) + end},
		{"index string", textBlock + delta(`"0"`, `{"type":"text_delta","text":"lo"}`) + end},
		{"index true", textBlock + delta(`true`, `{"type":"text_delta","text":"lo"}`) + end},
		{"index missing", textBlock + event("content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}`) + end},
		{"index null", textBlock + delta(`null`, `{"type":"text_delta","text":"lo"}`) + end},
		{"delta type number", textBlock + delta("0", `{"type":5,"text":"lo"}`) + end},
		{"delta string", textBlock + delta("0", `"x"`) + end},
		{"delta array", textBlock + delta("0", `[1]`) + end},
		{"thinking number", thinkingBlock + delta("0", `{"type":"thinking_delta","thinking":5}`) + end},
		{"signature number", thinkingBlock + delta("0", `{"type":"signature_delta","signature":7}`) + end},
		{"partial_json number", toolBlock + delta("0", `{"type":"input_json_delta","partial_json":5}`) + delta("0", `{"type":"input_json_delta","partial_json":"}"}`) + end},
		{"partial_json array", toolBlock + delta("0", `{"type":"input_json_delta","partial_json":[1]}`) + delta("0", `{"type":"input_json_delta","partial_json":"}"}`) + end},
	}
	type result struct {
		Stop     string `json:"stop"`
		Error    string `json:"error"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		Tools    string `json:"tools"`
	}
	var want []result
	const script = `(async () => {
const [root, rowsJSON] = process.argv.slice(1);
const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
const { createServer } = require("node:http");
const api = await load("node_modules/@earendil-works/pi-ai/dist/api/anthropic-messages.js");
const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
const out = [];
for (const row of JSON.parse(rowsJSON)) {
	const server = createServer((req, res) => { req.resume(); req.on("end", () => { res.writeHead(200, { "content-type": "text/event-stream" }); res.end(row.body); }); });
	await new Promise((r) => server.listen(0, "127.0.0.1", r));
	const model = { id: "claude-sonnet-4-5", name: "c", api: "anthropic-messages", provider: "anthropic", baseUrl: "http://127.0.0.1:" + server.address().port, reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 1000 };
	const r = await api.stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: "k" }).result();
	out.push({
		stop: r.stopReason,
		error: r.errorMessage ?? "",
		text: r.content.filter((b) => b.type === "text").map((b) => b.text).join(""),
		thinking: r.content.filter((b) => b.type === "thinking").map((b) => b.thinking + "|" + b.thinkingSignature).join(""),
		tools: JSON.stringify(r.content.filter((b) => b.type === "toolCall").map((b) => [b.name, b.arguments])),
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
			message, _ := runAnthropicEvents(t, test.Body)
			var text, thinking strings.Builder
			var tools []any
			for _, block := range message.Content {
				switch block := block.(type) {
				case TextContent:
					text.WriteString(block.Text)
				case ThinkingContent:
					thinking.WriteString(block.Thinking + "|" + block.ThinkingSignature)
				case ToolCall:
					tools = append(tools, []any{block.Name, block.Arguments})
				}
			}
			encoded, _ := json.Marshal(tools)
			got := result{Stop: string(message.StopReason), Error: message.ErrorMessage, Text: text.String(), Thinking: thinking.String(), Tools: string(encoded)}
			if want[i].Tools == "" || want[i].Tools == "[]" {
				want[i].Tools = "null"
			}
			if got != want[i] {
				t.Fatalf("PiG %+v\n Pi %+v", got, want[i])
			}
		})
	}
}
