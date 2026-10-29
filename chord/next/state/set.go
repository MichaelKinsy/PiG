package state

import (
	"slices"
	"sync"
)

// orderedSet is JavaScript's Set for the collections a source keeps: insertion ordered, each member once. Iteration is
// over Snapshot, which is never mutated, so a member may leave the set while it is walked (a watch ending as it is
// advanced) and a walk allocates nothing; adding and removing copy, which is rare next to walking. The mutex guards the
// slice header only: a watch ends itself from a delivery goroutine, concurrently with the host's walk.
type orderedSet[K comparable] struct {
	mu    sync.Mutex
	items []K
}

// Add appends key unless it is a member.
func (set *orderedSet[K]) Add(key K) {
	set.mu.Lock()
	defer set.mu.Unlock()
	if slices.Contains(set.items, key) {
		return
	}
	next := make([]K, len(set.items)+1)
	copy(next, set.items)
	next[len(set.items)] = key
	set.items = next
}

// Delete removes key and reports whether it was a member.
func (set *orderedSet[K]) Delete(key K) bool {
	set.mu.Lock()
	defer set.mu.Unlock()
	for i, item := range set.items {
		if item != key {
			continue
		}
		next := make([]K, 0, len(set.items)-1)
		next = append(next, set.items[:i]...)
		next = append(next, set.items[i+1:]...)
		set.items = next
		return true
	}
	return false
}

// Has reports whether key is a member.
func (set *orderedSet[K]) Has(key K) bool {
	set.mu.Lock()
	defer set.mu.Unlock()
	return slices.Contains(set.items, key)
}

// Len is the number of members.
func (set *orderedSet[K]) Len() int {
	set.mu.Lock()
	defer set.mu.Unlock()
	return len(set.items)
}

// Clear removes every member.
func (set *orderedSet[K]) Clear() {
	set.mu.Lock()
	defer set.mu.Unlock()
	set.items = nil
}

// Snapshot returns the members in insertion order; the caller must not modify the slice.
func (set *orderedSet[K]) Snapshot() []K {
	set.mu.Lock()
	defer set.mu.Unlock()
	return set.items
}
