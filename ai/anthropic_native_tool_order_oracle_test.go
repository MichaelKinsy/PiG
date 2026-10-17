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

// With native tool changes, buildParams sends the initial tools followed by DEFERRED_TOOL_PLACEHOLDER (anthropic-messages.ts:1205-1218),
// whose literal writes name, description, input_schema { type, properties, required } and then defer_loading (:204-209). A later tool
// is defined by value in a tool_addition block through the same convertTools (:1149-1152), so its definition keeps convertTools' member
// order. Pi's own buildParams writes the oracle through onPayload; PiG's are the tools and messages members of the HTTP request body.
func TestAnthropicNativeToolChangesPlaceholderAndDefinitionsFollowPiMemberOrder(t *testing.T) {
	const initialTool = `{"name":"read","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"}},"required":["path"]}}`
	const laterTool = `{"name":"lookup","description":"Look up","parameters":{"required":["query"],"properties":{"query":{"type":"string"},"limit":{"type":"number"}},"type":"object"}}`
	const script = `(async () => {
  const [root, initialJSON, laterJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/anthropic-messages.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  const model = { id: 'm1', provider: 'custom', api: 'anthropic-messages', baseUrl: 'http://127.0.0.1:9', reasoning: false, input: ['text'], maxTokens: 32768,
    contextWindow: 131072, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    compat: { supportsMidConvoSystemMessages: true, supportsMidConvoToolChanges: true } };
  const context = transcript.normalizeContext({ messages: [
    { role: 'system', content: 'base', timestamp: 1, toolsAdded: [JSON.parse(initialJSON)] },
    { role: 'user', content: 'one', timestamp: 2 },
    { role: 'system', content: 'more', timestamp: 3, toolsAdded: [JSON.parse(laterJSON)] },
    { role: 'user', content: 'two', timestamp: 4 },
  ] });
  let out;
  const stream = lazy.anthropicMessagesApi().stream(model, context, { apiKey: 'k', cacheRetention: 'none',
    onPayload: (params) => { out = { tools: JSON.stringify(params.tools), messages: JSON.stringify(params.messages) }; throw new Error('captured'); } });
  await stream.result();
  console.log(JSON.stringify(out));
})()`
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), initialTool, laterTool)
	cmd.Stderr = new(bytes.Buffer)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, cmd.Stderr)
	}
	var want struct {
		Tools    string `json:"tools"`
		Messages string `json:"messages"`
	}
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}
	if want.Tools == "" || want.Messages == "" {
		t.Fatalf("oracle output %q has no request", out)
	}

	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	enabled := true
	provider := NewAnthropicProvider(AnthropicConfig{Model: "m1", ProviderID: "custom", BaseURL: server.URL, APIKey: "k",
		Compat: &ModelCompat{SupportsMidConvoSystemMessages: &enabled, SupportsMidConvoToolChanges: &enabled}})
	messages := []Message{
		SystemMessage{Content: SystemText("base"), Timestamp: 1, ToolsAdded: mustTools(t, "["+initialTool+"]")},
		UserMessage{Content: UserText("one"), Timestamp: 2},
		SystemMessage{Content: SystemText("more"), Timestamp: 3, ToolsAdded: mustTools(t, "["+laterTool+"]")},
		UserMessage{Content: UserText("two"), Timestamp: 4},
	}
	// A coding session always sets onPayload (sdk.ts installs it for before_provider_request).
	for _, onPayload := range []func(any, *Model) (any, error){nil, func(payload any, _ *Model) (any, error) { return payload, nil }} {
		body = nil
		stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages}), StreamOptions{CacheRetention: CacheRetentionNone, OnPayload: onPayload})
		if err == nil {
			for range stream.Events(t.Context()) {
			}
		}
		var request struct {
			Tools    json.RawMessage `json:"tools"`
			Messages json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("request body %q: %v", body, err)
		}
		if got := string(request.Tools); got != want.Tools {
			t.Errorf("tools (onPayload set: %t)\n got: %s\nwant: %s", onPayload != nil, got, want.Tools)
		}
		if got := string(request.Messages); got != want.Messages {
			t.Errorf("messages (onPayload set: %t)\n got: %s\nwant: %s", onPayload != nil, got, want.Messages)
		}
	}
}
