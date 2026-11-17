package extensionconformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// effectKind is one kind of host effect a handler produces in its synchronous part. seen reports whether the host applied it (the recording UI holds it).
type effectKind struct {
	name string
	seen func(h *harness) bool
	// rpcOnly kinds reach the host in RPC mode alone: Pi's other modes draw a string-array widget themselves, as a factory.
	rpcOnly bool
}

// nodeEffectKinds are the effects the Node fixture's effect_probe commands produce, one per fire-and-forget host call that Pi writes at the call. append and sendUserMessage follow a slow notify, which is
// how the recording UI makes the host action behind them late; failedAppend is a call the host refuses, ahead of a notify that must still apply and release the caller.
var nodeEffectKinds = []effectKind{
	{name: "notify", seen: func(h *harness) bool { return slices.Contains(h.ui.Recorded(), "effect_probe:notify:info") }},
	{name: "status", seen: func(h *harness) bool { return slices.Contains(h.ui.Statuses(), "effect_probe:status") }},
	{name: "title", seen: func(h *harness) bool { return slices.Contains(h.ui.Surfaces(), "title:effect_probe:title") }},
	{name: "widget", rpcOnly: true, seen: func(h *harness) bool {
		return slices.Contains(h.ui.Surfaces(), "widget:effect_probe:effect_probe:widget")
	}},
	{name: "editor", seen: func(h *harness) bool { return slices.Contains(h.ui.RecordedCalls(), "editorText:effect_probe:editor") }},
	{name: "append", seen: func(h *harness) bool { return slices.Contains(*h.actions, "appendEntry:effect_probe:entry") }},
	{name: "sendUserMessage", seen: func(h *harness) bool { return slices.Contains(*h.actions, "sendUserMessage:effect_probe:user:") }},
	{name: "failedAppend", seen: func(h *harness) bool { return slices.Contains(h.ui.Recorded(), "effect_probe:notify:info") }},
}

// effectOrderRow is one kind of waiter: the caller that waits for a handler of an extension, and what it learns when it is released. arm subscribes the handler; release runs it and
// reports whether applied was true at the instant the waiter was released.
type effectOrderRow struct {
	name string
	// nodeOnly rows use the Node fixture's effect_probe commands; the others use a probe every SDK's fixture has.
	nodeOnly bool
	rpcOnly  bool
	arm      func(t *testing.T, h *harness)
	applied  func(h *harness) bool
	release  func(t *testing.T, h *harness, applied func() bool) bool
}

