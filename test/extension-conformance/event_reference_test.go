package extensionconformance

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// eventReference is the Go reference for the notification members of extension.API (the pi.on overloads whose handler only observes the event):
// each OnX adds a handler for its event to a loaded extension, which the production Runner delivers to exactly as it delivers to an SDK extension's
// handler (extensions/types.ts:1558-1642; runner.ts emit).
type eventReference struct {
	extension.API
	ext *extension.Extension

	mu     sync.Mutex
	nextID int
}

func observe[T any](r *eventReference, event string, handler func(context.Context, T) error) func() {
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.mu.Unlock()
	r.ext.AddEventHandler(event, id, func(args ...any) (any, error) {
		return nil, handler(context.Background(), args[0].(T))
	})
	return func() { r.ext.RemoveEventHandler(event, id) }
}

func (r *eventReference) OnSessionStart(h func(context.Context, extension.SessionStartEvent) error) func() {
	return observe(r, "session_start", h)
}

func (r *eventReference) OnSessionInfoChanged(h func(context.Context, extension.SessionInfoChangedEvent) error) func() {
	return observe(r, "session_info_changed", h)
}

func (r *eventReference) OnSessionCompact(h func(context.Context, extension.SessionCompactEvent) error) func() {
	return observe(r, "session_compact", h)
}

func (r *eventReference) OnSessionCompactFailed(h func(context.Context, extension.SessionCompactFailedEvent) error) func() {
	return observe(r, "session_compact_failed", h)
}

func (r *eventReference) OnSessionShutdown(h func(context.Context, extension.SessionShutdownEvent) error) func() {
	return observe(r, "session_shutdown", h)
}

func (r *eventReference) OnSessionTree(h func(context.Context, extension.SessionTreeEvent) error) func() {
	return observe(r, "session_tree", h)
}

func (r *eventReference) OnAfterProviderResponse(h func(context.Context, extension.AfterProviderResponseEvent) error) func() {
	return observe(r, "after_provider_response", h)
}

func (r *eventReference) OnBeforeProviderHeaders(h func(context.Context, extension.BeforeProviderHeadersEvent) error) func() {
	return observe(r, "before_provider_headers", h)
}

func (r *eventReference) OnProviderStreamEvent(h func(context.Context, extension.ProviderStreamEvent) error) func() {
	return observe(r, "provider_stream_event", h)
}

func (r *eventReference) OnAgentStart(h func(context.Context, extension.AgentStartEvent) error) func() {
	return observe(r, "agent_start", h)
}

func (r *eventReference) OnAgentEnd(h func(context.Context, extension.AgentEndEvent) error) func() {
	return observe(r, "agent_end", h)
}

func (r *eventReference) OnAgentSettled(h func(context.Context, extension.AgentSettledEvent) error) func() {
	return observe(r, "agent_settled", h)
}

func (r *eventReference) OnUiPromptStart(h func(context.Context, extension.UIPromptStartEvent) error) func() {
	return observe(r, "ui_prompt_start", h)
}

func (r *eventReference) OnUiPromptEnd(h func(context.Context, extension.UIPromptEndEvent) error) func() {
	return observe(r, "ui_prompt_end", h)
}

func (r *eventReference) OnTurnStart(h func(context.Context, extension.TurnStartEvent) error) func() {
	return observe(r, "turn_start", h)
}

func (r *eventReference) OnMessageStart(h func(context.Context, extension.MessageStartEvent) error) func() {
	return observe(r, "message_start", h)
}

func (r *eventReference) OnMessageUpdate(h func(context.Context, extension.MessageUpdateEvent) error) func() {
	return observe(r, "message_update", h)
}

func (r *eventReference) OnToolExecutionStart(h func(context.Context, extension.ToolExecutionStartEvent) error) func() {
	return observe(r, "tool_execution_start", h)
}

func (r *eventReference) OnToolExecutionUpdate(h func(context.Context, extension.ToolExecutionUpdateEvent) error) func() {
	return observe(r, "tool_execution_update", h)
}

