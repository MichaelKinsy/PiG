package cli

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Pi's dispose() awaits session.abort() before session_shutdown (agent-session-runtime.ts:409-417), and abort() waits for the idle
// Session (agent-session.ts:2601-2612): the aborted run's turn_end handlers, including one that awaits a timer, finish before
// session_shutdown is emitted. flushRawStdout (output-guard.ts:95-108) then finds nothing left to join.
func TestRPCInputEndJoinsDelayedTurnEndBeforeShutdown(t *testing.T) {
	var wantEvents []string
	for _, event := range rpcMidPromptInputEndExtensionEvents {
		wantEvents = append(wantEvents, event)
		if event == "turn_end" {
			wantEvents = append(wantEvents, "tail_timer")
		}
	}
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_TAIL_DELAY=1")
			afterEOF, events := endInputMidPrompt(p, report)
			if !reflect.DeepEqual(afterEOF, []rpcRecord{rpcShutdownStartedNotify}) || !slices.Equal(events, wantEvents) {
				t.Fatalf("delayed boundary: stdout=%v events=%v; want only shutdown, events=%v", afterEOF, events, wantEvents)
			}
		})
	}
}

// Pi rpc-mode.ts:751 never resumes when a shutdown handler's Promise remains pending. Node exits on event-loop drain without completing
// the handler. The tool was aborted before the handler ran: dispose() awaits session.abort() first, so the run settled.
func TestRPCInputEndMidToolDoesNotCompletePendingShutdown(t *testing.T) {
	for _, impl := range rpcShutdownImplementations {
		t.Run(impl.name, func(t *testing.T) {
			p, report := impl.start(t, "RPC_SHUTDOWN_BLOCK=1")
			afterEOF, events := endInputMidPrompt(p, report)
			if !reflect.DeepEqual(afterEOF, []rpcRecord{rpcShutdownStartedNotify}) || !slices.Equal(events, rpcMidPromptInputEndExtensionEvents) {
				t.Fatalf("pending shutdown resumed: stdout=%v events=%v; want only shutdown, events=%v", afterEOF, events, rpcMidPromptInputEndExtensionEvents)
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
