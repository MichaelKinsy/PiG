//go:build !pig_strip_mistral_conversations

package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// comb-audit row 100 follow-up: a Mistral stream chunk that is valid JSON but holds a member of an unexpected type. Pi reads each member with its own
// expression (mistral-conversations.ts:541-731): `|| 0` on the usage counts, the cached-token `??` chain, `if (choice.finish_reason)` then mapChatStopReason's
// === switch, sanitizeSurrogates on a content item's text, and `delta.tool_calls || []` in a for-of. The rows run Pi's own stream (through @mistralai) over
// each body and compare the final message with PiG's.
func TestMistralChunkMistypedMembersMatchPi(t *testing.T) {
	chunk := func(data string) string { return "data: " + data + "\n\n" }
	hel := chunk(`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`)
	finish := func(reason string) string {
		return chunk(`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":` + reason + `}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}
	end := finish(`"stop"`) + "data: [DONE]\n\n"
	usage := func(value string) string {
		return chunk(`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":`+value+`}`) + "data: [DONE]\n\n"
	}
	delta := func(value string) string {
		return chunk(`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":` + value + `,"finish_reason":null}]}`)
	}
	type row struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	rows := []row{
		{"finish_reason number", hel + finish(`5`) + "data: [DONE]\n\n"},
		// The JS read takes the record after integer-valued floats are rewritten (77790f284b): JSON.parse reads 3.0 and 1E0 as integers.
		{"integer float beside mistyped member", hel + chunk(`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":"3","completion_tokens":1E0,"total_tokens":4.0}}`) + "data: [DONE]\n\n"},
		{"finish_reason array of stop", hel + finish(`["stop"]`) + "data: [DONE]\n\n"},
		{"finish_reason zero", hel + finish(`0`) + end},
		{"content item text number", hel + delta(`{"content":[{"type":"text","text":5}]}`) + end},
		{"content item text null", hel + delta(`{"content":[{"type":"text","text":null},{"type":"text","text":"!"}]}`) + end},
		{"content item number", hel + delta(`{"content":[5,{"type":"text","text":"!"}]}`) + end},
		{"content item type number", hel + delta(`{"content":[{"type":5,"text":7}]}`) + end},
		{"thinking part text number", hel + delta(`{"content":[{"type":"thinking","thinking":[{"text":5},{"text":"t"},{"text":["a","b"]}]}]}`) + end},
		{"thinking not array", hel + delta(`{"content":[{"type":"thinking","thinking":5}]}`) + end},
		{"tool_calls number", hel + delta(`{"tool_calls":5}`) + end},
		{"tool_calls string", hel + delta(`{"tool_calls":"ab"}`) + end},
		{"tool_calls zero", hel + delta(`{"tool_calls":0}`) + end},
		{"usage number", hel + usage(`5`)},
		{"usage number after usage", hel + finish(`null`) + usage(`5`)},
		{"usage prompt string", hel + usage(`{"prompt_tokens":"3","completion_tokens":1,"total_tokens":4}`)},
		{"usage prompt false", hel + usage(`{"prompt_tokens":false,"completion_tokens":1}`)},
		{"usage cached string", hel + usage(`{"prompt_tokens":5,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":"2"},"num_cached_tokens":3}`)},
		{"usage cached chain unread", hel + usage(`{"prompt_tokens":5,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":2},"num_cached_tokens":"x"}`)},
		{"usage details number", hel + usage(`{"prompt_tokens":5,"completion_tokens":1,"prompt_tokens_details":7,"num_cached_tokens":2}`)},
		{"id zero", chunk(`{"id":0,"object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`) + end},
		// The JS read keeps JSON.parse's member order, so object arguments are stringified as written (mistral-conversations.ts:744-747).
		{"tool call object arguments order beside id zero", chunk(`{"id":0,"object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call1abcd","index":0,"function":{"name":"read","arguments":{"zeta":1,"alpha":{"y":2,"b":3}}}}]},"finish_reason":null}]}`) + end},
	}
	type result struct {
		Stop     string `json:"stop"`
		Error    string `json:"error"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		Tools    string `json:"tools"`
		ID       string `json:"id"`
		Usage    [5]int `json:"usage"`
	}
	var want []result
	const script = `(async () => {
const [root, rowsJSON] = process.argv.slice(1);
const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
const { createServer } = require("node:http");
const api = await load("node_modules/@earendil-works/pi-ai/dist/api/mistral-conversations.js");
const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
const out = [];
for (const row of JSON.parse(rowsJSON)) {
	const server = createServer((req, res) => { req.resume(); req.on("end", () => { res.writeHead(200, { "content-type": "text/event-stream" }); res.end(row.body); }); });
	await new Promise((r) => server.listen(0, "127.0.0.1", r));
	const model = { id: "codestral-latest", name: "m", api: "mistral-conversations", provider: "mistral", baseUrl: "http://127.0.0.1:" + server.address().port, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 };
	const r = await api.stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: "k" }).result();
	out.push({
		stop: r.stopReason,
		error: r.errorMessage ?? "",
		text: r.content.filter((b) => b.type === "text").map((b) => b.text).join(""),
		thinking: r.content.filter((b) => b.type === "thinking").map((b) => b.thinking).join(""),
		tools: JSON.stringify(r.content.filter((b) => b.type === "toolCall").map((b) => [b.name, b.arguments])),
		id: r.responseId === undefined ? "" : String(r.responseId),
		usage: [r.usage.input, r.usage.output, r.usage.cacheRead, r.usage.cacheWrite, r.usage.totalTokens],
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
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, test.Body)
			}))
			defer server.Close()
			provider := NewMistralProvider(MistralConfig{APIKey: "k", Model: "codestral-latest", ProviderID: "mistral", BaseURL: server.URL})
			stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(context.Background()) {
			}
			message := stream.Result()
			var text, thinking strings.Builder
			var tools []any
			for _, block := range message.Content {
				switch block := block.(type) {
				case TextContent:
					text.WriteString(block.Text)
				case ThinkingContent:
					thinking.WriteString(block.Thinking)
				case ToolCall:
					arguments, _ := block.ArgumentsJSON()
					tools = append(tools, []any{block.Name, json.RawMessage(arguments)})
				}
			}
			encoded, _ := json.Marshal(tools)
			usage := message.Usage
			got := result{Stop: string(message.StopReason), Error: message.ErrorMessage, Text: text.String(), Thinking: thinking.String(), Tools: string(encoded),
				ID: message.ResponseID, Usage: [5]int{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.TotalTokens}}
			if want[i].Tools == "" || want[i].Tools == "[]" {
				want[i].Tools = "null"
			}
			if got != want[i] {
				t.Fatalf("PiG %+v\n Pi %+v", got, want[i])
			}
		})
	}
}
