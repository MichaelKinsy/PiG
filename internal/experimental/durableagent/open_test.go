package durableagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// provider is an OpenAI-compatible completions endpoint whose answers a test scripts: handle receives the number of the request and its body.
type provider struct {
	server *httptest.Server
	mu     sync.Mutex
	bodies []string
	handle func(n int, body string, w http.ResponseWriter, r *http.Request)
}

func newProvider(t *testing.T, handle func(n int, body string, w http.ResponseWriter, r *http.Request)) *provider {
	t.Helper()
	p := &provider{handle: handle}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.bodies = append(p.bodies, string(body))
		n := len(p.bodies)
		p.mu.Unlock()
		p.handle(n, string(body), w, r)
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *provider) requests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.bodies)
}

func sseText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`+"\n\ndata: [DONE]\n\n", text)
}

func sseToolCall(w http.ResponseWriter, name, id, arguments string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}`+"\n\n"+`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n", id, name, arguments)
}

// isolate points the agent directory, the home and the offline switch at fresh directories and writes models.json with two models of the provider, "model" and "reasoner" (which reasons), and the saved default.
func isolate(t *testing.T, p *provider, defaultModel string) string {
	t.Helper()
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	for key, value := range map[string]string{"HOME": root, "USERPROFILE": root, "PIG_HOME": filepath.Join(root, "pig"), "PIG_CODING_AGENT_DIR": agentDir, "PIG_USE_PI_DIRS": "", "PI_OFFLINE": "1"} {
		t.Setenv(key, value)
	}
	for _, name := range []string{"PIG_SESSION_DIR", "HTTP_PROXY", "HTTPS_PROXY"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	writeModels(t, agentDir, p)
	writeDefault(t, agentDir, defaultModel)
	return agentDir
}

func writeModels(t *testing.T, agentDir string, p *provider) {
	t.Helper()
	models := []any{
		map[string]any{"id": "model", "name": "Plain model", "reasoning": false, "input": []string{"text"}, "contextWindow": 32000, "maxTokens": 1234},
		map[string]any{"id": "reasoner", "name": "Reasoning model", "reasoning": true, "input": []string{"text"}, "contextWindow": 64000, "maxTokens": 1234},
	}
	encoded, err := json.Marshal(map[string]any{"providers": map[string]any{"scripted": map[string]any{"baseUrl": p.server.URL + "/v1", "api": "openai-completions", "apiKey": "fixture-key", "models": models}}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(agentDir, "models.json"), string(encoded))
}

func writeDefault(t *testing.T, agentDir, model string) {
	t.Helper()
	write(t, filepath.Join(agentDir, "settings.json"), `{"defaultProvider":"scripted","defaultModel":"`+model+`"}`)
}

// openSession opens a new session of the coding profile in a fresh project directory, closed when the test ends.
func openSession(t *testing.T, project string, continueSession bool) *OpenDurableResult {
	t.Helper()
	session, err := Open(context.Background(), OpenDurableOptions{CWD: project, ContinueSession: continueSession}, CodingProfile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func finished(completion Completion) bool {
	select {
	case <-completion:
		return true
	default:
		return false
	}
}

func awaitCompletion(t *testing.T, completion Completion) {
	t.Helper()
	select {
	case <-completion:
	case <-time.After(20 * time.Second):
		t.Fatal("a command did not finish")
	}
}

func notices(session *OpenDurableResult) []string {
	var messages []string
	for _, notice := range session.View.Current().Notices {
		messages = append(messages, string(notice.Level)+": "+notice.Message)
	}
	return messages
}

func assistantAnswers(view DurableView) []string {
	return assistantTexts(view.Conversation.Entries)
}

func assistantTexts(entries []durable.EntryRecord) []string {
	var texts []string
	for _, entry := range entries {
		if len(entry.Model) == 0 {
			continue
		}
		if message, ok := entry.Model[0].(ai.AssistantMessage); ok {
			for _, content := range message.Content {
				if block, ok := content.(ai.TextContent); ok {
					texts = append(texts, block.Text)
				}
			}
		}
	}
	return texts
}

func agentOfView(session *OpenDurableResult) harness.AgentState {
	return AgentOf(session.View.Current().Conversation)
}

func contains(values []string, want string) bool {
	return slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, want) })
}
