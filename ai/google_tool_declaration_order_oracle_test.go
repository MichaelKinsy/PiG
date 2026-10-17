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

// The @google/genai SDK passes Pi's function declarations through: convertTools' members (name, description, then parameters or
// parametersJsonSchema: google-shared.ts:388-397) with the tool's own schema object inside. Pi's own stream posts the oracle body to a
// local server; PiG's provider posts to another.
func TestGoogleToolDeclarationsFollowPiMemberOrder(t *testing.T) {
	type bodyCase struct {
		Name   string          `json:"name"`
		Model  string          `json:"model"`
		Schema json.RawMessage `json:"schema"`
		Strict bool            `json:"strict"`
	}
	cases := []bodyCase{
		{Name: "tool schema in declaration order", Model: "gemini-2.5-flash",
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string","description":"Z"},"a":{"type":"number"}},"required":["z"]}`)},
		{Name: "strict tool schema in declaration order", Model: "gemini-3-pro-preview", Strict: true,
			Schema: json.RawMessage(`{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"number"}},"required":["z"]}`)},
	}
	const script = `(async () => {
  const http = require('node:http');
  const [root, casesJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/google-generative-ai.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  let body;
  const server = http.createServer((req, res) => { let data = ''; req.on('data', (chunk) => data += chunk); req.on('end', () => { body = data; res.statusCode = 400; res.end('{}'); }); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const out = {};
  for (const c of JSON.parse(casesJSON)) {
    const model = { id: c.model, provider: 'google', api: 'google-generative-ai', baseUrl: 'http://127.0.0.1:' + server.address().port, reasoning: false,
      input: ['text'], maxTokens: 1000, contextWindow: 10000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    const tools = [{ name: 'lookup', description: 'Look up', parameters: c.schema, ...(c.strict ? { constrainedSampling: { type: 'json_schema' } } : {}) }];
    body = undefined;
    await lazy.googleGenerativeAIApi().stream(model, transcript.normalizeContext({ messages: [{ role: 'user', content: 'hi', timestamp: 1 }], tools }), { apiKey: 'k' }).result();
    out[c.name] = JSON.stringify(JSON.parse(body).tools);
  }
  server.close();
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
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			constrained := ""
			if c.Strict {
				constrained = `,"constrainedSampling":{"type":"json_schema"}`
			}
			tools := mustTools(t, `[{"name":"lookup","description":"Look up","parameters":`+string(c.Schema)+constrained+`}]`)
			provider := NewGoogleProvider(GoogleConfig{Model: c.Model, ProviderID: "google", BaseURL: server.URL, APIKey: "k"})
			// A coding session always sets onPayload (sdk.ts installs it for before_provider_request), which lowers the payload again.
			for _, onPayload := range []func(any, *Model) (any, error){nil, func(payload any, _ *Model) (any, error) { return payload, nil }} {
				body = nil
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}, Tools: tools}), StreamOptions{OnPayload: onPayload})
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
