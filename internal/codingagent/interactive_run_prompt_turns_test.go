package codingagent_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// toolTurnProvider answers the first request with a read tool call and every later one with text, recording each request's system prompt.
type toolTurnProvider struct {
	mu      sync.Mutex
	prompts []string
}

func (p *toolTurnProvider) ID() string   { return "faux" }
func (p *toolTurnProvider) Close() error { return nil }

func (p *toolTurnProvider) Stream(_ context.Context, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, ai.GetCurrentSystemPrompt(request.Messages()))
	first := len(p.prompts) == 1
	p.mu.Unlock()
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	if first {
		message.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "read", Arguments: ai.JsonObject{"path": "missing.txt"}}}
		message.StopReason = ai.StopReasonToolUse
	}
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: message})
	_ = stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message})
	return stream, nil
}

func (p *toolTurnProvider) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.prompts)
}

// agent-session.ts:864-883 (_installAgentNextTurnRefresh) rebuilds every later turn of a run from the run options with the base snippets and guidelines merged under the run's, and interactive mode prompts through the same AgentSession. A snippet a handler deleted is absent from the first request and listed again in the next turn's request; an edited guideline stays for every turn.
func TestInteractiveRunPromptRefreshesBaseSnippetsBeforeLaterTurns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	services, err := coding.NewServices(coding.ServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	ext := extension.Extension{Name: "edit", Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		options := extension.BeforeAgentStartOptions(args[1].(context.Context))
		delete(options.ToolSnippets, "read")
		options.ToolGuidelines["bash"] = []string{"RUN-GUIDELINE-MARK"}
		return nil, nil
	}}}}
	runner := inproc.NewRunner([]extension.Extension{ext}, cwd)
	provider := &toolTurnProvider{}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	options := extension.BuildSystemPromptOptions{Cwd: cwd, SelectedTools: session.ActiveToolNames(), ToolSnippets: prompts.DefaultToolSnippets()}
	h := icodingagent.NewTestHarness(t, icodingagent.InteractiveOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner,
		SystemPromptOptions: options, SystemPrompt: prompts.BuildDefaultPrompt(prompts.FromExtensionOptions(options)),
	}, nil)
	h.Do(func() { h.Enter("one") })
	deadline := time.Now().Add(10 * time.Second)
	for len(provider.requests()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	h.WaitIdle(t, 10*time.Second)
	requests := provider.requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if strings.Contains(requests[0], "\n- read: ") || !strings.Contains(requests[0], "RUN-GUIDELINE-MARK") {
		t.Fatalf("first request ignores the handler's edits:\n%s", requests[0])
	}
	if !strings.Contains(requests[1], "\n- read: ") || !strings.Contains(requests[1], "RUN-GUIDELINE-MARK") {
		t.Fatalf("the next turn does not refresh the base snippets under the run's edits:\n%s", requests[1])
	}
}
