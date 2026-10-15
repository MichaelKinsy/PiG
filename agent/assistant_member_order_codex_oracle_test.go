package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Codex is the one Pi provider that creates endTurn, and it creates it before rawStopReason: mapCodexEvents sets output.endTurn on the
// terminal event (openai-codex-responses.ts:774-778) before it yields that event to processResponsesStream, whose finalizeResponse then
// creates rawStopReason (openai-responses-shared.ts:589). The same SSE drives Pi's Codex stream and PiG's provider; the session-line and RPC
// encodings of PiG's message must equal JSON.stringify of Pi's.
func TestCodexAssistantEndTurnPrecedesRawStopReason(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m1","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hi"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","end_turn":true,"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	t.Cleanup(server.Close)
	claims, err := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct"}})
	if err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"

	const script = `(async () => {
  const [root, url, token] = process.argv.slice(1);
  const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
  const { stream } = await load("node_modules/@earendil-works/pi-ai/dist/api/openai-codex-responses.js");
  const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
  const model = { id: "gpt-5", provider: "openai-codex", api: "openai-codex-responses", baseUrl: url, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, maxTokens: 1000, contextWindow: 10000 };
  const s = stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: token, transport: "sse" });
  console.log(JSON.stringify(await s.result()));
})()`
	root := filepath.Join(testenv.ModuleRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, root, server.URL, token)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	want, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}

	provider := ai.NewOpenAICodexResponsesProvider(ai.OpenAICodexResponsesConfig{Model: "gpt-5", ProviderID: "openai-codex", BaseURL: server.URL, APIKey: token})
	t.Cleanup(func() { _ = provider.Close() })
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}}), ai.StreamOptions{Transport: ai.TransportSSE})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(t.Context()) {
	}
	got, err := json.Marshal(AgentMessage{Assistant: agentAssistantMessage(stream.Result())})
	if err != nil {
		t.Fatal(err)
	}
	volatile := regexp.MustCompile(`"(timestamp|durationMs)":\d+`)
	if g, w := volatile.ReplaceAllString(string(got), `"$1":0`), volatile.ReplaceAllString(string(bytes.TrimSpace(want)), `"$1":0`); g != w {
		t.Fatalf("assistant message\n got: %s\nwant: %s", g, w)
	}
}
