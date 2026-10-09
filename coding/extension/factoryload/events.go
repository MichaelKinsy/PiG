package factoryload

import (
	"context"
	"fmt"
	"reflect"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// handlerEvent returns the event a runner passed to a handler as E, whether the runner passed it by value or by pointer.
func handlerEvent[E any](event string, args []any) (E, context.Context, []any, error) {
	var zero E
	if len(args) < 2 {
		return zero, nil, nil, fmt.Errorf("%s handler called with %d arguments, want the event and its context", event, len(args))
	}
	var evt E
	switch v := args[0].(type) {
	case *E:
		if v == nil {
			return zero, nil, nil, fmt.Errorf("%s handler called with a nil event", event)
		}
		evt = *v
	case E:
		evt = v
	default:
		return zero, nil, nil, fmt.Errorf("%s handler called with event %T", event, args[0])
	}
	ctx, ok := args[1].(context.Context)
	if !ok {
		return zero, nil, nil, fmt.Errorf("%s handler called with context %T", event, args[1])
	}
	return evt, ctx, args[2:], nil
}

// resultOrNil is a handler result that carries nothing as nil, as a handler that returns `undefined` upstream, and as a pointer
// otherwise, which is the form the runner's emit methods read.
func resultOrNil[R any](result R) any {
	if reflect.ValueOf(&result).Elem().IsZero() {
		return nil
	}
	return &result
}

// on registers call for event and returns the function that removes exactly that registration.
// upstream: core/extensions/loader.ts:271-288 (on)
func (a *api) on(event string, call extension.HandlerFn) func() {
	a.assertActive()
	a.mu.Lock()
	a.nextHandler++
	id := a.nextHandler
	a.mu.Unlock()
	a.ext.AddEventHandler(event, id, call)
	return func() { a.ext.RemoveEventHandler(event, id) }
}

func onEvent[E any](a *api, event string, handler extension.ExtensionHandlerNoResult[E]) func() {
	return a.on(event, func(args ...any) (any, error) {
		evt, ctx, _, err := handlerEvent[E](event, args)
		if err != nil {
			return nil, err
		}
		return nil, handler(ctx, evt)
	})
}

func onResult[E, R any](a *api, event string, handler func(ctx context.Context, evt E) (R, error)) func() {
	return a.on(event, func(args ...any) (any, error) {
		evt, ctx, _, err := handlerEvent[E](event, args)
		if err != nil {
			return nil, err
		}
		result, err := handler(ctx, evt)
		return resultOrNil(result), err
	})
}

func (a *api) OnProjectTrust(handler extension.ProjectTrustHandler) func() {
	return a.on("project_trust", func(args ...any) (any, error) {
		evt, ctx, rest, err := handlerEvent[extension.ProjectTrustEvent]("project_trust", args)
		if err != nil {
			return nil, err
		}
		if len(rest) != 1 {
			return nil, fmt.Errorf("project_trust handler called without its trust context")
		}
		trust, ok := rest[0].(extension.ProjectTrustContext)
		if !ok {
			return nil, fmt.Errorf("project_trust handler called with trust context %T", rest[0])
		}
		return handler(ctx, evt, trust)
	})
}

func (a *api) OnResourcesDiscover(handler func(ctx context.Context, evt extension.ResourcesDiscoverEvent) (extension.ResourcesDiscoverResult, error)) func() {
	return onResult(a, "resources_discover", handler)
}

func (a *api) OnSessionStart(handler extension.ExtensionHandlerNoResult[extension.SessionStartEvent]) func() {
	return onEvent(a, "session_start", handler)
}

func (a *api) OnSessionInfoChanged(handler extension.ExtensionHandlerNoResult[extension.SessionInfoChangedEvent]) func() {
	return onEvent(a, "session_info_changed", handler)
}

func (a *api) OnMcpServersChange(handler extension.ExtensionHandlerNoResult[extension.McpServersChangeEvent]) func() {
	return onEvent(a, "mcp_servers_change", handler)
}

func (a *api) OnSessionBeforeSwitch(handler func(ctx context.Context, evt extension.SessionBeforeSwitchEvent) (extension.SessionBeforeSwitchResult, error)) func() {
	return onResult(a, "session_before_switch", handler)
}

func (a *api) OnSessionBeforeFork(handler func(ctx context.Context, evt extension.SessionBeforeForkEvent) (extension.SessionBeforeForkResult, error)) func() {
	return onResult(a, "session_before_fork", handler)
}

func (a *api) OnSessionBeforeCompact(handler func(ctx context.Context, evt extension.SessionBeforeCompactEvent) (extension.SessionBeforeCompactResult, error)) func() {
	return onResult(a, "session_before_compact", handler)
}

func (a *api) OnSessionCompact(handler extension.ExtensionHandlerNoResult[extension.SessionCompactEvent]) func() {
	return onEvent(a, "session_compact", handler)
}

func (a *api) OnSessionCompactFailed(handler extension.ExtensionHandlerNoResult[extension.SessionCompactFailedEvent]) func() {
	return onEvent(a, "session_compact_failed", handler)
}

func (a *api) OnSessionShutdown(handler extension.ExtensionHandlerNoResult[extension.SessionShutdownEvent]) func() {
	return onEvent(a, "session_shutdown", handler)
}

func (a *api) OnSessionBeforeTree(handler func(ctx context.Context, evt extension.SessionBeforeTreeEvent) (extension.SessionBeforeTreeResult, error)) func() {
	return onResult(a, "session_before_tree", handler)
}

func (a *api) OnSessionTree(handler extension.ExtensionHandlerNoResult[extension.SessionTreeEvent]) func() {
	return onEvent(a, "session_tree", handler)
}

func (a *api) OnContext(handler func(ctx context.Context, evt extension.ContextEvent) (extension.ContextEventResult, error)) func() {
	return onResult(a, "context", handler)
}

func (a *api) OnContextWithSystem(handler func(ctx context.Context, evt extension.ContextWithSystemEvent) (extension.ContextEventResult, error)) func() {
	return onResult(a, "context_with_system", handler)
}

// OnBeforeProviderRequest registers a handler whose non-nil result replaces the payload, as the runner reads it.
func (a *api) OnBeforeProviderRequest(handler func(ctx context.Context, evt extension.BeforeProviderRequestEvent) (extension.BeforeProviderRequestEventResult, error)) func() {
	return a.on("before_provider_request", func(args ...any) (any, error) {
		evt, ctx, _, err := handlerEvent[extension.BeforeProviderRequestEvent]("before_provider_request", args)
		if err != nil {
			return nil, err
		}
		return handler(ctx, evt)
	})
}

func (a *api) OnAfterProviderResponse(handler extension.ExtensionHandlerNoResult[extension.AfterProviderResponseEvent]) func() {
	return onEvent(a, "after_provider_response", handler)
}

func (a *api) OnBeforeProviderHeaders(handler extension.ExtensionHandlerNoResult[extension.BeforeProviderHeadersEvent]) func() {
	return onEvent(a, "before_provider_headers", handler)
}

func (a *api) OnProviderStreamEvent(handler extension.ExtensionHandlerNoResult[extension.ProviderStreamEvent]) func() {
	return onEvent(a, "provider_stream_event", handler)
}

func (a *api) OnBeforeAgentStart(handler func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error)) func() {
	return onResult(a, "before_agent_start", handler)
}

