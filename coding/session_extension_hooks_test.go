package coding

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// probeTool records every execution and the arguments it ran with.
type probeTool struct {
	mu    sync.Mutex
	calls []string
}

func (p *probeTool) Name() string  { return "probe" }
func (p *probeTool) Label() string { return "" }
func (p *probeTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "probe", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}
}
func (p *probeTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (p *probeTool) Execute(_ context.Context, _ string, params json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, string(params))
	return agent.AgentToolResult{Content: "probed"}, nil
}

func (p *probeTool) executions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func fauxProbeCall(args string) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		var arguments ai.JsonObject
		_ = json.Unmarshal([]byte(args), &arguments)
		return &ai.AssistantMessage{
			Content:  []ai.AssistantContentBlock{ai.ToolCall{ID: "call-probe", Name: "probe", Arguments: arguments}},
			Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: time.Now().UnixMilli(),
		}
	}
}

func toolCallExtension(handler extension.HandlerFn) extension.Extension {
	return extension.Extension{Path: "/ext/guard", Handlers: map[string][]extension.HandlerFn{"tool_call": {handler}}}
}

func toolResultMessages(messages []agent.AgentMessage) []*agent.ToolResultMessage {
	var out []*agent.ToolResultMessage
	for _, message := range messages {
		if message.ToolResult != nil {
			out = append(out, message.ToolResult)
		}
	}
	return out
}

// Upstream _installAgentToolHooks rethrows a tool_call handler error and the
// agent loop turns it into an error tool result: the tool never runs.
func TestToolCallHandlerErrorBlocksExecution(t *testing.T) {
	tool := &probeTool{}
	h := newRecoveryHarness(t, harnessOptions{
		tools: []agent.AgentTool{tool},
		extension: toolCallExtension(func(...any) (any, error) {
			return nil, errors.New("guard crashed")
		}),
	}, fauxProbeCall(`{"path":"a"}`), fauxReply("done", ai.StopReasonStop, 0))
	messages, err := h.session.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if calls := tool.executions(); len(calls) != 0 {
		t.Fatalf("tool executed %d time(s) after its tool_call handler failed", len(calls))
	}
	results := toolResultMessages(messages)
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Text(), "guard crashed") {
		t.Fatalf("tool results = %+v, want one error result carrying the handler error", results)
	}
}

// A blocking handler's terminate ends the run after the batch, and a handler
// that mutates event.input in place changes the arguments the tool runs with.
func TestToolCallTerminateAndInputMutation(t *testing.T) {
	t.Run("terminate", func(t *testing.T) {
		tool := &probeTool{}
		h := newRecoveryHarness(t, harnessOptions{
			tools: []agent.AgentTool{tool},
			extension: toolCallExtension(func(...any) (any, error) {
				return &extension.ToolCallEventResult{Block: true, Reason: "denied", Terminate: true}, nil
			}),
		}, fauxProbeCall(`{"path":"a"}`), fauxReply("should not be requested", ai.StopReasonStop, 0))
		if _, err := h.session.Send(context.Background(), "go"); err != nil {
			t.Fatal(err)
		}
		if calls := h.provider.callCount(); calls != 1 {
			t.Fatalf("provider called %d times; terminate must end the run after the blocked batch", calls)
		}
	})
	t.Run("mutation", func(t *testing.T) {
		tool := &probeTool{}
		h := newRecoveryHarness(t, harnessOptions{
			tools: []agent.AgentTool{tool},
			extension: toolCallExtension(func(args ...any) (any, error) {
				event := args[0].(extension.CustomToolCallEvent)
				event.Input["path"] = "rewritten"
				return nil, nil
			}),
		}, fauxProbeCall(`{"path":"a"}`), fauxReply("done", ai.StopReasonStop, 0))
		if _, err := h.session.Send(context.Background(), "go"); err != nil {
			t.Fatal(err)
		}
		calls := tool.executions()
		if len(calls) != 1 || calls[0] != `{"path":"rewritten"}` {
			t.Fatalf("tool ran with %v, want the mutated input", calls)
		}
	})
}

