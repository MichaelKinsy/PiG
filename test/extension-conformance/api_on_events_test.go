package extensionconformance

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// bridgeVoid turns the typed handlers an extension registered through extension.API.On* into the runner's handler form: the event and the handler context are the two arguments the runner passes (runner.ts emit calls handler(event, ctx)).
func bridgeVoid[E any](handlers []func(context.Context, E) error) []extension.HandlerFn {
	out := make([]extension.HandlerFn, 0, len(handlers))
	for _, handler := range handlers {
		out = append(out, func(args ...any) (any, error) {
			return nil, handler(args[1].(context.Context), args[0].(E))
		})
	}
	return out
}

// TestConformanceAPIOnVoidEventsReachTheTypedHandlers is the in-process Go reference for the conformance suites that register pi.on(event) in every SDK (extensions/types.ts:1558-1642, `on(event: "<name>", handler)`; runner.ts:988-1017 emit). A typed handler an extension registers through extension.API.On<Event> receives the host's event unchanged, in registration order, and a handler that fails does not stop the handlers after it (runner.ts:691-699).
// mutation-checked: the mutant "Runner.Emit returns the handler error instead of running the next handler" fails it.
// mutation-checked: the mutant "Runner.Emit runs only the first handler of a snapshot" fails it.
func TestConformanceAPIOnVoidEventsReachTheTypedHandlers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		register func(api extension.API, record func(any), fail error)
		handlers func(f *extensiontest.Fake) []extension.HandlerFn
		event    extension.ExtensionEvent
	}{
		{
			name: "after_provider_response",
			register: func(api extension.API, record func(any), fail error) {
				api.OnAfterProviderResponse(func(_ context.Context, evt extension.AfterProviderResponseEvent) error { record(evt); return fail })
				api.OnAfterProviderResponse(func(_ context.Context, evt extension.AfterProviderResponseEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn {
				return bridgeVoid(f.OnAfterProviderResponseHandlers)
			},
			event: extension.AfterProviderResponseEvent{Type: "after_provider_response"},
		},
		{
			name: "agent_end",
			register: func(api extension.API, record func(any), fail error) {
				api.OnAgentEnd(func(_ context.Context, evt extension.AgentEndEvent) error { record(evt); return fail })
				api.OnAgentEnd(func(_ context.Context, evt extension.AgentEndEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnAgentEndHandlers) },
			event:    extension.AgentEndEvent{Type: "agent_end"},
		},
		{
			name: "agent_settled",
			register: func(api extension.API, record func(any), fail error) {
				api.OnAgentSettled(func(_ context.Context, evt extension.AgentSettledEvent) error { record(evt); return fail })
				api.OnAgentSettled(func(_ context.Context, evt extension.AgentSettledEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnAgentSettledHandlers) },
			event:    extension.AgentSettledEvent{Type: "agent_settled"},
		},
		{
			name: "agent_start",
			register: func(api extension.API, record func(any), fail error) {
				api.OnAgentStart(func(_ context.Context, evt extension.AgentStartEvent) error { record(evt); return fail })
				api.OnAgentStart(func(_ context.Context, evt extension.AgentStartEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnAgentStartHandlers) },
			event:    extension.AgentStartEvent{Type: "agent_start"},
		},
		{
			name: "mcp_servers_change",
			register: func(api extension.API, record func(any), fail error) {
				api.OnMcpServersChange(func(_ context.Context, evt extension.McpServersChangeEvent) error { record(evt); return fail })
				api.OnMcpServersChange(func(_ context.Context, evt extension.McpServersChangeEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnMcpServersChangeHandlers) },
			event:    extension.McpServersChangeEvent{Type: "mcp_servers_change"},
		},
		{
			name: "message_start",
			register: func(api extension.API, record func(any), fail error) {
				api.OnMessageStart(func(_ context.Context, evt extension.MessageStartEvent) error { record(evt); return fail })
				api.OnMessageStart(func(_ context.Context, evt extension.MessageStartEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnMessageStartHandlers) },
			event:    extension.MessageStartEvent{Type: "message_start"},
		},
		{
			name: "message_update",
			register: func(api extension.API, record func(any), fail error) {
				api.OnMessageUpdate(func(_ context.Context, evt extension.MessageUpdateEvent) error { record(evt); return fail })
				api.OnMessageUpdate(func(_ context.Context, evt extension.MessageUpdateEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnMessageUpdateHandlers) },
			event:    extension.MessageUpdateEvent{Type: "message_update"},
		},
		{
			name: "provider_stream_event",
			register: func(api extension.API, record func(any), fail error) {
				api.OnProviderStreamEvent(func(_ context.Context, evt extension.ProviderStreamEvent) error { record(evt); return fail })
				api.OnProviderStreamEvent(func(_ context.Context, evt extension.ProviderStreamEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnProviderStreamEventHandlers) },
			event:    extension.ProviderStreamEvent{Type: "provider_stream_event"},
		},
		{
			name: "session_compact",
			register: func(api extension.API, record func(any), fail error) {
				api.OnSessionCompact(func(_ context.Context, evt extension.SessionCompactEvent) error { record(evt); return fail })
				api.OnSessionCompact(func(_ context.Context, evt extension.SessionCompactEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnSessionCompactHandlers) },
			event:    extension.SessionCompactEvent{Type: "session_compact"},
		},
		{
			name: "session_compact_failed",
			register: func(api extension.API, record func(any), fail error) {
				api.OnSessionCompactFailed(func(_ context.Context, evt extension.SessionCompactFailedEvent) error { record(evt); return fail })
				api.OnSessionCompactFailed(func(_ context.Context, evt extension.SessionCompactFailedEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnSessionCompactFailedHandlers) },
			event:    extension.SessionCompactFailedEvent{Type: "session_compact_failed"},
		},
		{
			name: "session_info_changed",
			register: func(api extension.API, record func(any), fail error) {
				api.OnSessionInfoChanged(func(_ context.Context, evt extension.SessionInfoChangedEvent) error { record(evt); return fail })
				api.OnSessionInfoChanged(func(_ context.Context, evt extension.SessionInfoChangedEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnSessionInfoChangedHandlers) },
			event:    extension.SessionInfoChangedEvent{Type: "session_info_changed"},
		},
		{
			name: "session_shutdown",
			register: func(api extension.API, record func(any), fail error) {
				api.OnSessionShutdown(func(_ context.Context, evt extension.SessionShutdownEvent) error { record(evt); return fail })
				api.OnSessionShutdown(func(_ context.Context, evt extension.SessionShutdownEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnSessionShutdownHandlers) },
			event:    extension.SessionShutdownEvent{Type: "session_shutdown"},
		},
		{
			name: "session_tree",
			register: func(api extension.API, record func(any), fail error) {
				api.OnSessionTree(func(_ context.Context, evt extension.SessionTreeEvent) error { record(evt); return fail })
				api.OnSessionTree(func(_ context.Context, evt extension.SessionTreeEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnSessionTreeHandlers) },
			event:    extension.SessionTreeEvent{Type: "session_tree"},
		},
		{
			name: "tool_execution_end",
			register: func(api extension.API, record func(any), fail error) {
				api.OnToolExecutionEnd(func(_ context.Context, evt extension.ToolExecutionEndEvent) error { record(evt); return fail })
				api.OnToolExecutionEnd(func(_ context.Context, evt extension.ToolExecutionEndEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnToolExecutionEndHandlers) },
			event:    extension.ToolExecutionEndEvent{Type: "tool_execution_end"},
		},
		{
			name: "tool_execution_start",
			register: func(api extension.API, record func(any), fail error) {
				api.OnToolExecutionStart(func(_ context.Context, evt extension.ToolExecutionStartEvent) error { record(evt); return fail })
				api.OnToolExecutionStart(func(_ context.Context, evt extension.ToolExecutionStartEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnToolExecutionStartHandlers) },
			event:    extension.ToolExecutionStartEvent{Type: "tool_execution_start"},
		},
		{
			name: "tool_execution_update",
			register: func(api extension.API, record func(any), fail error) {
				api.OnToolExecutionUpdate(func(_ context.Context, evt extension.ToolExecutionUpdateEvent) error { record(evt); return fail })
				api.OnToolExecutionUpdate(func(_ context.Context, evt extension.ToolExecutionUpdateEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnToolExecutionUpdateHandlers) },
			event:    extension.ToolExecutionUpdateEvent{Type: "tool_execution_update"},
		},
		{
			name: "turn_start",
			register: func(api extension.API, record func(any), fail error) {
				api.OnTurnStart(func(_ context.Context, evt extension.TurnStartEvent) error { record(evt); return fail })
				api.OnTurnStart(func(_ context.Context, evt extension.TurnStartEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnTurnStartHandlers) },
			event:    extension.TurnStartEvent{Type: "turn_start"},
		},
		{
			name: "ui_prompt_start",
			register: func(api extension.API, record func(any), fail error) {
				api.OnUiPromptStart(func(_ context.Context, evt extension.UIPromptStartEvent) error { record(evt); return fail })
				api.OnUiPromptStart(func(_ context.Context, evt extension.UIPromptStartEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnUiPromptStartHandlers) },
			event:    extension.UIPromptStartEvent{Type: "ui_prompt_start"},
		},
		{
			name: "ui_prompt_end",
			register: func(api extension.API, record func(any), fail error) {
				api.OnUiPromptEnd(func(_ context.Context, evt extension.UIPromptEndEvent) error { record(evt); return fail })
				api.OnUiPromptEnd(func(_ context.Context, evt extension.UIPromptEndEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnUiPromptEndHandlers) },
			event:    extension.UIPromptEndEvent{Type: "ui_prompt_end"},
		},
		{
			name: "model_select",
			register: func(api extension.API, record func(any), fail error) {
				api.OnModelSelect(func(_ context.Context, evt extension.ModelSelectEvent) error { record(evt); return fail })
				api.OnModelSelect(func(_ context.Context, evt extension.ModelSelectEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnModelSelectHandlers) },
			event:    extension.ModelSelectEvent{Type: "model_select"},
		},
		{
			name: "thinking_level_select",
			register: func(api extension.API, record func(any), fail error) {
				api.OnThinkingLevelSelect(func(_ context.Context, evt extension.ThinkingLevelSelectEvent) error { record(evt); return fail })
				api.OnThinkingLevelSelect(func(_ context.Context, evt extension.ThinkingLevelSelectEvent) error { record(evt); return nil })
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeVoid(f.OnThinkingLevelSelectHandlers) },
			event:    extension.ThinkingLevelSelectEvent{Type: "thinking_level_select"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &extensiontest.Fake{}
			var api extension.API = fake
			var seen []any
			boom := errors.New("handler failed")
			tc.register(api, func(evt any) { seen = append(seen, evt) }, boom)
			ext := extension.Extension{Path: "api-reference", Handlers: map[string][]extension.HandlerFn{tc.name: tc.handlers(fake)}}
			runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
			if _, err := runner.Emit(t.Context(), tc.event); err != nil {
				t.Fatalf("emit %s: %v", tc.name, err)
			}
			if len(seen) != 2 {
				t.Fatalf("%s reached %d handlers, want both: the first one failed and the second must still run", tc.name, len(seen))
			}
			for i, got := range seen {
				if !reflect.DeepEqual(got, tc.event) {
					t.Fatalf("%s handler %d saw %#v, want the emitted %#v", tc.name, i, got, tc.event)
				}
			}
		})
	}
}
