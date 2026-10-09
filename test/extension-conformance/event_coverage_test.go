package extensionconformance

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_EventDelivery pins Pi's `pi.on(event, handler)` (Pi 1.1.0 packages/coding-agent/src/core/extensions/types.ts:1569-1650) for the events no other conformance test
// emits: a handler an extension registers in every SDK after connecting receives each event the host emits, with the event's name. The
// handler reports `event_probe_on:<event>` through ctx.ui.notify, which only a delivered event produces.
func TestConformance_EventDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	message := wireAgentMessage(map[string]any{"role": "user", "content": "hello", "timestamp": float64(1)})
	emits := []struct {
		event string
		emit  func(context.Context, *harness) error
	}{
		{"agent_start", genericEmit(extension.AgentStartEvent{Type: "agent_start"})},
		{"agent_end", genericEmit(extension.AgentEndEvent{Type: "agent_end", Messages: []extension.AgentMessage{message}})},
		{"agent_settled", genericEmit(extension.AgentSettledEvent{Type: "agent_settled"})},
		{"turn_start", genericEmit(extension.TurnStartEvent{Type: "turn_start", TurnIndex: 1, Timestamp: 1})},
		{"message_start", genericEmit(extension.MessageStartEvent{Type: "message_start", Message: message})},
		{"tool_execution_start", genericEmit(extension.ToolExecutionStartEvent{Type: "tool_execution_start", ToolCallID: "call-1", ToolName: "read", Args: map[string]any{"path": "a"}})},
		{"model_select", genericEmit(extension.ModelSelectEvent{Type: "model_select", Model: &ai.Model{ID: "m"}, Source: "set"})},
		{"thinking_level_select", genericEmit(extension.ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: "high", PreviousLevel: "off"})},
		{"session_tree", genericEmit(extension.SessionTreeEvent{Type: "session_tree"})},
		{"session_before_switch", genericEmit(extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "new"})},
		{"session_before_fork", genericEmit(extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: "e1", Position: "at"})},
		{"session_before_tree", genericEmit(extension.SessionBeforeTreeEvent{Type: "session_before_tree"})},
		{"resources_discover", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitResourcesDiscover(ctx, ".", "startup")
			return err
		}},
		{"context_with_system", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitContextWithSystem(ctx, []extension.AgentMessage{message})
			return err
		}},
		{"before_provider_request", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitBeforeProviderRequest(ctx, map[string]any{"model": "m"})
			return err
		}},
		{"before_provider_headers", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitBeforeProviderHeaders(ctx, extension.ProviderHeaders{})
			return err
		}},
		{"message_end", func(ctx context.Context, h *harness) error {
			_, err := h.runner.EmitMessageEnd(ctx, extension.MessageEndEvent{Type: "message_end", Message: message})
			return err
		}},
	}

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
			ctx := context.Background()
			delivered := func() []string {
				var found []string
				for _, n := range *h.notify {
					if rest, ok := strings.CutPrefix(n, "event_probe_on:"); ok {
						found = append(found, strings.TrimSuffix(rest, ":info"))
					}
				}
				return found
			}
			for _, e := range emits {
				runConformanceCommandArgs(t, h, "event_probe_on", e.event)
				if err := e.emit(ctx, h); err != nil {
					t.Fatalf("emit %s: %v", e.event, err)
				}
				pollUntilConformance(t, 5*time.Second, e.event+" never reached the handler registered after connecting", func() bool {
					return slices.Contains(delivered(), e.event)
				})
			}
		})
	}
}

func genericEmit(event extension.ExtensionEvent) func(context.Context, *harness) error {
	return func(ctx context.Context, h *harness) error {
		_, err := h.runner.Emit(ctx, event)
		return err
	}
}

func runConformanceCommandArgs(t *testing.T, h *harness, name, args string) {
	t.Helper()
	cmd, ok := findCommand(h.runner, name)
	if !ok {
		t.Fatalf("%s command not registered", name)
	}
	if err := cmd.Handler(context.Background(), args); err != nil {
		t.Fatalf("%s command: %v", name, err)
	}
}

// TestConformance_AgentSettledAborted pins Pi 1.1.0's AgentSettledEvent.aborted (extensions/types.ts, agent-session.ts _emitAgentSettled; #10607): every SDK's
// handler reads the host's value, `true` for a run that ended because it was aborted and `false` otherwise. A handler that never receives the field reports
// `absent`, which neither value matches.
func TestConformance_AgentSettledAborted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

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
			ctx := context.Background()
			runConformanceCommandArgs(t, h, "event_field_probe", "agent_settled aborted")
			for _, aborted := range []bool{true, false} {
				want := "event_field:agent_settled.aborted=" + strconv.FormatBool(aborted)
				if _, err := h.runner.Emit(ctx, extension.AgentSettledEvent{Type: "agent_settled", Aborted: aborted}); err != nil {
					t.Fatalf("emit agent_settled: %v", err)
				}
				pollUntilConformance(t, 5*time.Second, want+" never reached the handler", func() bool {
					return slices.Contains(*h.notify, want+":info")
				})
			}
		})
	}
}

// TestConformance_ToolExecutionEndDurationMs pins Pi 1.1.0's ToolExecutionEndEvent.durationMs (types.ts; #10549): the value the host emits reaches every SDK's
// handler, and an event for a call that did not run carries none (`absent`).
func TestConformance_ToolExecutionEndDurationMs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

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
			ctx := context.Background()
			runConformanceCommandArgs(t, h, "event_field_probe", "tool_execution_end durationMs")
			duration := int64(4200)
			for _, tc := range []struct {
				durationMs *int64
				want       string
			}{{&duration, "4200"}, {nil, "absent"}} {
				want := "event_field:tool_execution_end.durationMs=" + tc.want
				event := extension.ToolExecutionEndEvent{Type: "tool_execution_end", ToolCallID: "call-1", ToolName: "bash", Result: map[string]any{"content": []any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "text", "text": "b"}}, "details": map[string]any{}}, DurationMs: tc.durationMs}
				if _, err := h.runner.Emit(ctx, event); err != nil {
					t.Fatalf("emit tool_execution_end: %v", err)
				}
				pollUntilConformance(t, 5*time.Second, want+" never reached the handler", func() bool {
					return slices.Contains(*h.notify, want+":info")
				})
			}
		})
	}
}