// The hook resolves the current runner and its handlers at call time: a
// handler subscribed after the Session started, or a runner swapped in by
// reload, is honored, and each handler runs exactly once per tool call.
func TestToolCallHookFollowsLateHandlersAndRunnerSwap(t *testing.T) {
	var lateCalls, swappedCalls atomic.Int32
	tool := &probeTool{}
	late := extension.Extension{Path: "/ext/late", Handlers: map[string][]extension.HandlerFn{}}
	late.InitializeEventHandlers() // shared with the runner's copy, as a subprocess extension is
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{tool}, extension: late},
		fauxProbeCall(`{"path":"a"}`), fauxReply("one", ai.StopReasonStop, 0),
		fauxProbeCall(`{"path":"b"}`), fauxReply("two", ai.StopReasonStop, 0))
	// Subscribe after the Session started, as a subprocess pi.on("tool_call")
	// from session_start does.
	late.AddEventHandler("tool_call", 1, func(...any) (any, error) { lateCalls.Add(1); return nil, nil })
	if _, err := h.session.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	if got := lateCalls.Load(); got != 1 {
		t.Fatalf("late-subscribed tool_call handler ran %d times, want 1", got)
	}

	swapped := extension.Extension{Path: "/ext/reloaded", Handlers: map[string][]extension.HandlerFn{"tool_call": {func(...any) (any, error) { swappedCalls.Add(1); return nil, nil }}}}
	h.session.ReplaceRunner(inproc.NewRunner([]extension.Extension{swapped}, t.TempDir()))
	if _, err := h.session.Send(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if got := swappedCalls.Load(); got != 1 {
		t.Fatalf("reloaded runner's tool_call handler ran %d times, want 1", got)
	}
	if got := lateCalls.Load(); got != 1 {
		t.Fatalf("replaced runner's handler ran again (%d calls)", got)
	}
}

