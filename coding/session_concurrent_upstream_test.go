package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
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

// The original MockAssistantStream emits an aborted ErrorEvent whose message still has stopReason=stop. Keep that payload, rather than silently replacing the test's terminal message.
type concurrentProvider struct {
	mode        string
	started     chan struct{}
	once        sync.Once
	jobs        sync.WaitGroup
	sawSteering atomic.Bool
}

func (*concurrentProvider) ID() string     { return "anthropic" }
func (p *concurrentProvider) Close() error { p.jobs.Wait(); return nil }
func concurrentMessage(text string) *ai.AssistantMessage {
	return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: ai.APIAnthropicMessages, Provider: "anthropic", Model: "mock", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
}
func (p *concurrentProvider) Stream(ctx context.Context, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	raw, err := json.Marshal(request.Messages())
	if err != nil {
		return nil, err
	}
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, err
	}
	hasResult, steered := false, false
	for _, m := range messages {
		if m.Role == "toolResult" {
			hasResult = true
		}
		if m.Role != "user" {
			continue
		}
		var text string
		if json.Unmarshal(m.Content, &text) != nil {
			var parts []struct{ Type, Text string }
			if err := json.Unmarshal(m.Content, &parts); err != nil {
				return nil, err
			}
			var texts []string
			for _, part := range parts {
				if part.Type == "text" {
					texts = append(texts, part.Text)
				}
			}
			text = strings.Join(texts, "\n")
		}
		if text == "Steer from extension" {
			steered = true
		}
	}
	if p.mode == "tool" || p.mode == "slow" {
		m := concurrentMessage("done")
		m.Usage.Input = 1
		m.Usage.Output = 1
		m.Usage.TotalTokens = 2
		if !hasResult {
			m.Content = []ai.AssistantContentBlock{}
			if p.mode == "slow" {
				m.Content = append(m.Content, ai.TextContent{Text: "calling tool"})
			}
			m.Content = append(m.Content, ai.ToolCall{ID: "toolu_1", Name: "dummy", Arguments: ai.JsonObject{"q": "x"}})
			if p.mode == "tool" {
				m.Content = append(m.Content, ai.ToolCall{ID: "toolu_2", Name: "dummy", Arguments: ai.JsonObject{"q": "y"}})
			}
			m.StopReason = ai.StopReasonToolUse
		}
		partial := *m
		partial.Content = []ai.AssistantContentBlock{}
		return newSessionTestStream(ai.StartEvent{Partial: &partial}, ai.DoneEvent{Reason: m.StopReason, Message: m}), nil
	}
	if p.mode == "sequential" || steered {
		text := "Done"
		if steered {
			p.sawSteering.Store(true)
			text = "Steered"
		}
		return newSessionTestStream(ai.StartEvent{Partial: concurrentMessage("")}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: concurrentMessage(text)}), nil
	}
	stream := newSessionTestStream(ai.StartEvent{Partial: concurrentMessage("")})
	p.jobs.Go(func() {
		p.once.Do(func() { close(p.started) })
		<-ctx.Done()
		_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: concurrentMessage("Aborted")})
	})
	return stream, nil
}

type concurrentDummyTool struct{}

