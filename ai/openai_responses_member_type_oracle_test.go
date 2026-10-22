package ai

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// comb-audit row 100 follow-up: an OpenAI Responses stream event or output item that is valid JSON but holds a member of an unexpected type. Pi reads each
// member with its own expression (openai-responses-shared.ts:463-800): `+=` appends a delta's String() form, an output_item.done joins its parts' String()
// forms or throws `item.content?.map is not a function`, `${item.call_id}|${item.id}` interpolates, finalizeResponse reads counts with `|| 0` and maps a status
// through an === switch. The rows run Pi's own stream over each body and compare the final message with PiG's.
func TestOpenAIResponsesMistypedMembersMatchPi(t *testing.T) {
	event := func(data string) string { return "data: " + data + "\n\n" }
	added := event(`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[]}}`)
	delta := func(value string) string {
		return event(`{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":` + value + `}`)
	}
	hel := added + delta(`"Hel"`)
	done := func(content string) string {
		return event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":` + content + `}}`)
	}
	completed := func(response string) string {
		return event(`{"type":"response.completed","response":{"id":"r","status":"completed",` + response + `}}`)
	}
	usage := `"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}`
	ok := completed(usage)
	toolAdded := event(`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc","call_id":"call_1","name":"read","arguments":""}}`)
	argsDelta := func(value string) string {
		return event(`{"type":"response.function_call_arguments.delta","item_id":"fc","output_index":0,"delta":` + value + `}`)
	}
	toolDone := func(item string) string {
		return event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc","name":"read",` + item + `}}`)
	}
	reasoning := event(`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs","summary":[]}}`)
	reasoningDone := func(item string) string {
		return event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs",` + item + `}}`)
	}
	type row struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	rows := []row{
		{"delta number", hel + delta(`5`) + ok},
		{"delta object", hel + delta(`{"a":1}`) + ok},
		{"delta null", hel + delta(`null`) + ok},
		{"delta missing", hel + event(`{"type":"response.output_text.delta","output_index":0}`) + ok},
		{"delta empty", hel + delta(`""`) + ok},
		{"done text number", hel + done(`[{"type":"output_text","text":5}]`) + ok},
		{"done part number", hel + done(`[5,{"type":"output_text","text":"x"}]`) + ok},
		{"done part null", hel + done(`[null]`) + ok},
		// outputSlots is a Map: an output_index is a key by SameValueZero, so "0" is not 0, and every event's object is its own key.
		{"output_index string", added + event(`{"type":"response.output_text.delta","output_index":"0","delta":"Hel"}`) + delta(`"lo"`) + ok},
		{"output_index string throughout", event(`{"type":"response.output_item.added","output_index":"0","item":{"type":"message","id":"m","content":[]}}`) +
			event(`{"type":"response.output_text.delta","output_index":"0","delta":"Hel"}`) + delta(`"x"`) +
			event(`{"type":"response.output_item.done","output_index":"0","item":{"type":"message","id":"m","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		{"output_index absent and 0", added + event(`{"type":"response.output_text.delta","delta":"Hel"}`) + delta(`"lo"`) + ok},
		{"output_index absent throughout", event(`{"type":"response.output_item.added","item":{"type":"message","id":"m","content":[]}}`) +
			event(`{"type":"response.output_text.delta","delta":"Hel"}`) + event(`{"type":"response.output_item.done","item":{"type":"message","id":"m","content":[{"type":"output_text","text":"Hel!"}]}}`) + ok},
		{"output_index null and absent", event(`{"type":"response.output_item.added","output_index":null,"item":{"type":"message","id":"m","content":[]}}`) +
			event(`{"type":"response.output_text.delta","output_index":null,"delta":"Hel"}`) + event(`{"type":"response.output_text.delta","delta":"x"}`) + ok},
		{"output_index object", event(`{"type":"response.output_item.added","output_index":{},"item":{"type":"message","id":"m","content":[]}}`) +
			event(`{"type":"response.output_text.delta","output_index":{},"delta":"Hel"}`) +
			event(`{"type":"response.output_item.done","output_index":{},"item":{"type":"message","id":"m","content":[{"type":"output_text","text":"done"}]}}`) + ok},
		{"output_index true", event(`{"type":"response.output_item.added","output_index":true,"item":{"type":"message","id":"m","content":[]}}`) +
			event(`{"type":"response.output_text.delta","output_index":true,"delta":"Hel"}`) + event(`{"type":"response.output_text.delta","output_index":false,"delta":"x"}`) + ok},
		{"output_index fraction and minus zero", event(`{"type":"response.output_item.added","output_index":-0,"item":{"type":"message","id":"m","content":[]}}`) +
			event(`{"type":"response.output_text.delta","output_index":0.0,"delta":"Hel"}`) + event(`{"type":"response.output_text.delta","output_index":0.5,"delta":"x"}`) +
			event(`{"type":"response.output_text.delta","output_index":1e400,"delta":"y"}`) + ok},
		{"output_index string with mistyped delta", added + event(`{"type":"response.output_text.delta","output_index":"0","delta":5}`) + delta(`"lo"`) + ok},
		{"done content number", hel + done(`5`) + ok},
		{"done content string", hel + done(`"x"`) + ok},
		{"done refusal number", hel + done(`[{"type":7,"refusal":8}]`) + ok},
		{"done item number", hel + event(`{"type":"response.output_item.done","output_index":0,"item":5}`) + ok},
		{"reasoning summary number", reasoning + reasoningDone(`"summary":5`) + ok},
		{"reasoning summary text number", reasoning + reasoningDone(`"summary":[{"type":"summary_text","text":5}]`) + ok},
		{"reasoning content object", reasoning + reasoningDone(`"summary":[],"content":{"a":1}`) + ok},
		{"tool call_id number", event(`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc","call_id":5,"name":"read","arguments":""}}`) +
			argsDelta(`"{}"`) + toolDone(`"call_id":5,"arguments":"{}"`) + ok},
		{"tool args delta number", toolAdded + argsDelta(`"{\"a\":"`) + argsDelta(`1`) + argsDelta(`"}"`) + toolDone(`"call_id":"call_1"`) + ok},
		{"tool done arguments number", toolAdded + argsDelta(`"{}"`) + toolDone(`"call_id":"call_1","arguments":5`) + ok},
		{"tool done arguments zero", toolAdded + argsDelta(`"{\"a\":1}"`) + toolDone(`"call_id":"call_1","arguments":0`) + ok},
		{"tool args done number", toolAdded + argsDelta(`"{}"`) + event(`{"type":"response.function_call_arguments.done","output_index":0,"arguments":5}`) + ok},
		{"tool args done false", toolAdded + argsDelta(`"{}"`) + event(`{"type":"response.function_call_arguments.done","output_index":0,"arguments":false}`) + ok},
		{"tool args done null", toolAdded + argsDelta(`"{}"`) + event(`{"type":"response.function_call_arguments.done","output_index":0,"arguments":null}`) + ok},
		{"tool args done missing", toolAdded + argsDelta(`"{}"`) + event(`{"type":"response.function_call_arguments.done","output_index":0}`) + ok},
		{"status number", hel + event(`{"type":"response.completed","response":{"id":"r","status":5,`+usage+`}}`)},
		{"status array", hel + event(`{"type":"response.completed","response":{"id":"r","status":["completed"],`+usage+`}}`)},
		// The JS read takes the record after integer-valued floats are rewritten (77790f284b): JSON.parse reads 3.0 and 1E0 as integers.
		{"integer float beside mistyped member", hel + event(`{"type":"response.completed","response":{"id":"r","status":5,"usage":{"input_tokens":3.0,"output_tokens":1E0,"total_tokens":4}}}`)},
		{"status zero", hel + event(`{"type":"response.completed","response":{"id":"r","status":0,`+usage+`}}`)},
		{"incomplete reason number", hel + event(`{"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":5},`+usage+`}}`)},
		{"usage number", hel + completed(`"usage":5`)},
		{"usage number after usage", hel + ok + completed(`"usage":5`)},
		{"usage input string", hel + completed(`"usage":{"input_tokens":"3","output_tokens":1,"total_tokens":4}`)},
		{"usage details number", hel + completed(`"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4,"input_tokens_details":5,"output_tokens_details":"x"}`)},
		{"usage cached false", hel + completed(`"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4,"input_tokens_details":{"cached_tokens":false}}`)},
		{"output number", hel + completed(`"output":5,`+usage)},
		{"output string", hel + completed(`"output":"ab",`+usage)},
		{"output element number", hel + completed(`"output":[5,{"type":"message","id":7}],`+usage)},
		{"response id zero", hel + event(`{"type":"response.completed","response":{"id":0,"status":"completed",`+usage+`}}`)},
		{"failed error number", hel + event(`{"type":"response.failed","response":{"status":"failed","error":{"code":5,"message":7}}}`)},
		{"failed error string", hel + event(`{"type":"response.failed","response":{"status":"failed","error":"x"}}`)},
		{"failed reason number", hel + event(`{"type":"response.failed","response":{"status":"failed","incomplete_details":{"reason":5}}}`)},
		{"event number", hel + event(`5`) + ok},
		{"event type number", hel + event(`{"type":5,"delta":7}`) + ok},
		// A message item's id is read only into the text signature, JSON.stringify({ v: 1, id, phase }) (:53-57, :703); `if (phase)` keeps any truthy phase.
		{"message id number", event(`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":5,"role":"assistant","content":[]}}`) + delta(`"Hel"`) +
			event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":5,"role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		{"message id null", event(`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":null,"role":"assistant","content":[]}}`) + delta(`"Hel"`) +
			event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":null,"role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		{"message id absent", event(`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`) + delta(`"Hel"`) +
			event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		{"message phase other", hel + event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m","phase":"analysis","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		{"message phase number", hel + event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m","phase":5,"role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		{"message phase final_answer", hel + event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m","phase":"final_answer","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		// A falsy phase is left out of the signature (`if (phase)`, and `?? undefined` for null), and a non-string id is JSON.stringify of the parsed value:
		// its whitespace is dropped, its member order kept and its escapes decoded.
		{"message phase empty", hel + event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m","phase":"","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		{"message phase false", hel + event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m","phase":false,"role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		{"message phase null", hel + event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m","phase":null,"role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		{"message id object spaced", event(`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":{ "b" : 2 , "a" : "\u0041" },"role":"assistant","content":[]}}`) + delta(`"Hel"`) +
			event(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":{ "b" : 2 , "a" : "\u0041" },"role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`) + ok},
		// createSlot and applyMessagePhaseStopReason read `item.type` first, so a missing or null item throws.
		{"added item absent", event(`{"type":"response.output_item.added","output_index":0}`) + delta(`"Hel"`) + ok},
		{"added item null", event(`{"type":"response.output_item.added","output_index":0,"item":null}`) + delta(`"Hel"`) + ok},
		{"done item null", hel + event(`{"type":"response.output_item.done","output_index":0,"item":null}`) + ok},
		{"unread member", hel + event(`{"type":"response.in_progress","response":{"id":5,"status":[]},"delta":5}`) + ok},
	}
	type result struct {
		Stop     string `json:"stop"`
		Error    string `json:"error"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		Tools    string `json:"tools"`
		ID       string `json:"id"`
		Usage    [5]int `json:"usage"`
		Blocks   int    `json:"blocks"`
		// Sigs is each text block's textSignature and each thinking block's thinkingSignature, in block order.
		Sigs string `json:"sigs"`
		// Transport is the cause of a failed request in Pi's run, empty when every request reached the server; PiG's run never has one, so a transport failure fails the row naming it.
		Transport string `json:"transport"`
	}
	var want []result
	const script = `(async () => {
const root = process.argv[1], rowsJSON = require("node:fs").readFileSync(0, "utf8");
const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
const { createServer } = require("node:http");
const api = await load("node_modules/@earendil-works/pi-ai/dist/api/openai-responses.js");
const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
const rows = JSON.parse(rowsJSON);
// One server answers every row, each on its own connection (connection: close): no listener is opened and closed per row and no keep-alive socket outlives its row.
const server = createServer((req, res) => { req.resume(); req.on("end", () => { res.writeHead(200, { "content-type": "text/event-stream", connection: "close" }); res.end(rows[Number(req.url.split("/")[2])].body); }); });
await new Promise((r) => server.listen(0, "127.0.0.1", r));
const out = [];
for (const [i, row] of rows.entries()) {
	// Pi reports a failed request as the SDK's "Connection error."; the fetch wrapper keeps the cause, so a transport failure names itself in the comparison.
	let transport = "";
	const fetchRecording = async (...args) => { try { return await fetch(...args); } catch (e) { transport = String(e?.cause?.code ?? e?.cause?.message ?? e?.message ?? e); throw e; } };
	const model = { id: "test", name: "t", api: "openai-responses", provider: "openai", baseUrl: "http://127.0.0.1:" + server.address().port + "/v1/" + i, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 };
	const r = await api.stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: "k", fetch: fetchRecording }).result();
	out.push({
		stop: r.stopReason,
		error: r.errorMessage ?? "",
		text: r.content.filter((b) => b.type === "text").map((b) => b.text).join(""),
		thinking: r.content.filter((b) => b.type === "thinking").map((b) => b.thinking).join(""),
		tools: JSON.stringify(r.content.filter((b) => b.type === "toolCall").map((b) => [b.id, b.name, b.arguments])),
		id: r.responseId === undefined ? "" : String(r.responseId),
		usage: [r.usage.input, r.usage.output, r.usage.cacheRead, r.usage.cacheWrite, r.usage.totalTokens],
		blocks: r.content.length,
		sigs: JSON.stringify(r.content.filter((b) => b.type === "text" || b.type === "thinking").map((b) => (b.type === "text" ? b.textSignature : b.thinkingSignature) ?? null)),
		transport,
	});
}
// Exit only after the pipe has taken the whole line: process.exit drops pending stdout writes.
process.stdout.write(JSON.stringify(out) + "\n", () => process.exit(0));
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
			message, _ := runOpenAIResponsesEvents(t, test.Body)
			var text, thinking strings.Builder
			var tools []any
			sigs := []any{}
			for _, block := range message.Content {
				switch block := block.(type) {
				case TextContent:
					text.WriteString(block.Text)
					sigs = append(sigs, optionalSignature(block.TextSignature))
				case ThinkingContent:
					thinking.WriteString(block.Thinking)
					sigs = append(sigs, optionalSignature(block.ThinkingSignature))
				case ToolCall:
					tools = append(tools, []any{block.ID, block.Name, block.Arguments})
				}
			}
			encoded, _ := json.Marshal(tools)
			encodedSigs, _ := json.Marshal(sigs)
			usage := message.Usage
			got := result{Blocks: len(message.Content), Stop: string(message.StopReason), Error: message.ErrorMessage, Text: text.String(), Thinking: thinking.String(), Tools: string(encoded),
				Sigs: string(encodedSigs), ID: message.ResponseID, Usage: [5]int{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.TotalTokens}}
			if want[i].Tools == "" || want[i].Tools == "[]" {
				want[i].Tools = "null"
			}
			if got != want[i] {
				t.Fatalf("PiG %+v\n Pi %+v", got, want[i])
			}
		})
	}
}

// optionalSignature is a block signature as Pi's `?? null` reads it: an absent signature is null.
func optionalSignature(signature string) any {
	if signature == "" {
		return nil
	}
	return signature
}

// An output item whose mistyped member Pi stores raw (a reasoning id, a function_call name, a custom tool call's members) cannot be reproduced in PiG's typed
// item, so it ends the turn with the decode error. It used to be skipped, and the stream then finished as a success without the item's block.
func TestOpenAIResponsesItemPiStoresRawEndsTheTurnWithTheDecodeError(t *testing.T) {
	event := func(data string) string { return "data: " + data + "\n\n" }
	completed := event(`{"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`)
	for name, item := range map[string]string{
		"reasoning id number":           `{"type":"reasoning","id":5,"summary":[]}`,
		"function_call name number":     `{"type":"function_call","id":"fc","call_id":"c","name":5,"arguments":""}`,
		"custom_tool_call input number": `{"type":"custom_tool_call","id":"ct","call_id":"c","name":"x","input":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := event(`{"type":"response.output_item.added","output_index":0,"item":`+item+`}`) + completed
			message, _ := runOpenAIResponsesEvents(t, body)
			if message.StopReason != StopReasonError || !strings.Contains(message.ErrorMessage, "openai-responses: invalid SSE JSON") {
				t.Fatalf("stop %q error %q; want the decode error", message.StopReason, message.ErrorMessage)
			}
		})
	}
}
