package ai

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Pi keeps the Infinity JSON.parse reads for 1e999 (and the -Infinity, NaN and Infinity that partial-json reads) in a tool call's arguments, and every
// serialization, JSON.stringify, writes null. A tool call in memory may therefore hold a non-finite number: the streamed text parses to one, and a Go caller
// may place one. Publishing, observing, cloning, validating and writing such a call never fails.

func nonFiniteArguments() JsonObject {
	return JsonObject{"n": math.Inf(1), "a": []any{math.Inf(-1), math.NaN(), map[string]any{"b": float32(math.Inf(1))}}, "z": math.Copysign(0, -1), "s": "x"}
}

const nonFiniteArgumentsJSON = `{"a":[null,null,{"b":null}],"n":null,"s":"x","z":0}`

func TestNonFiniteArgumentsAreValidCloneAndSerializeAsStringify(t *testing.T) {
	arguments := nonFiniteArguments()
	if err := validateArgumentsValue(map[string]any(arguments)); err != nil {
		t.Fatalf("validateArgumentsValue rejected a non-finite number: %v", err)
	}
	copied := cloneArgumentsValue(map[string]any(arguments)).(map[string]any)
	if got := copied["n"].(float64); !math.IsInf(got, 1) {
		t.Fatalf("clone changed +Inf to %v", got)
	}
	items := copied["a"].([]any)
	if !math.IsInf(items[0].(float64), -1) || !math.IsNaN(items[1].(float64)) || !math.IsInf(float64(items[2].(map[string]any)["b"].(float32)), 1) {
		t.Fatalf("clone changed the nested numbers: %#v", items)
	}
	arguments["n"] = 1.0
	if !math.IsInf(copied["n"].(float64), 1) {
		t.Fatal("the clone shares the source map")
	}

	call := ToolCall{ID: "c", Name: "t", Arguments: nonFiniteArguments()}
	if got, err := call.ArgumentsJSON(); err != nil || string(got) != nonFiniteArgumentsJSON {
		t.Fatalf("ArgumentsJSON = %s, %v; want %s", got, err, nonFiniteArgumentsJSON)
	}
	wantCall := `{"type":"toolCall","id":"c","name":"t","arguments":` + nonFiniteArgumentsJSON + `}`
	if got, err := json.Marshal(call); err != nil || string(got) != wantCall {
		t.Fatalf("json.Marshal(ToolCall) = %s, %v; want %s", got, err, wantCall)
	}
	ordered := ToolCall{ID: "c", Name: "t", Arguments: nonFiniteArguments(), argumentOrder: schemaObjectOrder{"": {"s", "n", "a", "z"}}}
	if got, err := json.Marshal(ordered); err != nil || !strings.Contains(string(got), `"arguments":{"s":"x","n":null,"a":[null,null,{"b":null}],"z":0}`) {
		t.Fatalf("ordered json.Marshal(ToolCall) = %s, %v", got, err)
	}
	message := AssistantMessage{Content: []AssistantContentBlock{call}}
	if got, err := json.Marshal(message); err != nil || !strings.Contains(string(got), `"arguments":`+nonFiniteArgumentsJSON) {
		t.Fatalf("json.Marshal(AssistantMessage) = %s, %v", got, err)
	}
	if got := SafeJsonStringify(map[string]any(nonFiniteArguments())); got != nonFiniteArgumentsJSON {
		t.Fatalf("SafeJsonStringify = %s, want %s", got, nonFiniteArgumentsJSON)
	}
	if members, err := call.ArgumentMembers(); err != nil || members[0].Key != "a" || members[0].JSON != `[null,null,{"b":null}]` {
		t.Fatalf("ArgumentMembers = %v, %v", members, err)
	}
}

// A cell publishes, views and observes a message whose tool call holds a non-finite number: this panicked with "clone validated JSON value".
func TestCellPublishesNonFiniteArguments(t *testing.T) {
	message := &AssistantMessage{Model: "m", Content: []AssistantContentBlock{ToolCall{ID: "c", Name: "t", Arguments: nonFiniteArguments()}}}
	cell := newAssistantMessageCell(message)
	cell.publish(message, assistantMessageReplacements{Content: true})
	view := cell.view()
	for name, observed := range map[string]*AssistantMessage{"Observe": view.Observe(), "ShallowCopy": view.ShallowCopy(), "delivered": view.delivered(), "unpublished": message.Observe()} {
		call := observed.Content[0].(ToolCall)
		if !math.IsInf(call.Arguments["n"].(float64), 1) {
			t.Errorf("%s: n = %v, want +Inf as Pi keeps it in memory", name, call.Arguments["n"])
		}
		if got, err := call.ArgumentsJSON(); err != nil || string(got) != nonFiniteArgumentsJSON {
			t.Errorf("%s: ArgumentsJSON = %s, %v", name, got, err)
		}
	}
}

