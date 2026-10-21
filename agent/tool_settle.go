package agent

import (
	"context"
	"strconv"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

type toolSettleKey struct{}

type toolSettle struct {
	slot       *jsloop.SettleSlot
	toolCallID string
	batch      uint64
}

// toolBatches numbers the parallel batches of the process.
var toolBatches atomic.Uint64

// withToolSettle carries the slot of one call of a parallel batch, and the batch's number, to its tool.
func withToolSettle(ctx context.Context, slot *jsloop.SettleSlot, toolCallID string, batch uint64) context.Context {
	return context.WithValue(ctx, toolSettleKey{}, toolSettle{slot: slot, toolCallID: toolCallID, batch: batch})
}

// ToolBatchFromContext returns the identifier of the parallel batch the call toolCallID belongs to, "" for a call outside one (a sequential batch, or a call
// another tool makes inside its own). It is the same for every call of a batch and differs between batches. A runtime that executes the batch's calls on
// threads of its own uses it to order only the calls of one batch, as Pi's single thread orders them.
func ToolBatchFromContext(ctx context.Context, toolCallID string) string {
	settle, ok := ctx.Value(toolSettleKey{}).(toolSettle)
	if !ok || settle.toolCallID != toolCallID {
		return ""
	}
	return strconv.FormatUint(settle.batch, 10)
}

// ToolSettleFromContext returns the slot of the call toolCallID of a parallel
// batch. A tool whose result arrives from outside this process settles it from
// the goroutine that reads the result, so the batch emits the calls'
// tool_execution_end events in the order the results arrived (agent-loop.ts
// executeToolCallsParallel: each call's end follows its own settle). A tool
// that never settles it settles it when its Execute returns. The slot belongs
// to one call: a call another tool makes inside its own (a different
// toolCallID) gets none.
func ToolSettleFromContext(ctx context.Context, toolCallID string) *jsloop.SettleSlot {
	settle, ok := ctx.Value(toolSettleKey{}).(toolSettle)
	if !ok || settle.toolCallID != toolCallID {
		return nil
	}
	return settle.slot
}

type toolHookStartKey struct{}

// withToolHookStart carries to the after-tool-call hooks of a parallel batch's call the function that reports the call's hooks started.
func withToolHookStart(ctx context.Context, started func()) context.Context {
	return context.WithValue(ctx, toolHookStartKey{}, started)
}

// ToolHookStartedFromContext returns the function a call's after-tool-call hooks call once their first extension handler has been invoked, or
// nil for a call outside a parallel batch. Calling it lets the batch start the hooks of the calls that settled after this one (agent-loop.ts:619-647:
// the hooks start in settle order and each runs to its first await before the next starts). It is idempotent, and the batch calls it when the hooks
// return.
func ToolHookStartedFromContext(ctx context.Context) func() {
	started, _ := ctx.Value(toolHookStartKey{}).(func())
	return started
}
