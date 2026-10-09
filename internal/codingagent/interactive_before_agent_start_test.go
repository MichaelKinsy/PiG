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

// loadoutProvider records the tools and system prompt of every request.
type loadoutProvider struct {
	mu      sync.Mutex
	tools   [][]string
	prompts []string
}

func (p *loadoutProvider) ID() string   { return "faux" }
func (p *loadoutProvider) Close() error { return nil }

func (p *loadoutProvider) Stream(_ context.Context, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	messages := request.Messages()
	names := []string{}
	for _, tool := range ai.GetCurrentTools(messages) {
		names = append(names, tool.Name)
	}
	p.mu.Lock()
	p.tools = append(p.tools, names)
	p.prompts = append(p.prompts, ai.GetCurrentSystemPrompt(messages))
	p.mu.Unlock()
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: message})
	_ = stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message})
	return stream, nil
}

func (p *loadoutProvider) requests() ([][]string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.tools), slices.Clone(p.prompts)
}

// agent-session.ts:1702-1714,1747 with 1409-1419 and 1485: interactive prompts run through AgentSession.prompt. The run's prompt is built after before_agent_start from the handlers' options with the resulting live tools: an edited selectedTools list becomes the loadout and stays active, per-run sections end with the run, and a setActiveTools call inside a handler is reflected when the list is unchanged. Handlers receive the base options, which an edit leaves unchanged.
func TestInteractiveBeforeAgentStartControlsRunPromptAndLoadout(t *testing.T) {
	full := []string{"read", "bash", "edit", "write"}
	for _, tc := range []struct {
		name   string
		handle func(context.Context, *extension.BuildSystemPromptOptions)
		// seen is the selectedTools list the second event carries: an edit keeps the base options, while setActiveTools rebuilds them (agent-session.ts:1385).
		seen []string
	}{
		{"edited options", func(_ context.Context, options *extension.BuildSystemPromptOptions) {
			*options.Sections = append(*options.Sections, ai.PromptSection{Name: "plan_mode", Value: new("Plan only.")})
			options.SelectedTools = []string{"read", "read", "missing"}
		}, full},
		{"live setActiveTools", func(ctx context.Context, options *extension.BuildSystemPromptOptions) {
			*options.Sections = append(*options.Sections, ai.PromptSection{Name: "plan_mode", Value: new("Plan only.")})
			extension.FromContext(ctx).SetActiveTools([]string{"read"})
		}, []string{"read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			cwd := t.TempDir()
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			// Pi prompt() requires usable auth for the model's provider before it runs.
			if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
				t.Fatal(err)
			}
			var seen [][]string
			var idlePrompts []string
			settled := make(chan struct{}, 2)
			ext := extension.Extension{Name: "edit", Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
				event := args[0].(extension.BeforeAgentStartEvent)
				seen = append(seen, slices.Clone(event.SystemPromptOptions.SelectedTools))
				if event.Prompt == "one" {
					tc.handle(args[1].(context.Context), extension.BeforeAgentStartOptions(args[1].(context.Context)))
				}
				return nil, nil
			}}, "agent_settled": {func(args ...any) (any, error) {
				text, err := extension.FromContext(args[1].(context.Context)).GetSystemPrompt()
				if err != nil {
					return nil, err
				}
				idlePrompts = append(idlePrompts, text)
				settled <- struct{}{}
				return nil, nil
			}}}}
			runner := inproc.NewRunner([]extension.Extension{ext}, cwd)
			provider := &loadoutProvider{}
			model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
			session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			base := session.ActiveToolNames()
			if !slices.Equal(base, full) {
				t.Fatalf("base tools=%v", base)
			}
			options := extension.BuildSystemPromptOptions{Cwd: cwd, SelectedTools: base, ToolSnippets: prompts.DefaultToolSnippets()}
			h := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{
				CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session,
				SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner,
				SystemPromptOptions: options, SystemPrompt: prompts.BuildDefaultPrompt(prompts.FromExtensionOptions(options)),
			}, nil)
			for i, text := range []string{"one", "two"} {
				h.Do(func() { h.Enter(text) })
				deadline := time.Now().Add(10 * time.Second)
				for tools, _ := provider.requests(); len(tools) <= i && time.Now().Before(deadline); tools, _ = provider.requests() {
					time.Sleep(10 * time.Millisecond)
				}
				h.WaitIdle(t, 10*time.Second)
				select {
				case <-settled:
				case <-time.After(10 * time.Second):
					t.Fatal("agent_settled did not finish")
				}
			}
			tools, requestPrompts := provider.requests()
			if !slices.EqualFunc(tools, [][]string{{"read"}, {"read"}}, slices.Equal) {
				t.Fatalf("provider tools=%v", tools)
			}
			const section = "<plan_mode>\nPlan only.\n</plan_mode>"
			// agent-session.ts:1409-1419 admits the edited list through new Set before rendering the run prompt, so the duplicate "read" is listed once.
			if count := strings.Count(requestPrompts[0], "- read: "); count != 1 {
				t.Fatalf("first request lists read %d times: %q", count, requestPrompts[0])
			}
			if !strings.Contains(requestPrompts[0], section) || !strings.Contains(requestPrompts[0], "- read: Read file contents") || strings.Contains(requestPrompts[0], "- bash:") {
				t.Fatalf("first request prompt=%q", requestPrompts[0])
			}
			if strings.Contains(requestPrompts[1], section) || !strings.Contains(requestPrompts[1], "- read: Read file contents") || strings.Contains(requestPrompts[1], "- bash:") {
				t.Fatalf("second request prompt=%q", requestPrompts[1])
			}
			for _, text := range idlePrompts {
				if strings.Contains(text, section) || strings.Contains(text, "- bash:") != slices.Contains(tc.seen, "bash") {
					t.Fatalf("idle prompt differs from base options: %q", text)
				}
			}
			if len(idlePrompts) != 2 {
				t.Fatalf("settled prompts=%v", idlePrompts)
			}
			if !slices.EqualFunc(seen, [][]string{full, tc.seen}, slices.Equal) {
				t.Fatalf("handlers saw selectedTools=%v, want %v then %v", seen, full, tc.seen)
			}
		})
	}
}