// Every mode builds its Session through Runtime; the Runtime path must dispatch
// tool_call exactly once per tool call (the interactive mode used to install a
// second copy of the hook on the same agent).
func TestRuntimeToolCallHandlerRunsOncePerCall(t *testing.T) {
	var calls atomic.Int32
	svcs := newTestServices(t)
	rt, err := NewRuntime(RuntimeOptions{Services: svcs, NewExtensions: []extension.Extension{
		toolCallExtension(func(...any) (any, error) { calls.Add(1); return nil, nil }),
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	provider := &scriptedProvider{responses: []scriptedResponse{fauxProbeCall(`{"path":"a"}`), fauxReply("done", ai.StopReasonStop, 0)}}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
	sess, err := rt.New(SessionStartOptions{Model: model, SkipBuiltinTools: true, ExtraTools: []agent.AgentTool{&probeTool{}}, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	if _, err := sess.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("tool_call handler ran %d times for one tool call, want 1", got)
	}
}

func drainSessionEvents(t *testing.T, sess *Session) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range sess.Events() {
		}
	}()
	t.Cleanup(func() {
		_ = sess.Close()
		<-done
	})
}

// payloadProvider builds a wire payload per request, passes it through
// OnPayload as a provider does, and records what it would send.
type payloadProvider struct {
	mu   sync.Mutex
	sent []any
}

func (p *payloadProvider) ID() string   { return "faux" }
func (p *payloadProvider) Close() error { return nil }
func (p *payloadProvider) Stream(_ context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	var payload any = map[string]any{"model": "faux-1", "max_tokens": float64(10)}
	if options.OnPayload != nil {
		replaced, err := options.OnPayload(payload, nil)
		if err != nil {
			return nil, err
		}
		if replaced != nil {
			payload = replaced
		}
	}
	p.mu.Lock()
	p.sent = append(p.sent, payload)
	p.mu.Unlock()
	message := fauxReply("ok", ai.StopReasonStop, 0)(nil)
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

// The context and before_provider_request events are installed on the Session
// for every mode (upstream sdk.ts), and a context handler's returned messages
// replace the conversation the provider receives while the prompt stays.
func TestContextHandlerResultShrinksProviderRequest(t *testing.T) {
	var sawSystem atomic.Bool
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/window", Handlers: map[string][]extension.HandlerFn{
		"context": {func(args ...any) (any, error) {
			event := args[0].(extension.ContextEvent)
			for _, message := range event.Messages {
				if m, ok := message.(agent.AgentMessage); ok && m.System != nil {
					sawSystem.Store(true)
				}
			}
			return &extension.ContextEventResult{Messages: event.Messages[len(event.Messages)-1:]}, nil
		}},
	}}}, fauxReply("first answer", ai.StopReasonStop, 0), fauxReply("second answer", ai.StopReasonStop, 0))
	for _, prompt := range []string{"alpha question", "omega question"} {
		if _, err := h.session.Send(context.Background(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	h.provider.mu.Lock()
	second := h.provider.requests[1]
	h.provider.mu.Unlock()
	if strings.Contains(second, "alpha question") || strings.Contains(second, "first answer") || !strings.Contains(second, "omega question") {
		t.Fatalf("second provider request = %s; want only the handler's last message", second)
	}
	if !strings.Contains(second, `"role":"system"`) {
		t.Fatalf("second provider request = %s; want the system prompt kept at the head", second)
	}
	if sawSystem.Load() {
		t.Fatal("context handlers saw a system message; upstream passes only the conversation")
	}
}

func TestBeforeProviderRequestRewritesPayloadInSDKSession(t *testing.T) {
	provider := &payloadProvider{}
	svcs := newTestServices(t)
	runner := inproc.NewRunner([]extension.Extension{{Path: "/ext/payload", Handlers: map[string][]extension.HandlerFn{
		"before_provider_request": {func(args ...any) (any, error) {
			payload := args[0].(extension.BeforeProviderRequestEvent).Payload.(map[string]any)
			rewritten := map[string]any{"model": payload["model"], "max_tokens": float64(123)}
			return rewritten, nil
		}},
	}}}, t.TempDir())
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
	sess, err := NewSession(svcs, SessionOptions{Model: model, SkipBuiltinTools: true, NoSession: true, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	if _, err := sess.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.sent) != 1 || provider.sent[0].(map[string]any)["max_tokens"] != float64(123) {
		t.Fatalf("sent payloads = %v, want the handler's rewrite", provider.sent)
	}
}

// before_provider_headers handlers mutate the merged request headers in place
// before the provider sends them (sdk.ts transformHeaders): after attribution,
// and a nil value deletes a header.
func TestBeforeProviderHeadersReachProviderRequest(t *testing.T) {
	capture := &attributionCaptureProvider{}
	provider := newProviderAttributionProvider(capture, "opencode", "https://opencode.ai/zen/v1", func() bool { return false }, nil)
	runner := inproc.NewRunner([]extension.Extension{{Path: "/ext/trace", Handlers: map[string][]extension.HandlerFn{
		"before_provider_headers": {func(args ...any) (any, error) {
			headers := args[0].(extension.BeforeProviderHeadersEvent).Headers
			headers["x-test"] = new("traced")
			headers["x-opencode-client"] = nil
			return nil, nil
		}},
	}}}, t.TempDir())
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: model, SkipBuiltinTools: true, NoSession: true, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	if _, err := sess.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	headers := capture.options.Headers
	if value := headers["x-test"]; value == nil || *value != "traced" {
		t.Fatalf("provider headers = %v, want x-test: traced", headers)
	}
	if value, ok := headers["x-opencode-client"]; !ok || value != nil {
		t.Fatalf("provider headers = %v, want the attribution header deleted (nil)", headers)
	}
	if value := headers["x-opencode-session"]; value == nil || *value != sess.ID() {
		t.Fatalf("provider headers = %v, want the attribution session header kept", headers)
	}
}

// A message_end handler's same-role replacement is what agent state, the
// session file and listeners see (agent-session.ts _emitExtensionEvent then
// persistence); a role-changing replacement is reported and ignored.
func TestMessageEndReplacementPersisted(t *testing.T) {
	wantUsage := ai.Usage{
		Input: 37, Output: 13, CacheRead: 11, CacheWrite: 7, TotalTokens: 68,
		Cost: ai.UsageCost{Input: 0.125, Output: 0.25, CacheRead: 0.0625, CacheWrite: 0.0625, Total: 0.5},
	}
	var reported atomic.Int32
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/redact", Handlers: map[string][]extension.HandlerFn{
		"message_end": {
			func(args ...any) (any, error) {
				message := args[0].(extension.MessageEndEvent).Message.(agent.AgentMessage)
				if message.Assistant == nil {
					return nil, nil
				}
				replacement := *message.Assistant
				replacement.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "[redacted]"}}
				replacement.Usage = &wantUsage
				var out extension.AgentMessage = agent.AgentMessage{Assistant: &replacement}
				return &extension.MessageEndEventResult{Message: &out}, nil
			},
			func(args ...any) (any, error) {
				if args[0].(extension.MessageEndEvent).Message.(agent.AgentMessage).Assistant == nil {
					return nil, nil
				}
				var out extension.AgentMessage = map[string]any{"role": "user", "content": []any{}}
				return &extension.MessageEndEventResult{Message: &out}, nil
			},
		},
	}}}, fauxReply("secret token", ai.StopReasonStop, 0))
	h.session.currentRunner().AddErrorListener(func(e *extension.ExtensionError) {
		if e.Event == "message_end" {
			reported.Add(1)
		}
	})
	messages, err := h.session.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if got := assistantText(lastAssistantMessage(messages)); got != "[redacted]" {
		t.Fatalf("agent state assistant = %q, want the replacement", got)
	}
	if got := lastAssistantMessage(messages).Usage; got == nil || *got != wantUsage {
		t.Fatalf("agent state usage = %+v, want %+v", got, wantUsage)
	}
	var persisted []string
	for _, entry := range h.entries("message") {
		if message, ok := entry.AsMessage(); ok && message.Message.Assistant != nil {
			persisted = append(persisted, assistantText(message.Message.Assistant))
			if got := message.Message.Assistant.Usage; got == nil || *got != wantUsage {
				t.Fatalf("persisted usage = %+v, want %+v", got, wantUsage)
			}
		}
	}
	if len(persisted) != 1 || persisted[0] != "[redacted]" {
		t.Fatalf("persisted assistant messages = %q, want the replacement", persisted)
	}
	for _, event := range h.settle(t) {
		if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Assistant != nil {
			if assistantText(end.Message.Assistant) != "[redacted]" {
				t.Fatalf("listeners saw assistant %q, want the replacement", assistantText(end.Message.Assistant))
			}
			if got := end.Message.Assistant.Usage; got == nil || *got != wantUsage {
				t.Fatalf("listeners saw usage = %+v, want %+v", got, wantUsage)
			}
		}
	}
	stats := h.session.GetSessionStats()
	wantTokens := SessionStatsTokens{Input: wantUsage.Input, Output: wantUsage.Output, CacheRead: wantUsage.CacheRead, CacheWrite: wantUsage.CacheWrite, Total: wantUsage.TotalTokens}
	if stats.Tokens != wantTokens || stats.Cost != wantUsage.Cost.Total {
		t.Fatalf("session accounting = %+v, want tokens %+v and cost %v", stats, wantTokens, wantUsage.Cost.Total)
	}
	if reported.Load() == 0 {
		t.Fatal("a role-changing replacement was not reported")
	}
}

