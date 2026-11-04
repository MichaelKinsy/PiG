package harness

// scheduler.ts:955-960: expiring idle contexts walks the expiry order lazily and stops at the first context that is still
// retained, so a settle that expires nothing costs the same with any number of idle contexts.

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// idleSchedulerOf is a bare scheduler whose idle order holds contexts idle since 0, 1, ... in that order.
func idleSchedulerOf(contexts int) *TaskScheduler {
	s := &TaskScheduler{contexts: map[durable.ConversationId]*keptContext{}, idleOrder: newOrderedSet[durable.ConversationId]()}
	for index := range contexts {
		id := durable.ConversationId(index + 1)
		since := float64(index)
		s.contexts[id] = &keptContext{idleSince: &since}
		s.idleOrder.Add(id)
	}
	return s
}

func TestExpiringIdleContextsStopsAtTheFirstRetainedOne(t *testing.T) {
	s := idleSchedulerOf(10)
	// Retained for 5 at time 7.5: the contexts idle since 0, 1, 2 (IDs 1-3) have expired, the one since 3 has not.
	s.mu.Lock()
	s.expireContextsLocked(7.5, 5)
	s.mu.Unlock()
	var kept []durable.ConversationId
	for id := range s.contexts {
		kept = append(kept, id)
	}
	slices.Sort(kept)
	if want := []durable.ConversationId{4, 5, 6, 7, 8, 9, 10}; !slices.Equal(kept, want) {
		t.Fatalf("kept %v, want %v", kept, want)
	}
	if first, ok := s.idleOrder.First(); !ok || first != 4 {
		t.Fatalf("the oldest idle context is %v (%v), want 4", first, ok)
	}
	s.mu.Lock()
	s.expireContextsLocked(100, 0)
	s.mu.Unlock()
	if len(s.contexts) != 0 || s.idleOrder.Len() != 0 {
		t.Fatalf("%d contexts and %d in the order after expiring with no retention, want none", len(s.contexts), s.idleOrder.Len())
	}
}

// A settle that expires nothing allocates nothing, at any number of idle contexts.
func TestExpiringIdleContextsAllocatesNothingWhenTheOldestIsRetained(t *testing.T) {
	for _, contexts := range []int{100, 1000, 4000} {
		t.Run(fmt.Sprintf("contexts=%d", contexts), func(t *testing.T) {
			s := idleSchedulerOf(contexts)
			result := testing.Benchmark(func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					s.mu.Lock()
					s.expireContextsLocked(float64(contexts), float64(contexts)+10)
					s.mu.Unlock()
				}
			})
			if bytes := result.AllocedBytesPerOp(); bytes != 0 {
				t.Fatalf("%d B/op with %d idle contexts, want 0", bytes, contexts)
			}
			if len(s.contexts) != contexts {
				t.Fatalf("%d contexts left, want %d", len(s.contexts), contexts)
			}
		})
	}
}
