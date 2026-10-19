// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// graphHandler is a handler whose mirror holds n live tasks that each own one conversation.
func graphHandler(t *testing.T, n int) *handler {
	t.Helper()
	h := &handler{mir: newMirror(), b: abi.NewBuilder()}
	for i := range n {
		task := int64(2 + 2*i)
		rec, err := jv.Parse([]byte(`{"id":` + itoa(task) + `,"conversationId":1,"kind":"k","background":false,"abortRequested":false,"state":{"status":"pending","checkpoint":{"phase":"start"}}}`))
		if err != nil {
			t.Fatal(err)
		}
		h.mir.putTask(task, headOfTask(rec.(*jv.Object)), rec.(*jv.Object), nil)
		conv, err := jv.Parse([]byte(`{"id":` + itoa(task+1) + `,"owner":{"kind":"task","taskId":` + itoa(task) + `}}`))
		if err != nil {
			t.Fatal(err)
		}
		h.mir.convs.put(task+1, conv.(*jv.Object))
	}
	return h
}

// The graph finds the conversations each task owns through the conversation table's owner index. A pass over the conversations
// for every live task made the build cost tasks x conversations (116x for 8x the tasks); the index is read once per task. The
// conversation records are removed after the table indexed them, so a build that reads them (a scan per task, or one pass) finds no
// owner and the nodes lose their conversations: the test is deterministic, with no clock.
func TestBuildTaskGraphReadsOwnedConversationsFromTheOwnerIndex(t *testing.T) {
	for _, n := range []int{1, 50, 1000} {
		h := graphHandler(t, n)
		clear(h.mir.convs.recs)
		graph := h.buildTaskGraph()
		tasks := objOf(graph, "tasks")
		if tasks.Len() != n {
			t.Fatalf("graph has %d tasks, want %d", tasks.Len(), n)
		}
		for i := range n {
			task := int64(2 + 2*i)
			node := objOf(tasks, itoa(task))
			if got, want := jv.Stringify(valueOf(node, "conversations")), "["+itoa(task+1)+"]"; got != want {
				t.Fatalf("n=%d task %d: conversations %s, want %s", n, task, got, want)
			}
		}
	}
}

// Allocations of one build grow with the live tasks, not with tasks x conversations.
func TestBuildTaskGraphAllocationsGrowLinearly(t *testing.T) {
	allocs := func(n int) float64 {
		h := graphHandler(t, n)
		return testing.AllocsPerRun(3, func() { h.buildTaskGraph() })
	}
	small, large := allocs(1000), allocs(8000)
	t.Logf("allocations per build: 1000 tasks %.0f, 8000 tasks %.0f", small, large)
	if large > 9*small {
		t.Fatalf("8x the tasks allocates %.1fx per build", large/small)
	}
}
