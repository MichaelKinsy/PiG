//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const (
	// quitToolCallSource is a tool_call handler that waits for the run's abort and answers after it: the way an extension author stays responsive.
	quitToolCallSource = `export default function (pi) {
  pi.on("tool_call", async (_event, ctx) => {
    if (!ctx.signal.aborted) await new Promise((resolve) => ctx.signal.addEventListener("abort", resolve, { once: true }));
    return { block: true, reason: "answered after the abort" };
  });
}`
	// quitNeverTurnEndSource is a turn_end handler that awaits a promise that never settles: it holds no timer and no I/O.
	quitNeverTurnEndSource = `export default function (pi) { pi.on("turn_end", () => new Promise(() => {})); }`
)

// TestToolCallEventHandlerIsAwaitedThroughTheAbort: Pi awaits every tool_call handler, with no race against the abort: the run goes on with the handler's result
// (agent-loop.ts:738 awaits config.beforeToolCall = agent-session.ts:719-729, which awaits runner.ts:1256-1274 emitToolCall). The extension sees the abort as its signal,
// ctx.signal, and answers. Pi 1.5.2 under tmux and over RPC, a tool_call handler on a 3 s timer when the quit's abort arrives: the exit follows the handler, 2.41 s and
// 3.02 s; pig abandoned the handler at the abort and exited after 95 ms and 20 ms.
func TestToolCallEventHandlerIsAwaitedThroughTheAbort(t *testing.T) {
	t.Parallel()
	budget := testbudget.Wait(t)
	h, exts, _, ctx := startQuitRuntimes(t, []string{"guard"}, map[string]string{"guard": quitToolCallSource})
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
	abort()
	select {
	case got := <-answered:
		if got.err != nil {
			t.Fatalf("the tool_call handler's request ended with %v after the abort, want the handler's own answer: Pi awaits the handler", got.err)
		}
		if text := fmt.Sprint(got.result); !strings.Contains(text, "answered after the abort") {
			t.Fatalf("the tool_call handler's answer = %s, want the block it returned after the abort", text)
		}
	case <-t.Context().Done():
	case <-time.After(budget):
		t.Fatal("the tool_call handler did not answer after the abort")
	}
}

// TestNodeRunHandlerThatNeverSettlesDoesNotHoldTheQuit: Pi's dispose awaits the aborted run, which awaits a turn_end handler that never settles; a pending promise holds
// no handle, so Pi's process exits when its loop drains (Pi 1.5.2: 15 ms over RPC, 93-129 ms under tmux; pig held for ever).
func TestNodeRunHandlerThatNeverSettlesDoesNotHoldTheQuit(t *testing.T) {
	t.Parallel()
	budget := testbudget.Wait(t)
	h, exts, drained, _ := startQuitRuntimes(t, []string{"never"}, map[string]string{"never": quitNeverTurnEndSource})
	handlers := exts["never"].EventHandlers("turn_end")
	if len(handlers) != 1 {
		t.Fatalf("turn_end handlers = %d, want 1", len(handlers))
	}
	go func() { _, _ = handlers[0](map[string]any{"type": "turn_end"}) }()
	awaitExtensionRequest(t, h, "never")
	h.EndInput()
	select {
	case <-drained:
	case <-time.After(budget):
		t.Fatalf("the host never handed the drained loop to its owner: a Node handler that awaits nothing held the quit\n%s", quitHoldState(h))
	}
}

const (
	// quitToolCallThrowSource waits for the run's abort and then throws.
	quitToolCallThrowSource = `export default function (pi) {
  pi.on("tool_call", async (_event, ctx) => {
    if (!ctx.signal.aborted) await new Promise((resolve) => ctx.signal.addEventListener("abort", resolve, { once: true }));
    throw new Error("boom after the abort");
  });
}`
	// quitToolCallNeverSource awaits a promise that never settles.
	quitToolCallNeverSource = `export default function (pi) { pi.on("tool_call", () => new Promise(() => {})); }`
)

// tool_call event of the abort test: the handler's own error after the abort surfaces as the request's error, as it does without an abort.
func TestToolCallEventHandlerThrowingAfterTheAbortSurfaces(t *testing.T) {
	t.Parallel()
	budget := testbudget.Wait(t)
	h, exts, _, ctx := startQuitRuntimes(t, []string{"guard"}, map[string]string{"guard": quitToolCallThrowSource})
	run, abort := context.WithCancel(ctx)
	holdRunSignal(h, run)
	errs := make(chan error, 1)
	go func() {
		_, err := exts["guard"].EventHandlers("tool_call")[0](extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "call_1", ToolName: "probe", Input: map[string]any{}}, run)
		errs <- err
	}()
	awaitExtensionRequest(t, h, "guard")
	abort()
	select {
	case err := <-errs:
		if err == nil || !strings.Contains(err.Error(), "boom after the abort") {
			t.Fatalf("the handler's error after the abort = %v, want its own message", err)
		}
	case <-time.After(budget):
		t.Fatal("the request did not end with the handler's error after the abort")
	}
}

// TestToolCallEventAwaitedAfterTheAbortEndsWhenTheExtensionProcessDies: the wait for a handler that Pi awaits must not become a hang when the extension's process ends: the
// request ends with the connection's error and the run goes on.
func TestToolCallEventAwaitedAfterTheAbortEndsWhenTheExtensionProcessDies(t *testing.T) {
	t.Parallel()
	budget := testbudget.Wait(t)
	h, exts, _, ctx := startQuitRuntimes(t, []string{"never"}, map[string]string{"never": quitToolCallNeverSource})
	run, abort := context.WithCancel(ctx)
	errs := make(chan error, 1)
	go func() {
		_, err := exts["never"].EventHandlers("tool_call")[0](extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "call_1", ToolName: "probe", Input: map[string]any{}}, run)
		errs <- err
	}()
	awaitExtensionRequest(t, h, "never")
	abort()
	h.TerminateProcesses()
	select {
	case err := <-errs:
		if err == nil || errors.Is(err, context.Canceled) {
			t.Fatalf("the request ended with %v, want the connection's error: the abort alone must not end the request of a handler Pi awaits", err)
		}
	case <-time.After(budget):
		t.Fatal("the request awaited after the abort did not end when the extension's process ended")
	}
}

// awaitExtensionRequest returns once a request is in flight on the named extension's connection: an abort or the end of input sent before it would test a run that never reached the handler.
func awaitExtensionRequest(t *testing.T, h *Host, name string) {
	t.Helper()
	h.mu.Lock()
	me := h.exts[name]
	h.mu.Unlock()
	awaitPendingRequest(t, me.connection())
}

// holdRunSignal makes run the active run's signal of h, which a runtime exposes as ctx.signal (runner.ts:917-920): undefined while no run is active, so a handler that reads it would throw.
func holdRunSignal(h *Host, run context.Context) {
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("getSignal", func() context.Context { return run })
	h.SetUIBridge(bridge)
}