// streamPieces streams one tool call's argument text, piece by piece, through the provider stream builder and reads every published view, as the stream's
// consumers do. It returns the stringified arguments of each delta's view and of the final message.
func streamPieces(t testing.TB, pieces []string) (partials []string, final string) {
	t.Helper()
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "test", "model")
	runManagedBuilder(builder, func() {
		builder.start()
		builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call_0", name: "tool"})
		for _, piece := range pieces {
			builder.toolCallDelta(streamToolCallDelta{index: 0, argumentsDelta: piece})
			view := builder.publish()
			for _, observed := range []*AssistantMessage{view.Observe(), view.delivered(), view.ShallowCopy()} {
				stringifyToolArguments(t, observed)
			}
			partials = append(partials, stringifyToolArguments(t, view.Observe()))
		}
		builder.endToolCall(0)
		builder.done(StopReasonToolUse, &Usage{}, "")
	})
	return partials, stringifyToolArguments(t, builder.stream.Result())
}

func stringifyToolArguments(t testing.TB, message *AssistantMessage) string {
	t.Helper()
	call := message.Content[0].(ToolCall)
	encoded, err := call.ArgumentsJSON()
	if err != nil {
		t.Fatalf("ArgumentsJSON: %v", err)
	}
	if whole, err := json.Marshal(message); err != nil || !json.Valid(whole) {
		t.Fatalf("json.Marshal(message) = %s, %v", whole, err)
	}
	return string(encoded)
}

// A streamed argument text whose number is out of float64 range reaches the cell as a non-finite number; the turn goes on and writes null.
func TestStreamedNonFiniteArgumentsDoNotPanic(t *testing.T) {
	for _, test := range []struct {
		pieces   []string
		partials []string
		final    string
	}{
		{[]string{`{"n":1e999,`, `"m":2}`}, []string{`{"n":null}`, `{"n":null,"m":2}`}, `{"n":null,"m":2}`},
		{[]string{`{"n":-1e999,"a":[1e999,{"b":1e999}]}`}, []string{`{"n":null,"a":[null,{"b":null}]}`}, `{"n":null,"a":[null,{"b":null}]}`},
		{[]string{`{"n":1e9`, `99,"x":1}`}, []string{`{"n":1000000000}`, `{"n":null,"x":1}`}, `{"n":null,"x":1}`},
		{[]string{`{"n":-0,"u":1e-999}`}, []string{`{"n":0,"u":0}`}, `{"n":0,"u":0}`},
	} {
		partials, final := streamPieces(t, test.pieces)
		if !reflect.DeepEqual(partials, test.partials) || final != test.final {
			t.Errorf("%q: partials %q final %q, want %q %q", test.pieces, partials, final, test.partials, test.final)
		}
	}
}

// numberText is what JSON.stringify writes for the JSON number literal.
func numberText(t testing.TB, literal string) string {
	t.Helper()
	value, err := strconv.ParseFloat(literal, 64)
	if err != nil && !math.IsInf(value, 0) {
		t.Fatalf("ParseFloat(%q): %v", literal, err)
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return "null"
	}
	return jsnumber.String(value)
}

var numberLiteralSeeds = []string{
	"0", "-0", "0.0", "-0.0", "1", "-1", "1e999", "-1e999", "1E999", "1e+999", "1e308", "1.7976931348623157e308", "1.7976931348623159e308", "-1.7976931348623159e308",
	"1e309", "123456789e999999", "1e99999999999999999999", "0e999", "1e-999", "-1e-999", "5e-324", "2e-324", "1e-400", "4.9e-324", "0.000001", "1e21", "1e-7", "100000000000000000000000",
}

