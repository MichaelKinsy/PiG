package codingagent_test

// Pi's interactive mode draws nothing for an extension handler: packages/coding-agent/src/modes/interactive/interactive-mode.ts handleEvent (3467-3865)
// reads no handler state, and agent-session.ts runs a handler that returns nothing without adding an entry, so the transcript of a run is the same
// with and without such handlers. Scenarios extensions-runtime/94 and 95 hold the rows above the editor border to Pi's in a tmux pane.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// layoutProvider answers a request with the text "42", or, when tool is set, answers the first request with a bash tool call and the next with "42".
type layoutProvider struct {
	tool     bool
	requests atomic.Int32
	// answered closes with the answer to the request numbered answeredAt.
	answeredAt int32
	answered   chan struct{}
}

func newLayoutProvider(tool bool, answeredAt int32) *layoutProvider {
	return &layoutProvider{tool: tool, answeredAt: answeredAt, answered: make(chan struct{})}
}

func (p *layoutProvider) ID() string   { return "faux" }
func (p *layoutProvider) Close() error { return nil }

func (p *layoutProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	number := p.requests.Add(1)
	first := number == 1
	if number == p.answeredAt {
		defer close(p.answered)
	}
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "42"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	if p.tool && first {
		message.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "bash", Arguments: ai.JsonObject{"command": "expr 20 + 22"}}}
		message.StopReason = ai.StopReasonToolUse
	}
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: message})
	_ = stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message})
	return stream, nil
}

// noopHandlerEvents is every agent, turn, message, tool, provider and input event a run emits to extensions.
var noopHandlerEvents = []string{
	icodingagent.EventInput, icodingagent.EventBeforeAgentStart, icodingagent.EventAgentStart, icodingagent.EventTurnStart,
	icodingagent.EventContext, icodingagent.EventBeforeProviderRequest, icodingagent.EventAfterProviderResponse,
	icodingagent.EventMessageStart, icodingagent.EventMessageUpdate, icodingagent.EventMessageEnd,
	icodingagent.EventToolExecutionStart, icodingagent.EventToolCall, icodingagent.EventToolExecutionUpdate, icodingagent.EventToolResult,
	icodingagent.EventToolExecutionEnd, icodingagent.EventTurnEnd, icodingagent.EventAgentBeforeSettle, icodingagent.EventAgentEnd,
	icodingagent.EventAgentSettled,
}

func noopHandlers() map[string][]extension.HandlerFn {
	handlers := map[string][]extension.HandlerFn{}
	for _, event := range noopHandlerEvents {
		handlers[event] = []extension.HandlerFn{func(...any) (any, error) { return nil, nil }}
	}
	return handlers
}

var toolDuration = regexp.MustCompile(`Took \d+(\.\d+)?m?s`)

// runLayoutTranscript sends "prompt" through the real owner loop and a real Session and returns the transcript rows once the run has settled.
func runLayoutTranscript(t *testing.T, provider *layoutProvider, handlers map[string][]extension.HandlerFn) string {
	t.Helper()
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
	if err := configureFauxAuth(services, "faux-key"); err != nil {
		t.Fatal(err)
	}
	exts := []extension.Extension{}
	if handlers != nil {
		exts = append(exts, extension.Extension{Name: "noop", Handlers: handlers})
	}
	runner := inproc.NewRunner(exts, cwd)
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
	h.Do(func() { h.Enter("What is 20+22?") })
	// The loop is idle until the turn starts, so the provider's last answer comes before the idle wait.
	select {
	case <-provider.answered:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("the provider answered %d of %d requests", provider.requests.Load(), provider.answeredAt)
	}
	h.WaitIdle(t, 10*time.Second)
	rows := strings.Split(toolDuration.ReplaceAllString(h.Chat(), "Took Ns"), "\n")
	for i, row := range rows {
		rows[i] = strings.TrimRight(row, " ")
	}
	return strings.Join(rows, "\n")
}

// A text run ends with the reply and no spacer after it, exactly as Pi lays it out (scenario 94: the rows above the editor border equal Pi's), with or
// without handlers; a tool run keeps its tool card and spacers (scenario 95).
func TestRunWithNoopEventHandlersKeepsTheTranscriptLayout(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tool     bool
		requests int32
		want     string
	}{
		{"text", false, 1, "\n What is 20+22?\n\n\n 42"},
		{"tool", true, 2, "\n What is 20+22?\n\n\n\n $ expr 20 + 22\n\n 42\n\n Took Ns\n\n\n 42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			without := runLayoutTranscript(t, newLayoutProvider(tc.tool, tc.requests), nil)
			with := runLayoutTranscript(t, newLayoutProvider(tc.tool, tc.requests), noopHandlers())
			if with != without {
				t.Fatalf("handlers that return nothing changed the transcript\nwithout:\n%q\nwith:\n%q", without, with)
			}
			if tc.want != "" && with != tc.want {
				t.Fatalf("transcript = %q, want %q", with, tc.want)
			}
		})
	}
}