// agent-session.ts:1409-1419 applies the deduplicated selection before buildSystemPromptSections validates the section names, so an interactive prompt rejected for an invalid section still changes the loadout and sends no request.
func TestInteractiveInvalidSectionRejectionKeepsAdmittedLoadout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	ext := extension.Extension{Name: "edit", Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		options := extension.BeforeAgentStartOptions(args[1].(context.Context))
		options.SelectedTools = []string{"read", "read", "missing"}
		*options.Sections = append(*options.Sections, ai.PromptSection{Name: "preamble", Value: new("invalid")})
		return nil, nil
	}}}}
	runner := inproc.NewRunner([]extension.Extension{ext}, cwd)
	provider := &loadoutProvider{}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	options := extension.BuildSystemPromptOptions{Cwd: cwd, SelectedTools: session.ActiveToolNames(), ToolSnippets: prompts.DefaultToolSnippets()}
	h := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner,
		SystemPromptOptions: options, SystemPrompt: prompts.BuildDefaultPrompt(prompts.FromExtensionOptions(options)),
	}, nil)
	h.Do(func() { h.Enter("bad") })
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.Chat(), "Error:") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	h.WaitIdle(t, 10*time.Second)
	if chat := h.Chat(); !strings.Contains(chat, "Error: Invalid system prompt section name: preamble") {
		t.Fatalf("chat=%q", chat)
	}
	if tools, _ := provider.requests(); len(tools) != 0 {
		t.Fatalf("rejected prompt sent %v", tools)
	}
	if active := session.ActiveToolNames(); !slices.Equal(active, []string{"read"}) {
		t.Fatalf("active tools=%v, want [read]", active)
	}
}
