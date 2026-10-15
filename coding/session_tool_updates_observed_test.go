package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// updateProbeTool records agent.ToolUpdatesObserved for the callback the loop gave it and sends two updates either way.
type updateProbeTool struct{ observed *atomic.Bool }

func (updateProbeTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (updateProbeTool) Name() string                           { return "probe" }
func (updateProbeTool) Label() string                          { return "probe" }
func (updateProbeTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "probe", Description: "Probe", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (p updateProbeTool) Execute(ctx context.Context, _ string, _ json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	p.observed.Store(agent.ToolUpdatesObserved(ctx, onUpdate))
	for _, text := range []string{"one", "two"} {
		onUpdate(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: map[string]any{}})
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, nil
}

func updateProbeStream(_ context.Context, _ *ai.Model, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, API: ai.APIAnthropicMessages, Provider: "anthropic", Model: "mock", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	raw, err := json.Marshal(request.Messages())
	if err != nil {
		return nil, err
	}
	answered := strings.Contains(string(raw), `"role":"toolResult"`)
	if !answered {
		message.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "call", Name: "probe", Arguments: ai.JsonObject{}}}
		message.StopReason = ai.StopReasonToolUse
	}
	partial := *message
	partial.Content = []ai.AssistantContentBlock{}
	return newSessionTestStream(ai.StartEvent{Partial: &partial}, ai.DoneEvent{Reason: message.StopReason, Message: message}), nil
}

// Each way a mode observes a Session's events makes ToolUpdatesObserved true and receives the tool's updates as
// before; print text mode, which discards Events and subscribes nothing, is the one where it is false.
func TestSessionToolUpdatesObservedPerMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
		// observe sets the mode's observers up and returns the updates they saw, read after the run.
		observe func(*Session) func() []string
		ext     func(seen *[]string, mu *sync.Mutex) extension.Extension
	}{
		{name: "print text", want: false, observe: func(s *Session) func() []string { s.DiscardEvents(); return nil }},
		{name: "print json and rpc (session listener)", want: true, observe: func(s *Session) func() []string {
			s.DiscardEvents()
			var seen []string
			s.Subscribe(func(event agent.AgentEvent) {
				if update, ok := event.(agent.ToolExecutionUpdateEvent); ok {
					seen = append(seen, update.PartialResult.Text())
				}
			})
			return func() []string { return seen }
		}},
		{name: "interactive TUI (Events consumer)", want: true, observe: func(s *Session) func() []string {
			var mu sync.Mutex
			var seen []string
			events := s.Events()
			go func() {
				for event := range events {
					if update, ok := event.(agent.ToolExecutionUpdateEvent); ok {
						mu.Lock()
						seen = append(seen, update.PartialResult.Text())
						mu.Unlock()
					}
					AcknowledgeEvent(event)
				}
			}()
			return func() []string {
				_ = s.FlushEvents(context.Background())
				mu.Lock()
				defer mu.Unlock()
				return seen
			}
		}},
		{name: "agent-level Subscribe listener", want: true, observe: func(s *Session) func() []string {
			s.DiscardEvents()
			var seen []string
			s.Agent().Subscribe(func(_ context.Context, event agent.AgentEvent) error {
				if update, ok := event.(agent.ToolExecutionUpdateEvent); ok {
					seen = append(seen, update.PartialResult.Text())
				}
				return nil
			})
			return func() []string { return seen }
		}},
		{name: "extension tool_execution_update handler", want: true, observe: func(s *Session) func() []string { s.DiscardEvents(); return nil },
			ext: func(seen *[]string, mu *sync.Mutex) extension.Extension {
				return extension.Extension{Handlers: map[string][]extension.HandlerFn{"tool_execution_update": {func(args ...any) (any, error) {
					if event, ok := args[0].(extension.ToolExecutionUpdateEvent); ok {
						var partial struct{ Content []struct{ Text string } }
						raw, _ := json.Marshal(event.PartialResult)
						_ = json.Unmarshal(raw, &partial)
						mu.Lock()
						for _, block := range partial.Content {
							*seen = append(*seen, block.Text)
						}
						mu.Unlock()
					}
					return nil, nil
				}}}}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observed atomic.Bool
			var extMu sync.Mutex
			var extSeen []string
			catalog, ok := ai.LookupModelExact("anthropic/claude-sonnet-4-5")
			if !ok {
				t.Fatal("fixture model missing")
			}
			model := catalog.ToModel()
			model.Provider = &concurrentProvider{}
			options := SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true, Tools: []agent.AgentTool{updateProbeTool{&observed}}}
			if tc.ext != nil {
				options.Runner = inproc.NewRunner([]extension.Extension{tc.ext(&extSeen, &extMu)}, t.TempDir())
			}
			services := newTestServices(t)
			if err := services.Auth().Set("anthropic", ai.Credential{Type: ai.CredentialAPIKey, Key: "test-key"}); err != nil {
				t.Fatal(err)
			}
			session, err := NewSession(services, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			session.Agent().SetStreamFunction(updateProbeStream)
			seen := tc.observe(session)
			if _, err := session.Send(context.Background(), "go"); err != nil {
				t.Fatal(err)
			}
			if observed.Load() != tc.want {
				t.Fatalf("ToolUpdatesObserved = %v, want %v", observed.Load(), tc.want)
			}
			var got []string
			switch {
			case tc.ext != nil:
				extMu.Lock()
				got = extSeen
				extMu.Unlock()
			case seen != nil:
				got = seen()
			default:
				return
			}
			if !reflect.DeepEqual(got, []string{"one", "two"}) {
				t.Fatalf("observer saw updates %q, want [one two]", got)
			}
		})
	}
}
