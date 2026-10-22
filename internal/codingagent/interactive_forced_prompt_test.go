package codingagent_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// requestShapeProvider records the structure of every request: the role of each message, what each system message carries, the rendered current system prompt and the declared tools.
type requestShapeProvider struct {
	mu     sync.Mutex
	shapes []requestShape
}

type requestShape struct {
	Messages []string
	Prompt   string
	Tools    []string
}

func (p *requestShapeProvider) ID() string   { return "faux" }
func (p *requestShapeProvider) Close() error { return nil }

func (p *requestShapeProvider) Stream(_ context.Context, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	messages := request.Messages()
	shape := requestShape{Prompt: ai.GetCurrentSystemPrompt(messages), Tools: []string{}}
	for _, tool := range ai.GetCurrentTools(messages) {
		shape.Tools = append(shape.Tools, tool.Name)
	}
	for _, message := range messages {
		system, ok := message.(ai.SystemMessage)
		if !ok {
			shape.Messages = append(shape.Messages, message.MessageRole())
			continue
		}
		var sections, added []string
		for _, section := range system.Sections {
			sections = append(sections, section.Name)
		}
		for _, tool := range system.ToolsAdded {
			added = append(added, tool.Name)
		}
		shape.Messages = append(shape.Messages, fmt.Sprintf("system(forcedText=%t sections=%v toolsAdded=%v toolsRemoved=%d)", systemText(system) != "", sections, added, len(system.ToolsRemoved)))
	}
	p.mu.Lock()
	p.shapes = append(p.shapes, shape)
	p.mu.Unlock()
	reply := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: reply})
	_ = stream.Push(ai.DoneEvent{Reason: reply.StopReason, Message: reply})
	return stream, nil
}

func systemText(message ai.SystemMessage) string {
	switch content := message.Content.(type) {
	case ai.SystemText:
		return string(content)
	case ai.SystemTextBlocks:
		text := ""
		for _, block := range content {
			text += block.Text
		}
		return text
	}
	return ""
}

func (p *requestShapeProvider) requests() []requestShape {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.shapes)
}

// forcedPromptBuild is one Session with a compiled-in extension that narrows the active tools between two prompts.
type forcedPromptBuild struct {
	services *coding.AgentSessionServices
	session  *coding.Session
	runner   *inproc.Runner
	provider *requestShapeProvider
	model    *ai.Model
	pi       extension.API
	home     string
}

// newForcedPromptBuild builds the Session; register, when non-nil, runs inside the extension's factory.
func newForcedPromptBuild(t *testing.T, register func(extension.API)) *forcedPromptBuild {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if err := configureFauxAuth(services, "faux-key"); err != nil {
		t.Fatal(err)
	}
	b := &forcedPromptBuild{services: services, provider: &requestShapeProvider{}, home: home}
	runtime := extension.CreateExtensionRuntime()
	ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		b.pi = pi
		if register != nil {
			register(pi)
		}
		return nil
	}, cwd, extension.CreateEventBus(), runtime, "fused:probe")
	if err != nil {
		t.Fatal(err)
	}
	b.runner = inproc.NewRunner([]extension.Extension{ext}, cwd, runtime)
	b.model = &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: b.provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	b.session, err = coding.NewSession(services, coding.SessionOptions{Model: b.model, Runner: b.runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.session.Close() })
	return b
}

func (b *forcedPromptBuild) interactive(t *testing.T) *icodingagent.TestHarness {
	t.Helper()
	// Production hands the mode the base options its Session built, tool guidelines included.
	options := *b.session.GetSystemPromptOptions()
	return icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{
		CWD: b.services.CWD(), AgentDir: b.services.AgentDir(), Model: b.model, SessionHandle: b.session,
		SettingsManager: b.services.SettingsManager(), Settings: b.services.SettingsManager().Get(), ExtensionRunner: b.runner,
		SystemPromptOptions: options, SystemPrompt: prompts.BuildDefaultPrompt(prompts.FromExtensionOptions(options)),
	}, nil)
}