func (a *api) OnAgentStart(handler extension.ExtensionHandlerNoResult[extension.AgentStartEvent]) func() {
	return onEvent(a, "agent_start", handler)
}

func (a *api) OnAgentEnd(handler extension.ExtensionHandlerNoResult[extension.AgentEndEvent]) func() {
	return onEvent(a, "agent_end", handler)
}

func (a *api) OnAgentBeforeSettle(handler func(ctx context.Context, evt *extension.AgentBeforeSettleEvent) (extension.AgentBeforeSettleEventResult, error)) func() {
	return onResult(a, "agent_before_settle", handler)
}

func (a *api) OnAgentSettled(handler extension.ExtensionHandlerNoResult[extension.AgentSettledEvent]) func() {
	return onEvent(a, "agent_settled", handler)
}

func (a *api) OnUiPromptStart(handler extension.ExtensionHandlerNoResult[extension.UIPromptStartEvent]) func() {
	return onEvent(a, "ui_prompt_start", handler)
}

func (a *api) OnUiPromptEnd(handler extension.ExtensionHandlerNoResult[extension.UIPromptEndEvent]) func() {
	return onEvent(a, "ui_prompt_end", handler)
}

func (a *api) OnCacheWarmingDecision(handler func(ctx context.Context, evt extension.CacheWarmingDecisionEvent) (extension.CacheWarmingDecisionEventResult, error)) func() {
	return onResult(a, "cache_warming_decision", handler)
}

