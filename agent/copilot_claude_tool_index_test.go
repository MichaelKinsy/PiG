package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type recordingTool struct {
	name string
	mu   *sync.Mutex
	args *[]string
}

func (t recordingTool) Name() string        { return t.name }
func (t recordingTool) Label() string       { return "" }
func (t recordingTool) Description() string { return t.name }
func (t recordingTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: t.name, Parameters: map[string]any{"type": "object"}}
}
func (t recordingTool) ExecutionMode() ToolExecutionMode { return ToolModeSequential }
func (t recordingTool) Execute(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
	t.mu.Lock()
	*t.args = append(*t.args, t.name+" "+string(args))
	t.mu.Unlock()
	return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
}

// A turn whose Copilot Claude stream numbers tool calls by content block (first
// call at index 1 or 2, never 0, or descending) must still execute every call
// with its complete arguments, in arrival order. The fixtures are hand-written
// synthetic streams with that structure.
func TestOpenAICompletionsCopilotClaudeContentBlockIndexesExecute(t *testing.T) {
	const textSSE = "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"index\":0}],\"id\":\"msg_2\"}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\",\"index\":0}],\"id\":\"msg_2\"}\n\ndata: [DONE]\n\n"
	const read = `read {"path":"src/a.go"}`
	const grep = `grep {"pattern":"main","path":"src"}`
	for _, tc := range []struct {
		name, fixture string
		want          []string
	}{
		{"index 1", "copilot_claude_idx1.sse", []string{read}},
		{"text then index 2", "copilot_claude_idx2_text.sse", []string{read}},
		{"indexes 1 and 2", "copilot_claude_idx12.sse", []string{read, grep}},
		{"text then indexes 2 and 3", "copilot_claude_idx23_text.sse", []string{read, grep}},
		{"indexes 2 then 1", "copilot_claude_idx21_descending.sse", []string{read, grep}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, err := os.ReadFile("../ai/testdata/" + tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				if requests.Add(1) == 1 {
					_, _ = w.Write(fixture)
					return
				}
				_, _ = io.WriteString(w, textSSE)
			}))
			defer server.Close()

			var mu sync.Mutex
			var executed []string
			var tools []AgentTool
			for _, name := range []string{"read", "grep"} {
				tools = append(tools, recordingTool{name: name, mu: &mu, args: &executed})
			}
			provider := ai.NewOpenAIProvider(ai.OpenAIConfig{Model: "claude-synthetic", BaseURL: server.URL, APIKey: "k", ProviderID: "openai"})
			agent := mustNewAgent(AgentOptions{Model: fakeTestModel(provider), Tools: tools, MaxTurns: 5})
			if _, err := agent.Send(context.Background(), "go"); err != nil {
				t.Fatalf("Send: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(executed) != len(tc.want) {
				t.Fatalf("executed %q, want %q (%d provider requests)", executed, tc.want, requests.Load())
			}
			for i, want := range tc.want {
				if !strings.EqualFold(executed[i], want) {
					t.Fatalf("execution %d = %q, want %q", i, executed[i], want)
				}
			}
		})
	}
}
