package ai

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// comb-audit row 100 follow-up: an Anthropic message_start, content_block_start or message_delta that is valid JSON but holds a member of an unexpected type.
// Pi reads each member with its own expression (anthropic-messages.ts:665-857): a non-object read through `.x` yields undefined, `|| 0` turns a falsy count into 0,
// `?? ""` keeps a text member that `+=` later coerces, `if (event.usage)` skips nothing a non-object holds, and mapStopReason's === switch throws
// `Unhandled stop reason: ${reason}` for a truthy reason that is not a string. The rows run Pi's own stream over each body and compare the final message with PiG's.
// responseModel is compared in its String() form: Pi stores a non-string model raw.
func TestAnthropicEventMistypedMembersMatchPi(t *testing.T) {
	event := func(kind, data string) string { return "event: " + kind + "\ndata: " + data + "\n\n" }
	startWith := func(message string) string {
		return event("message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"stop_reason":null,`+message+`}}`)
	}
	start := startWith(`"model":"model","usage":{"input_tokens":5,"output_tokens":1}`)
	blockStart := func(block string) string {
		return event("content_block_start", `{"type":"content_block_start","index":0,"content_block":`+block+`}`)
	}
	textDelta := event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`)
	text := blockStart(`{"type":"text","text":""}`) + textDelta
	stop := event("content_block_stop", `{"type":"content_block_stop","index":0}`)
	messageDelta := func(body string) string { return event("message_delta", `{"type":"message_delta",`+body+`}`) }
	end := func(delta string) string {
		return stop + messageDelta(delta) + event("message_stop", `{"type":"message_stop"}`)
	}
	ok := end(`"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}`)
	type row struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	rows := []row{
		{"message model number", startWith(`"model":5,"usage":{"input_tokens":5,"output_tokens":1}`) + text + ok},
		// The JS read takes the record after integer-valued floats are rewritten (77790f284b): JSON.parse reads 3.0 and 1E0 as integers.
		{"integer float beside mistyped member", startWith(`"model":5,"usage":{"input_tokens":5.0,"output_tokens":1E0}`) + text + ok},
		{"message model object", startWith(`"model":{"a":1},"usage":{"input_tokens":5,"output_tokens":1}`) + text + ok},
		{"message usage number", startWith(`"model":"model","usage":5`) + text + ok},
		{"message usage string", startWith(`"model":"model","usage":"x"`) + text + ok},
		{"message usage array", startWith(`"model":"model","usage":[1]`) + text + ok},
		{"message usage count false", startWith(`"model":"model","usage":{"input_tokens":false,"output_tokens":1,"cache_read_input_tokens":""}`) + text + ok},
		{"message usage cache_creation number", startWith(`"model":"model","usage":{"input_tokens":5,"output_tokens":1,"cache_creation":5}`) + text + ok},
		{"message missing", event("message_start", `{"type":"message_start"}`) + text + ok},
		{"message null", event("message_start", `{"type":"message_start","message":null}`) + text + ok},
		{"message number", event("message_start", `{"type":"message_start","message":5}`) + text + ok},
		{"message usage missing", startWith(`"model":"other"`) + text + ok},
		{"message usage null", startWith(`"model":"other","usage":null`) + text + ok},
		{"message usage zero counts", startWith(`"model":"model","usage":{"input_tokens":0,"output_tokens":0}`) + text + ok},
		{"message id missing", event("message_start", `{"type":"message_start","message":{"model":"model","usage":{"input_tokens":5,"output_tokens":1}}}`) + text + ok},
		{"block missing", start + event("content_block_start", `{"type":"content_block_start","index":0}`) + textDelta + ok},
		{"block null", start + blockStart(`null`) + textDelta + ok},
		{"block type missing", start + blockStart(`{"text":""}`) + textDelta + ok},
		{"block delta missing", text + event("content_block_delta", `{"type":"content_block_delta","index":0}`) + ok},
		{"block delta null", start + text + event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":null}`) + ok},
		{"block delta null bad index", start + text + event("content_block_delta", `{"type":"content_block_delta","index":"x","delta":null}`) + ok},
		{"block delta type missing", start + text + event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"text":"x"}}`) + ok},
		{"delta missing", start + text + end(`"usage":{"output_tokens":2}`)},
		{"delta null", start + text + end(`"delta":null,"usage":{"output_tokens":2}`)},
		{"delta stop_reason missing", start + text + messageDelta(`"delta":{},"usage":{"output_tokens":2}`) + ok},
		{"message usage thinking_tokens", startWith(`"model":"model","usage":{"input_tokens":5,"output_tokens":1,"output_tokens_details":{"thinking_tokens":4}}`) + text + ok},
		{"delta usage thinking_tokens", start + text + end(`"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"output_tokens_details":{"thinking_tokens":4}}`)},
		{"message usage output_tokens_details number", startWith(`"model":"model","usage":{"input_tokens":5,"output_tokens":1,"output_tokens_details":5}`) + text + ok},
		{"block type number", start + blockStart(`{"type":5,"text":""}`) + textDelta + ok},
		{"block number", start + blockStart(`5`) + textDelta + ok},
		{"block text number", start + blockStart(`{"type":"text","text":5}`) + textDelta + ok},
		{"block text array", start + blockStart(`{"type":"text","text":[1,"x"]}`) + textDelta + ok},
		{"block text unread member", start + blockStart(`{"type":"text","text":"","id":5,"thinking":[1]}`) + textDelta + ok},
		{"block thinking signature number", start + blockStart(`{"type":"thinking","thinking":7,"signature":8}`) +
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Th"}}`) + ok},
		{"delta number", start + text + end(`"delta":5,"usage":{"output_tokens":2}`)},
		{"delta string", start + text + end(`"delta":"x","usage":{"output_tokens":2}`)},
		{"delta array", start + text + end(`"delta":["end_turn"],"usage":{"output_tokens":2}`)},
		{"stop_reason number", start + text + end(`"delta":{"stop_reason":5},"usage":{"output_tokens":2}`)},
		{"stop_reason array", start + text + end(`"delta":{"stop_reason":["end_turn"]},"usage":{"output_tokens":2}`)},
		{"stop_reason empty array", start + text + end(`"delta":{"stop_reason":[]},"usage":{"output_tokens":2}`)},
		{"stop_reason object", start + text + end(`"delta":{"stop_reason":{"a":1}},"usage":{"output_tokens":2}`)},
		{"stop_reason true", start + text + end(`"delta":{"stop_reason":true},"usage":{"output_tokens":2}`)},
		{"stop_reason zero", start + text + messageDelta(`"delta":{"stop_reason":0},"usage":{"output_tokens":2}`) + ok},
		{"stop_reason false", start + text + messageDelta(`"delta":{"stop_reason":false},"usage":{"output_tokens":2}`) + ok},
		{"stop_details number", start + text + end(`"delta":{"stop_reason":"end_turn","stop_details":5},"usage":{"output_tokens":2}`)},
		{"refusal stop_details number", start + text + end(`"delta":{"stop_reason":"refusal","stop_details":5},"usage":{"output_tokens":2}`)},
		{"refusal explanation zero", start + text + end(`"delta":{"stop_reason":"refusal","stop_details":{"type":7,"explanation":0}},"usage":{"output_tokens":2}`)},
		{"delta usage number", start + text + end(`"delta":{"stop_reason":"end_turn"},"usage":5`)},
		{"delta usage string", start + text + end(`"delta":{"stop_reason":"end_turn"},"usage":"x"`)},
		{"delta usage cache_creation number", start + text + end(`"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"cache_creation":5}`)},
		{"delta usage output_tokens_details string", start + text + end(`"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"output_tokens_details":"x"}`)},
	}
	type result struct {
		Stop      string `json:"stop"`
		Error     string `json:"error"`
		Text      string `json:"text"`
		Thinking  string `json:"thinking"`
		Tools     string `json:"tools"`
		Model     string `json:"model"`
		Usage     [5]int `json:"usage"`
		Reasoning int    `json:"reasoning"`
	}
	var want []result
	const script = `(async () => {
const root = process.argv[1], rowsJSON = require("node:fs").readFileSync(0, "utf8");
const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
const { createServer } = require("node:http");
const api = await load("node_modules/@earendil-works/pi-ai/dist/api/anthropic-messages.js");
const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
const out = [];
for (const row of JSON.parse(rowsJSON)) {
	const server = createServer((req, res) => { req.resume(); req.on("end", () => { res.writeHead(200, { "content-type": "text/event-stream" }); res.end(row.body); }); });
	await new Promise((r) => server.listen(0, "127.0.0.1", r));
	const model = { id: "model", name: "c", api: "anthropic-messages", provider: "anthropic", baseUrl: "http://127.0.0.1:" + server.address().port, reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 1000 };
	const r = await api.stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: "k" }).result();
	out.push({
		stop: r.stopReason,
		error: r.errorMessage ?? "",
		text: r.content.filter((b) => b.type === "text").map((b) => b.text).join(""),
		thinking: r.content.filter((b) => b.type === "thinking").map((b) => b.thinking + "|" + b.thinkingSignature).join(""),
		tools: JSON.stringify(r.content.filter((b) => b.type === "toolCall").map((b) => [b.name, b.arguments])),
		model: r.responseModel === undefined ? "" : String(r.responseModel),
		usage: [r.usage.input, r.usage.output, r.usage.cacheRead, r.usage.cacheWrite, r.usage.totalTokens],
		reasoning: r.usage.reasoning ?? -1,
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
	// The rows go on stdin: as an argument they overflow Windows' 32 KiB command line.
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t))
	cmd.Stdin = strings.NewReader(string(encodedRows))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("undecodable Pi output %q: %v", output, err)
	}
	for i, test := range rows {
		t.Run(test.Name, func(t *testing.T) {
			provider := &anthropicProvider{cfg: AnthropicConfig{ProviderID: "anthropic", Model: "model"}}
			builder := newAssistantStreamBuilder(t.Context(), APIAnthropicMessages, "anthropic", "model")
			go provider.parseAnthropicSSE(t.Context(), strings.NewReader(test.Body), builder, anthropicStreamNames{})
			message := builder.stream.Result()
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
			usage := message.Usage
			reasoning := -1
			if usage.Reasoning != nil {
				reasoning = *usage.Reasoning
			}
			got := result{Stop: string(message.StopReason), Error: message.ErrorMessage, Text: text.String(), Thinking: thinking.String(), Tools: string(encoded),
				Model: message.ResponseModel, Usage: [5]int{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.TotalTokens}, Reasoning: reasoning}
			if want[i].Tools == "" || want[i].Tools == "[]" {
				want[i].Tools = "null"
			}
			if got != want[i] {
				t.Fatalf("PiG %+v\n Pi %+v", got, want[i])
			}
		})
	}
}