func (r *eventReference) OnToolExecutionEnd(h func(context.Context, extension.ToolExecutionEndEvent) error) func() {
	return observe(r, "tool_execution_end", h)
}

func (r *eventReference) OnModelSelect(h func(context.Context, extension.ModelSelectEvent) error) func() {
	return observe(r, "model_select", h)
}

func (r *eventReference) OnThinkingLevelSelect(h func(context.Context, extension.ThinkingLevelSelectEvent) error) func() {
	return observe(r, "thinking_level_select", h)
}

// jsonField reads the member at a dotted path of a JSON document as its compact JSON.
func jsonField(document []byte, path string) string {
	var value any
	if json.Unmarshal(document, &value) != nil {
		return "<invalid json>"
	}
	for part := range strings.SplitSeq(path, ".") {
		switch node := value.(type) {
		case map[string]any:
			value = node[part]
		case []any:
			index := 0
			for _, digit := range part {
				index = index*10 + int(digit-'0')
			}
			if index >= len(node) {
				return "<missing>"
			}
			value = node[index]
		default:
			return "<missing>"
		}
	}
	out, _ := json.Marshal(value)
	return string(out)
}

// TestConformance_ObservedEventPayloads pins the payload of the pi.on events a handler only observes (extensions/types.ts:1558-1642): the host's
// Runner delivers each event to the SDK's handler with the field Pi's type names, and the Go reference of extension.API.OnX, a handler on the same
// Runner, receives the same field. The value is chosen by the test, so it can only reach the SDK through the host's encoding of the event.
func TestConformance_ObservedEventPayloads(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	leaf, probeHeader := "leaf-probe", "probe-header"
	stamp := float64(4242)
	message := wireAgentMessage(map[string]any{"role": "user", "content": "probe", "timestamp": stamp})
	type row struct {
		event string
		value any
		// field is the dotted path of the member that carries the value, and want its compact JSON.
		field, want string
		// observe subscribes through the extension.API member for the event and returns the JSON of the event the handler saw.
		observe func(api extension.API, saw func(any)) func()
	}
	rows := []row{
		{"session_start", extension.SessionStartEvent{Type: "session_start", Reason: "resume", PreviousSessionFile: "/prev/probe.jsonl"}, "previousSessionFile", `"/prev/probe.jsonl"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnSessionStart(func(_ context.Context, e extension.SessionStartEvent) error { saw(e); return nil })
			}},
		{"session_info_changed", extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: "probe-name"}, "name", `"probe-name"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnSessionInfoChanged(func(_ context.Context, e extension.SessionInfoChangedEvent) error { saw(e); return nil })
			}},
		{"session_compact", extension.SessionCompactEvent{Type: "session_compact", FromExtension: true}, "fromExtension", `true`,
			func(api extension.API, saw func(any)) func() {
				return api.OnSessionCompact(func(_ context.Context, e extension.SessionCompactEvent) error { saw(e); return nil })
			}},
		{"session_compact_failed", extension.SessionCompactFailedEvent{Type: "session_compact_failed", Reason: "overflow", ErrorMessage: "probe failure"}, "errorMessage", `"probe failure"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnSessionCompactFailed(func(_ context.Context, e extension.SessionCompactFailedEvent) error { saw(e); return nil })
			}},
		{"session_shutdown", extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "reload", TargetSessionFile: "/target/probe.jsonl"}, "targetSessionFile", `"/target/probe.jsonl"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnSessionShutdown(func(_ context.Context, e extension.SessionShutdownEvent) error { saw(e); return nil })
			}},
		{"session_tree", extension.SessionTreeEvent{Type: "session_tree", NewLeafID: &leaf}, "newLeafId", `"leaf-probe"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnSessionTree(func(_ context.Context, e extension.SessionTreeEvent) error { saw(e); return nil })
			}},
		{"after_provider_response", extension.AfterProviderResponseEvent{Type: "after_provider_response", Status: 418, Headers: map[string]string{}}, "status", `418`,
			func(api extension.API, saw func(any)) func() {
				return api.OnAfterProviderResponse(func(_ context.Context, e extension.AfterProviderResponseEvent) error { saw(e); return nil })
			}},
		{"before_provider_headers", extension.ProviderHeaders{"x-probe": &probeHeader}, "headers.x-probe", `"probe-header"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnBeforeProviderHeaders(func(_ context.Context, e extension.BeforeProviderHeadersEvent) error { saw(e); return nil })
			}},
		{"provider_stream_event", extension.ProviderStreamEvent{Type: "provider_stream_event", Provider: "prov-probe", API: "api-probe", Model: "model-probe", Data: map[string]any{}}, "provider", `"prov-probe"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnProviderStreamEvent(func(_ context.Context, e extension.ProviderStreamEvent) error { saw(e); return nil })
			}},
		{"agent_start", extension.AgentStartEvent{Type: "agent_start"}, "type", `"agent_start"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnAgentStart(func(_ context.Context, e extension.AgentStartEvent) error { saw(e); return nil })
			}},
		{"agent_end", extension.AgentEndEvent{Type: "agent_end", Messages: []extension.AgentMessage{message}}, "messages.0.timestamp", `4242`,
			func(api extension.API, saw func(any)) func() {
				return api.OnAgentEnd(func(_ context.Context, e extension.AgentEndEvent) error { saw(e); return nil })
			}},
		{"agent_settled", extension.AgentSettledEvent{Type: "agent_settled", Aborted: true}, "aborted", `true`,
			func(api extension.API, saw func(any)) func() {
				return api.OnAgentSettled(func(_ context.Context, e extension.AgentSettledEvent) error { saw(e); return nil })
			}},
		{"ui_prompt_start", extension.UIPromptStartEvent{Type: "ui_prompt_start", Reason: "ui_prompt", Kind: extension.UIPromptKindSelect, Title: "probe title"}, "title", `"probe title"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnUiPromptStart(func(_ context.Context, e extension.UIPromptStartEvent) error { saw(e); return nil })
			}},
		{"ui_prompt_end", extension.UIPromptEndEvent{Type: "ui_prompt_end", Reason: "ui_prompt", Kind: extension.UIPromptKindInput, Title: "probe end title"}, "title", `"probe end title"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnUiPromptEnd(func(_ context.Context, e extension.UIPromptEndEvent) error { saw(e); return nil })
			}},
		{"turn_start", extension.TurnStartEvent{Type: "turn_start", TurnIndex: 7, Timestamp: 1}, "turnIndex", `7`,
			func(api extension.API, saw func(any)) func() {
				return api.OnTurnStart(func(_ context.Context, e extension.TurnStartEvent) error { saw(e); return nil })
			}},
		{"message_start", extension.MessageStartEvent{Type: "message_start", Message: message}, "message.timestamp", `4242`,
			func(api extension.API, saw func(any)) func() {
				return api.OnMessageStart(func(_ context.Context, e extension.MessageStartEvent) error { saw(e); return nil })
			}},
		{"message_update", extension.MessageUpdateEvent{Type: "message_update", Message: message, AssistantMessageEvent: map[string]any{"type": "text_delta", "delta": "probe-delta"}}, "assistantMessageEvent.delta", `"probe-delta"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnMessageUpdate(func(_ context.Context, e extension.MessageUpdateEvent) error { saw(e); return nil })
			}},
		{"tool_execution_start", extension.ToolExecutionStartEvent{Type: "tool_execution_start", ToolCallID: "call-probe", ToolName: "read", Args: map[string]any{"path": "a"}}, "toolCallId", `"call-probe"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnToolExecutionStart(func(_ context.Context, e extension.ToolExecutionStartEvent) error { saw(e); return nil })
			}},
		{"tool_execution_update", extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: "call-update", ToolName: "read", Args: map[string]any{"path": "a"}, PartialResult: map[string]any{"n": 1}}, "toolCallId", `"call-update"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnToolExecutionUpdate(func(_ context.Context, e extension.ToolExecutionUpdateEvent) error { saw(e); return nil })
			}},
		{"tool_execution_end", extension.ToolExecutionEndEvent{Type: "tool_execution_end", ToolCallID: "call-end", ToolName: "read", Result: map[string]any{"content": []any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "image", "data": "d", "mimeType": "image/png"}}, "details": map[string]any{}}, IsError: true}, "isError", `true`,
			func(api extension.API, saw func(any)) func() {
				return api.OnToolExecutionEnd(func(_ context.Context, e extension.ToolExecutionEndEvent) error { saw(e); return nil })
			}},
		{"model_select", extension.ModelSelectEvent{Type: "model_select", Model: &ai.Model{ID: "model-probe"}, Source: "set"}, "model.id", `"model-probe"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnModelSelect(func(_ context.Context, e extension.ModelSelectEvent) error { saw(e); return nil })
			}},
		{"thinking_level_select", extension.ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: "high", PreviousLevel: "off"}, "level", `"high"`,
			func(api extension.API, saw func(any)) func() {
				return api.OnThinkingLevelSelect(func(_ context.Context, e extension.ThinkingLevelSelectEvent) error { saw(e); return nil })
			}},
	}

	// referenceFields are the paths of members in the Go struct's own encoding where it differs from the wire name (ai.Model has no JSON tags).
	referenceFields := map[string]string{"model_select": "model.ID"}

	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.host == nil {
				t.Skip("no subprocess host for " + tc.name)
			}
			reference := extension.Extension{Path: "go-reference", ResolvedPath: "go-reference"}
			reference.InitializeEventHandlers()
			var api extension.API = &eventReference{ext: &reference}
			var mu sync.Mutex
			saw := map[string]any{}
			for _, r := range rows {
				event := r.event
				r.observe(api, func(e any) {
					mu.Lock()
					saw[event] = e
					mu.Unlock()
				})
			}
			runner := inproc.NewRunner(append(h.host.Extensions(), reference), t.TempDir(), h.host.Runtime())
			t.Cleanup(runner.Shutdown)
			runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)

			sdkData := func(event string) []byte {
				prefix := "event_probe_data:" + event + ":"
				for _, n := range *h.notify {
					if rest, ok := strings.CutPrefix(n, prefix); ok {
						return []byte(strings.TrimSuffix(rest, ":info"))
					}
				}
				return nil
			}
			for _, r := range rows {
				runConformanceCommandArgs(t, h, "event_probe_on", r.event)
				pollUntilConformance(t, 5*time.Second, r.event+": the host never received the SDK's subscription", func() bool { return runner.HasHandlers(r.event) })
				if headers, ok := r.value.(extension.ProviderHeaders); ok {
					// The headers event is built by the Runner from the provider's headers (runner.ts emitBeforeProviderHeaders).
					if _, err := runner.EmitBeforeProviderHeaders(context.Background(), headers); err != nil {
						t.Fatalf("emit %s: %v", r.event, err)
					}
				} else if _, err := runner.Emit(context.Background(), r.value.(extension.ExtensionEvent)); err != nil {
					t.Fatalf("emit %s: %v", r.event, err)
				}
				pollUntilConformance(t, 5*time.Second, r.event+": the event never reached the SDK's handler", func() bool { return sdkData(r.event) != nil })
				if got := jsonField(sdkData(r.event), r.field); got != r.want {
					t.Errorf("%s: the SDK saw %s = %s, want %s", r.event, r.field, got, r.want)
				}
				pollUntilConformance(t, 5*time.Second, r.event+": the event never reached the Go reference handler", func() bool {
					mu.Lock()
					defer mu.Unlock()
					return saw[r.event] != nil
				})
				mu.Lock()
				encoded, err := json.Marshal(saw[r.event])
				mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				field := r.field
				if renamed, ok := referenceFields[r.event]; ok {
					field = renamed
				}
				if got := jsonField(encoded, field); got != r.want {
					t.Errorf("%s: the Go reference saw %s = %s, want %s", r.event, r.field, got, r.want)
				}
			}
		})
	}
}
