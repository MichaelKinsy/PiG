package extensionconformance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// subscribedEventNames lists every pi.on event name of the Pi 1.1.0 extension API (types.ts:1569-1650); everyEventEmit must dispatch each one.
var subscribedEventNames = []string{
	"resources_discover",
	"session_start",
	"session_info_changed",
	"session_before_switch",
	"session_before_fork",
	"session_before_compact",
	"project_trust",
	"session_compact",
	"session_compact_failed",
	"session_shutdown",
	"session_before_tree",
	"session_tree",
	"context",
	"context_with_system",
	"before_provider_request",
	"mcp_servers_change",
	"provider_stream_event",
	"after_provider_response",
	"before_provider_headers",
	"before_agent_start",
	"agent_start",
	"agent_end",
	"agent_before_settle",
	"agent_settled",
	"ui_prompt_start",
	"ui_prompt_end",
	"cache_warming_decision",
	"turn_start",
	"turn_end",
	"message_start",
	"message_update",
	"message_end",
	"tool_execution_start",
	"tool_execution_update",
	"tool_execution_end",
	"model_select",
	"thinking_level_select",
	"tool_call",
	"tool_result",
	"user_bash",
	"input",
}

// everyEventEmit returns, for each pi.on event name, a production Runner dispatch that delivers it.
func everyEventEmit() map[string]func(context.Context, *harness) error {
	message := wireAgentMessage(map[string]any{"role": "user", "content": "hello", "timestamp": float64(1)})
	ignore := func(_ any, err error) error { return err }
	return map[string]func(context.Context, *harness) error{
		"resources_discover": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitResourcesDiscover(ctx, ".", "startup"))
		},
		"session_start":          genericEmit(extension.SessionStartEvent{Type: "session_start", Reason: "startup"}),
		"session_info_changed":   genericEmit(extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: "probe"}),
		"session_before_switch":  genericEmit(extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "new"}),
		"session_before_fork":    genericEmit(extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: "e1", Position: "at"}),
		"session_before_compact": genericEmit(extension.SessionBeforeCompactEvent{Type: "session_before_compact", Reason: "manual"}),
		"project_trust": func(ctx context.Context, h *harness) error {
			// The fixtures leave the "/probe" cwd undecided, so the dispatch reaches the handlers registered after connecting.
			_, _, err := inproc.EmitProjectTrust(h.runner, ctx, extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/probe"}, extension.ProjectTrustContext{})
			return err
		},
		"session_compact":        genericEmit(extension.SessionCompactEvent{Type: "session_compact", Reason: "manual"}),
		"session_compact_failed": genericEmit(extension.SessionCompactFailedEvent{Type: "session_compact_failed", Reason: "manual", ErrorMessage: "probe"}),
		"session_shutdown":       genericEmit(extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "reload"}),
		"session_before_tree":    genericEmit(extension.SessionBeforeTreeEvent{Type: "session_before_tree"}),
		"session_tree":           genericEmit(extension.SessionTreeEvent{Type: "session_tree"}),
		"context": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitContext(ctx, []extension.AgentMessage{message}))
		},
		"context_with_system": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitContextWithSystem(ctx, []extension.AgentMessage{message}))
		},
		"before_provider_request": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitBeforeProviderRequest(ctx, map[string]any{"model": "m"}))
		},
		"mcp_servers_change":      genericEmit(extension.McpServersChangeEvent{Type: "mcp_servers_change", Servers: []extension.RegisteredMcpServer{}}),
		"provider_stream_event":   genericEmit(extension.ProviderStreamEvent{Type: "provider_stream_event", Provider: "p", API: "a", Model: "m", Data: map[string]any{"type": "probe"}}),
		"after_provider_response": genericEmit(extension.AfterProviderResponseEvent{Type: "after_provider_response", Status: 200, Headers: map[string]string{}}),
		"before_provider_headers": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitBeforeProviderHeaders(ctx, extension.ProviderHeaders{}))
		},
		"before_agent_start": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitBeforeAgentStart(ctx, "probe", nil, extension.BuildSystemPromptOptions{}))
		},
		"agent_start": genericEmit(extension.AgentStartEvent{Type: "agent_start"}),
		"agent_end":   genericEmit(extension.AgentEndEvent{Type: "agent_end", Messages: []extension.AgentMessage{message}}),
		"agent_before_settle": func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitBoundary(ctx, &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Outcome: extension.AgentActivityCompleted}},
				func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
					return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, len(entries))}, nil
				})
			return err
		},
		"agent_settled":   genericEmit(extension.AgentSettledEvent{Type: "agent_settled"}),
		"ui_prompt_start": genericEmit(extension.UIPromptStartEvent{Type: "ui_prompt_start", Reason: "ui_prompt", Kind: "input", Title: "probe"}),
		"ui_prompt_end":   genericEmit(extension.UIPromptEndEvent{Type: "ui_prompt_end", Reason: "ui_prompt", Kind: "input", Title: "probe"}),
		"cache_warming_decision": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitCacheWarmingDecision(ctx, extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", Action: "skip"}))
		},
		"turn_start":     genericEmit(extension.TurnStartEvent{Type: "turn_start", TurnIndex: 1, Timestamp: 1}),
		"turn_end":       genericEmit(extension.TurnEndEvent{Type: "turn_end", Message: wireAgentMessage(map[string]any{"role": "assistant", "content": []any{}}), MessageEntryID: "probe", ToolResultEntryIds: []string{}}),
		"message_start":  genericEmit(extension.MessageStartEvent{Type: "message_start", Message: message}),
		"message_update": genericEmit(extension.MessageUpdateEvent{Type: "message_update", Message: message}),
		"message_end": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitMessageEnd(ctx, extension.MessageEndEvent{Type: "message_end", Message: message}))
		},
		"tool_execution_start":  genericEmit(extension.ToolExecutionStartEvent{Type: "tool_execution_start", ToolCallID: "call-1", ToolName: "probe", Args: map[string]any{}}),
		"tool_execution_update": genericEmit(extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: "call-1", ToolName: "probe", Args: map[string]any{}, PartialResult: map[string]any{}}),
		"tool_execution_end":    genericEmit(extension.ToolExecutionEndEvent{Type: "tool_execution_end", ToolCallID: "call-1", ToolName: "probe", Result: map[string]any{}}),
		"model_select":          genericEmit(extension.ModelSelectEvent{Type: "model_select", Model: &ai.Model{ID: "m"}, Source: "set"}),
		"thinking_level_select": genericEmit(extension.ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: "high", PreviousLevel: "off"}),
		"tool_call": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitToolCall(ctx, extension.CustomToolCallEvent{
				ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "probe-call"}, ToolName: "probe", Input: map[string]any{},
			}))
		},
		"tool_result": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitToolResult(ctx, extension.CustomToolResultEvent{
				ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "probe-call", Input: map[string]any{}, Content: []any{}},
				ToolName:            "probe",
			}))
		},
		"user_bash": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitUserBash(ctx, extension.UserBashEvent{Type: "user_bash", Command: "probe", Cwd: "/probe"}))
		},
		"input": func(ctx context.Context, h *harness) error {
			return ignore(h.runner.EmitInput(ctx, "probe", nil, "interactive", ""))
		},
	}
}

