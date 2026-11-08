package coding

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Ports packages/coding-agent/test/suite/regressions/9221-reload-during-tool.test.ts (Pi c3b938e3e): reload is rejected while the session is running or
// compacting, before session_shutdown or any runner invalidation, so a running tool keeps its context. Each case holds a real operation open (a tool, a manual
// compaction, a tree navigation) through the extension hook or the tool itself, as Pi's test does, and reloads through Session.Reload; the "command" case of
// Pi's it.each, the reload an extension command makes through ctx.reload, runs the real binary in coding/cli (TestRefusedReloadDuringARunningToolKeepsTheExtensionHostLive).

// releasedTool runs until release is closed, then succeeds; started closes when it begins.
type releasedTool struct {
	started chan struct{}
	release chan struct{}
}

func (releasedTool) Name() string                           { return "wait_then_succeed" }
func (releasedTool) Label() string                          { return "Wait then succeed" }
func (releasedTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (releasedTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "wait_then_succeed", Description: "Use the tool context after waiting", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (tool releasedTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(tool.started)
	<-tool.release
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "tool succeeded"}}}, nil
}

// reloadBusyWant is the refusal of a reload during a response or an operation of the session's own.
const (
	reloadBusyResponse   = "Wait for the current response to finish before reloading."
	reloadBusyCompaction = "Wait for compaction to finish before reloading."
)

// TestSessionReloadRejectedWhileRunningOrCompacting is Pi's "reload during an active session operation" suite.
func TestSessionReloadRejectedWhileRunningOrCompacting(t *testing.T) {
	t.Run("rejects direct reload without invalidating an active tool's context", reloadDuringATurn)
	t.Run("rejects reload during compaction and tree navigation", reloadDuringCompactionOrTreeNavigation)
	t.Run("still reloads and replaces the runner when idle", reloadWhenIdle)
}

