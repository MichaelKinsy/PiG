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

const (
	// quitToolCallIgnoresAbortSource is a tool_call handler that holds no handle and ignores the run's signal.
	quitToolCallIgnoresAbortSource = `export default function (pi) { pi.on("tool_call", () => new Promise(() => {})); }`
	// quitMessageEndIgnoresAbortSource is a message_end handler that holds no handle and ignores the run's signal: the run's abort reaches it only as the run_signal frame, which no request cancel accompanies.
	quitMessageEndIgnoresAbortSource = `export default function (pi) { pi.on("message_end", () => new Promise(() => {})); }`
)

// awaitLoopDrained returns once the named extension's Node process has reported that its event loop drained with the requests it holds unanswered
// (Conn.coverDrainReport reads runtime_commands_drained and runtime_drained alike). A runtime reports only after runtime_input_end, so this is the proof that
// its loop emptied after input ended. It fails when the process ended instead: nothing kept it alive to read the abort that follows.
func awaitLoopDrained(t *testing.T, h *Host, name string) {
	t.Helper()
	h.mu.Lock()
	me := h.exts[name]
	h.mu.Unlock()
	conn := me.connection()
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn.pendingMu.Lock()
		drained := conn.loopDrained
		conn.pendingMu.Unlock()
		if drained {
			return
		}
		if conn.closed.Load() {
			t.Fatalf("the runtime's process ended before it reported its drained loop: nothing kept it alive to read the abort\n%s", quitHoldState(h))
		}
		if time.Now().After(deadline) {
			t.Fatalf("the runtime never reported its drained loop after input ended\n%s", quitHoldState(h))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestNodeHandlerWaitingForTheRunsAbortIsNotReportedDrainedBeforeTheAbort: Pi's dispose aborts the run in the tick stdin ends, so a tool_call handler that waits
// for nothing but ctx.signal is woken before Pi's loop turns, and session_shutdown follows (rpc-mode.ts:744-751, agent-session-runtime.ts:415-424). Here the host
// sends the abort after runtime_input_end. The order is forced: the abort is not sent until the runtime's loop has emptied after input ended and it said so. The
// runtime must keep its process alive, read the abort, and let the handler answer; a report of the handler as held would make the host exit 0 with no
// session_shutdown (the fault of RPC stdin end while a tool that awaits only its abort is in flight).
func TestNodeHandlerWaitingForTheRunsAbortIsNotReportedDrainedBeforeTheAbort(t *testing.T) {
	t.Parallel()
	budget := testbudget.Wait(t)
	h, exts, drained, ctx := startQuitRuntimes(t, []string{"guard"}, map[string]string{"guard": quitToolCallSource})
	handlers := exts["guard"].EventHandlers("tool_call")
	if len(handlers) != 1 {
		t.Fatalf("tool_call handlers = %d, want 1", len(handlers))
	}
	run, abort := context.WithCancel(ctx)
	holdRunSignal(h, run)
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
	abort()
	select {
	case got := <-answered:
		if got.err != nil {
			t.Fatalf("the tool_call handler's request ended with %v after the abort, want the handler's own answer: the runtime's process did not live to read the abort\n%s", got.err, quitHoldState(h))
		}
		if text := fmt.Sprint(got.result); !strings.Contains(text, "answered after the abort") {
			t.Fatalf("the tool_call handler's answer = %s, want the block it returned after the abort", text)
		}
	case <-time.After(budget):
		t.Fatalf("the tool_call handler did not answer after the abort\n%s", quitHoldState(h))
	}
	// The answer follows the runtime's reports on the connection, which the host acts on as it reads them: a report of the handler as held has been handed to the owner by now.
	select {
	case <-drained:
		t.Fatalf("the host was handed a drained loop while the run's abort had not reached the runtime and the handler waited for exactly that abort: Pi aborts in the tick stdin ends, so its handler is woken and session_shutdown follows; the owner would exit 0 without it\n%s", quitHoldState(h))
	default:
	}
}

// TestNodeHandlerIgnoringTheRunsAbortIsReportedDrainedAfterTheAbort: a tool_call handler that ignores the abort and holds no handle leaves Pi's loop empty after
// the abort, and Pi's process exits 0 (Pi 1.5.2: 15 ms over RPC): the host is handed the drained loop, after the abort and not before.
func TestNodeHandlerIgnoringTheRunsAbortIsReportedDrainedAfterTheAbort(t *testing.T) {
	t.Parallel()
	budget := testbudget.Wait(t)
	h, exts, drained, ctx := startQuitRuntimes(t, []string{"deaf"}, map[string]string{"deaf": quitToolCallIgnoresAbortSource})
	handlers := exts["deaf"].EventHandlers("tool_call")
	if len(handlers) != 1 {
		t.Fatalf("tool_call handlers = %d, want 1", len(handlers))
	}
	run, abort := context.WithCancel(ctx)
	holdRunSignal(h, run)
	go func() {
		_, _ = handlers[0](extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "call_1", ToolName: "probe", Input: map[string]any{}}, run)
	}()
	awaitExtensionRequest(t, h, "deaf")
	h.EndInput()
	awaitLoopDrained(t, h, "deaf")
	abort()
	select {
	case <-drained:
	case <-time.After(budget):
		t.Fatalf("the host was never handed the drained loop after the abort: a handler that ignores the abort and holds no handle must not keep the quit waiting\n%s", quitHoldState(h))
	}
}

// TestNodeRunHandlerIgnoringTheRunsAbortIsReportedDrainedAfterTheRunSignal: the abort of the run reaches a message_end handler only as the run_signal frame (the
// request is not cancelled): the frame must make the runtime report again, as its transports were referenced by the report that preceded the abort.
func TestNodeRunHandlerIgnoringTheRunsAbortIsReportedDrainedAfterTheRunSignal(t *testing.T) {
	t.Parallel()
	budget := testbudget.Wait(t)
	h, exts, drained, ctx := startQuitRuntimes(t, []string{"deaf"}, map[string]string{"deaf": quitMessageEndIgnoresAbortSource})
	handlers := exts["deaf"].EventHandlers("message_end")
	if len(handlers) != 1 {
		t.Fatalf("message_end handlers = %d, want 1", len(handlers))
	}
	run, abort := context.WithCancel(ctx)
	holdRunSignal(h, run)
	go func() { _, _ = handlers[0](map[string]any{"type": "message_end"}) }()
	awaitExtensionRequest(t, h, "deaf")
	h.EndInput()
	awaitLoopDrained(t, h, "deaf")
	abort()
	select {
	case <-drained:
	case <-time.After(budget):
		t.Fatalf("the host was never handed the drained loop after the run's abort: a message_end handler that ignores the abort and holds no handle must not keep the quit waiting\n%s", quitHoldState(h))
	}
}