// TestConformance_EventUnsubscribeEveryEvent pins Pi's `pi.on(event, handler): () => void` (extensions/types.ts:1569-1636, one
// overload per event) for every overload in every SDK, isolated and packed: a handler registered after connecting receives the
// event from the production Runner dispatch, the unsubscribe it returned stops the next dispatch, and a fresh subscription receives
// the one after. The handler reports `event_probe_on:<event>`, which only a delivered event produces, so an SDK that ignores the
// unsubscribe reports the event three times instead of twice.
func TestConformance_EventUnsubscribeEveryEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	emits := everyEventEmit()
	for _, event := range subscribedEventNames {
		if emits[event] == nil {
			t.Fatalf("%s: no Runner dispatch for this pi.on event", event)
		}
	}
	if len(emits) != len(subscribedEventNames) {
		t.Fatalf("%d dispatches for %d pi.on events", len(emits), len(subscribedEventNames))
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
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
			ctx := context.Background()
			for _, event := range subscribedEventNames {
				delivered := func() int {
					count := 0
					for _, n := range *h.notify {
						if strings.TrimSuffix(n, ":info") == "event_probe_on:"+event {
							count++
						}
					}
					return count
				}
				emit := func() {
					t.Helper()
					if err := emits[event](ctx, h); err != nil {
						t.Fatalf("emit %s: %v", event, err)
					}
				}
				*h.notify = (*h.notify)[:0]
				runConformanceCommandArgs(t, h, "event_probe_on", event)
				emit()
				pollUntilConformance(t, 5*time.Second, event+": the subscribed handler never ran", func() bool { return delivered() == 1 })
				runConformanceCommandArgs(t, h, "event_probe_off", event)
				emit()
				runConformanceCommandArgs(t, h, "event_probe_on", event)
				emit()
				pollUntilConformance(t, 5*time.Second, event+": the second subscription never ran", func() bool { return delivered() >= 2 })
				if got := delivered(); got != 2 {
					t.Fatalf("%s: delivered %d times, want 2 (the unsubscribed handler ran)", event, got)
				}
				runConformanceCommandArgs(t, h, "event_probe_off", event)
			}
		})
	}
}