// Extension handlers for agent events finish before the loop continues:
// a tool_execution_start handler completes before the tool runs.
func TestAgentEventHandlersRunBeforeLoopContinues(t *testing.T) {
	var handled atomic.Bool
	var ranAfterHandler atomic.Bool
	tool := &hookOrderTool{run: func() { ranAfterHandler.Store(handled.Load()) }}
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{tool}, extension: extension.Extension{Path: "/ext/observe", Handlers: map[string][]extension.HandlerFn{
		"tool_execution_start": {func(...any) (any, error) {
			time.Sleep(50 * time.Millisecond)
			handled.Store(true)
			return nil, nil
		}},
	}}}, fauxProbeCall(`{"path":"a"}`), fauxReply("done", ai.StopReasonStop, 0))
	if _, err := h.session.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if !ranAfterHandler.Load() {
		t.Fatal("the tool ran before its tool_execution_start handler finished")
	}
}

type hookOrderTool struct {
	probeTool
	run func()
}

func (h *hookOrderTool) Execute(ctx context.Context, id string, params json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	h.run()
	return h.probeTool.Execute(ctx, id, params, onUpdate)
}

// optionsProvider answers with a probe tool call, then text, and records the
// stream options of every request.
type optionsProvider struct {
	mu      sync.Mutex
	options []ai.StreamOptions
}