// runTwoPrompts prompts twice, narrowing the active tools in between as an extension does with pi.setActiveTools, and returns the two requests with this build's temporary paths replaced.
func (b *forcedPromptBuild) runTwoPrompts(t *testing.T, prompt func(text string)) []requestShape {
	t.Helper()
	prompt("one")
	b.pi.SetActiveTools([]string{"read"})
	prompt("two")
	requests := b.provider.requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	// system-prompt.ts:170 renders the cwd with forward slashes (cwd.replace(/\\/g, "/")), so a Windows temp path appears in the prompt in that form.
	cwd, home := b.services.CWD(), b.home
	replacer := strings.NewReplacer(cwd, "<cwd>", filepath.ToSlash(cwd), "<cwd>", home, "<home>", filepath.ToSlash(home), "<home>")
	for i := range requests {
		requests[i].Prompt = replacer.Replace(requests[i].Prompt)
	}
	return requests
}

// sessionRequests prompts a Session of its own, as print and RPC mode do.
func sessionRequests(t *testing.T, register func(extension.API)) []requestShape {
	t.Helper()
	b := newForcedPromptBuild(t, register)
	return b.runTwoPrompts(t, func(text string) {
		if err := b.session.Prompt(t.Context(), text); err != nil {
			t.Fatal(err)
		}
	})
}

// interactiveRequests prompts the same build through interactive mode.
func interactiveRequests(t *testing.T, register func(extension.API)) []requestShape {
	t.Helper()
	b := newForcedPromptBuild(t, register)
	h := b.interactive(t)
	return b.runTwoPrompts(t, func(text string) {
		before := len(b.provider.requests())
		h.Do(func() { h.Enter(text) })
		deadline := time.Now().Add(10 * time.Second)
		for len(b.provider.requests()) <= before && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		h.WaitIdle(t, 10*time.Second)
	})
}

func requireSameRequestShape(t *testing.T, got, want requestShape) {
	t.Helper()
	if !slices.Equal(got.Messages, want.Messages) {
		t.Errorf("request messages:\n interactive %v\n Session     %v", got.Messages, want.Messages)
	}
	if got.Prompt != want.Prompt {
		at := 0
		for at < len(got.Prompt) && at < len(want.Prompt) && got.Prompt[at] == want.Prompt[at] {
			at++
		}
		around := func(text string) string { return text[max(at-60, 0):min(at+100, len(text))] }
		t.Errorf("request system prompt differs at byte %d:\n interactive ...%q\n Session     ...%q", at, around(got.Prompt), around(want.Prompt))
	}
	if !slices.Equal(got.Tools, want.Tools) {
		t.Errorf("request tools: interactive %v, Session %v", got.Tools, want.Tools)
	}
}

// agent-session.ts:1743-1755 records a changed prompt or tool loadout as a system message in the transcript and forces a prompt onto the request only when before_agent_start returned systemPrompt (1792-1807). Interactive mode prompts through the same AgentSession, so a request after a mid-session tool change has the shape the Session's own prompt gives, not one head rewritten with the forced base prompt.
func TestInteractiveRequestAfterToolChangeHasTheSessionsShape(t *testing.T) {
	want := sessionRequests(t, nil)
	got := interactiveRequests(t, nil)
	requireSameRequestShape(t, got[1], want[1])
	requireSameRequestShape(t, got[0], want[0])
}

// A before_agent_start handler's systemPrompt is the head of every request of its run (agent-session.ts:1792-1807), in interactive mode as in the Session's own prompt.
func TestInteractiveForcedSystemPromptRequestsHaveTheSessionsShape(t *testing.T) {
	force := func(pi extension.API) {
		pi.OnBeforeAgentStart(func(context.Context, extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
			return extension.BeforeAgentStartEventResult{SystemPrompt: new("FORCED PROMPT")}, nil
		})
	}
	want := sessionRequests(t, force)
	got := interactiveRequests(t, force)
	for i := range want {
		if want[i].Prompt != "FORCED PROMPT" {
			t.Fatalf("Session request %d prompt = %q, want the forced prompt", i, want[i].Prompt)
		}
		requireSameRequestShape(t, got[i], want[i])
	}
}
