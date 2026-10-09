package inproc

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
)

// voidSubscription registers a handler through one API.On* member on a fake API and returns the stored handler, so the runner
// dispatches exactly what the member kept.
func voidSubscription[E any](t *testing.T, on func(extension.API, func(context.Context, E) error) func(), stored func(*extensiontest.Fake) []func(context.Context, E) error, seen *[]E) extension.HandlerFn {
	t.Helper()
	fake := &extensiontest.Fake{}
	unsubscribe := on(fake, func(_ context.Context, event E) error {
		*seen = append(*seen, event)
		return nil
	})
	handlers := stored(fake)
	if len(handlers) != 1 || unsubscribe == nil {
		t.Fatalf("API.On* kept %d handlers, unsubscribe nil=%v", len(handlers), unsubscribe == nil)
	}
	return func(args ...any) (any, error) { return nil, handlers[0](context.Background(), args[0].(E)) }
}

func emitsTo[E extension.ExtensionEvent](t *testing.T, eventType string, event E, on func(extension.API, func(context.Context, E) error) func(), stored func(*extensiontest.Fake) []func(context.Context, E) error) {
	t.Helper()
	var seen []E
	handler := voidSubscription(t, on, stored, &seen)
	runner := NewRunner([]extension.Extension{{Path: "typed", Handlers: map[string][]extension.HandlerFn{eventType: {handler}}}}, t.TempDir())
	if _, err := runner.Emit(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 {
		t.Fatalf("%s: typed handler saw %d events, want 1", eventType, len(seen))
	}
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1563 (API.on, "session_start"); every `on(event, handler)` overload of
// ExtensionAPI subscribes a handler that the runner's emit delivers the matching event to. Each Go API.On* member keeps its
// handler and the runner dispatches the typed event to it.
func TestAPIOnMembersDeliverTheirEventsThroughTheRunner(t *testing.T) {
	emitsTo(t, "session_start", extension.SessionStartEvent{Type: "session_start", Reason: "startup"},
		func(a extension.API, h func(context.Context, extension.SessionStartEvent) error) func() {
			return a.OnSessionStart(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.SessionStartEvent) error {
			return f.OnSessionStartHandlers
		})
	emitsTo(t, "session_info_changed", extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: "n"},
		func(a extension.API, h func(context.Context, extension.SessionInfoChangedEvent) error) func() {
			return a.OnSessionInfoChanged(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.SessionInfoChangedEvent) error {
			return f.OnSessionInfoChangedHandlers
		})
	emitsTo(t, "session_compact_failed", extension.SessionCompactFailedEvent{Type: "session_compact_failed"},
		func(a extension.API, h func(context.Context, extension.SessionCompactFailedEvent) error) func() {
			return a.OnSessionCompactFailed(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.SessionCompactFailedEvent) error {
			return f.OnSessionCompactFailedHandlers
		})
	emitsTo(t, "session_compact", extension.SessionCompactEvent{Type: "session_compact"},
		func(a extension.API, h func(context.Context, extension.SessionCompactEvent) error) func() {
			return a.OnSessionCompact(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.SessionCompactEvent) error {
			return f.OnSessionCompactHandlers
		})
	emitsTo(t, "session_shutdown", extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "quit"},
		func(a extension.API, h func(context.Context, extension.SessionShutdownEvent) error) func() {
			return a.OnSessionShutdown(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.SessionShutdownEvent) error {
			return f.OnSessionShutdownHandlers
		})
	emitsTo(t, "mcp_servers_change", extension.McpServersChangeEvent{Type: "mcp_servers_change"},
		func(a extension.API, h func(context.Context, extension.McpServersChangeEvent) error) func() {
			return a.OnMcpServersChange(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.McpServersChangeEvent) error {
			return f.OnMcpServersChangeHandlers
		})
	emitsTo(t, "session_tree", extension.SessionTreeEvent{Type: "session_tree"},
		func(a extension.API, h func(context.Context, extension.SessionTreeEvent) error) func() {
			return a.OnSessionTree(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.SessionTreeEvent) error {
			return f.OnSessionTreeHandlers
		})
	emitsTo(t, "after_provider_response", extension.AfterProviderResponseEvent{Type: "after_provider_response", Status: 200},
		func(a extension.API, h func(context.Context, extension.AfterProviderResponseEvent) error) func() {
			return a.OnAfterProviderResponse(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.AfterProviderResponseEvent) error {
			return f.OnAfterProviderResponseHandlers
		})
	emitsTo(t, "provider_stream_event", extension.ProviderStreamEvent{Type: "provider_stream_event", Provider: "p"},
		func(a extension.API, h func(context.Context, extension.ProviderStreamEvent) error) func() {
			return a.OnProviderStreamEvent(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.ProviderStreamEvent) error {
			return f.OnProviderStreamEventHandlers
		})
	emitsTo(t, "agent_start", extension.AgentStartEvent{Type: "agent_start"},
		func(a extension.API, h func(context.Context, extension.AgentStartEvent) error) func() {
			return a.OnAgentStart(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.AgentStartEvent) error {
			return f.OnAgentStartHandlers
		})
	emitsTo(t, "agent_end", extension.AgentEndEvent{Type: "agent_end"},
		func(a extension.API, h func(context.Context, extension.AgentEndEvent) error) func() {
			return a.OnAgentEnd(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.AgentEndEvent) error {
			return f.OnAgentEndHandlers
		})
	emitsTo(t, "agent_settled", extension.AgentSettledEvent{Type: "agent_settled"},
		func(a extension.API, h func(context.Context, extension.AgentSettledEvent) error) func() {
			return a.OnAgentSettled(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.AgentSettledEvent) error {
			return f.OnAgentSettledHandlers
		})
	emitsTo(t, "ui_prompt_start", extension.UIPromptStartEvent{Type: "ui_prompt_start"},
		func(a extension.API, h func(context.Context, extension.UIPromptStartEvent) error) func() {
			return a.OnUiPromptStart(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.UIPromptStartEvent) error {
			return f.OnUiPromptStartHandlers
		})
	emitsTo(t, "ui_prompt_end", extension.UIPromptEndEvent{Type: "ui_prompt_end"},
		func(a extension.API, h func(context.Context, extension.UIPromptEndEvent) error) func() {
			return a.OnUiPromptEnd(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.UIPromptEndEvent) error {
			return f.OnUiPromptEndHandlers
		})
	emitsTo(t, "turn_start", extension.TurnStartEvent{Type: "turn_start", TurnIndex: 3},
		func(a extension.API, h func(context.Context, extension.TurnStartEvent) error) func() {
			return a.OnTurnStart(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.TurnStartEvent) error {
			return f.OnTurnStartHandlers
		})
	emitsTo(t, "message_start", extension.MessageStartEvent{Type: "message_start"},
		func(a extension.API, h func(context.Context, extension.MessageStartEvent) error) func() {
			return a.OnMessageStart(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.MessageStartEvent) error {
			return f.OnMessageStartHandlers
		})
	emitsTo(t, "message_update", extension.MessageUpdateEvent{Type: "message_update"},
		func(a extension.API, h func(context.Context, extension.MessageUpdateEvent) error) func() {
			return a.OnMessageUpdate(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.MessageUpdateEvent) error {
			return f.OnMessageUpdateHandlers
		})
	emitsTo(t, "tool_execution_start", extension.ToolExecutionStartEvent{Type: "tool_execution_start", ToolCallID: "c"},
		func(a extension.API, h func(context.Context, extension.ToolExecutionStartEvent) error) func() {
			return a.OnToolExecutionStart(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.ToolExecutionStartEvent) error {
			return f.OnToolExecutionStartHandlers
		})
	emitsTo(t, "tool_execution_update", extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: "c"},
		func(a extension.API, h func(context.Context, extension.ToolExecutionUpdateEvent) error) func() {
			return a.OnToolExecutionUpdate(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.ToolExecutionUpdateEvent) error {
			return f.OnToolExecutionUpdateHandlers
		})
	emitsTo(t, "tool_execution_end", extension.ToolExecutionEndEvent{Type: "tool_execution_end", ToolCallID: "c"},
		func(a extension.API, h func(context.Context, extension.ToolExecutionEndEvent) error) func() {
			return a.OnToolExecutionEnd(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.ToolExecutionEndEvent) error {
			return f.OnToolExecutionEndHandlers
		})
	emitsTo(t, "model_select", extension.ModelSelectEvent{Type: "model_select"},
		func(a extension.API, h func(context.Context, extension.ModelSelectEvent) error) func() {
			return a.OnModelSelect(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.ModelSelectEvent) error {
			return f.OnModelSelectHandlers
		})
	emitsTo(t, "thinking_level_select", extension.ThinkingLevelSelectEvent{Type: "thinking_level_select"},
		func(a extension.API, h func(context.Context, extension.ThinkingLevelSelectEvent) error) func() {
			return a.OnThinkingLevelSelect(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.ThinkingLevelSelectEvent) error {
			return f.OnThinkingLevelSelectHandlers
		})
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1586 (API.on, "context"); packages/coding-agent/src/core/extensions/types.ts:1587 (API.on, "context_with_system"); packages/coding-agent/src/core/extensions/types.ts:1596 (API.on, "before_provider_headers"); packages/coding-agent/src/core/extensions/types.ts:1625 (API.on, "input").
// A typed handler an API.On* member kept returns a result the runner applies: context handlers replace the messages, the
// input handler transforms the text, and the provider request handler replaces the payload.
func TestAPIOnMembersKeepHandlersWhoseResultsTheRunnerApplies(t *testing.T) {
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	api.OnContext(func(_ context.Context, event extension.ContextEvent) (extension.ContextEventResult, error) {
		return extension.ContextEventResult{Messages: append(slices.Clone(event.Messages), customMessage("added by context"))}, nil
	})
	api.OnContextWithSystem(func(_ context.Context, event extension.ContextWithSystemEvent) (extension.ContextEventResult, error) {
		return extension.ContextEventResult{Messages: append(slices.Clone(event.Messages), customMessage("added with system"))}, nil
	})
	api.OnInput(func(_ context.Context, event extension.InputEvent) (extension.InputEventResult, error) {
		return extension.InputEventResultTransform{Text: event.Text + "!"}, nil
	})
	api.OnBeforeProviderRequest(func(_ context.Context, event extension.BeforeProviderRequestEvent) (extension.BeforeProviderRequestEventResult, error) {
		return "replaced", nil
	})
	runner := NewRunner([]extension.Extension{{Path: "typed", Handlers: map[string][]extension.HandlerFn{
		"context": {func(args ...any) (any, error) {
			result, err := fake.OnContextHandlers[0](context.Background(), args[0].(extension.ContextEvent))
			return &result, err
		}},
		"context_with_system": {func(args ...any) (any, error) {
			result, err := fake.OnContextWithSystemHandlers[0](context.Background(), args[0].(extension.ContextWithSystemEvent))
			return &result, err
		}},
		"input": {func(args ...any) (any, error) {
			return fake.OnInputHandlers[0](context.Background(), args[0].(extension.InputEvent))
		}},
		"before_provider_request": {func(args ...any) (any, error) {
			return fake.OnBeforeProviderRequestHandlers[0](context.Background(), args[0].(extension.BeforeProviderRequestEvent))
		}},
	}}}, t.TempDir())
	messages, err := runner.EmitContext(t.Context(), []extension.AgentMessage{customMessage("first")})
	if err != nil || !reflect.DeepEqual(messages, []extension.AgentMessage{customMessage("first"), customMessage("added by context")}) {
		t.Fatalf("context messages = %v, %v", messages, err)
	}
	messages, err = runner.EmitContextWithSystem(t.Context(), []extension.AgentMessage{customMessage("first")})
	if err != nil || !reflect.DeepEqual(messages, []extension.AgentMessage{customMessage("first"), customMessage("added with system")}) {
		t.Fatalf("context_with_system messages = %v, %v", messages, err)
	}
	input, err := runner.EmitInput(t.Context(), "hello", nil, extension.InputSource("interactive"), "")
	if transform, ok := input.(extension.InputEventResultTransform); err != nil || !ok || transform.Text != "hello!" {
		t.Fatalf("input result = %#v, %v", input, err)
	}
	payload, err := runner.EmitBeforeProviderRequest(t.Context(), "original")
	if err != nil || payload != "replaced" {
		t.Fatalf("payload = %v, %v", payload, err)
	}
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1563 (API.on, "session_start"); a typed handler of a session_before_* event that cancels stops dispatch: later handlers do not run
// and the runner returns the cancelling result (runner.ts emit for the four session_before_* events).
func TestAPIOnSessionBeforeMembersKeepCancellingHandlers(t *testing.T) {
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	api.OnSessionBeforeSwitch(func(context.Context, extension.SessionBeforeSwitchEvent) (extension.SessionBeforeSwitchResult, error) {
		return extension.SessionBeforeSwitchResult{Cancel: true}, nil
	})
	api.OnSessionBeforeFork(func(context.Context, extension.SessionBeforeForkEvent) (extension.SessionBeforeForkResult, error) {
		return extension.SessionBeforeForkResult{Cancel: true}, nil
	})
	api.OnSessionBeforeCompact(func(context.Context, extension.SessionBeforeCompactEvent) (extension.SessionBeforeCompactResult, error) {
		return extension.SessionBeforeCompactResult{Cancel: true}, nil
	})
	api.OnSessionBeforeTree(func(context.Context, extension.SessionBeforeTreeEvent) (extension.SessionBeforeTreeResult, error) {
		return extension.SessionBeforeTreeResult{Cancel: true}, nil
	})
	later := 0
	counted := func(...any) (any, error) { later++; return nil, nil }
	handlers := func(typed extension.HandlerFn, name string) map[string][]extension.HandlerFn {
		return map[string][]extension.HandlerFn{name: {typed, counted}}
	}
	for _, c := range []struct {
		name  string
		event extension.ExtensionEvent
		typed extension.HandlerFn
	}{
		{"session_before_switch", extension.SessionBeforeSwitchEvent{Type: "session_before_switch"}, func(args ...any) (any, error) {
			return fake.OnSessionBeforeSwitchHandlers[0](context.Background(), args[0].(extension.SessionBeforeSwitchEvent))
		}},
		{"session_before_fork", extension.SessionBeforeForkEvent{Type: "session_before_fork"}, func(args ...any) (any, error) {
			return fake.OnSessionBeforeForkHandlers[0](context.Background(), args[0].(extension.SessionBeforeForkEvent))
		}},
		{"session_before_compact", extension.SessionBeforeCompactEvent{Type: "session_before_compact"}, func(args ...any) (any, error) {
			return fake.OnSessionBeforeCompactHandlers[0](context.Background(), args[0].(extension.SessionBeforeCompactEvent))
		}},
		{"session_before_tree", extension.SessionBeforeTreeEvent{Type: "session_before_tree"}, func(args ...any) (any, error) {
			return fake.OnSessionBeforeTreeHandlers[0](context.Background(), args[0].(extension.SessionBeforeTreeEvent))
		}},
	} {
		runner := NewRunner([]extension.Extension{{Path: "typed", Handlers: handlers(c.typed, c.name)}}, t.TempDir())
		result, err := runner.Emit(t.Context(), c.event)
		if err != nil || result == nil {
			t.Fatalf("%s: result %v, %v", c.name, result, err)
		}
		if later != 0 {
			t.Fatalf("%s: a handler after the cancelling one ran", c.name)
		}
	}
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1622 (API.on, "tool_call"); packages/coding-agent/src/core/extensions/types.ts:1623 (API.on, "tool_result"); packages/coding-agent/src/core/extensions/types.ts:1596 (API.on, "before_provider_headers").
// A tool_call handler an API.On* member kept blocks the call with its reason, a tool_result handler marks the result as an error,
// and a before_provider_headers handler receives the headers.
func TestAPIOnToolMembersKeepHandlersWhoseResultsTheRunnerReturns(t *testing.T) {
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	api.OnToolCall(func(_ context.Context, event extension.ToolCallEvent) (extension.ToolCallEventResult, error) {
		return extension.ToolCallEventResult{Block: true, Reason: "denied"}, nil
	})
	api.OnToolResult(func(_ context.Context, event extension.ToolResultEvent) (extension.ToolResultEventResult, error) {
		return extension.ToolResultEventResult{IsError: new(true)}, nil
	})
	runner := NewRunner([]extension.Extension{{Path: "typed", Handlers: map[string][]extension.HandlerFn{
		"tool_call": {func(args ...any) (any, error) {
			result, err := fake.OnToolCallHandlers[0](context.Background(), args[0].(extension.ToolCallEvent))
			return &result, err
		}},
		"tool_result": {func(args ...any) (any, error) {
			result, err := fake.OnToolResultHandlers[0](context.Background(), args[0].(extension.ToolResultEvent))
			return &result, err
		}},
	}}}, t.TempDir())
	call, err := runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "c1"}, ToolName: "bash"})
	if err != nil || call == nil || !call.Block || call.Reason != "denied" {
		t.Fatalf("tool_call result = %+v, %v", call, err)
	}
	result, err := runner.EmitToolResult(t.Context(), extension.BashToolResultEvent{ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "c1"}, ToolName: "bash"})
	if err != nil || result == nil || result.IsError == nil || !*result.IsError {
		t.Fatalf("tool_result result = %+v, %v", result, err)
	}
	emitsTo(t, "before_provider_headers", extension.BeforeProviderHeadersEvent{Type: "before_provider_headers", Headers: extension.ProviderHeaders{"x": new("1")}},
		func(a extension.API, h func(context.Context, extension.BeforeProviderHeadersEvent) error) func() {
			return a.OnBeforeProviderHeaders(h)
		},
		func(f *extensiontest.Fake) []func(context.Context, extension.BeforeProviderHeadersEvent) error {
			return f.OnBeforeProviderHeadersHandlers
		})
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1558 (API.on, "project_trust"); packages/coding-agent/src/core/extensions/types.ts:1559 (API.on, "resources_discover"); packages/coding-agent/src/core/extensions/types.ts:1599 (API.on, "before_agent_start").
// A typed project_trust handler an API.On* member kept decides the trust, a resources_discover handler contributes paths attributed to its
// extension, and a before_agent_start handler replaces the system prompt.
func TestAPIOnLifecycleMembersKeepHandlersWhoseResultsTheRunnerAggregates(t *testing.T) {
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	api.OnProjectTrust(func(_ context.Context, event extension.ProjectTrustEvent, _ extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
		return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustNo}, nil
	})
	api.OnResourcesDiscover(func(_ context.Context, event extension.ResourcesDiscoverEvent) (extension.ResourcesDiscoverResult, error) {
		return extension.ResourcesDiscoverResult{SkillPaths: []string{"skills/a"}}, nil
	})
	api.OnBeforeAgentStart(func(_ context.Context, event extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
		return extension.BeforeAgentStartEventResult{SystemPrompt: new("replaced prompt")}, nil
	})
	runner := NewRunner([]extension.Extension{{Path: "typed", Handlers: map[string][]extension.HandlerFn{
		"project_trust": {func(args ...any) (any, error) {
			result, err := fake.OnProjectTrustHandlers[0](context.Background(), args[0].(extension.ProjectTrustEvent), args[2].(extension.ProjectTrustContext))
			return result, err
		}},
		"resources_discover": {func(args ...any) (any, error) {
			result, err := fake.OnResourcesDiscoverHandlers[0](context.Background(), args[0].(extension.ResourcesDiscoverEvent))
			return &result, err
		}},
		"before_agent_start": {func(args ...any) (any, error) {
			result, err := fake.OnBeforeAgentStartHandlers[0](context.Background(), args[0].(extension.BeforeAgentStartEvent))
			return &result, err
		}},
	}}}, t.TempDir())
	trust, _, err := EmitProjectTrust(runner, t.Context(), extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/w"}, extension.ProjectTrustContext{})
	if err != nil || trust == nil || trust.Trusted != extension.ProjectTrustNo {
		t.Fatalf("project_trust = %+v, %v", trust, err)
	}
	resources, err := runner.EmitResourcesDiscover(t.Context(), "/w", "startup")
	if err != nil || len(resources.SkillPaths) != 1 || resources.SkillPaths[0].Path != "skills/a" || resources.SkillPaths[0].ExtensionPath == "" {
		t.Fatalf("resources = %+v, %v", resources, err)
	}
	start, err := runner.EmitBeforeAgentStart(t.Context(), "hi", nil, extension.BuildSystemPromptOptions{})
	if err != nil || start == nil || start.SystemPrompt == nil || *start.SystemPrompt != "replaced prompt" {
		t.Fatalf("before_agent_start = %+v, %v", start, err)
	}
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1616 (API.on, "message_end"); packages/coding-agent/src/core/extensions/types.ts:1624 (API.on, "user_bash").
// A message_end handler an API.On* member kept replaces the message (same role), and a user_bash handler supplies the command's result.
func TestAPIOnMessageEndAndUserBashMembersKeepHandlersWhoseResultsTheRunnerReturns(t *testing.T) {
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	replacement := assistantText("edited")
	api.OnMessageEnd(func(_ context.Context, event extension.MessageEndEvent) (extension.MessageEndEventResult, error) {
		return extension.MessageEndEventResult{Message: &replacement}, nil
	})
	api.OnUserBash(func(_ context.Context, event extension.UserBashEvent) (extension.UserBashEventResult, error) {
		return extension.UserBashEventResult{Result: map[string]any{"output": "from handler", "exitCode": 0, "cancelled": false, "truncated": false}}, nil
	})
	runner := NewRunner([]extension.Extension{{Path: "typed", Handlers: map[string][]extension.HandlerFn{
		"message_end": {func(args ...any) (any, error) {
			result, err := fake.OnMessageEndHandlers[0](context.Background(), args[0].(extension.MessageEndEvent))
			return &result, err
		}},
		"user_bash": {func(args ...any) (any, error) {
			result, err := fake.OnUserBashHandlers[0](context.Background(), args[0].(extension.UserBashEvent))
			return &result, err
		}},
	}}}, t.TempDir())
	ended, err := runner.EmitMessageEnd(t.Context(), extension.MessageEndEvent{Type: "message_end", Message: assistantText("original")})
	if err != nil || ended == nil || !reflect.DeepEqual(*ended, replacement) {
		t.Fatalf("message_end = %v, %v", ended, err)
	}
	bash, err := runner.EmitUserBash(t.Context(), extension.UserBashEvent{Type: "user_bash", Command: "ls", Cwd: "/w"})
	if err != nil || bash == nil || !reflect.DeepEqual(bash.Result, map[string]any{"output": "from handler", "exitCode": 0, "cancelled": false, "truncated": false}) {
		t.Fatalf("user_bash = %+v, %v", bash, err)
	}
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1606 (API.on, "agent_before_settle").
// An agent_before_settle handler an API.On* member kept returns boundary entries and a continuation the runner's boundary dispatch
// applies, as it does for turn_end.
func TestAPIOnAgentBeforeSettleMemberKeepsAHandlerWhoseBoundaryResultTheRunnerApplies(t *testing.T) {
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	api.OnAgentBeforeSettle(func(_ context.Context, event *extension.AgentBeforeSettleEvent) (extension.AgentBeforeSettleEventResult, error) {
		draft := append(slices.Clone(event.Entries), extension.SessionBoundaryDraft{Type: "custom", CustomType: "settle"})
		proceed := true
		return extension.AgentBeforeSettleEventResult{Entries: &draft, Continue: &proceed}, nil
	})
	runner := NewRunner([]extension.Extension{{Path: "typed", Handlers: map[string][]extension.HandlerFn{
		"agent_before_settle": {func(args ...any) (any, error) {
			return fake.OnAgentBeforeSettleHandlers[0](context.Background(), args[0].(*extension.AgentBeforeSettleEvent))
		}},
	}}}, t.TempDir())
	result, err := runner.EmitBoundary(context.Background(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Outcome: extension.AgentActivityCompleted}},
		func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, len(entries))}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || !result.Continue || len(result.Entries) != 1 || result.Entries[0].CustomType != "settle" {
		t.Fatalf("result = %+v, want the typed handler's entry and continuation", result)
	}
}

// customMessage is a custom-role AgentMessage distinguished by its text.
func customMessage(text string) extension.AgentMessage {
	return agent.AgentMessage{Custom: map[string]any{"role": "custom", "content": text}}
}

// assistantText is an assistant AgentMessage with one text block.
func assistantText(text string) extension.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}}}
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1559 (ExtensionHandler<E, R>) and :1622 (on "tool_call"): a handler of event E whose result is R, run by the runner for that event.
// A tool_call handler declared as ExtensionHandler[ToolCallEvent, ToolCallEventResult] blocks the call with its reason, and a handler that returns an error fails the emit.
func TestExtensionHandlerTypesTheHandlerTheRunnerRunsPerEvent(t *testing.T) {
	blocking := func(_ context.Context, event extension.CustomToolCallEvent) (extension.ToolCallEventResult, error) {
		return extension.ToolCallEventResult{Block: true, Reason: "denied " + event.ToolName}, nil
	}
	adapt := func(handler extension.ExtensionHandler[extension.CustomToolCallEvent, extension.ToolCallEventResult]) extension.HandlerFn {
		return func(args ...any) (any, error) {
			result, err := handler(context.Background(), args[0].(extension.CustomToolCallEvent))
			return &result, err
		}
	}
	event := extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "c1"}, ToolName: "bash"}
	runner := NewRunner([]extension.Extension{{Path: "typed", Handlers: map[string][]extension.HandlerFn{"tool_call": {adapt(blocking)}}}}, t.TempDir())
	call, err := runner.EmitToolCall(t.Context(), event)
	if err != nil || call == nil || !call.Block || call.Reason != "denied bash" {
		t.Fatalf("EmitToolCall = %+v, %v, want the typed handler's block", call, err)
	}
	failing := adapt(func(context.Context, extension.CustomToolCallEvent) (extension.ToolCallEventResult, error) {
		return extension.ToolCallEventResult{}, errors.New("handler failed")
	})
	runner = NewRunner([]extension.Extension{{Path: "typed", Handlers: map[string][]extension.HandlerFn{"tool_call": {failing}}}}, t.TempDir())
	if call, err := runner.EmitToolCall(t.Context(), event); err == nil || call != nil && call.Block {
		t.Fatalf("EmitToolCall with a failing handler = %+v, %v, want the handler's error (runner.ts emitToolCall does not catch a throwing handler)", call, err)
	}
}