func (p *optionsProvider) ID() string   { return "faux" }
func (p *optionsProvider) Close() error { return nil }
func (p *optionsProvider) Stream(_ context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.options = append(p.options, options)
	first := len(p.options)%2 == 1
	p.mu.Unlock()
	message := fauxReply("done", ai.StopReasonStop, 0)(nil)
	if first {
		message = fauxProbeCall(`{"path":"a"}`)(nil)
	}
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: message.StopReason, Message: message}), nil
}

// A clone is a full Session: extension and caller tool hooks, thinking
// budgets, the model runtime and the configured event buffer all carry over.
func TestCloneKeepsToolCallHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"thinkingBudgets":{"high":4321}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	var extensionCalls, callerCalls atomic.Int32
	runner := inproc.NewRunner([]extension.Extension{toolCallExtension(func(...any) (any, error) {
		extensionCalls.Add(1)
		return &extension.ToolCallEventResult{Block: true, Reason: "denied"}, nil
	})}, t.TempDir())
	provider := &optionsProvider{}
	tool := &probeTool{}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000, MaxThinking: ai.ThinkingHigh}}
	sess, err := NewSession(svcs, SessionOptions{
		Model: model, SkipBuiltinTools: true, Tools: []agent.AgentTool{tool}, Runner: runner, EventBufferSize: 7,
		BeforeToolCall: []agent.BeforeToolCallHook{func(context.Context, string, string, json.RawMessage) agent.ToolCallHookResult {
			callerCalls.Add(1)
			return agent.ToolCallHookResult{}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	appendUser(t, sess, "shared turn")
	appendAsst(t, sess, "shared reply")
	clone, err := sess.Clone()
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, clone)
	if err := clone.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	if _, err := clone.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if extensionCalls.Load() != 1 || callerCalls.Load() != 1 || len(tool.executions()) != 0 {
		t.Fatalf("clone tool hooks: extension=%d caller=%d executions=%d; want the extension to block the call", extensionCalls.Load(), callerCalls.Load(), len(tool.executions()))
	}
	provider.mu.Lock()
	budgets := provider.options[0].ThinkingBudgets
	provider.mu.Unlock()
	if budgets == nil || budgets.High != 4321 {
		t.Fatalf("clone thinking budgets = %+v, want the configured high budget", budgets)
	}
	if clone.ModelRuntime() == nil || clone.ModelRegistry() == nil {
		t.Fatal("clone has no model runtime or registry")
	}
	if got := cap(clone.Events()); got != 7 {
		t.Fatalf("clone event buffer = %d, want the configured 7", got)
	}
}

// A before_agent_start system prompt applies only to the run it was returned
// for (upstream resets _runSystemPromptOptions when the run settles).
func TestForcedSystemPromptIsPerRun(t *testing.T) {
	var override atomic.Bool
	override.Store(true)
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/prompt", Handlers: map[string][]extension.HandlerFn{
		"before_agent_start": {func(...any) (any, error) {
			if !override.Load() {
				return nil, nil
			}
			return &extension.BeforeAgentStartEventResult{SystemPrompt: new("FORCED-PROMPT")}, nil
		}},
	}}}, fauxReply("one", ai.StopReasonStop, 0), fauxReply("two", ai.StopReasonStop, 0))
	if _, err := h.session.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	override.Store(false)
	if _, err := h.session.Send(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	h.provider.mu.Lock()
	first, second := h.provider.requests[0], h.provider.requests[1]
	h.provider.mu.Unlock()
	if !strings.Contains(first, "FORCED-PROMPT") {
		t.Fatalf("first request = %s; want the forced prompt", first)
	}
	if strings.Contains(second, "FORCED-PROMPT") {
		t.Fatalf("second request = %s; the forced prompt leaked into a later run", second)
	}
}

