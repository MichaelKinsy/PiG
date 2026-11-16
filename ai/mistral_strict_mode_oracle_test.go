//go:build !pig_strip_mistral_conversations

package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// Pi 2c2a77f9f3ab (#12148, packages/ai/src/api/mistral-conversations.ts buildChatPayload and toFunctionTools): a tool goes out strict only when the model's
// compat.supportsStrictMode is true (absent is false). The oracle is the pinned Pi plus the post-release backports (automation/gen/pi-post-release-backports.mjs, the
// 2c2a77f9f3ab entries). Pi's stream and PiG's post to the same server; the bodies must be identical, and a "require" tool the model cannot send strict must fail
// with the same message before any request.
func TestMistralStrictToolsRequestBodyMatchesPi(t *testing.T) {
	const schema = `{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"number"},"m":{"type":"object","properties":{"y":{"type":"string"},"b":{"type":"integer"}},"required":["y"]}},"required":["z"]}`
	type strictCase struct {
		Name     string          `json:"name"`
		Compat   json.RawMessage `json:"compat,omitempty"`
		Strict   string          `json:"strict"`
		Provider string          `json:"provider"`
	}
	cases := []strictCase{
		{Name: "prefer, no compat", Strict: "prefer", Provider: "mistral"},
		{Name: "prefer, compat without the member", Compat: json.RawMessage(`{"supportsMidConvoSystemMessages":true}`), Strict: "prefer", Provider: "mistral"},
		{Name: "prefer, supportsStrictMode false", Compat: json.RawMessage(`{"supportsStrictMode":false}`), Strict: "prefer", Provider: "mistral"},
		{Name: "prefer, supportsStrictMode true", Compat: json.RawMessage(`{"supportsStrictMode":true}`), Strict: "prefer", Provider: "mistral"},
		{Name: "require, supportsStrictMode true", Compat: json.RawMessage(`{"supportsStrictMode":true}`), Strict: "require", Provider: "mistral"},
		{Name: "require, no compat", Strict: "require", Provider: "mistral"},
		{Name: "require, supportsStrictMode false", Compat: json.RawMessage(`{"supportsStrictMode":false}`), Strict: "require", Provider: "mistral"},
		{Name: "custom provider, prefer, no compat", Strict: "prefer", Provider: "custom"},
		{Name: "custom provider, prefer, supportsStrictMode true", Compat: json.RawMessage(`{"supportsStrictMode":true}`), Strict: "prefer", Provider: "custom"},
		{Name: "custom provider, require, no compat", Strict: "require", Provider: "custom"},
	}
	var mu sync.Mutex
	bodies := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		segments := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
		if len(segments) >= 2 {
			mu.Lock()
			bodies[segments[0]+"/"+segments[1]] = string(body)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	const script = `(async () => {
  const [root, baseUrl, casesJSON, schemaJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, 'node_modules/@earendil-works/pi-ai/dist', rel)).href);
  const transcript = await load('utils/transcript.js');
  const api = (await load('api/mistral-conversations.lazy.js')).mistralConversationsApi();
  const errors = {};
  for (const [index, c] of JSON.parse(casesJSON).entries()) {
    const model = { id: 'mistral-small', name: 'mistral-small', provider: c.provider, api: 'mistral-conversations', baseUrl: baseUrl + '/pi/' + index, reasoning: false,
      input: ['text'], maxTokens: 1000, contextWindow: 10000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    if (c.compat) model.compat = c.compat;
    const tools = [{ name: 'lookup', description: 'Look up', parameters: JSON.parse(schemaJSON), constrainedSampling: { type: 'json_schema', strict: c.strict } }];
    const context = transcript.normalizeContext({ systemPrompt: 'be brief', messages: [{ role: 'user', content: 'hi', timestamp: 1 }], tools });
    const result = await api.stream(model, context, { apiKey: 'k', maxRetries: 0 }).result();
    errors[index] = result.stopReason === 'error' ? result.errorMessage : '';
  }
  process.stdout.write(JSON.stringify(errors));
})()`
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), server.URL, string(encoded), schema)
	cmd.Stdout, cmd.Stderr = &stdout, new(bytes.Buffer)
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, cmd.Stderr)
	}
	var piErrors map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &piErrors); err != nil {
		t.Fatalf("Pi's results %q: %v", stdout.String(), err)
	}

	for index, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			key := string(rune('0' + index))
			mu.Lock()
			want := bodies["pi/"+key]
			mu.Unlock()
			baseURL := server.URL + "/pig/" + key
			model := &Model{ID: "mistral-small", Input: []string{"text"}, ProviderMeta: ProviderMetadata{ProviderID: c.Provider, API: APIMistralConversations, BaseURL: baseURL}}
			if c.Compat != nil {
				model.ProviderMeta.Compat = new(ModelCompat)
				if err := json.Unmarshal(c.Compat, model.ProviderMeta.Compat); err != nil {
					t.Fatal(err)
				}
			}
			provider := NewMistralProvider(MistralConfig{Model: "mistral-small", ProviderID: c.Provider, BaseURL: baseURL, APIKey: "k", ModelMetadata: model})
			var tool ToolSchema
			raw := `{"name":"lookup","description":"Look up","parameters":` + schema + `,"constrainedSampling":{"type":"json_schema","strict":"` + c.Strict + `"}}`
			if err := json.Unmarshal([]byte(raw), &tool); err != nil {
				t.Fatal(err)
			}
			context := NormalizeContext(Context{SystemPrompt: "be brief", Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}, Tools: []ToolSchema{tool}})
			stream, err := provider.Stream(t.Context(), context, StreamOptions{MaxRetries: new(0)})
			failure := ""
			if err != nil {
				failure = err.Error()
			} else if result := stream.Result(); result.StopReason == StopReasonError {
				failure = result.ErrorMessage
			}
			mu.Lock()
			got := bodies["pig/"+key]
			mu.Unlock()
			if want == "" {
				// Pi sent no request: its error is the resolver's message, and PiG must stop before the transport with the same text.
				if got != "" || piErrors[key] == "" || failure != piErrors[key] {
					t.Fatalf("a failure before any request: PiG body %q, failure\n got: %q\nwant: %q", got, failure, piErrors[key])
				}
				return
			}
			if got != want {
				t.Fatalf("request body\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