func effectOrderRows() []effectOrderRow {
	emitAwaited := func(event extension.ExtensionEvent) func(*testing.T, *harness, func() bool) bool {
		return func(t *testing.T, h *harness, applied func() bool) bool {
			t.Helper()
			if _, err := h.runner.Emit(context.Background(), event); err != nil {
				t.Fatalf("emit: %v", err)
			}
			return applied()
		}
	}
	// emitAcknowledged is Session.dispatchUnawaitedEvent: the caller goes on at the handler's acknowledgment.
	emitAcknowledged := func(event extension.ExtensionEvent) func(*testing.T, *harness, func() bool) bool {
		return func(t *testing.T, h *harness, applied func() bool) bool {
			t.Helper()
			released := make(chan bool, 1)
			ctx := invocation.WithAcknowledgment(context.Background(), func() { released <- applied() })
			done := make(chan error, 1)
			go func() {
				_, err := h.runner.Emit(ctx, event)
				done <- err
			}()
			var seen bool
			select {
			case seen = <-released:
			case err := <-done:
				t.Fatalf("the emission ended without acknowledging: %v", err)
			}
			if err := <-done; err != nil {
				t.Fatalf("emit: %v", err)
			}
			return seen
		}
	}
	// emitAcknowledgedUnfinished is emitAcknowledged for a handler that goes on awaiting after its calls: the caller goes on at the acknowledgment, which comes at the handler's first await, not at its end.
	emitAcknowledgedUnfinished := func(event extension.ExtensionEvent) func(*testing.T, *harness, func() bool) bool {
		return func(t *testing.T, h *harness, applied func() bool) bool {
			t.Helper()
			released := make(chan bool, 1)
			ctx := invocation.WithAcknowledgment(context.Background(), func() { released <- applied() })
			done := make(chan error, 1)
			go func() {
				_, err := h.runner.Emit(ctx, event)
				done <- err
			}()
			select {
			case seen := <-released:
				return seen
			case err := <-done:
				t.Fatalf("the emission ended without acknowledging: %v", err)
			}
			return false
		}
	}
	runCommand := func(name, args string) func(*testing.T, *harness, func() bool) bool {
		return func(t *testing.T, h *harness, applied func() bool) bool {
			t.Helper()
			cmd, ok := findCommand(h.runner, name)
			if !ok {
				t.Fatalf("%s command not registered", name)
			}
			if err := cmd.Handler(context.Background(), args); err != nil {
				t.Fatalf("%s command: %v", name, err)
			}
			return applied()
		}
	}
	probeOn := func(event string) func(*testing.T, *harness) {
		return func(t *testing.T, h *harness) { runConformanceCommandArgs(t, h, "event_probe_on", event) }
	}
	rows := []effectOrderRow{
		{
			name:    "awaited event notify",
			arm:     probeOn("turn_start"),
			applied: func(h *harness) bool { return slices.Contains(h.ui.Recorded(), "event_probe_on:turn_start:info") },
			release: emitAwaited(extension.TurnStartEvent{Type: "turn_start", TurnIndex: 1, Timestamp: 1}),
		},
		{
			name:    "unawaited session_info_changed notify",
			applied: func(h *harness) bool { return slices.Contains(h.ui.Recorded(), "session_info_changed:named:info") },
			release: emitAcknowledged(extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: "named"}),
		},
		{
			name: "unawaited thinking_level_select notify",
			arm:  probeOn("thinking_level_select"),
			applied: func(h *harness) bool {
				return slices.Contains(h.ui.Recorded(), "event_probe_on:thinking_level_select:info")
			},
			release: emitAcknowledged(extension.ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: "high", PreviousLevel: "off"}),
		},
		{
			name:    "command status",
			applied: func(h *harness) bool { return slices.Contains(h.ui.Statuses(), "conformance:ok") },
			release: runCommand("status", ""),
		},
		{
			name:    "command title",
			applied: func(h *harness) bool { return slices.Contains(h.ui.Surfaces(), "title:Conformance title") },
			release: runCommand("liveness_fire_call", ""),
		},
	}
	for _, kind := range nodeEffectKinds {
		probeKind := func(event string, style ...string) func(*testing.T, *harness) {
			return func(t *testing.T, h *harness) {
				runConformanceCommandArgs(t, h, "effect_probe_on", strings.Join(append([]string{event, kind.name}, style...), " "))
			}
		}
		node := func(row effectOrderRow) effectOrderRow {
			row.name = "node " + row.name + " " + kind.name
			row.nodeOnly, row.rpcOnly, row.applied = true, kind.rpcOnly, kind.seen
			return row
		}
		rows = append(rows,
			node(effectOrderRow{
				name: "awaited event", arm: probeKind("turn_start"),
				release: emitAwaited(extension.TurnStartEvent{Type: "turn_start", TurnIndex: 1, Timestamp: 1}),
			}),
			node(effectOrderRow{
				name: "unawaited thinking_level_select", arm: probeKind("thinking_level_select"),
				release: emitAcknowledged(extension.ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: "high", PreviousLevel: "off"}),
			}),
			node(effectOrderRow{
				name: "unawaited thinking_level_select sync handler", arm: probeKind("thinking_level_select", "sync"),
				release: emitAcknowledged(extension.ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: "high", PreviousLevel: "off"}),
			}),
			node(effectOrderRow{
				name: "unawaited thinking_level_select handler awaiting a timer", arm: probeKind("thinking_level_select", "await"),
				release: emitAcknowledgedUnfinished(extension.ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: "high", PreviousLevel: "off"}),
			}),
			node(effectOrderRow{
				name:    "command",
				release: runCommand("effect_probe_run", kind.name),
			}),
			node(effectOrderRow{
				name: "tool result",
				arm:  func(t *testing.T, h *harness) { runConformanceCommand(t, h, "effect_probe_tool") },
				release: func(t *testing.T, h *harness, applied func() bool) bool {
					t.Helper()
					tool, ok := findTool(h.runner, "effect_probe_tool")
					if !ok {
						t.Fatal("effect_probe_tool not registered")
					}
					ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
					defer cancel()
					args, _ := json.Marshal(map[string]string{"kind": kind.name})
					var onUpdate agent.ToolUpdateCallback = func(agent.AgentToolResult) {}
					if _, err := tool.Definition.Execute(ctx, "tc-effect", args, onUpdate); err != nil {
						t.Fatalf("effect_probe_tool: %v", err)
					}
					return applied()
				},
			}),
		)
	}
	return rows
}

// TestConformance_HandlerEffectsPrecedeTheReleaseOfItsCaller pins Pi's order for a handler that calls a host API in its synchronous part (runner.ts:1110: `await handler(event, ctx)` runs the handler to
// its first await inside emit; ctx.ui.notify and the like write at the call): the effect is applied before the caller that waits for the handler goes on. The recording UI is slow to apply each effect, a
// terminal or pipe that takes a moment to write, so a caller released at the handler's report instead of after its calls fails every time.
func TestConformance_HandlerEffectsPrecedeTheReleaseOfItsCaller(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Cleanup(func() { rpcRowMode = "" })
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, mode := range []string{"tui", "rpc"} {
		rpcRowMode = mode
		for _, tc := range cases {
			for _, row := range effectOrderRows() {
				if (row.nodeOnly && !strings.Contains(tc.name, "node")) || (row.rpcOnly && mode != "rpc") {
					continue
				}
				t.Run(mode+"/"+tc.name+"/"+row.name, func(t *testing.T) {
					h := tc.make(t)
					t.Cleanup(func() {
						if h.cleanup != nil {
							h.cleanup()
						}
						h.host.Shutdown("test done")
					})
					if mode == "rpc" && h.bridge != nil {
						// RPC mode writes a string-array widget as the client's request (rpc-mode.ts setWidget); the recording UI keeps it, slow like the rest.
						h.bridge.SetWidgetRequestFunc(func(_, key string, lines []string, _ *extension.ExtensionWidgetOptions) {
							h.ui.recordSurface1("widget:" + key + ":" + strings.Join(lines, "|"))
						})
					}
					if row.arm != nil {
						row.arm(t, h)
					}
					h.ui.applyDelay.Store(int64(25 * time.Millisecond))
					if !row.release(t, h, func() bool { return row.applied(h) }) {
						t.Errorf("the caller was released before the handler's effect was applied")
					}
				})
			}
		}
	}
}