func (a *api) OnTurnStart(handler extension.ExtensionHandlerNoResult[extension.TurnStartEvent]) func() {
	return onEvent(a, "turn_start", handler)
}

func (a *api) OnTurnEnd(handler func(ctx context.Context, evt extension.TurnEndEvent) (extension.TurnEndEventResult, error)) func() {
	return onResult(a, "turn_end", handler)
}

func (a *api) OnMessageStart(handler extension.ExtensionHandlerNoResult[extension.MessageStartEvent]) func() {
	return onEvent(a, "message_start", handler)
}

func (a *api) OnMessageUpdate(handler extension.ExtensionHandlerNoResult[extension.MessageUpdateEvent]) func() {
	return onEvent(a, "message_update", handler)
}

func (a *api) OnMessageEnd(handler func(ctx context.Context, evt extension.MessageEndEvent) (extension.MessageEndEventResult, error)) func() {
	return onResult(a, "message_end", handler)
}

func (a *api) OnToolExecutionStart(handler extension.ExtensionHandlerNoResult[extension.ToolExecutionStartEvent]) func() {
	return onEvent(a, "tool_execution_start", handler)
}

func (a *api) OnToolExecutionUpdate(handler extension.ExtensionHandlerNoResult[extension.ToolExecutionUpdateEvent]) func() {
	return onEvent(a, "tool_execution_update", handler)
}

func (a *api) OnToolExecutionEnd(handler extension.ExtensionHandlerNoResult[extension.ToolExecutionEndEvent]) func() {
	return onEvent(a, "tool_execution_end", handler)
}

func (a *api) OnModelSelect(handler extension.ExtensionHandlerNoResult[extension.ModelSelectEvent]) func() {
	return onEvent(a, "model_select", handler)
}

func (a *api) OnThinkingLevelSelect(handler extension.ExtensionHandlerNoResult[extension.ThinkingLevelSelectEvent]) func() {
	return onEvent(a, "thinking_level_select", handler)
}

func (a *api) OnToolCall(handler func(ctx context.Context, evt extension.ToolCallEvent) (extension.ToolCallEventResult, error)) func() {
	return onResult(a, "tool_call", handler)
}

func (a *api) OnToolResult(handler func(ctx context.Context, evt extension.ToolResultEvent) (extension.ToolResultEventResult, error)) func() {
	return onResult(a, "tool_result", handler)
}

func (a *api) OnUserBash(handler func(ctx context.Context, evt extension.UserBashEvent) (extension.UserBashEventResult, error)) func() {
	return onResult(a, "user_bash", handler)
}

// OnInput registers a handler whose result is the InputEventResult the runner reads as it is.
func (a *api) OnInput(handler func(ctx context.Context, evt extension.InputEvent) (extension.InputEventResult, error)) func() {
	return a.on("input", func(args ...any) (any, error) {
		evt, ctx, _, err := handlerEvent[extension.InputEvent]("input", args)
		if err != nil {
			return nil, err
		}
		return handler(ctx, evt)
	})
}