// FuzzToolArgumentNumberLiterals streams `{"n":L,...}` split anywhere: no stage panics, every serialization succeeds, and the final arguments hold what JSON.stringify writes for L.
func FuzzToolArgumentNumberLiterals(f *testing.F) {
	for _, literal := range numberLiteralSeeds {
		f.Add(literal, uint8(0))
		f.Add(literal, uint8(7))
	}
	f.Fuzz(func(t *testing.T, literal string, split uint8) {
		if !json.Valid([]byte(literal)) || strings.ContainsAny(literal, " \t\r\n\"[]{},:") || literal == "null" || literal == "true" || literal == "false" {
			t.Skip()
		}
		text := `{"n":` + literal + `,"a":[` + literal + `,{"b":` + literal + `}]}`
		cut := int(split) % (len(text) + 1)
		partials, final := streamPieces(t, []string{text[:cut], text[cut:]})
		want := numberText(t, literal)
		if wantFinal := `{"n":` + want + `,"a":[` + want + `,{"b":` + want + `}]}`; final != wantFinal {
			t.Fatalf("%q split %d: final %s, want %s", literal, cut, final, wantFinal)
		}
		for _, partial := range partials {
			if !json.Valid([]byte(partial)) {
				t.Fatalf("%q split %d: partial %q is not JSON", literal, cut, partial)
			}
		}
		for _, parsed := range []string{text[:cut], text} {
			var call ToolCall
			call.SetStreamingArguments(parsed)
			if _, err := call.ArgumentsJSON(); err != nil {
				t.Fatalf("SetStreamingArguments(%q): %v", parsed, err)
			}
		}
		var call ToolCall
		call.SetFinalArguments(text)
		if got, err := call.ArgumentsJSON(); err != nil || string(got) != final {
			t.Fatalf("SetFinalArguments(%q) = %s, %v; want %s", text, got, err, final)
		}
	})
}

