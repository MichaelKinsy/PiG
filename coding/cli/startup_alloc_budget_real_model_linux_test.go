// SPDX-License-Identifier: MIT

//go:build linux

package cli

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// realModelShape is one way a user names the model of a print-mode run that the faux shortcut (testFauxCLIModel) never takes: the model is
// resolved through the catalog and the model resolver and the request goes to a real provider API (a hermetic server stands in for the
// provider's address), with the credential the shape stores. AGENTS.md: a shared path is tested with several provider shapes.
type realModelShape struct {
	// id is the shape's segment of its ratchet keys, process.print_real_model.<id>.allocs and .bytes.
	id string
	// args select the model; empty selects the provider's default model.
	args []string
	// provider and credential are the stored auth.json entry; provider is empty for a shape whose credential is in models.json.
	provider   string
	credential ai.Credential
	// models is the models.json that points the provider at the hermetic server (%s is the server's address); the catalog's own address is
	// otherwise a real host.
	models string
	// wire is the request path, after the models.json base URL, that serves the model's API.
	wire string
	// authorization is the header the provider's credential makes the request carry.
	authorization string
	// upgrades is how many WebSocket upgrade attempts precede the request: Pi's Codex transport is "auto" and tries a WebSocket first
	// (openai-codex-responses.ts:313-320), which the server refuses, so the stream falls back to SSE.
	upgrades int
}

// realModelShapes: an API-key provider of the Responses API, an OAuth (subscription) provider with a stored credential, a custom
// OpenAI-compatible provider with its own base URL and compat, and the default model of an API-key provider (no --model, no saved default).
// The credentials are fixtures that name no real account and are valid only for the hermetic server.
var realModelShapes = []realModelShape{
	{
		id: "api_key", args: []string{"--model", "openai/gpt-4o-mini"},
		provider: "openai", credential: ai.Credential{Type: ai.CredentialAPIKey, Key: "fixture-api-key"},
		models: `{"providers":{"openai":{"baseUrl":"%s"}}}`, wire: "/responses", authorization: "Bearer fixture-api-key",
	},
	{
		id: "oauth", args: []string{"--model", "openai-codex/gpt-5.6-sol"},
		provider: "openai-codex", credential: ai.Credential{Type: ai.CredentialOAuth, Refresh: "fixture-refresh", Access: codexStoredAccessToken, Expires: 4102444800000},
		models: `{"providers":{"openai-codex":{"baseUrl":"%s"}}}`, wire: "/codex/responses", authorization: "Bearer " + codexStoredAccessToken, upgrades: 1,
	},
	{
		id:     "openai_compatible",
		args:   []string{"--model", "local/local-model"},
		models: startupRealModelCompatJSON, wire: "/v1/chat/completions", authorization: "Bearer local-key",
	},
	{
		id: "default_model", args: nil,
		provider: "deepseek", credential: ai.Credential{Type: ai.CredentialAPIKey, Key: "fixture-deepseek-key"},
		models: `{"providers":{"deepseek":{"baseUrl":"%s"}}}`, wire: "/chat/completions", authorization: "Bearer fixture-deepseek-key",
	},
}

const startupRealModelCompatJSON = `{"providers":{"local":{"baseUrl":"%s/v1","api":"openai-completions","apiKey":"local-key",
  "compat":{"supportsDeveloperRole":false,"maxTokensField":"max_tokens"},
  "models":[{"id":"local-model","name":"Local Model","reasoning":true,"input":["text"],"contextWindow":128000,"maxTokens":8192,
    "cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":0}}]}}}`

// realModelServer is the hermetic provider: it answers a streamed request of the Responses API or of chat completions with one reply, "ok",
// and records what it was asked.
type realModelServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []realModelRequest
}

type realModelRequest struct {
	method, path, authorization string
	upgrade                     bool
}

