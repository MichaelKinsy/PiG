package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// comb-audit row 100 follow-up: a Gemini stream chunk that is valid JSON but holds a member of an unexpected type. Pi reads each member with its own
// expression (google-generative-ai.ts:106-235): `chunk.candidates?.[0]`, `for (const part of candidate.content.parts)`, `part.text !== undefined` then `+=`,
// `part.thought === true`, mapStopReason's === switch, and `|| 0` on the usage counts. The rows run Pi's own stream (through @google/genai) over each body
// and compare the final message with PiG's. Generated tool call ids carry the clock, so tool calls compare by name and arguments.
func TestGoogleChunkMistypedMembersMatchPi(t *testing.T) {
	chunk := func(data string) string { return "data: " + data + "\r\n\r\n" }
	text := func(value string) string {
		return chunk(`{"candidates":[{"content":{"role":"model","parts":[{"text":` + value + `}]}}]}`)
	}
	hel := text(`"Hel"`)
	end := chunk(`{"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`)
	finish := func(reason string) string {
		return chunk(`{"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":` + reason + `}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`)
	}
	usage := func(value string) string {
		return chunk(`{"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":` + value + `}`)
	}
	parts := func(value string) string {
		return chunk(`{"candidates":[{"content":{"role":"model","parts":` + value + `}}]}`)
	}
	type row struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	rows := []row{
		{"text number", hel + text(`5`) + end},
		// `(candidatesTokenCount || 0) + (thoughtsTokenCount || 0)` and `promptTokenCount - ...` turn true into 1.
		{"usage candidates true", hel + usage(`{"promptTokenCount":3,"candidatesTokenCount":true,"totalTokenCount":4}`)},
		{"usage candidates true thoughts number", hel + usage(`{"promptTokenCount":3,"candidatesTokenCount":true,"thoughtsTokenCount":2,"totalTokenCount":6}`)},
		{"usage prompt true", hel + usage(`{"promptTokenCount":true,"candidatesTokenCount":1,"totalTokenCount":2}`)},
		// The JS read takes the chunk after integer-valued floats are rewritten (77790f284b): JSON.parse reads 3.0 and 1E0 as integers.
		{"integer float beside mistyped member", hel + chunk(`{"candidates":[{"content":{"role":"model","parts":[{"text":5}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3.0,"candidatesTokenCount":1E0,"totalTokenCount":4}}`)},
		{"text array", hel + text(`["x",1]`) + end},
		{"text null", hel + text(`null`) + end},
		{"text object", hel + text(`{"a":1}`) + end},
		{"thought number", hel + chunk(`{"candidates":[{"content":{"parts":[{"text":"t","thought":5}]}}]}`) + end},
		{"thought string", chunk(`{"candidates":[{"content":{"parts":[{"text":"t","thought":"true"}]}}]}`) + end},
		{"thoughtSignature number", hel + chunk(`{"candidates":[{"content":{"parts":[{"text":"t","thoughtSignature":5}]}}]}`) + end},
		{"candidates number", hel + chunk(`{"candidates":5}`) + end},
		{"candidates string", hel + chunk(`{"candidates":"x"}`) + end},
		{"candidates object", hel + chunk(`{"candidates":{"0":{"content":{"parts":[{"text":"!"}]}}}}`) + end},
		{"candidate number", hel + chunk(`{"candidates":[5]}`) + end},
		{"candidate null", hel + chunk(`{"candidates":[null]}`) + end},
		{"content number", hel + chunk(`{"candidates":[{"content":5}]}`) + end},
		{"parts number", hel + parts(`5`) + end},
		{"parts object", hel + parts(`{"a":1}`) + end},
		{"parts string", hel + parts(`"ab"`) + end},
		{"parts zero", hel + parts(`0`) + end},
		{"part number", hel + parts(`[5,{"text":"!"}]`) + end},
		{"part null after text", hel + parts(`[{"text":"!"},null]`) + end},
		{"functionCall number", hel + parts(`[{"functionCall":5}]`) + end},
		{"functionCall zero", hel + parts(`[{"functionCall":0}]`) + end},
		{"functionCall id zero", parts(`[{"functionCall":{"name":"read","args":{"a":1},"id":0}}]`) + end},
		// The JS read keeps JSON.parse's member order, so a functionCall's args reach the tool call as written (google-generative-ai.ts:207).
		{"functionCall args order beside null text", parts(`[{"text":null},{"functionCall":{"name":"read","args":{"zeta":1,"alpha":{"y":2,"b":3}}}}]`) + end},
		{"finishReason number", hel + finish(`5`)},
		{"finishReason array", hel + finish(`["STOP"]`)},
		{"finishReason zero", hel + finish(`0`) + end},
		{"usage number", hel + usage(`5`)},
		{"usage number after usage", hel + chunk(`{"candidates":[{"content":{"parts":[{"text":"!"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`) + usage(`5`)},
		{"usage prompt string", hel + usage(`{"promptTokenCount":"3","candidatesTokenCount":1,"totalTokenCount":4}`)},
		{"usage prompt false", hel + usage(`{"promptTokenCount":false,"candidatesTokenCount":1,"totalTokenCount":4,"cachedContentTokenCount":""}`)},
		{"responseId zero", chunk(`{"responseId":0,"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`) + end},
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
const api = await load("node_modules/@earendil-works/pi-ai/dist/api/google-generative-ai.js");
const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
const out = [];
for (const row of JSON.parse(rowsJSON)) {
	const server = createServer((req, res) => { req.resume(); req.on("end", () => { res.writeHead(200, { "content-type": "text/event-stream" }); res.end(row.body); }); });
	await new Promise((r) => server.listen(0, "127.0.0.1", r));
	const model = { id: "gemini-2.5-flash", name: "g", api: "google-generative-ai", provider: "google", baseUrl: "http://127.0.0.1:" + server.address().port, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 };
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
			message, _ := runGoogleStream(t, t.Context(), func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, test.Body)
			})
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
