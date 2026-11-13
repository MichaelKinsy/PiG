package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// A reopened session sends its stored tool calls back to the model with their arguments in the member order the session file holds. Pi reads the file with
// JSON.parse and writes each request with JSON.stringify: insertion order, with integer-like keys first in ascending order. Each side opens the same session
// file with its own session manager, converts the branch to model messages and sends it with its own provider to a local server that records the request
// body. For each API kind the replay reaches, the replayed arguments are equal byte for byte, and so are the bodies. The Anthropic bodies are compared up to
// the last user message: its text block carries cache_control, which PiG writes before the block's text and Pi after it. That block is not a replayed call,
// and its member order is not this test's subject.
//
// pi: packages/coding-agent/src/core/session-manager.ts (SessionManager.open, buildSessionContext), packages/coding-agent/src/core/messages.ts (convertToLlm),
// packages/ai/src/api/anthropic-messages.ts, openai-completions.ts, openai-responses.ts, google-generative-ai.ts
func TestResumedSessionToolCallArgumentsReachEachProviderInTheStoredOrder(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required for the Pi oracle")
	}
	const (
		storedPlain = `{"path":"notes.constructor","content":"x = 1","nested":{"z":1,"a":{"y":[{"k":2,"b":1}],"c":null}}}`
		// JSON.parse puts the integer-like keys first, ascending; the others keep the file's order.
		storedIndexed = `{"zeta":true,"10":"ten","alpha":"a","2":"two"}`
		sentIndexed   = `{"2":"two","10":"ten","zeta":true,"alpha":"a"}`
	)
	type replayCase struct {
		API           string `json:"api"`
		Provider      string `json:"provider"`
		Model         string `json:"model"`
		Module        string `json:"module"`
		BasePath      string `json:"basePath"`
		MaxTokens     int    `json:"maxTokens"`
		ContextWindow int    `json:"contextWindow"`
		Session       string `json:"session"`
		// key is the request member that holds a replayed call's arguments; text says the API sends them as a JSON string.
		key  string
		text bool
		// upTo, when set, starts the part of the body that is not compared: the last occurrence of this text.
		upTo string
	}
	cases := []replayCase{
		{API: "anthropic-messages", Provider: "custom", Model: "m1", Module: "anthropic-messages.js", MaxTokens: 32768, ContextWindow: 131072, key: "input", upTo: `,{"role":"user"`},
		{API: "openai-completions", Provider: "custom", Model: "m1", Module: "openai-completions.js", BasePath: "/v1", MaxTokens: 32768, ContextWindow: 131072, key: "arguments", text: true},
		{API: "openai-responses", Provider: "custom", Model: "m1", Module: "openai-responses.js", BasePath: "/v1", MaxTokens: 32768, ContextWindow: 131072, key: "arguments", text: true},
		{API: "google-generative-ai", Provider: "google", Model: "gemini-2.5-flash", Module: "google-generative-ai.js", MaxTokens: 1000, ContextWindow: 10000, key: "args"},
	}
	dir := t.TempDir()
	const usage = `{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	for i := range cases {
		c := &cases[i]
		cwd, err := json.Marshal(dir)
		if err != nil {
			t.Fatal(err)
		}
		lines := []string{
			`{"type":"session","version":3,"id":"resumed-order-` + c.API + `","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + string(cwd) + `}`,
			`{"type":"message","id":"a0000001","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"go"}],"timestamp":1}}`,
			`{"type":"message","id":"a0000002","parentId":"a0000001","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"write","arguments":` + storedPlain +
				`},{"type":"toolCall","id":"call_2","name":"probe","arguments":` + storedIndexed + `}],"api":"` + c.API + `","provider":"` + c.Provider + `","model":"` + c.Model + `","usage":` + usage + `,"stopReason":"toolUse","timestamp":2}}`,
			`{"type":"message","id":"a0000003","parentId":"a0000002","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"write","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":3}}`,
			`{"type":"message","id":"a0000004","parentId":"a0000003","timestamp":"2026-01-01T00:00:04.000Z","message":{"role":"toolResult","toolCallId":"call_2","toolName":"probe","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":4}}`,
			`{"type":"message","id":"a0000005","parentId":"a0000004","timestamp":"2026-01-01T00:00:05.000Z","message":{"role":"user","content":[{"type":"text","text":"next"}],"timestamp":5}}`,
		}
		c.Session = filepath.Join(dir, c.API+".jsonl")
		if err := os.WriteFile(c.Session, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	const script = `(async () => {
  const http = require('node:http');
  const path = require('node:path');
  const [root, casesJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(path.join(root, rel)).href);
  const { SessionManager } = await load('dist/core/session-manager.js');
  const { convertToLlm } = await load('dist/core/messages.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  let body;
  const server = http.createServer((req, res) => { let data = ''; req.on('data', (chunk) => data += chunk); req.on('end', () => { body = data; res.statusCode = 400; res.setHeader('content-type', 'application/json'); res.end('{"error":{"message":"captured"}}'); }); });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const out = {};
  for (const c of JSON.parse(casesJSON)) {
    const api = await load('node_modules/@earendil-works/pi-ai/dist/api/' + c.module);
    const model = { id: c.model, name: c.model, provider: c.provider, api: c.api, baseUrl: 'http://127.0.0.1:' + server.address().port + c.basePath, reasoning: false, input: ['text'],
      maxTokens: c.maxTokens, contextWindow: c.contextWindow, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    const messages = convertToLlm(SessionManager.open(c.session).buildSessionContext().messages);
    body = undefined;
    const result = await api.stream(model, transcript.normalizeContext({ messages }), { apiKey: 'k' }).result();
    out[c.api] = { body: body ?? '', outcome: String(result.errorMessage ?? result.stopReason) };
  }
  server.close();
  console.log(JSON.stringify(out));
})()`
	root, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, root, string(encoded))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, &stderr)
	}
	var want map[string]struct {
		Body    string `json:"body"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}

	// argumentsOf returns, in order, the raw text of each value of the member named key.
	argumentsOf := func(body, key string) []string {
		var found []string
		marker := `"` + key + `":`
		for rest := body; ; {
			at := strings.Index(rest, marker)
			if at < 0 {
				return found
			}
			rest = rest[at+len(marker):]
			var raw json.RawMessage
			if err := json.NewDecoder(strings.NewReader(rest)).Decode(&raw); err != nil {
				return found
			}
			found = append(found, string(raw))
		}
	}
	for _, c := range cases {
		t.Run(c.API, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"captured"}}`)
			}))
			defer server.Close()
			model := &ai.Model{ID: c.Model, Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: c.Provider, API: ai.API(c.API), BaseURL: server.URL + c.BasePath},
				Capabilities: ai.ModelCapabilities{MaxOutputTokens: c.MaxTokens, ContextWindow: c.ContextWindow}}
			var provider ai.Provider
			switch ai.API(c.API) {
			case ai.APIAnthropicMessages:
				provider = ai.NewAnthropicProvider(ai.AnthropicConfig{Model: c.Model, ModelMetadata: model, ProviderID: c.Provider, BaseURL: server.URL, APIKey: "k"})
			case ai.APIOpenAICompletions:
				provider = ai.NewOpenAIProvider(ai.OpenAIConfig{Model: c.Model, ModelMetadata: model, ProviderID: c.Provider, BaseURL: server.URL + c.BasePath, APIKey: "k"})
			case ai.APIOpenAIResponses:
				provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{Model: c.Model, ModelMetadata: model, ProviderID: c.Provider, BaseURL: server.URL + c.BasePath, APIKey: "k"})
			case ai.APIGoogleGenerativeAI:
				provider = ai.NewGoogleProvider(ai.GoogleConfig{Model: c.Model, ProviderID: c.Provider, BaseURL: server.URL, APIKey: "k",
					ModelMetadata: &ai.Model{ID: c.Model, Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: c.Provider, API: ai.APIGoogleGenerativeAI}}})
			default:
				t.Fatalf("no provider for %s", c.API)
			}
			defer func() { _ = provider.Close() }()
			session, err := NewSessionManagerWithDir(dir, dir).Load(c.Session)
			if err != nil {
				t.Fatal(err)
			}
			replay := ai.NormalizeContext(ai.Context{Messages: agent.ConvertToLLM(session.BuildSessionContext().Messages)})
			stream, err := provider.Stream(t.Context(), replay, ai.StreamOptions{})
			if err == nil {
				for range stream.Events(t.Context()) {
				}
			}
			pi, pig := want[c.API].Body, string(body)
			if pi == "" || pig == "" {
				t.Fatalf("a side sent no request: Pi (%s) %d bytes, PiG (stream error %v) %d bytes", want[c.API].Outcome, len(pi), err, len(pig))
			}
			expect := []string{storedPlain, sentIndexed}
			if c.text {
				for i, arguments := range expect {
					quoted, err := json.Marshal(arguments)
					if err != nil {
						t.Fatal(err)
					}
					expect[i] = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(string(quoted))
				}
			}
			piArguments, pigArguments := argumentsOf(pi, c.key), argumentsOf(pig, c.key)
			if fmt.Sprint(piArguments) != fmt.Sprint(expect) {
				t.Errorf("Pi replays the stored calls as\n%q\nnot in the stored order\n%q\nbody: %s", piArguments, expect, pi)
			}
			if fmt.Sprint(pigArguments) != fmt.Sprint(piArguments) {
				t.Errorf("replayed arguments differ:\nPi  %q\nPiG %q", piArguments, pigArguments)
			}
			if c.upTo != "" {
				piEnd, pigEnd := strings.LastIndex(pi, c.upTo), strings.LastIndex(pig, c.upTo)
				if piEnd < 0 || pigEnd < 0 {
					t.Fatalf("a body has no %s:\nPi  %s\nPiG %s", c.upTo, pi, pig)
				}
				pi, pig = pi[:piEnd], pig[:pigEnd]
			}
			if pig != pi {
				t.Errorf("request bodies differ:\nPi  %s\nPiG %s", pi, pig)
			}
		})
	}
}
