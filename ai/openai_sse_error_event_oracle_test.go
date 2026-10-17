package ai

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// An SSE record whose event is "error" never reaches Pi's stream loop: the OpenAI SDK parses its data and throws
// `new APIError(undefined, data?.error ?? data)` (openai core/streaming.js), whose message is the error's message, JSON.stringify of a message that is not
// a string, JSON.stringify of the error, or "(no status code or body)" (core/error.js makeMessage). Pi then reports that message unprefixed, since the
// APIError has no status (utils/error-body.ts formatProviderError). Both OpenAI APIs share the SDK stream; the rows run Pi's own streams over each body.
func TestOpenAISSEErrorEventMatchesPi(t *testing.T) {
	responsesHead := "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"m\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"m\",\"output_index\":0,\"content_index\":0,\"delta\":\"Hel\"}\n\n"
	completionsHead := "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n"
	errorEvent := func(data string) string { return "event: error\ndata: " + data + "\n\n" }
	type row struct {
		Name string `json:"name"`
		API  string `json:"api"`
		Body string `json:"body"`
	}
	var rows []row
	for _, record := range []struct{ name, data string }{
		{"rate limit", `{"type":"error","code":"rate_limit_exceeded","message":"Rate limit reached","param":null}`},
		{"nested error", `{"type":"error","error":{"message":"inner","code":"c"}}`},
		{"nested error without message", `{"error":{"code":"c"}}`},
		{"null nested error", `{"error":null,"message":"outer"}`},
		{"message array", `{"type":"error","code":5,"message":["m"]}`},
		{"message null", `{"type":"error","code":null,"message":null}`},
		{"no message", `{"type":"error"}`},
		{"null data", `null`},
		{"false nested error", `{"error":false,"message":"outer"}`},
	} {
		rows = append(rows,
			row{"responses " + record.name, "openai-responses", responsesHead + errorEvent(record.data)},
			row{"completions " + record.name, "openai-completions", completionsHead + errorEvent(record.data)})
	}
	// A record whose data has a truthy error member throws in the SDK too (`if (data && data.error)`), so neither the observer nor
	// `output.responseId ||= chunk.id` sees it, even when its other members are mistyped.
	for _, record := range []struct{ name, responses, completions string }{
		{"data error first", `{"type":"response.created","response":{"id":"r"},"error":{"message":"first"}}`, `{"id":"c","model":"x","error":{"message":"first"},"choices":[]}`},
		{"data error mistyped", `{"type":5,"delta":7,"error":{"message":"mistyped"}}`, `{"id":"c","choices":5,"error":{"message":"mistyped"}}`},
		{"data error falsy", `{"type":"response.created","response":{"id":"r"},"error":0}`, `{"id":"c","error":"","choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":"stop"}]}`},
	} {
		rows = append(rows,
			row{"responses " + record.name, "openai-responses", "data: " + record.responses + "\n\n"},
			row{"completions " + record.name, "openai-completions", "data: " + record.completions + "\n\ndata: [DONE]\n\n"})
	}
	type result struct {
		Stop     string `json:"stop"`
		Error    string `json:"error"`
		Text     string `json:"text"`
		ID       string `json:"id"`
		Observed int    `json:"observed"`
	}
	var want []result
	const script = `(async () => {
const [root, rowsJSON] = process.argv.slice(1);
const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
const { createServer } = require("node:http");
const apis = { "openai-responses": await load("node_modules/@earendil-works/pi-ai/dist/api/openai-responses.js"), "openai-completions": await load("node_modules/@earendil-works/pi-ai/dist/api/openai-completions.js") };
const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
const out = [];
for (const row of JSON.parse(rowsJSON)) {
	const server = createServer((req, res) => { req.resume(); req.on("end", () => { res.writeHead(200, { "content-type": "text/event-stream" }); res.end(row.body); }); });
	await new Promise((r) => server.listen(0, "127.0.0.1", r));
	const model = { id: "test", name: "test", api: row.api, provider: "openai", baseUrl: "http://127.0.0.1:" + server.address().port + "/v1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 };
	let observed = 0;
	const r = await apis[row.api].stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: "k", onProviderStreamEvent: () => { observed++; } }).result();
	out.push({ stop: r.stopReason, error: r.errorMessage ?? "", text: r.content.filter((b) => b.type === "text").map((b) => b.text).join(""), id: r.responseId ?? "", observed });
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
			observed := 0
			options := StreamOptions{OnProviderStreamEvent: func(context.Context, any, *Model) error { observed++; return nil }}
			var builder *assistantStreamBuilder
			if test.API == "openai-responses" {
				builder = newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "test")
				builder.setProviderEventObserver(options, nil)
				provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "test"}}
				provider.parseResponsesSSE(context.Background(), strings.NewReader(test.Body), builder, nil)
			} else {
				builder = newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openai", "test")
				builder.setProviderEventObserver(options, nil)
				provider := &openAIProvider{cfg: OpenAIConfig{ProviderID: "openai", Model: "test"}}
				provider.parseSSE(context.Background(), strings.NewReader(test.Body), builder, nil)
			}
			message := builder.stream.Result()
			var text strings.Builder
			for _, block := range message.Content {
				if block, ok := block.(TextContent); ok {
					text.WriteString(block.Text)
				}
			}
			got := result{Stop: string(message.StopReason), Error: message.ErrorMessage, Text: text.String(), ID: message.ResponseID, Observed: observed}
			if got != want[i] {
				t.Fatalf("PiG %+v\n Pi %+v", got, want[i])
			}
		})
	}
}