// Messages returned by before_agent_start join the prompt as custom messages
// after the user message (agent-session.ts prompt()), reach the provider, and
// are persisted.
func TestBeforeAgentStartMessagesReachProvider(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/context", Handlers: map[string][]extension.HandlerFn{
		"before_agent_start": {func(...any) (any, error) {
			return &extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "role-context", Content: "INJECTED-CONTEXT", Display: false}}, nil
		}},
	}}}, fauxReply("ok", ai.StopReasonStop, 0))
	if _, err := h.session.Send(context.Background(), "USER-PROMPT"); err != nil {
		t.Fatal(err)
	}
	h.provider.mu.Lock()
	request := h.provider.requests[0]
	h.provider.mu.Unlock()
	user, injected := strings.Index(request, "USER-PROMPT"), strings.Index(request, "INJECTED-CONTEXT")
	if user < 0 || injected < user {
		t.Fatalf("provider request = %s; want the injected message after the user message", request)
	}
	if entries := h.entries("custom_message"); len(entries) != 1 || !strings.Contains(string(entries[0].Raw()), "role-context") {
		t.Fatalf("custom_message entries = %d; want the injected message persisted", len(entries))
	}
}

// ctx.navigateTree from an extension command reaches the Session in every mode
// (upstream binds it from session.navigateTree), including after a runner
// swap, and a clone sharing the runner does not take the binding over.
func TestExtensionCommandNavigateTreeIsBound(t *testing.T) {
	svcs := newTestServices(t)
	runner := inproc.NewRunner(nil, t.TempDir())
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	first := appendUser(t, sess, "first")
	appendAsst(t, sess, "reply")
	appendUser(t, sess, "second")
	appendAsst(t, sess, "reply two")
	clone, err := sess.Clone()
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, clone)

	navigate := func(r *inproc.Runner) {
		t.Helper()
		before := *sess.Inner().LeafID()
		result, err := r.CreateCommandContext().NavigateTree(first, nil)
		if err != nil || result.Cancelled {
			t.Fatalf("NavigateTree = %+v, %v; want a navigation", result, err)
		}
		if leaf := sess.Inner().LeafID(); leaf != nil && *leaf == before {
			t.Fatal("the Session's leaf did not move")
		}
	}
	navigate(runner)

	appendUser(t, sess, "third")
	reloaded := inproc.NewRunner(nil, t.TempDir())
	sess.ReplaceRunner(reloaded)
	navigate(reloaded)
}

// ctx.waitForIdle from an extension command uses the Session's run state in
// every mode (upstream binds it from session.waitForIdle).
func TestExtensionCommandWaitForIdleIsBound(t *testing.T) {
	svcs := newTestServices(t)
	runner := inproc.NewRunner(nil, t.TempDir())
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	drainSessionEvents(t, sess)

	_, cancelRun := sess.beginAgentRun(t.Context())
	waited := make(chan error, 1)
	go func() { waited <- runner.CreateCommandContext().WaitForIdle() }()
	select {
	case err := <-waited:
		t.Fatalf("WaitForIdle returned during the run: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	sess.endAgentRun(cancelRun)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitForIdle did not return after the run ended")
	}
}

// context_with_system handlers run after context handlers, see the system
// messages, and their result is sent as returned; dropping the leading system
// message is reported but honored (runner.ts emitContext second phase).
func TestContextWithSystemHandlerSeesAndReplacesTranscript(t *testing.T) {
	var sawSystem atomic.Bool
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/full", Handlers: map[string][]extension.HandlerFn{
		"context_with_system": {func(args ...any) (any, error) {
			messages := args[0].(extension.ContextWithSystemEvent).Messages
			if first, ok := messages[0].(agent.AgentMessage); ok && first.System != nil {
				sawSystem.Store(true)
			}
			replacement := &ai.SystemMessage{Content: ai.SystemText("REPLACED-PROMPT")}
			out := []extension.AgentMessage{agent.AgentMessage{System: replacement}}
			return &extension.ContextEventResult{Messages: append(out, messages[1:]...)}, nil
		}},
	}}}, fauxReply("ok", ai.StopReasonStop, 0))
	if _, err := h.session.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if !sawSystem.Load() {
		t.Fatal("context_with_system handler did not see the leading system message")
	}
	h.provider.mu.Lock()
	request := h.provider.requests[0]
	h.provider.mu.Unlock()
	if !strings.Contains(request, "REPLACED-PROMPT") || strings.Contains(request, "expert coding assistant") {
		t.Fatalf("provider request = %s; want the handler's system prompt", request)
	}
}

