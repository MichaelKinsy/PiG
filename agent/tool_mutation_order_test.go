package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// fakeMutationQueue is a minimal, test-local stand-in for
// internal/codingagent/tools.FileMutationQueue's Reserve/Wait/Release
// semantics: agent cannot import that package (it already imports agent).
// It exists to prove that executeToolCallsParallel reserves a
// QueueOrderable tool's queue position in tool-call source order, in its
// own synchronous loop, before any goroutine starts.
type fakeMutationQueue struct {
	mu   sync.Mutex
	tail chan struct{}
}

func (q *fakeMutationQueue) reserve() *MutationTicket {
	q.mu.Lock()
	prev := q.tail
	next := make(chan struct{})
	q.tail = next
	q.mu.Unlock()
	return &MutationTicket{
		Wait: func() {
			if prev != nil {
				<-prev
			}
		},
		Release: func() { close(next) },
	}
}

// orderableScriptTool is a scriptTool that also implements QueueOrderable,
// logging each ReserveMutationOrder call under the reserving call's id.
type orderableScriptTool struct {
	*scriptTool
	queue *fakeMutationQueue
	log   *orderLog
}

func (t *orderableScriptTool) ReserveMutationOrder(args json.RawMessage) (*MutationTicket, bool) {
	t.log.add("reserve:" + argValue(args))
	return t.queue.reserve(), true
}

// TestParallelDispatchReservesMutationOrderInSourceOrder proves
// executeToolCallsParallel reserves a QueueOrderable tool's queue position
// for every call, synchronously, in tool-call source order, before spawning
// any goroutine — matching upstream's Promise.all(calls.map(...)), whose
// per-call synchronous prefix (including withFileMutationQueue's
// registration step) runs in call order before any call's async work
// begins. Without this, admission order would depend on which goroutine
// happens to reach the queue first, which is exactly the bug
// parity/scenarios/tools/10-print-batched-file-mutation.toml caught: a
// batched write-then-edit on one file could apply out of order.
func TestParallelDispatchReservesMutationOrderInSourceOrder(t *testing.T) {
	ids := []string{"call-1", "call-2", "call-3"}
	var log orderLog
	queue := &fakeMutationQueue{}
	release := map[string]chan struct{}{}
	for _, id := range ids {
		release[id] = make(chan struct{})
	}

	allReserved := make(chan struct{})
	var once sync.Once
	base := &scriptTool{name: "p", mode: ToolModeParallel, params: valueSchema,
		execute: func(ctx context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			ticket, ok := MutationTicketFromContext(ctx)
			if !ok {
				t.Error("no reserved mutation ticket in context")
				return AgentToolResult{}, nil
			}
			// Every call's reservation happens before any goroutine starts,
			// so by the time this (the first, since its ticket has no
			// predecessor) call gets past Wait, every reservation in the
			// batch has already landed.
			ticket.Wait()
			once.Do(func() { close(allReserved) })
			id := argValue(args)
			awaitSignal(t, release[id], "release of "+id)
			log.add("run:" + id)
			ticket.Release()
			return AgentToolResult{Content: id}, nil
		}}
	tool := &orderableScriptTool{scriptTool: base, queue: queue, log: &log}

	calls := make([]ai.ToolCall, len(ids))
	for i, id := range ids {
		calls[i] = toolCall(id, "p", ai.JsonObject{"value": id})
	}
	a := NewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(calls...)}),
		Tools: []AgentTool{tool},
	})

	done := sendAsync(t, a, "run")
	waitSignal(t, allReserved, "every call's mutation reservation")

	if got := log.list(); !reflect.DeepEqual(got, []string{"reserve:call-1", "reserve:call-2", "reserve:call-3"}) {
		t.Fatalf("reservation order = %v, want source order", got)
	}

	// Release out of source order: the reservation chain, not release
	// timing, must decide run order.
	for _, id := range []string{"call-3", "call-2", "call-1"} {
		close(release[id])
	}
	waitSignal(t, done, "the run")

	got := log.list()[len(ids):]
	if want := []string{"run:call-1", "run:call-2", "run:call-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("run order = %v, want reservation (source) order %v", got, want)
	}
}