func newRealModelServer(t *testing.T) *realModelServer {
	t.Helper()
	s := &realModelServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		s.mu.Lock()
		upgrade := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		s.requests = append(s.requests, realModelRequest{method: r.Method, path: r.URL.Path, authorization: r.Header.Get("Authorization"), upgrade: upgrade})
		s.mu.Unlock()
		if upgrade {
			http.Error(w, "no WebSocket here", http.StatusUpgradeRequired)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case strings.HasSuffix(r.URL.Path, "/responses"):
			_, _ = io.WriteString(w, realModelResponsesReply)
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = io.WriteString(w, realModelCompletionsReply)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// take returns the requests since the last take.
func (s *realModelServer) take() []realModelRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	taken := s.requests
	s.requests = nil
	return taken
}

const realModelResponsesReply = `data: {"type":"response.created","response":{"id":"resp_fixture","status":"in_progress"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_fixture","role":"assistant","status":"in_progress","content":[]}}

data: {"type":"response.output_text.delta","output_index":0,"item_id":"msg_fixture","content_index":0,"delta":"ok"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_fixture","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}}

data: {"type":"response.completed","response":{"id":"resp_fixture","status":"completed","usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"input_tokens_details":{"cached_tokens":0}}}}

`

const realModelCompletionsReply = `data: {"id":"chatcmpl-fixture","model":"fixture","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}

data: [DONE]

`

// startupRealModelRuns is how many warm runs of a shape are measured; every run is logged and the smallest is gated, as the faux budgets do.
const startupRealModelRuns = 3

// TestStartupAllocationBudgetRealModel gates the start-up allocations of a print-mode run whose model is resolved the way a user's --model
// (or default model) is: through the catalog, the model resolver and the provider's credential, with one request over HTTP. TestStartupAllocationBudget
// measures the faux shortcut (testFauxCLIModel), which skips that resolution, so a cost of the real path was gated by nothing. The binary
// runs as a user's does: PIG_TEST_FAUX is not set, so there is no registered test-faux provider either.
func TestStartupAllocationBudgetRealModel(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	for _, shape := range realModelShapes {
		t.Run(shape.id, func(t *testing.T) {
			fixture := newStartupFixture(t)
			fixture.production = true
			server := newRealModelServer(t)
			if shape.provider != "" {
				auth, err := ai.NewAuthStorage(filepath.Join(fixture.agentDir, "auth.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := auth.Set(shape.provider, shape.credential); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(fixture.agentDir, "models.json"), []byte(strings.ReplaceAll(shape.models, "%s", server.URL)), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx := testbudget.Context(t)
			args := append(append([]string{}, shape.args...), "--no-extensions", "-p", "reply with exactly: ok")
			run := func() (objects, bytes uint64) {
				t.Helper()
				stats, output := fixture.run(ctx, t, fixture.cwd, args...)
				if strings.TrimSpace(output) != "ok" {
					t.Fatalf("pig %v printed %q, want the hermetic provider's reply \"ok\"", args, output)
				}
				requests := server.take()
				if len(requests) != shape.upgrades+1 {
					t.Fatalf("the provider was asked %+v, want %d WebSocket upgrade attempts and one request", requests, shape.upgrades)
				}
				for i, request := range requests {
					if request.upgrade != (i < shape.upgrades) || request.method != map[bool]string{true: http.MethodGet, false: http.MethodPost}[request.upgrade] || !strings.HasSuffix(request.path, shape.wire) || request.authorization != shape.authorization {
						t.Fatalf("request %d of %+v is not the %s request to ...%s with Authorization %q", i, requests, map[bool]string{true: "WebSocket upgrade", false: "streamed"}[request.upgrade], shape.wire, shape.authorization)
					}
				}
				return stats.AllocObjects, stats.AllocBytes
			}
			run() // stages the embedded docs and SDKs
			var bestObjects, bestBytes uint64
			for i := range startupRealModelRuns {
				objects, bytes := run()
				t.Logf("run %d: %d bytes, %d objects", i+1, bytes, objects)
				if i == 0 || objects < bestObjects {
					bestObjects = objects
				}
				if i == 0 || bytes < bestBytes {
					bestBytes = bytes
				}
			}
			perfgate.Check(t, fmt.Sprintf("process.print_real_model.%s.allocs", shape.id), float64(bestObjects))
			perfgate.Check(t, fmt.Sprintf("process.print_real_model.%s.bytes", shape.id), float64(bestBytes))
		})
	}
}
