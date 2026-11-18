//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestNodeHandlerWaitingForTheRunsAbortIsNotReportedDrainedWhenTheCancelPrecedesTheRunSignal: the run's abort reaches a tool_call handler as two frames, the run_signal
// frame that wakes a handler waiting on ctx.signal and the request's signal-only cancel. Here the run_signal frame is held back until the host has queued the cancel
// or the runtime has reported its drained loop, the order the two watchers of the run's signal can produce under load. A runtime that reads the cancel first
// counted the handler as held, its loop emptied and the host's owner was handed the drained loop: it would exit 0 without the session_shutdown that Pi's abort,
// which wakes the handler in the same tick, is followed by. The host queues the run_signal frame first, so the cancel waits behind the held frame and the
// runtime reports nothing until the abort has reached it. isolated and shared-ok (packed: both extensions in one Node process) must behave alike.
func TestNodeHandlerWaitingForTheRunsAbortIsNotReportedDrainedWhenTheCancelPrecedesTheRunSignal(t *testing.T) {
	t.Parallel()
	for _, isolation := range []string{"isolated", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			t.Parallel()
			budget := testbudget.Wait(t)
			h, exts, drained, ctx := startQuitRuntimesIn(t, isolation, []string{"guard", "idle"}, map[string]string{"guard": quitToolCallSource, "idle": quitIdleSource})
			h.mu.Lock()
			guard, idle := h.exts["guard"], h.exts["idle"]
			h.mu.Unlock()
			if packed := processIdentity(guard) == processIdentity(idle); packed != (isolation == "shared-ok") {
				t.Fatalf("with isolation %s the two extensions share a process = %v", isolation, packed)
			}
			handlers := exts["guard"].EventHandlers("tool_call")
			if len(handlers) != 1 {
				t.Fatalf("tool_call handlers = %d, want 1", len(handlers))
			}
			run, abort := context.WithCancel(ctx)
			holdRunSignal(h, run)
			conn := guard.connection()
			// The cancel's watcher reaches beforeCancel first when the run_signal frame is ordered ahead of the cancel: the signal is that the host is syncing the run.
			syncing := make(chan struct{}, 8)
			beforeCancel := conn.beforeCancel
			conn.beforeCancel = func() {
				select {
				case syncing <- struct{}{}:
				default:
				}
				beforeCancel()
			}
			type answer struct {
				result any
				err    error
			}
			answered := make(chan answer, 1)
			go func() {
				result, err := handlers[0](extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "call_1", ToolName: "probe", Input: map[string]any{}}, run)
				answered <- answer{result, err}
			}()
			awaitExtensionRequest(t, h, "guard")
			h.EndInput()
			awaitLoopDrained(t, h, "guard")
			// Hold the run_signal frame back: a cancel queued now is read by the runtime with the run's signal still unaborted.
			conn.runSignal.mu.Lock()
			abort()
			select {
			case <-syncing:
			case <-drained:
				conn.runSignal.mu.Unlock()
				t.Fatalf("the cancel of the request was queued before the run's abort frame: the runtime reported the handler as held and the host was handed the drained loop, where Pi's abort wakes the handler and session_shutdown follows\n%s", quitHoldState(h))
			case <-time.After(budget):
				conn.runSignal.mu.Unlock()
				t.Fatalf("the host neither synced the run signal nor was handed a drained loop after the abort\n%s", quitHoldState(h))
			}
			conn.runSignal.mu.Unlock()
			select {
			case got := <-answered:
				if got.err != nil {
					t.Fatalf("the tool_call handler's request ended with %v after the abort, want the handler's own answer\n%s", got.err, quitHoldState(h))
				}
				if text := fmt.Sprint(got.result); !strings.Contains(text, "answered after the abort") {
					t.Fatalf("the tool_call handler's answer = %s, want the block it returned after the abort", text)
				}
			case <-time.After(budget):
				t.Fatalf("the tool_call handler did not answer after the abort\n%s", quitHoldState(h))
			}
			// The answer follows the runtime's reports on the connection, which the host acts on as it reads them: a report of the handler as held has been handed over by now.
			select {
			case <-drained:
				t.Fatalf("the host was handed a drained loop while the handler waited for the run's abort\n%s", quitHoldState(h))
			default:
			}
		})
	}
}