// A message_end handler that returns the event's own custom message keeps it
// intact, and a distinct same-role replacement replaces it in the session
// file (agent-session.ts _replaceMessageInPlace returns early on identity).
func TestMessageEndCustomMessageReplacement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replace func(extension.AgentMessage) extension.AgentMessage
		want    string
	}{
		{"same message", func(message extension.AgentMessage) extension.AgentMessage { return message }, "INJECTED-CONTEXT"},
		{"distinct message", func(extension.AgentMessage) extension.AgentMessage {
			return agent.AgentMessage{Custom: map[string]any{"role": "custom", "customType": "role-context", "content": "REWRITTEN-CONTEXT", "display": false}}
		}, "REWRITTEN-CONTEXT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/custom", Handlers: map[string][]extension.HandlerFn{
				"before_agent_start": {func(...any) (any, error) {
					return &extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "role-context", Content: "INJECTED-CONTEXT"}}, nil
				}},
				"message_end": {func(args ...any) (any, error) {
					message := args[0].(extension.MessageEndEvent).Message
					if m, ok := message.(agent.AgentMessage); !ok || m.Custom == nil {
						return nil, nil
					}
					out := tc.replace(message)
					return &extension.MessageEndEventResult{Message: &out}, nil
				}},
			}}}, fauxReply("ok", ai.StopReasonStop, 0))
			if _, err := h.session.Send(context.Background(), "go"); err != nil {
				t.Fatal(err)
			}
			entries := h.entries("custom_message")
			if len(entries) != 1 || !strings.Contains(string(entries[0].Raw()), tc.want) || !strings.Contains(string(entries[0].Raw()), "role-context") {
				t.Fatalf("custom_message entries = %d %v; want %q with its customType", len(entries), entries, tc.want)
			}
		})
	}
}

// Request-time transforms are request-only: a context or context_with_system
// handler that edits messages in place changes the provider request, never the
// session transcript (upstream emitContext starts from structuredClone).
func TestContextTransformsDoNotMutateTranscript(t *testing.T) {
	for _, event := range []string{"context", "context_with_system"} {
		t.Run(event, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/edit", Handlers: map[string][]extension.HandlerFn{
				event: {func(args ...any) (any, error) {
					var messages []extension.AgentMessage
					switch e := args[0].(type) {
					case extension.ContextEvent:
						messages = e.Messages
					case extension.ContextWithSystemEvent:
						messages = e.Messages
					}
					for _, message := range messages {
						if m, ok := message.(agent.AgentMessage); ok && m.User != nil {
							m.User.Content = []ai.UserContentBlock{ai.TextContent{Text: "REQUEST-ONLY"}}
						}
					}
					return nil, nil
				}},
			}}}, fauxReply("ok", ai.StopReasonStop, 0))
			messages, err := h.session.Send(context.Background(), "ORIGINAL-PROMPT")
			if err != nil {
				t.Fatal(err)
			}
			h.provider.mu.Lock()
			request := h.provider.requests[0]
			h.provider.mu.Unlock()
			if !strings.Contains(request, "REQUEST-ONLY") {
				t.Fatalf("provider request = %s; want the in-place edit", request)
			}
			raw, _ := json.Marshal(messages)
			if strings.Contains(string(raw), "REQUEST-ONLY") || !strings.Contains(string(raw), "ORIGINAL-PROMPT") {
				t.Fatalf("session transcript = %s; the request-only edit leaked into history", raw)
			}
			for _, entry := range h.entries("message") {
				if strings.Contains(string(entry.Raw()), "REQUEST-ONLY") {
					t.Fatalf("persisted entry %s carries the request-only edit", entry.Raw())
				}
			}
		})
	}
}
