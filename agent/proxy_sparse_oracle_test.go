package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type proxyOracleOutcome struct {
	Types      []string        `json:"types"`
	Reason     string          `json:"reason"`
	Error      string          `json:"error"`
	Content    json.RawMessage `json:"content"`
	Usage      json.RawMessage `json:"usage"`
	DoneAfter  string          `json:"doneAfter"`
	ResultKind string          `json:"resultKind"`
}

// proxyLines turns events and raw lines into a response body.
func proxyBody(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func proxyOracleScripts() map[string]string {
	text := func(i int) string { return `data: {"type":"text_start","contentIndex":` + itoa(i) + `}` }
	delta := func(i int, d string) string {
		return `data: {"type":"text_delta","contentIndex":` + itoa(i) + `,"delta":"` + d + `"}`
	}
	usage := `"usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	done := `data: {"type":"done","reason":"stop",` + usage + `}`
	return map[string]string{
		"plain text":            proxyBody(`data: {"type":"start"}`, text(0), delta(0, "hi"), `data: {"type":"text_end","contentIndex":0,"contentSignature":"s"}`, done),
		"sparse text start":     proxyBody(`data: {"type":"start"}`, text(3), delta(3, "late"), done),
		"sparse then fill":      proxyBody(text(2), delta(2, "c"), text(0), delta(0, "a"), done),
		"sparse thinking":       proxyBody(`data: {"type":"thinking_start","contentIndex":2}`, `data: {"type":"thinking_delta","contentIndex":2,"delta":"t"}`, done),
		"sparse tool call":      proxyBody(`data: {"type":"toolcall_start","contentIndex":4,"id":"c1","toolName":"read"}`, `data: {"type":"toolcall_delta","contentIndex":4,"delta":"{\"a\":1}"}`, done),
		"delta past end":        proxyBody(text(0), delta(5, "x"), done),
		"delta on hole":         proxyBody(text(2), delta(1, "x"), done),
		"end past end":          proxyBody(`data: {"type":"text_end","contentIndex":0}`, done),
		"toolcall end past end": proxyBody(`data: {"type":"toolcall_end","contentIndex":3}`, done),
		"toolcall end on hole":  proxyBody(text(2), `data: {"type":"toolcall_end","contentIndex":0}`, done),
		"delta after done":      proxyBody(text(0), delta(0, "a"), done, delta(0, "b")),
		"start after done":      proxyBody(text(0), delta(0, "a"), done, text(1), delta(1, "z")),
		"text end after done":   proxyBody(text(0), delta(0, "a"), done, `data: {"type":"text_end","contentIndex":0,"contentSignature":"late"}`),
		"second done":           proxyBody(text(0), delta(0, "a"), done, `data: {"type":"done","reason":"length",`+usage+`}`),
		"error then delta":      proxyBody(text(0), delta(0, "a"), `data: {"type":"error","reason":"error","errorMessage":"bad",`+usage+`}`, delta(0, "b")),
		"malformed after done":  proxyBody(text(0), done, `data: {not json`),
		"bad delta after done":  proxyBody(text(0), done, delta(4, "x")),
		"no terminal":           proxyBody(text(0), delta(0, "a")),
		"unknown event":         proxyBody(`data: {"type":"mystery"}`, text(0), done),
		"negative index":        proxyBody(`data: {"type":"text_start","contentIndex":-1}`, done),
		"huge index":            proxyBody(`data: {"type":"text_start","contentIndex":2000000}`, done),
		"fractional index":      proxyBody(`data: {"type":"text_start","contentIndex":1.5}`, done),
		"non-canonical string":  proxyBody(`data: {"type":"text_start","contentIndex":"01"}`, done),
		"past the last index":   proxyBody(`data: {"type":"text_start","contentIndex":4294967295}`, done),
		// Reads at a key that is no array index find no block, so Pi throws its own message or, for toolcall_end, skips the event.
		"delta without index":     proxyBody(text(0), `data: {"type":"text_delta","delta":"x"}`, done),
		"text end null index":     proxyBody(text(0), delta(0, "a"), `data: {"type":"text_end","contentIndex":null}`, done),
		"thinking delta negative": proxyBody(`data: {"type":"thinking_start","contentIndex":0}`, `data: {"type":"thinking_delta","contentIndex":-1,"delta":"t"}`, done),
		"thinking end fractional": proxyBody(`data: {"type":"thinking_start","contentIndex":0}`, `data: {"type":"thinking_end","contentIndex":0.5}`, done),
		"toolcall delta string":   proxyBody(`data: {"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"read"}`, `data: {"type":"toolcall_delta","contentIndex":"x","delta":"{}"}`, done),
		"toolcall end no index":   proxyBody(`data: {"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"read"}`, `data: {"type":"toolcall_end","toolCall":{"type":"toolCall","id":"c2","name":"write","arguments":{"a":1}}}`, done),
		"delta non-canonical":     proxyBody(text(0), `data: {"type":"text_delta","contentIndex":"00","delta":"a"}`, done),
		"delta at last index":     proxyBody(text(0), `data: {"type":"text_delta","contentIndex":4294967294,"delta":"a"}`, done),
		"delta past last index":   proxyBody(text(0), `data: {"type":"text_delta","contentIndex":4294967295,"delta":"a"}`, done),
		// An unfinished tool call keeps its partialJson member in the delivered message.
		"drop during tool call":  proxyBody(`data: {"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"read"}`, `data: {"type":"toolcall_delta","contentIndex":0,"delta":"{\"path\":\"a"}`),
		"error during tool call": proxyBody(`data: {"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"read"}`, `data: {"type":"toolcall_delta","contentIndex":0,"delta":"{\"p\":1}"}`, `data: {"type":"error","reason":"error","errorMessage":"upstream failed",`+usage+`}`),
		"done before tool end":   proxyBody(`data: {"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"read"}`, done),
		"tool end drops partial": proxyBody(`data: {"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"read"}`, `data: {"type":"toolcall_delta","contentIndex":0,"delta":"{\"p\":1}"}`, `data: {"type":"toolcall_end","contentIndex":0,"toolCall":{"type":"toolCall","id":"c1","name":"read","arguments":{"p":1},"partialJson":"x"}}`, done),
		"tool end without call":  proxyBody(`data: {"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"read"}`, `data: {"type":"toolcall_delta","contentIndex":0,"delta":"{\"p\":1}"}`, `data: {"type":"toolcall_end","contentIndex":0}`, done),
		// Keys that are array indices in another spelling: canonical numeric strings, -0, exponent and fraction forms of integers.
		"string indices": proxyBody(`data: {"type":"text_start","contentIndex":"0"}`, `data: {"type":"text_delta","contentIndex":"0","delta":"a"}`, `data: {"type":"text_end","contentIndex":0,"contentSignature":"s"}`,
			`data: {"type":"toolcall_start","contentIndex":"1","id":"c1","toolName":"read"}`, `data: {"type":"toolcall_delta","contentIndex":1,"delta":"{\"p\":2}"}`, `data: {"type":"toolcall_end","contentIndex":"1","toolCall":{"type":"toolCall","id":"c1","name":"read","arguments":{"p":3}}}`, done),
		"number spellings": proxyBody(`data: {"type":"thinking_start","contentIndex":-0}`, `data: {"type":"thinking_delta","contentIndex":0e3,"delta":"t"}`, `data: {"type":"thinking_end","contentIndex":0.0,"contentSignature":"g"}`,
			`data: {"type":"text_start","contentIndex":1E0}`, `data: {"type":"text_delta","contentIndex":10e-1,"delta":"b"}`, done),
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// proxyLateScripts continue after the terminal event. Pi keeps applying those events to the object it delivered; Pig delivers the message as it was at the terminal event, because the stream's consumers read it concurrently (a numbered divergence is proposed in docs/parity/gap-closure/gap-core.md).
var proxyLateScripts = []string{"delta after done", "start after done", "text end after done", "second done", "error then delta", "malformed after done", "bad delta after done"}

// proxySparseScripts index the content array past its end, or with an index that is not an array index. Pi builds a sparse array or sets a property JSON never shows; Pig ends the stream with an error (also proposed).
var proxySparseScripts = []string{"sparse text start", "sparse then fill", "sparse thinking", "sparse tool call", "delta on hole", "toolcall end on hole", "negative index", "fractional index", "huge index", "non-canonical string", "past the last index"}

func proxyTerminalPrefix(body string) string {
	lines := strings.SplitAfter(body, "\n")
	for i, line := range lines {
		if strings.Contains(line, `"type":"done"`) || strings.Contains(line, `"type":"error"`) {
			return strings.Join(lines[:i+1], "")
		}
	}
	return body
}

// runPiProxy streams each body through Pi's streamProxy and returns what the consumer holds once the body has been read.
func runPiProxy(t *testing.T, bodies []string) []proxyOracleOutcome {
	t.Helper()
	var out []proxyOracleOutcome
	pioracle.Run(t, `
const http = (await import("node:http")).default;
const { streamProxy } = await load("pi-agent-core/proxy.js");
const results = [];
for (const body of input) {
  const server = http.createServer((req, res) => { req.resume(); res.writeHead(200, { "content-type": "text/event-stream" }); res.end(body); });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const model = { id: "m", api: "openai-responses", provider: "openai" };
  const stream = streamProxy(model, { messages: [] }, { authToken: "t", proxyUrl: "http://127.0.0.1:" + server.address().port });
  const types = [];
  for await (const event of stream) types.push(event.type);
  const message = await stream.result();
  // Late events change the delivered message after the stream ended; give them time to land.
  await new Promise((resolve) => setTimeout(resolve, 50));
  results.push({ types, reason: message.stopReason, error: message.errorMessage ?? "", content: JSON.stringify(message.content), usage: JSON.stringify(message.usage) });
  server.close();
}
emit(results);`, bodies, &out)
	return out
}

// TestStreamProxyMatchesPi runs Pi's streamProxy and StreamProxy against the same response bodies and compares the event types, the stop reason and error message, the content and the usage.
func TestStreamProxyMatchesPi(t *testing.T) {
	scripts := proxyOracleScripts()
	skip := map[string]bool{}
	for _, name := range append(slices.Clone(proxyLateScripts), proxySparseScripts...) {
		skip[name] = true
	}
	var names, bodies []string
	for name, body := range scripts {
		if !skip[name] {
			names, bodies = append(names, name), append(bodies, body)
		}
	}
	want := runPiProxy(t, bodies)
	for i, name := range names {
		got := runProxyOracleGo(t, bodies[i])
		if !reflect.DeepEqual(canonProxy(got), canonProxy(want[i])) {
			t.Errorf("%s:\n got  %v\n want %v", name, canonProxy(got), canonProxy(want[i]))
		}
	}
}

// TestStreamProxyIgnoresEventsAfterTheTerminalEvent: the message Pig delivers is the one Pi had at the terminal event (Pi's later mutation of the delivered object is the proposed divergence).
func TestStreamProxyIgnoresEventsAfterTheTerminalEvent(t *testing.T) {
	scripts := proxyOracleScripts()
	var bodies []string
	for _, name := range proxyLateScripts {
		bodies = append(bodies, proxyTerminalPrefix(scripts[name]))
	}
	want := runPiProxy(t, bodies)
	for i, name := range proxyLateScripts {
		full := scripts[name]
		if got := runProxyOracleGo(t, full); !reflect.DeepEqual(canonProxy(got), canonProxy(want[i])) {
			t.Errorf("%s:\n got  %v\n want %v", name, canonProxy(got), canonProxy(want[i]))
		}
		// Pi's message differs once the late events are applied, so the scripts do exercise the divergence.
		if piFull := runPiProxy(t, []string{full})[0]; reflect.DeepEqual(canonProxy(piFull), canonProxy(want[i])) {
			t.Errorf("%s: Pi's late events changed nothing, so the script does not exercise the divergence", name)
		}
	}
}

// TestStreamProxyFailsOnContentIndicesPiLeavesSparse: Pi writes a sparse array; Pig ends the stream with an error and keeps the blocks stored before the bad event.
func TestStreamProxyFailsOnContentIndicesPiLeavesSparse(t *testing.T) {
	scripts := proxyOracleScripts()
	var bodies []string
	for _, name := range proxySparseScripts {
		bodies = append(bodies, scripts[name])
	}
	pi := runPiProxy(t, bodies)
	for i, name := range proxySparseScripts {
		if pi[i].Reason == "error" && !strings.Contains(name, "hole") {
			t.Errorf("%s: Pi fails too (%s), so the script does not exercise the divergence", name, pi[i].Error)
		}
		got := runProxyOracleGo(t, bodies[i])
		if got.Reason != "error" || got.Error == "" || got.Types[len(got.Types)-1] != "error" {
			t.Errorf("%s: Pig outcome %v, want an error stream", name, canonProxy(got))
		}
		if strings.Contains(string(got.Content), "null") {
			t.Errorf("%s: Pig's content holds a hole: %s", name, got.Content)
		}
	}
}

// canonProxy decodes the JSON members so key order and spacing do not matter.
func canonProxy(o proxyOracleOutcome) map[string]any {
	decode := func(raw json.RawMessage) any {
		var text string
		if json.Unmarshal(raw, &text) == nil && text != "" {
			raw = json.RawMessage(text)
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		if members, ok := v.(map[string]any); ok {
			delete(members, "timestamp")
		}
		return v
	}
	return map[string]any{"types": o.Types, "reason": o.Reason, "error": o.Error, "content": decode(o.Content), "usage": decode(o.Usage)}
}

// runProxyOracleGo streams a body through StreamProxy and reads the delivered message once the stream has ended. It reads it once: a later change would be the late mutation Pig does not make, and the race detector reports one.
func runProxyOracleGo(t *testing.T, body string) proxyOracleOutcome {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	stream := StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{AuthToken: "t", ProxyURL: server.URL})
	events, result := collectProxyEvents(t, stream)
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = string(event.EventType())
	}
	content, _ := json.Marshal(result.Content)
	usage, _ := json.Marshal(result.Usage)
	return proxyOracleOutcome{Types: types, Reason: string(result.StopReason), Error: result.ErrorMessage, Content: content, Usage: usage}
}
