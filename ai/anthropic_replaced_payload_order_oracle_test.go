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

// An onPayload replacement is sent as `{ ...nextParams, stream: true }` (anthropic-messages.ts:640-642): the replacement's members keep
// their order, an existing stream member keeps its place, and a missing one is appended. The SDK's create() then takes betas out of the
// body (`const { betas, ...body } = params`, @anthropic-ai/sdk beta/messages.js:27) and JSON.stringify writes the rest in order. Pi's
// stream posts the oracle body to a local server; PiG's provider posts to another. A subprocess extension's replacement reaches the
// provider as raw JSON.
func TestAnthropicReplacedPayloadKeepsPiMemberOrder(t *testing.T) {
	type replacedCase struct {
		Name        string          `json:"name"`
		Replacement json.RawMessage `json:"replacement"`
	}
	cases := []replacedCase{
		{Name: "stream member keeps its place and betas leave the body",
			Replacement: json.RawMessage(`{"model":"m1","messages":[{"role":"user","content":"hi"}],"max_tokens":5,"betas":["b1","b2"],"system":"s","stream":false,"temperature":0.5}`)},
		{Name: "missing stream member is appended",
			Replacement: json.RawMessage(`{"max_tokens":5,"model":"m1","metadata":{"z":1,"a":2},"messages":[{"content":"hi","role":"user"}]}`)},
		{Name: "betas as a string",
			Replacement: json.RawMessage(`{"system":"s","betas":"one","model":"m1","max_tokens":5,"messages":[]}`)},
		// JSON.parse puts array-index keys first in ascending order, and a repeated key keeps its first place with its last value.
		{Name: "index keys, a repeated key and escapes",
			Replacement: json.RawMessage(`{ "model" : "m1", "10": 1, "2": 2, "max_tokens": 5, "model": "m2", "messages": [], "system": "<&>\u2028\u00e9\ud800" }`)},
	}
	const script = `(async () => {
  const http = require('node:http');
  const [root, casesJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/anthropic-messages.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  let captured;
  const server = http.createServer((req, res) => { let data = ''; req.on('data', (chunk) => data += chunk); req.on('end', () => {
    captured = { body: data, betas: req.headers['anthropic-beta'] ?? null }; res.statusCode = 400; res.setHeader('content-type', 'application/json'); res.end('{"type":"error","error":{"type":"invalid_request_error","message":"stop"}}'); }); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const out = {};
  for (const c of JSON.parse(casesJSON)) {
    const model = { id: 'm1', provider: 'custom', api: 'anthropic-messages', baseUrl: 'http://127.0.0.1:' + server.address().port, reasoning: false, input: ['text'],
      maxTokens: 32768, contextWindow: 131072, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    captured = undefined;
    const replacement = JSON.parse(JSON.stringify(c.replacement));
    const context = transcript.normalizeContext({ messages: [{ role: 'user', content: 'hi', timestamp: 1 }] });
    await lazy.anthropicMessagesApi().stream(model, context, { apiKey: 'k', maxRetries: 0, onPayload: () => replacement }).result();
    out[c.name] = captured;
  }
  server.close();
  console.log(JSON.stringify(out));
})()`
	// JSON.stringify(c.replacement) above re-encodes the parsed cases, so the oracle sees the same object JSON.parse builds from the raw text.
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
	type capture struct {
		Body  string  `json:"body"`
		Betas *string `json:"betas"`
	}
	var want map[string]*capture
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if want[c.Name] == nil {
				t.Fatal("oracle captured no request")
			}
			var got capture
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				got.Body = string(body)
				if values, ok := r.Header["Anthropic-Beta"]; ok {
					joined := values[0]
					got.Betas = &joined
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"stop"}}`))
			}))
			defer server.Close()
			provider := NewAnthropicProvider(AnthropicConfig{Model: "m1", ProviderID: "custom", BaseURL: server.URL, APIKey: "k"})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}}),
				StreamOptions{MaxRetries: new(int), OnPayload: func(any, *Model) (any, error) { return c.Replacement, nil }})
			if err == nil {
				for range stream.Events(t.Context()) {
				}
			}
			if got.Body != want[c.Name].Body {
				t.Fatalf("request body\n got: %s\nwant: %s", got.Body, want[c.Name].Body)
			}
			if (got.Betas == nil) != (want[c.Name].Betas == nil) || got.Betas != nil && *got.Betas != *want[c.Name].Betas {
				t.Fatalf("anthropic-beta = %v, want %v", got.Betas, want[c.Name].Betas)
			}
		})
	}
}
