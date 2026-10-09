package extensionconformance

import (
	"context"
	"slices"
	"testing"
	"time"
)

// TestConformance_ExtensionAPIEventSubscriptions pins `pi.on(event, handler)` (Pi 1.1.0 packages/coding-agent/src/core/extensions/types.ts:1569-1650,
// one overload per event name): every SDK subscribes to each event name the extension API declares, after connecting, and the production host
// (the Runner) then holds a handler for it. The fixtures already subscribe to some of these events at load (tool_call, session_start, ...),
// so for those HasHandlers cannot tell the probe's subscription apart from the load-time one: the host delivers each such event, and the
// probe handler registered after connecting must report `event_probe_on:<event>` through ctx.ui.notify, which only it produces.
//
// mutation-checked: the event_probe_on command never sent, so no event is held by the host; the host's event.subscribe handler dropping
// the load-subscribed tool_call, so only the probe's delivery fails.
func TestConformance_ExtensionAPIEventSubscriptions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	// Every pi.on event name of the Pi 1.1.0 extension API (types.ts:1569-1650), the SDK fixtures subscribe to through the event_probe_on command.
	piOnEvents := []string{
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
	if !slices.Equal(slices.Sorted(slices.Values(piOnEvents)), slices.Sorted(slices.Values(subscribedEventNames))) {
		t.Fatalf("subscribedEventNames drifted from the Pi 1.1.0 pi.on list: %v", subscribedEventNames)
	}
	if len(piOnEvents) != len(slices.Compact(slices.Sorted(slices.Values(piOnEvents)))) {
		t.Fatalf("the list names an event twice: %v", piOnEvents)
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
			var loadSubscribed []string
			for _, event := range piOnEvents {
				if h.runner.HasHandlers(event) {
					loadSubscribed = append(loadSubscribed, event)
				}
			}
			for _, event := range piOnEvents {
				runConformanceCommandArgs(t, h, "event_probe_on", event)
				pollUntilConformance(t, 5*time.Second, "pi.on("+event+"): the host never received the SDK's subscription", func() bool {
					return h.runner.HasHandlers(event)
				})
			}
			emitters := everyEventEmit()
			for _, event := range loadSubscribed {
				emit, ok := emitters[event]
				if !ok {
					t.Fatalf("everyEventEmit has no emitter for %s", event)
				}
				// Each event goes through the Runner path that emits it, with the payload the host builds for it; a load-time fixture
				// handler that needs fields the probe lacks fails, the Runner records the failure and runs the next handler, as Pi does
				// (Pi 1.1.0 packages/coding-agent/src/core/extensions/runner.ts:1104-1107).
				if err := emit(context.Background(), h); err != nil {
					t.Fatalf("emit %s: %v", event, err)
				}
				pollUntilConformance(t, 5*time.Second, "pi.on("+event+"): the handler registered after connecting never received the event", func() bool {
					return slices.Contains(h.ui.Recorded(), "event_probe_on:"+event+":info")
				})
			}
		})
	}
}
