package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// codingProvider is an OpenAI-compatible endpoint that answers the first request with a read tool call for notes.txt and the second with text, and records every request body.
type codingProvider struct {
	server *httptest.Server
	api    string
	mu     sync.Mutex
	bodies []string
}

func newCodingProvider(t *testing.T, api string) *codingProvider {
	t.Helper()
	provider := &codingProvider{api: api}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		provider.mu.Lock()
		provider.bodies = append(provider.bodies, string(body))
		first := len(provider.bodies) == 1
		provider.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case api == "openai-completions" && first:
			_, _ = fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"notes.txt\"}"}}]},"finish_reason":null}]}`+"\n\n"+`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
		case api == "openai-completions":
			_, _ = fmt.Fprint(w, `data: {"id":"y","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"read it"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
		case first:
			_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}`+"\n\n"+
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"notes.txt\"}"}}`+"\n\n"+
				`data: {"type":"response.completed","response":{"id":"r1","status":"completed"}}`+"\n\n")
		default:
			_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m1","content":[]}}`+"\n\n"+
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","content":[{"type":"output_text","text":"read it"}]}}`+"\n\n"+
				`data: {"type":"response.completed","response":{"id":"r2","status":"completed"}}`+"\n\n")
		}
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func (provider *codingProvider) requests() []string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]string{}, provider.bodies...)
}

// configureCodingProvider points a fresh agent directory at provider with two models and a saved default.
func configureCodingProvider(t *testing.T, agentDir string, provider *codingProvider, defaultModel string) {
	t.Helper()
	models := []any{}
	for _, id := range []string{"model", "other"} {
		models = append(models, map[string]any{"id": id, "name": id, "reasoning": false, "input": []string{"text"}, "contextWindow": 32000, "maxTokens": 1234})
	}
	encoded, err := json.Marshal(map[string]any{"providers": map[string]any{"worker-factory": map[string]any{"baseUrl": provider.server.URL + "/v1", "api": provider.api, "apiKey": "fixture-key", "models": models}}})
	if err != nil {
		t.Fatal(err)
	}
	writeNodeFacetFile(t, filepath.Join(agentDir, "models.json"), string(encoded))
	writeNodeFacetFile(t, filepath.Join(agentDir, "settings.json"), `{"defaultProvider":"worker-factory","defaultModel":"`+defaultModel+`"}`)
}

func workerOptions(project string) SessionWorkerOptions {
	return SessionWorkerOptions{Metadata: SessionCatalogMetadata{ID: "session", Cwd: project, Path: filepath.Join(project, "session")}}
}

func agentModel(t *testing.T, agent services.AgentDocument) string {
	t.Helper()
	defer agent.Dispose()
	state := agent.Value()
	if state == nil || state.Model == nil {
		t.Fatalf("agent state = %+v, want a model", state)
	}
	return state.Model.Provider + "/" + state.Model.ModelId
}

