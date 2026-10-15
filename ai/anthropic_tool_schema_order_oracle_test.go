package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
)

// JSON.stringify writes each Anthropic tool in the order convertTools creates its members: name, description, eager_input_streaming,
// strict, input_schema, cache_control (anthropic-messages.ts:1602-1609). The legacy input_schema is the literal { type, properties, required }
// with the tool's own properties object inside it; a strict input_schema spreads the strict parameters and then that literal, so the
// literal's members overwrite in place or append (:1589-1600). Pi's own buildParams writes the oracle tools through onPayload; PiG's are the
// tools member of the HTTP request body.
func TestAnthropicToolsFollowPiMemberOrder(t *testing.T) {
	type toolOrderCase struct {
		Name    string          `json:"name"`
		Compat  json.RawMessage `json:"compat"`
		Options json.RawMessage `json:"options"`
		Schema  json.RawMessage `json:"schema"`
		Strict  bool            `json:"strict"`
	}
	cases := []toolOrderCase{
		{Name: "legacy schema keeps the properties' declaration order", Compat: json.RawMessage(`{}`), Options: json.RawMessage(`{}`),
			Schema: json.RawMessage(`{"properties":{"path":{"type":"string","description":"File"},"offset":{"type":"number"}},"required":["path"],"type":"object"}`)},
		{Name: "legacy schema without required", Compat: json.RawMessage(`{"supportsEagerToolInputStreaming":false}`), Options: json.RawMessage(`{"cacheRetention":"none"}`),
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"number"}}}`)},
		{Name: "strict request that falls back to the legacy schema", Compat: json.RawMessage(`{"supportsStrictTools":true}`), Options: json.RawMessage(`{}`),
			Schema: json.RawMessage(`{"title":"Lookup","properties":{"query":{"type":"string"},"opts":{"type":"object","properties":{"limit":{"type":"number"},"cursor":{"type":"string"}},"required":["limit"]}},"required":["query"]}`), Strict: true},
		{Name: "strict schema spreads the parameters then the literal", Compat: json.RawMessage(`{"supportsStrictTools":true}`), Options: json.RawMessage(`{}`),
			Schema: json.RawMessage(`{"description":"Lookup","type":"object","properties":{"query":{"type":"string"},"opts":{"type":"object","properties":{"limit":{"type":"number"},"cursor":{"type":"string"}},"required":["limit"]}},"required":["query"]}`), Strict: true},
		// A strict object without properties gets required from makeStrictJsonSchema; the literal appends properties after it.
		{Name: "strict schema without properties", Compat: json.RawMessage(`{"supportsStrictTools":true}`), Options: json.RawMessage(`{}`),
			Schema: json.RawMessage(`{"type":"object"}`), Strict: true},
		// A strict schema keeps the order of its objects outside properties too; the legacy literal drops them.
		{Name: "strict schema annotations keep their order", Compat: json.RawMessage(`{"supportsStrictTools":true}`), Options: json.RawMessage(`{}`),
			Schema: json.RawMessage(`{"type":"object","x-ui":{"z":1,"a":2},"properties":{"item":{"type":"string"}},"required":["item"]}`), Strict: true},
		{Name: "strict schema without required", Compat: json.RawMessage(`{"supportsStrictTools":true,"supportsEagerToolInputStreaming":false}`), Options: json.RawMessage(`{}`),
			Schema: json.RawMessage(`{"properties":{"b":{"type":"string"}},"type":"object","additionalProperties":false}`), Strict: true},
	}
	const script = `(async () => {
  const [root, casesJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/anthropic-messages.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  const out = {};
  for (const c of JSON.parse(casesJSON)) {
    const model = { id: 'm1', provider: 'custom', api: 'anthropic-messages', baseUrl: 'http://127.0.0.1:9', reasoning: false, input: ['text'], maxTokens: 32768,
      contextWindow: 131072, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, compat: c.compat };
    const tools = [{ name: 'read', description: 'Read a file', parameters: { type: 'object', properties: {} } }, { name: 'lookup', description: 'Look up', parameters: c.schema,
      ...(c.strict ? { constrainedSampling: { type: 'json_schema' } } : {}) }];
    const context = transcript.normalizeContext({ messages: [{ role: 'user', content: 'hi', timestamp: 1 }], tools });
    const stream = lazy.anthropicMessagesApi().stream(model, context, { apiKey: 'k', ...c.options,
      onPayload: (params) => { out[c.name] = JSON.stringify(params.tools); throw new Error('captured'); } });
    await stream.result();
  }
  console.log(JSON.stringify(out));
})()`
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), string(encoded))
	cmd.Stderr = new(bytes.Buffer)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, cmd.Stderr)
	}
	var want map[string]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var compat ModelCompat
			if err := json.Unmarshal(c.Compat, &compat); err != nil {
				t.Fatal(err)
			}
			var options struct {
				CacheRetention CacheRetention `json:"cacheRetention"`
			}
			if err := json.Unmarshal(c.Options, &options); err != nil {
				t.Fatal(err)
			}
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			provider := NewAnthropicProvider(AnthropicConfig{Model: "m1", ProviderID: "custom", BaseURL: server.URL, APIKey: "k", Compat: &compat})
			constrained := ""
			if c.Strict {
				constrained = `,"constrainedSampling":{"type":"json_schema"}`
			}
			tools := mustTools(t, `[{"name":"read","description":"Read a file","parameters":{"type":"object","properties":{}}},{"name":"lookup","description":"Look up","parameters":`+string(c.Schema)+constrained+`}]`)
			// A coding session always sets onPayload (sdk.ts installs it for before_provider_request).
			for _, onPayload := range []func(any, *Model) (any, error){nil, func(payload any, _ *Model) (any, error) { return payload, nil }} {
				body = nil
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}, Tools: tools}),
					StreamOptions{CacheRetention: options.CacheRetention, OnPayload: onPayload})
				if err == nil {
					for range stream.Events(t.Context()) {
					}
				}
				var request struct {
					Tools json.RawMessage `json:"tools"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					t.Fatalf("request body %q: %v", body, err)
				}
				if got := string(request.Tools); got != want[c.Name] {
					t.Fatalf("tools (onPayload set: %t)\n got: %s\nwant: %s", onPayload != nil, got, want[c.Name])
				}
			}
		})
	}
}