func reloadDuringATurn(t *testing.T) {
	tool := releasedTool{started: make(chan struct{}), release: make(chan struct{})}
	var shutdowns atomic.Int32
	var providerResults []string
	ext := extension.Extension{Path: "reload-during-tool", Handlers: map[string][]extension.HandlerFn{"session_shutdown": {func(...any) (any, error) {
		shutdowns.Add(1)
		return nil, nil
	}}}}
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true, tools: []agent.AgentTool{tool}, extension: ext}, fauxToolCall("wait_then_succeed"), func(messages []ai.Message) *ai.AssistantMessage {
		for _, message := range messages {
			if result, ok := message.(ai.ToolResultMessage); ok {
				for _, block := range result.Content {
					if text, ok := block.(ai.TextContent); ok {
						providerResults = append(providerResults, text.Text)
					}
				}
			}
		}
		return fauxReply("done", ai.StopReasonStop, 0)(messages)
	})
	runner := h.session.ExtensionRunner()
	prompt := make(chan error, 1)
	go func() { prompt <- h.session.Prompt(t.Context(), "run the tool", nil) }()
	select {
	case <-tool.started:
	case err := <-prompt:
		t.Fatalf("the prompt ended without running the tool: %v", err)
	}
	err := h.session.Reload(t.Context())
	close(tool.release)
	if promptErr := <-prompt; promptErr != nil {
		t.Fatal(promptErr)
	}
	if err == nil || err.Error() != reloadBusyResponse {
		t.Fatalf("Reload during the tool = %v, want %q", err, reloadBusyResponse)
	}
	var persisted []string
	var isError bool
	for _, entry := range h.session.SessionManager().GetEntries() {
		if entry.Base().Type != "message" {
			continue
		}
		var envelope struct {
			Message struct {
				Role    string `json:"role"`
				IsError bool   `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(entry.Raw(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Message.Role == "toolResult" {
			isError = envelope.Message.IsError
			for _, block := range envelope.Message.Content {
				persisted = append(persisted, block.Text)
			}
		}
	}
	if !slices.Equal(persisted, []string{"tool succeeded"}) || isError {
		t.Fatalf("persisted tool results = %q (isError %v), want one successful %q", persisted, isError, "tool succeeded")
	}
	if !slices.Equal(providerResults, persisted) {
		t.Fatalf("the provider saw tool results %q, the session persisted %q", providerResults, persisted)
	}
	if shutdowns.Load() != 0 || runner.IsStale() || h.session.ExtensionRunner() != runner {
		t.Fatalf("a rejected reload touched the runner: shutdowns=%d stale=%v same=%v", shutdowns.Load(), runner.IsStale(), h.session.ExtensionRunner() == runner)
	}
}

func reloadDuringCompactionOrTreeNavigation(t *testing.T) {
	for _, operation := range []string{"compaction", "tree navigation"} {
		t.Run(operation, func(t *testing.T) {
			provider := &scriptedProvider{}
			for range 8 {
				provider.responses = append(provider.responses, fauxReply("complete summary", ai.StopReasonStop, 0))
			}
			model := fakeModelWithProvider(provider)
			model.ID = "faux-1"
			model.Capabilities.ContextWindow = 200000
			model.Capabilities.MaxOutputTokens = 8192
			s, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model, SystemPrompt: "You are a helpful assistant. Be concise."})
			if err != nil {
				t.Fatal(err)
			}
			_ = s.Events()
			t.Cleanup(func() { _ = s.Close() })
			started, release := make(chan struct{}), make(chan struct{})
			var shutdowns atomic.Int32
			hold := func(...any) (any, error) {
				close(started)
				<-release
				return extension.SessionBeforeCompactResult{Cancel: true}, nil
			}
			holdTree := func(...any) (any, error) {
				close(started)
				<-release
				return extension.SessionBeforeTreeResult{Cancel: true}, nil
			}
			runner := inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{
				"session_shutdown":       {func(...any) (any, error) { shutdowns.Add(1); return nil, nil }},
				"session_before_compact": {hold},
				"session_before_tree":    {holdTree},
			}}}, t.TempDir())
			s.ReplaceRunner(runner)
			for _, prompt := range []string{"What is 2+2? Reply with just the number.", "What is 3+3? Reply with just the number."} {
				if _, err := s.Send(t.Context(), prompt); err != nil {
					t.Fatal(err)
				}
				drainEvents(t, s)
			}
			target := ""
			for _, entry := range s.inner.GetEntries() {
				if entry.Base().Type == "message" {
					target = entry.Base().ID
					break
				}
			}
			if target == "" {
				t.Fatal("no message entry to navigate to")
			}
			finished := make(chan error, 1)
			go func() {
				var err error
				if operation == "compaction" {
					_, err = s.Compact(t.Context(), "")
				} else {
					_, err = s.NavigateTree(t.Context(), target, NavigateTreeOptions{})
				}
				finished <- err
			}()
			select {
			case <-started:
			case err := <-finished:
				t.Fatalf("%s ended without reaching its extension hook: %v", operation, err)
			}
			if s.IsStreaming() {
				t.Fatal("the session streams during the held operation")
			}
			err = s.Reload(t.Context())
			close(release)
			<-finished
			if err == nil || err.Error() != reloadBusyCompaction {
				t.Fatalf("Reload during %s = %v, want %q", operation, err, reloadBusyCompaction)
			}
			if shutdowns.Load() != 0 || runner.IsStale() || s.ExtensionRunner() != runner {
				t.Fatalf("a rejected reload touched the runner: shutdowns=%d stale=%v same=%v", shutdowns.Load(), runner.IsStale(), s.ExtensionRunner() == runner)
			}
			if s.IsCompacting() {
				t.Fatal("the session still reports an operation after it ended")
			}
		})
	}
}

func reloadWhenIdle(t *testing.T) {
	root := t.TempDir()
	shutdowns := 0
	runner := inproc.NewRunner([]extension.Extension{{Path: "ext", Handlers: map[string][]extension.HandlerFn{
		"session_shutdown": {func(...any) (any, error) { shutdowns++; return nil, nil }},
	}}}, root)
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
	})
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: runner, ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Reload(t.Context()); err != nil {
		t.Fatalf("idle Reload: %v", err)
	}
	if shutdowns != 1 || !runner.IsStale() || session.ExtensionRunner() == runner {
		t.Fatalf("an idle reload must shut the old runner down and replace it: shutdowns=%d stale=%v same=%v", shutdowns, runner.IsStale(), session.ExtensionRunner() == runner)
	}
}
