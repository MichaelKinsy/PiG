package coding

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// reloadGateTool waits for release, then reports whether its context is still live, as Pi's wait_then_succeed uses its tool context after the wait.
type reloadGateTool struct {
	started chan struct{}
	release chan struct{}
}

func (reloadGateTool) Name() string                           { return "wait_then_succeed" }
func (reloadGateTool) Label() string                          { return "Wait then succeed" }
func (reloadGateTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (reloadGateTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "wait_then_succeed", Description: "Use the tool context after waiting", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (tool reloadGateTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(tool.started)
	<-tool.release
	if err := ctx.Err(); err != nil {
		return agent.AgentToolResult{}, err
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool succeeded"}}}, nil
}

// Ports the "direct" source of 9221-reload-during-tool.test.ts (Pi c3b938e3e): Session.Reload called while a real tool runs is rejected without
// shutting the runner down, the tool finishes with its result persisted as a success equal to what the provider saw, and the runner and its active tools stay.
func TestSessionReloadDuringARunningToolIsRejectedAndTheToolSucceeds(t *testing.T) {
	tool := reloadGateTool{started: make(chan struct{}), release: make(chan struct{})}
	var mu sync.Mutex
	shutdowns := 0
	ext := extension.Extension{Path: "reload-during-tool", Handlers: map[string][]extension.HandlerFn{"session_shutdown": {func(...any) (any, error) {
		mu.Lock()
		shutdowns++
		mu.Unlock()
		return nil, nil
	}}}}
	var providerResults []ai.Message
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true, tools: []agent.AgentTool{tool}, extension: ext},
		fauxToolCall("wait_then_succeed"),
		func(request []ai.Message) *ai.AssistantMessage {
			for _, message := range request {
				if message.MessageRole() == "toolResult" {
					providerResults = append(providerResults, message)
				}
			}
			return fauxReply("done", ai.StopReasonStop, 0)(request)
		})
	runner := h.session.ExtensionRunner()
	prompt := make(chan error, 1)
	go func() { prompt <- h.session.Prompt(t.Context(), "run the tool", nil) }()
	select {
	case <-tool.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool never started")
	}
	reloadErr := h.session.Reload(t.Context())
	close(tool.release)
	if err := <-prompt; err != nil {
		t.Fatal(err)
	}
	if reloadErr == nil || reloadErr.Error() != "Wait for the current response to finish before reloading." {
		t.Fatalf("Reload during the tool = %v, want the running-session rejection", reloadErr)
	}
	var persisted []json.RawMessage
	for _, entry := range h.session.Entries() {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		var role struct {
			Role string `json:"role"`
		}
		if fields.Type == "message" && json.Unmarshal(fields.Message, &role) == nil && role.Role == "toolResult" {
			persisted = append(persisted, fields.Message)
		}
	}
	if len(persisted) != 1 {
		t.Fatalf("persisted tool results = %d, want 1", len(persisted))
	}
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(persisted[0], &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Text != "tool succeeded" {
		t.Fatalf("persisted tool result = %s, want a success with the text %q", persisted[0], "tool succeeded")
	}
	if len(providerResults) != 1 {
		t.Fatalf("the provider saw %d tool results, want 1", len(providerResults))
	}
	if seen, err := json.Marshal(providerResults[0]); err != nil || !jsonEqual(t, seen, persisted[0]) {
		t.Fatalf("the provider saw %s (err %v), persisted %s: want them equal", seen, err, persisted[0])
	}
	mu.Lock()
	defer mu.Unlock()
	if shutdowns != 0 || h.session.ExtensionRunner() != runner || runner.IsStale() {
		t.Fatalf("the rejected reload touched the runner: shutdowns=%d same=%v stale=%v", shutdowns, h.session.ExtensionRunner() == runner, runner.IsStale())
	}
	if !slices.Contains(h.session.ActiveToolNames(), "wait_then_succeed") {
		t.Fatalf("active tools = %v, want wait_then_succeed", h.session.ActiveToolNames())
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var left, right any
	if err := json.Unmarshal(a, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &right); err != nil {
		t.Fatal(err)
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

// Ports "still reloads and replaces the runner when idle" of 9221-reload-during-tool.test.ts: the reload shuts the old runner down once, the
// rebuilt runner's extension gets session_start with reason "reload", and the session holds a new runner while the old one is stale.
func TestIdleSessionReloadReplacesTheRunnerAndStartsTheExtensionsWithReasonReload(t *testing.T) {
	root := t.TempDir()
	shutdowns := 0
	runner := inproc.NewRunner([]extension.Extension{{Path: "ext", Handlers: map[string][]extension.HandlerFn{
		"session_shutdown": {func(...any) (any, error) { shutdowns++; return nil, nil }},
	}}}, root)
	var mu sync.Mutex
	var reasons []string
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		ExtensionFactories: []extension.InlineExtension{extension.ExtensionFactory(func(pi extension.API) error {
			pi.OnSessionStart(func(_ context.Context, event extension.SessionStartEvent) error {
				mu.Lock()
				reasons = append(reasons, event.Reason)
				mu.Unlock()
				return nil
			})
			return nil
		})},
	})
	t.Cleanup(loader.Close)
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: runner, ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	// A mode that binds an error listener replays session_start on reload (agent-session.ts:3688-3698).
	if err := session.BindExtensions(t.Context(), ExtensionBindings{OnError: func(*extension.ExtensionError) {}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	reasons = nil
	mu.Unlock()
	if err := session.Reload(t.Context()); err != nil {
		t.Fatalf("idle Reload: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if shutdowns != 1 || !runner.IsStale() || session.ExtensionRunner() == runner {
		t.Fatalf("an idle reload must shut the old runner down and replace it: shutdowns=%d stale=%v replaced=%v", shutdowns, runner.IsStale(), session.ExtensionRunner() != runner)
	}
	if !slices.Equal(reasons, []string{"reload"}) {
		t.Fatalf("session_start reasons after the reload = %v, want [reload]", reasons)
	}
}