func (concurrentDummyTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

func (concurrentDummyTool) Name() string  { return "dummy" }
func (concurrentDummyTool) Label() string { return "dummy" }
func (concurrentDummyTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "dummy", Description: "Dummy tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []string{"q"}}}
}
func (concurrentDummyTool) Execute(_ context.Context, _ string, args json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var params struct {
		Q string `json:"q"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return agent.AgentToolResult{}, err
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "result:" + params.Q}}, Details: map[string]any{}}, nil
}

func newConcurrentSession(t *testing.T, p *concurrentProvider, ext extension.Extension, tools ...agent.AgentTool) *Session {
	t.Helper()
	catalog, ok := ai.LookupModelExact("anthropic/claude-sonnet-4-5")
	if !ok {
		t.Fatal("upstream fixture model missing")
	}
	model := catalog.ToModel()
	model.Provider = p
	var runner *inproc.Runner
	if ext.Handlers != nil {
		runner = inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	}
	options := SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: len(tools) > 0, Tools: tools, Runner: runner}
	if len(tools) == 0 {
		options.ActiveBuiltinTools = map[string]struct{}{"read": {}, "bash": {}, "edit": {}, "write": {}}
	}
	services := newTestServices(t)
	if err := services.Auth().Set("anthropic", ai.Credential{Type: ai.CredentialAPIKey, Key: "test-key"}); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, options)
	if err != nil {
		t.Fatal(err)
	}
	// The upstream fixture injects Agent.streamFn directly, not the SDK ModelRuntime wrapper. Its mock can answer a request whose signal is already aborted.
	session.Agent().SetStreamFunction(func(ctx context.Context, _ *ai.Model, request ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return p.Stream(ctx, request, options)
	})
	// Upstream injects an Agent whose initial system/tools message is not yet in the SessionManager. SetSystemPrompt would instead create a per-request override in Go.
	initialAgent := agent.NewAgent(agent.AgentOptions{Model: model, SystemPrompt: "Test", Tools: tools})
	session.Agent().SetMessages(initialAgent.Messages())
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range session.Events() {
		}
	}()
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		if err := p.Close(); err != nil {
			t.Error(err)
		}
		<-drained
	})
	return session
}

func concurrentPersistedRoles(session *Session) []string {
	var roles []string
	for _, entry := range session.inner.Entries() {
		if message, ok := entry.AsMessage(); ok {
			roles = append(roles, message.Message.Role())
		}
	}
	return roles
}

func TestUpstreamConcurrentPrompt(t *testing.T) {
	observed := []any{}
	for _, mode := range []string{"reject", "steer", "followUp"} {
		name := "should throw when prompt() called while streaming" // .upstream/v0.87.1/packages/coding-agent/test/agent-session-concurrent.test.ts:134
		if mode == "steer" {
			name = "should allow steer() while streaming"
		} // .upstream/v0.87.1/packages/coding-agent/test/agent-session-concurrent.test.ts:156
		if mode == "followUp" {
			name = "should allow followUp() while streaming"
		} // .upstream/v0.87.1/packages/coding-agent/test/agent-session-concurrent.test.ts:172
		t.Run(name, func(t *testing.T) {
			p := &concurrentProvider{started: make(chan struct{})}
			s := newConcurrentSession(t, p, extension.Extension{})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first := make(chan error, 1)
			go func() { _, err := s.Prompt(ctx, "First message"); first <- err }()
			<-p.started
			if !s.IsStreaming() {
				t.Fatal("isStreaming=false")
			}
			if mode == "reject" {
				second := make(chan error, 1)
				go func() { _, err := s.Prompt(ctx, "Second message"); second <- err }()
				secondReturned := false
				select {
				case err := <-second:
					secondReturned = true
					if err == nil || err.Error() != "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message." {
						t.Errorf("second prompt error=%v", err)
					}
				case <-time.After(time.Second):
					t.Error("second prompt waited for active prompt instead of rejecting")
				}
				if !secondReturned {
					cancel() // Release a regressed blocked admission before cleanup; the passing path aborts only the Agent, like Pi.
				}
				if err := s.Abort(t.Context()); err != nil {
					t.Error(err)
				}
				<-first
				if !secondReturned {
					<-second
				}
				observed = append(observed, []any{mode, true})
			} else {
				if mode == "steer" {
					if err := s.Steer(t.Context(), "Steering message", nil, nil); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := s.FollowUp(t.Context(), "Follow-up message", nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				if count := s.PendingMessageCount(); count != 1 {
					t.Errorf("pendingMessageCount=%d want1", count)
				}
				observed = append(observed, []any{mode, s.PendingMessageCount()})
				if err := s.Abort(t.Context()); err != nil {
					t.Error(err)
				}
				<-first
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-concurrent.test.ts:188
	t.Run("should queue extension-origin steering messages while streaming", func(t *testing.T) {
		p := &concurrentProvider{started: make(chan struct{})}
		var inputSource extension.InputSource
		var extensionAPI *extension.Context
		s := newConcurrentSession(t, p, extension.Extension{Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) {
			inputSource = args[0].(extension.InputEvent).Source
			if extensionAPI == nil {
				extensionAPI = extension.FromContext(args[1].(context.Context))
			}
			return nil, nil
		}}}})
		var mu sync.Mutex
		var queues []agent.QueueUpdateEvent
		queuedSeen := make(chan struct{})
		var queuedOnce sync.Once
		unsub := s.Subscribe(func(event agent.AgentEvent) {
			if q, ok := event.(agent.QueueUpdateEvent); ok {
				mu.Lock()
				queues = append(queues, q)
				mu.Unlock()
				if slices.Contains(q.Steering, "Steer from extension") {
					queuedOnce.Do(func() { close(queuedSeen) })
				}
			}
		})
		defer unsub()
		first := make(chan error, 1)
		go func() { _, err := s.Prompt(t.Context(), "First message"); first <- err }()
		<-p.started
		if !s.IsStreaming() {
			t.Fatal("isStreaming=false")
		}
		if extensionAPI == nil {
			t.Fatal("extension API was not bound to the input handler")
		}
		if err := extensionAPI.SendUserMessage("Steer from extension", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsSteer}); err != nil {
			t.Fatal(err)
		}
		if count := s.PendingMessageCount(); count != 1 {
			t.Fatalf("bound extension sendUserMessage returned without queueing: pending=%d", count)
		}
		<-queuedSeen
		if s.PendingMessageCount() != 1 {
			t.Errorf("pendingMessageCount=%d want1", s.PendingMessageCount())
		}
		s.queueMu.Lock()
		steering := slices.Clone(s.queuedSteering)
		s.queueMu.Unlock()
		if !slices.Contains(steering, "Steer from extension") {
			t.Errorf("steering=%v", steering)
		}
		if inputSource != extension.InputSourceExtension {
			t.Errorf("input source=%q", inputSource)
		}
		mu.Lock()
		queued := slices.ContainsFunc(queues, func(q agent.QueueUpdateEvent) bool { return slices.Contains(q.Steering, "Steer from extension") })
		mu.Unlock()
		if !queued {
			t.Error("queue_update omitted extension steering")
		}
		if err := s.Abort(t.Context()); err != nil {
			t.Fatal(err)
		}
		<-first
		if !p.sawSteering.Load() {
			t.Error("provider did not see extension steering after abort")
		}
		observed = append(observed, []any{"extension", inputSource, p.sawSteering.Load()})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-concurrent.test.ts:298
	t.Run("should allow prompt() after previous completes", func(t *testing.T) {
		s := newConcurrentSession(t, &concurrentProvider{mode: "sequential"}, extension.Extension{})
		if _, err := s.Prompt(t.Context(), "First message"); err != nil {
			t.Fatal(err)
		}
		if s.IsStreaming() {
			t.Fatal("isStreaming=true after completion")
		}
		if _, err := s.Prompt(t.Context(), "Second message"); err != nil {
			t.Fatal(err)
		}
		observed = append(observed, []any{"sequential", true})
	})
	for _, slow := range []bool{false, true} {
		name := "should wait for queued agent events before emitting tool_call" // .upstream/v0.87.1/packages/coding-agent/test/agent-session-concurrent.test.ts:343
		if slow {
			name = "should persist message_end events in order with slow extension handlers"
		} // .upstream/v0.87.1/packages/coding-agent/test/agent-session-concurrent.test.ts:491
		t.Run(name, func(t *testing.T) {
			var s *Session
			var snapshots [][]string
			var snapshotsMu sync.Mutex
			entered := make(chan int, 2)
			release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
			var releaseOnce [2]sync.Once
			var assistantEnds atomic.Int32
			handlers := map[string][]extension.HandlerFn{}
			mode := "tool"
			if slow {
				mode = "slow"
				handlers["message_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
					if args[0].(extension.MessageEndEvent).Message.(agent.AgentMessage).Assistant != nil {
						index := int(assistantEnds.Add(1)) - 1
						if index >= len(release) {
							return nil, fmt.Errorf("unexpected assistant message_end %d", index)
						}
						entered <- index
						<-release[index]
					}
					return nil, nil
				}}
			} else {
				handlers["tool_call"] = []extension.HandlerFn{func(...any) (any, error) {
					snapshotsMu.Lock()
					snapshots = append(snapshots, concurrentPersistedRoles(s))
					snapshotsMu.Unlock()
					return nil, nil
				}}
			}
			s = newConcurrentSession(t, &concurrentProvider{mode: mode}, extension.Extension{Handlers: handlers}, concurrentDummyTool{})
			t.Cleanup(func() {
				for index := range release {
					releaseOnce[index].Do(func() { close(release[index]) })
				}
			})
			promptDone := make(chan error, 1)
			go func() { _, err := s.Prompt(t.Context(), "hi"); promptDone <- err }()
			if slow {
				prefixes := [][]string{{"system", "user"}, {"system", "user", "assistant", "toolResult", "system"}}
				for index, want := range prefixes {
					if got := <-entered; got != index {
						t.Fatalf("message_end index=%d, want %d", got, index)
					}
					if got := concurrentPersistedRoles(s); !reflect.DeepEqual(got, want) {
						t.Errorf("persistence before handler completion=%v, want %v", got, want)
					}
					releaseOnce[index].Do(func() { close(release[index]) })
				}
			}
			if err := <-promptDone; err != nil {
				t.Fatal(err)
			}
			if err := s.WaitForIdle(t.Context()); err != nil {
				t.Fatal(err)
			}
			if slow {
				roles := concurrentPersistedRoles(s)
				want := []string{"system", "user", "assistant", "toolResult", "system", "assistant"}
				if !reflect.DeepEqual(roles, want) {
					t.Errorf("persisted roles=%v want=%v", roles, want)
				}
				observed = append(observed, []any{"slow", roles})
			} else {
				want := [][]string{{"system", "user", "assistant"}, {"system", "user", "assistant"}}
				if !reflect.DeepEqual(snapshots, want) {
					t.Errorf("tool snapshots=%v want=%v", snapshots, want)
				}
				observed = append(observed, []any{"tool", snapshots})
			}
		})
	}
	if !t.Failed() {
		raw, err := json.Marshal(observed)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("SESSION_CONCURRENT %s\n", raw)
	}
}