// The streamed arguments of an Anthropic tool_use block, and the stringified value of every delta's partial message, equal what Pi's own stream gives
// (anthropic-messages.js over the same SSE body, node). Pi holds Infinity in memory; JSON.stringify writes null.
func TestStreamedNonFiniteArgumentsMatchPi(t *testing.T) {
	type row struct {
		Start  string   `json:"start"`
		Pieces []string `json:"pieces"`
	}
	pieceRows := [][]string{
		{`{"n":1e999,`, `"m":2}`}, {`{"n":1e999`}, {`{"n":-1e999,"a":[1e999,{"b":1e999}]}`}, {`{"n":Infinity,"k":NaN}`}, {`{"n":-Infinity}`}, {`{"n":-0,"u":1e-999}`},
		{`{"n":1e`}, {`{"n":1e9`, `99,"x":1}`}, {`{"a":[1e999`}, {`{"a":[1e999,`, `2]`}, {`{"a":{"b":1e999`}, {`{"n":-1e9`, `99}`}, {`{"n":-`}, {`{"n":1E+999,"m":1.5e308}`},
		{`{"n":1.7976931348623159e308}`}, {`{"n":123456789e999999,"q":-0.0}`}, {`{"n":0e999,"p":5e-324,"r":2e-324}`}, {`{"n":1e99999999999999999999}`}, {`[1e999]`}, {`1e999`}, {`{"n":1e999} tail`},
	}
	var rows []row
	for _, pieces := range pieceRows {
		rows = append(rows, row{Start: `{}`, Pieces: pieces})
	}
	// (Members of the seed are written in sorted order: a Go map keeps no member order.) content_block_start's input seeds the call's arguments (anthropic-messages.ts:659-674); the streamed text replaces it at content_block_stop. JSON.parse reads 1e999 in it as an infinity, and the turn goes on.
	rows = append(rows,
		row{Start: `{"n":1e999}`, Pieces: []string{`{"n":1}`}},
		row{Start: `{"a":[1e999,{"b":1e999}],"n":-1e999}`, Pieces: []string{}},
		row{Start: `{"k":"v","n":1e999}`, Pieces: []string{`{"n":1e9`, `99}`}},
		row{Start: `null`, Pieces: []string{`{"q":2}`}},
	)
	encodedRows, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
const root = process.argv[1], rows = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
const { createServer } = require("node:http");
const api = await load("node_modules/@earendil-works/pi-ai/dist/api/anthropic-messages.js");
const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
const ev = (k, d) => "event: " + k + "\ndata: " + JSON.stringify(d) + "\n\n";
const out = [];
for (const { start, pieces } of rows) {
	let body = ev("message_start", { type: "message_start", message: { id: "m", type: "message", role: "assistant", content: [], model: "model", stop_reason: null, usage: { input_tokens: 5, output_tokens: 1 } } });
	body += "event: content_block_start\ndata: " + '{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"tool","input":' + start + "}}" + "\n\n";
	for (const p of pieces) body += ev("content_block_delta", { type: "content_block_delta", index: 0, delta: { type: "input_json_delta", partial_json: p } });
	body += ev("content_block_stop", { type: "content_block_stop", index: 0 });
	body += ev("message_delta", { type: "message_delta", delta: { stop_reason: "tool_use" }, usage: { output_tokens: 2 } });
	body += ev("message_stop", { type: "message_stop" });
	const server = createServer((req, res) => { req.resume(); req.on("end", () => { res.writeHead(200, { "content-type": "text/event-stream" }); res.end(body); }); });
	await new Promise((r) => server.listen(0, "127.0.0.1", r));
	const model = { id: "model", name: "c", api: "anthropic-messages", provider: "anthropic", baseUrl: "http://127.0.0.1:" + server.address().port, reasoning: true, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 1000 };
	const s = api.stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: "k" });
	const partials = [];
	let started = null;
	for await (const e of s) {
		if (e.type === "toolcall_start") started = JSON.stringify(e.partial.content[0].arguments);
		if (e.type === "toolcall_delta") partials.push(JSON.stringify(e.partial.content[0].arguments));
	}
	const r = await s.result();
	out.push({ started, partials, final: JSON.stringify(r.content[0]?.arguments), stop: r.stopReason });
	server.close();
}
console.log(JSON.stringify(out));
process.exit(0);
})()`
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t))
	cmd.Stdin = strings.NewReader(string(encodedRows))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	var want []struct {
		Started  string   `json:"started"`
		Partials []string `json:"partials"`
		Final    string   `json:"final"`
		Stop     string   `json:"stop"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("undecodable Pi output %q: %v", output, err)
	}
	for i, row := range rows {
		pieces := row.Pieces
		t.Run(fmt.Sprintf("%s %q", row.Start, pieces), func(t *testing.T) {
			var body strings.Builder
			event := func(kind, data string) { body.WriteString("event: " + kind + "\ndata: " + data + "\n\n") }
			event("message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"model":"model","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}`)
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"tool","input":`+row.Start+`}}`)
			for _, piece := range pieces {
				encoded, _ := json.Marshal(piece)
				event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":`+string(encoded)+`}}`)
			}
			event("content_block_stop", `{"type":"content_block_stop","index":0}`)
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":2}}`)
			event("message_stop", `{"type":"message_stop"}`)
			provider := &anthropicProvider{cfg: AnthropicConfig{ProviderID: "anthropic", Model: "model"}}
			builder := newAssistantStreamBuilder(t.Context(), APIAnthropicMessages, "anthropic", "model")
			go provider.parseAnthropicSSE(t.Context(), strings.NewReader(body.String()), builder, anthropicStreamNames{})
			var partials []string
			var started string
			for event := range builder.stream.Events(t.Context()) {
				if start, ok := event.(ToolCallStartEvent); ok {
					started = string(mustArgumentsJSON(t, start.Partial.Content[0].(ToolCall)))
				}
				if delta, ok := event.(ToolCallDeltaEvent); ok {
					partials = append(partials, string(mustArgumentsJSON(t, delta.Partial.Content[0].(ToolCall))))
				}
			}
			message := builder.stream.Result()
			if string(message.StopReason) != want[i].Stop {
				t.Fatalf("stop %s, Pi %s", message.StopReason, want[i].Stop)
			}
			if started != want[i].Started {
				t.Errorf("toolcall_start arguments %s, Pi %s", started, want[i].Started)
			}
			if len(partials) != len(want[i].Partials) || len(partials) > 0 && !reflect.DeepEqual(partials, want[i].Partials) {
				t.Errorf("partials %q, Pi %q", partials, want[i].Partials)
			}
			if got := string(mustArgumentsJSON(t, message.Content[0].(ToolCall))); got != want[i].Final {
				t.Errorf("final %s, Pi %s", got, want[i].Final)
			}
		})
	}
}

func mustArgumentsJSON(t testing.TB, call ToolCall) []byte {
	t.Helper()
	encoded, err := call.ArgumentsJSON()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// A non-finite number does not make a faulty tree valid: a cycle or a value with no JSON form is still rejected, and the check for a non-finite number does not follow a cycle.
func TestNonFiniteArgumentsDoNotExcuseOtherFaults(t *testing.T) {
	cycle := map[string]any{"n": math.Inf(1)}
	cycle["self"] = cycle
	if err := validateArgumentsValue(cycle); err == nil {
		t.Error("a cyclic tree was accepted")
	}
	if err := validateArgumentsValue(map[string]any{"n": math.Inf(1), "c": make(chan int)}); err == nil {
		t.Error("a channel was accepted")
	}
	for name, value := range map[string]any{"cycle": cycle, "channel": map[string]any{"n": math.NaN(), "c": make(chan int)}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: cloneArgumentsValue returned a copy", name)
				}
			}()
			cloneArgumentsValue(value)
		}()
	}
	if err := validateJsonValue(map[string]any{"n": math.Inf(1)}); err == nil {
		t.Error("validateJsonValue (tool result details, tool schemas) accepted a non-finite number")
	}
}
