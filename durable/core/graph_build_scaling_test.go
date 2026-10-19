// SPDX-License-Identifier: MIT

package core

import (
	"testing"
	"time"

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

// graphBuildTime is the least of three builds of the task graph of n live tasks that each own one conversation.
func graphBuildTime(t *testing.T, n int) time.Duration {
	t.Helper()
	h := graphHandler(t, n)
	best := time.Duration(1 << 62)
	for range 3 {
		start := time.Now()
		graph := h.buildTaskGraph()
		best = min(best, time.Since(start))
		if tasks := objOf(graph, "tasks"); tasks.Len() != n {
			t.Fatalf("graph has %d tasks, want %d", tasks.Len(), n)
		}
	}
	return best
}

// Building the graph finds each task's conversations in one pass over the conversations, not one pass per task: the cost grows
// with the live tasks and the conversations, not with their product.
func TestBuildTaskGraphCostGrowsLinearlyWithTasksAndConversations(t *testing.T) {
	small, large := graphBuildTime(t, 1000), graphBuildTime(t, 8000)
	t.Logf("graph build: 1000 tasks %v, 8000 tasks %v", small, large)
	if large > 24*small+5*time.Millisecond {
		t.Fatalf("8x the tasks and conversations cost %.1fx (%v vs %v): the build is not linear", float64(large)/float64(small), large, small)
	}
}
