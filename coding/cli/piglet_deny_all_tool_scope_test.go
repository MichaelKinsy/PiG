package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

// newAnthropicScopeProvider is an Anthropic Messages endpoint that records the tool names of each request it receives
// and answers with a short text reply.
func newAnthropicScopeProvider(t *testing.T) *scopeProvider {
	t.Helper()
	provider := &scopeProvider{recorded: make(chan struct{}, 1)}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	provider.url = "http://" + listener.Addr().String()
	provider.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		names := []string{}
		for _, tool := range body.Tools {
			names = append(names, tool.Name)
		}
		provider.record(names)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range [][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"model":"fake","usage":{"input_tokens":1,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event[0], event[1])
		}
	})}
	go func() { _ = provider.server.Serve(listener) }()
	t.Cleanup(func() { _ = provider.server.Close() })
	return provider
}

// An extension that denies every tool with setActiveTools([]) keeps the model request free of tools when a Piglet is
// active, in every mode and for each provider API. Pi applies exactly the requested names (agent-session.ts
// setActiveToolsByName) and nothing re-widens them. The Piglet reapplies its scope on before_agent_start; it read the
// empty selection as "no earlier narrowing" and offered its full scope again. Each mode binds its own getActiveTools, so
// each must report the empty selection as a non-nil list, and each provider API must then declare no tools.
func TestPigletKeepsAnExtensionsDenyAllSelectionInEveryMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Node extensions")
	}
	t.Parallel()
	for _, api := range []struct {
		name     string
		provider func(*testing.T) *scopeProvider
	}{
		{"openai-completions", newScopeProvider},
		{"anthropic-messages", newAnthropicScopeProvider},
	} {
		t.Run(api.name, func(t *testing.T) {
			for _, mode := range []string{"print", "json", "rpc", "interactive"} {
				t.Run(mode, func(t *testing.T) {
					provider := api.provider(t)
					home, piglet := scopeFixture(t, provider, "")
					writeStartupFixtureFile(t, filepath.Join(home, "agent", "models.json"), fmt.Sprintf(`{"providers":{"fake":{"baseUrl":%q,"api":%q,"apiKey":"synthetic-test-key","models":[{"id":"fake","name":"Fake","reasoning":false,"input":["text"],"contextWindow":128000,"maxTokens":1024,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, provider.url, api.name))
					writeStartupFixtureFile(t, filepath.Join(piglet, "scoped.mjs"), `export default function (pi) {
  pi.registerTool({ name: "scoped_tool", label: "Scoped", description: "scoped", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });
  pi.on("session_start", () => { pi.setActiveTools([]); });
}
`)
					if names := firstRequestTools(t, provider, home, piglet, mode); len(names) != 0 {
						t.Errorf("a deny-all selection offered %v, want no tools", names)
					}
				})
			}
		})
	}
}