// session-worker.ts createCodingAgentHarness, through the whole production path: the worker's Harness runs pi's read tool in the project directory against a real provider endpoint, sends pi's system prompt, and keeps its durable model on a later start. Both OpenAI-compatible API kinds carry the same request content.
func TestCreateCodingAgentHarnessRunsPiToolsAndPromptOverAProvider(t *testing.T) {
	for _, api := range []string{"openai-completions", "openai-responses"} {
		t.Run(api, func(t *testing.T) {
			agentDir := isolateExperimentalTest(t)
			provider := newCodingProvider(t, api)
			configureCodingProvider(t, agentDir, provider, "model")
			project := t.TempDir()
			writeNodeFacetFile(t, filepath.Join(project, "notes.txt"), "durable notes")
			writeNodeFacetFile(t, filepath.Join(project, "AGENTS.md"), "project convention: tabs")
			database := filepath.Join(t.TempDir(), "session.sqlite")

			runtime, err := createCodingAgentHarness(t.Context(), database, workerOptions(project))
			if err != nil {
				t.Fatal(err)
			}
			id, err := runtime.Conversation.Submit(t.Context(), ai.UserText("read the notes"), durable.WhenBusySteer)
			if err != nil {
				t.Fatal(err)
			}
			submission, err := runtime.Harness.Submission(t.Context(), id)
			if err != nil || submission == nil {
				t.Fatalf("submission = %v, %v", submission, err)
			}
			settled, err := submission.Wait(t.Context())
			if err != nil || settled.Unanswered {
				t.Fatalf("settled = %+v, %v", settled, err)
			}
			requests := provider.requests()
			if len(requests) != 2 {
				t.Fatalf("provider saw %d requests, want the tool call and the answer", len(requests))
			}
			// Pi's prompt, with the PiG identity and the project context, and pi's four coding tools.
			for _, want := range []string{"operating inside pig, a coding agent harness", "project convention: tabs", `\u003ctools\u003e`} {
				if !strings.Contains(requests[0], want) && !strings.Contains(requests[0], strings.ReplaceAll(want, `\u003c`, "<")) {
					t.Errorf("first request lacks %q:\n%s", want, requests[0])
				}
			}
			if strings.Contains(requests[0], "operating inside pi,") {
				t.Errorf("the prompt names Pi as its own identity:\n%s", requests[0])
			}
			for _, tool := range []string{"read", "write", "edit", "bash"} {
				if !strings.Contains(requests[0], `"name":"`+tool+`"`) {
					t.Errorf("first request does not offer the %s tool", tool)
				}
			}
			// The read tool ran in the project directory and its result went back to the model.
			if !strings.Contains(requests[1], "durable notes") {
				t.Errorf("second request lacks the read result:\n%s", requests[1])
			}
			if err := errors.Join(runtime.Harness.Close(t.Context()), runtime.Cleanup(t.Context())); err != nil {
				t.Fatal(err)
			}

			// A later start keeps the root conversation's durable model, whatever the saved default says now.
			configureCodingProvider(t, agentDir, provider, "other")
			again, err := createCodingAgentHarness(t.Context(), database, workerOptions(project))
			if err != nil {
				t.Fatal(err)
			}
			agent, err := again.Harness.AgentDocument(t.Context(), again.Conversation.ID())
			if err != nil || agent == nil {
				t.Fatalf("agent document = %v, %v", agent, err)
			}
			if got := agentModel(t, agent); got != "worker-factory/model" {
				t.Errorf("reopened model = %s, want worker-factory/model", got)
			}
			if err := errors.Join(again.Harness.Close(t.Context()), again.Cleanup(t.Context())); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// session-worker.ts createCodingAgentHarness: a new root conversation starts with the model the saved default selects.
func TestCreateCodingAgentHarnessStartsWithTheSavedDefaultModel(t *testing.T) {
	agentDir := isolateExperimentalTest(t)
	provider := newCodingProvider(t, "openai-completions")
	configureCodingProvider(t, agentDir, provider, "other")
	runtime, err := createCodingAgentHarness(t.Context(), filepath.Join(t.TempDir(), "session.sqlite"), workerOptions(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = errors.Join(runtime.Harness.Close(context.Background()), runtime.Cleanup(context.Background()))
	}()
	agent, err := runtime.Harness.AgentDocument(t.Context(), runtime.Conversation.ID())
	if err != nil || agent == nil {
		t.Fatalf("agent document = %v, %v", agent, err)
	}
	if got := agentModel(t, agent); got != "worker-factory/other" {
		t.Fatalf("model = %s, want worker-factory/other", got)
	}
}

// harness-setup.ts:98-103 through the factory: an explicit model that does not resolve fails startup, and the failed open leaves no Harness behind to hold the Session storage.
func TestCreateCodingAgentHarnessFailsStartupOnAnUnresolvableModel(t *testing.T) {
	agentDir := isolateExperimentalTest(t)
	configureCodingProvider(t, agentDir, newCodingProvider(t, "openai-completions"), "model")
	options := workerOptions(t.TempDir())
	options.Provider, options.Model = "", "nowhere/missing"
	database := filepath.Join(t.TempDir(), "session.sqlite")
	_, err := createCodingAgentHarness(t.Context(), database, options)
	if err == nil || !strings.HasPrefix(err.Error(), "Could not resolve model: ") {
		t.Fatalf("error = %v, want Could not resolve model", err)
	}
	// Startup failed after the Harness opened, so cleanup closed it and its storage.
	if open := openFileDescriptors(t, database); open != 0 {
		t.Fatalf("%d descriptors still open on %s after the failed startup", open, database)
	}
	options.Provider, options.Model = "", ""
	runtime, err := createCodingAgentHarness(t.Context(), database, options)
	if err != nil {
		t.Fatalf("reopen after the failed startup: %v", err)
	}
	if err := errors.Join(runtime.Harness.Close(t.Context()), runtime.Cleanup(t.Context())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(database); err != nil {
		t.Fatal(err)
	}
}

// openFileDescriptors counts this process's open descriptors on path (Linux only: the count is read from /proc/self/fd).
func openFileDescriptors(t *testing.T, path string) int {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("open descriptors are counted through /proc/self/fd")
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name())); err == nil && strings.HasPrefix(target, path) {
			count++
		}
	}
	return count
}

// session-worker.ts:782-800: the initial model resolves only when the root conversation is created, so a later start does not fail on a model that no longer resolves.
func TestCreateCodingAgentHarnessDoesNotResolveTheInitialModelForAnExistingRoot(t *testing.T) {
	agentDir := isolateExperimentalTest(t)
	configureCodingProvider(t, agentDir, newCodingProvider(t, "openai-completions"), "model")
	project := t.TempDir()
	database := filepath.Join(t.TempDir(), "session.sqlite")
	first, err := createCodingAgentHarness(t.Context(), database, workerOptions(project))
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(first.Harness.Close(t.Context()), first.Cleanup(t.Context())); err != nil {
		t.Fatal(err)
	}
	options := workerOptions(project)
	options.Provider, options.Model = "", "nowhere/missing"
	again, err := createCodingAgentHarness(t.Context(), database, options)
	if err != nil {
		t.Fatalf("a later start must not resolve the initial model: %v", err)
	}
	if err := errors.Join(again.Harness.Close(t.Context()), again.Cleanup(t.Context())); err != nil {
		t.Fatal(err)
	}
}
