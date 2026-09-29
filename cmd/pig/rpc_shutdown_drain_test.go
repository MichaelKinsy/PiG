package main

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Pi flushRawStdout (output-guard.ts:95-108) flushes writes and immediately fulfilled continuations, not future timers in post-disposal turn_end callbacks.
func TestRPCInputEndDoesNotJoinDelayedTurnEnd(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_TAIL_DELAY=1")
			afterEOF, events := endInputMidPrompt(p, report)
			wantEvents := rpcMidPromptInputEndExtensionEvents[:len(rpcMidPromptInputEndExtensionEvents)-1]
			if !reflect.DeepEqual(afterEOF, []rpcRecord{rpcShutdownStartedNotify}) || !slices.Equal(events, wantEvents) {
				t.Fatalf("shutdown joined a delayed boundary: stdout=%v events=%v; want only shutdown, events=%v", afterEOF, events, wantEvents)
			}
		})
	}
}

// Pi rpc-mode.ts:738 never resumes when a shutdown handler's Promise remains pending. Node exits on event-loop drain without aborting the tool or completing the handler.
func TestRPCInputEndMidToolDoesNotCompletePendingShutdown(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_BLOCK=1")
			afterEOF, events := endInputMidPrompt(p, report)
			wantEvents := rpcMidPromptInputEndExtensionEvents[:len(rpcMidPromptInputEndExtensionEvents)-2]
			if !reflect.DeepEqual(afterEOF, []rpcRecord{rpcShutdownStartedNotify}) || !slices.Equal(events, wantEvents) {
				t.Fatalf("pending shutdown resumed: stdout=%v events=%v; want only shutdown, events=%v", afterEOF, events, wantEvents)
			}
		})
	}
}

// Upstream runs an event handler's ctx.ui.setStatus at once, whatever another
// request of the extension awaits. A command that awaits ctx.waitForIdle()
// during a run therefore does not hold back the turn_end handler that must
// finish before the run can end, and both complete.
func TestRPCWaitForIdleDoesNotHoldBackTurnEndHostCall(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_TURN_END_STATUS=1", "RPC_SHUTDOWN_NO_HANDLER=1")
			p.sendJSON(map[string]any{"id": "stream", "type": "prompt", "message": "TUI_LIVE_STREAM_PAUSE"})
			p.await("paused stream", func(r rpcRecord) bool {
				return r["type"] == "message_update" && strings.Contains(fmt.Sprint(r), "LIVE-STREAM-08")
			})
			p.sendJSON(map[string]any{"id": "wait", "type": "prompt", "message": "/wait"})
			var status, notified, responded bool
			p.await("turn_end status, idle notification and wait response", func(r rpcRecord) bool {
				status = status || (r["type"] == "extension_ui_request" && r["method"] == "setStatus")
				notified = notified || (r["type"] == "extension_ui_request" && r["method"] == "notify" && r["message"] == "idle reached")
				responded = responded || (r["type"] == "response" && r["id"] == "wait")
				return status && notified && responded
			})
			p.closeInput()
			drainRPCOutput(p)
			p.waitForExit("after the wait command completed")
			if events := readRPCShutdownReport(t, report); !slices.Contains(events, "wait:idle") {
				t.Fatalf("extension events = %v, want wait:idle", events)
			}
		})
	}
}
