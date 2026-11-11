package store

import (
	"errors"
	"sync"
)

// Ports packages/chord/src/next/store/allocator.ts
//
// The store's id allocator (slice 6b D3, §1.2): one counter for every id in the store (Durable's `mintId`, Chord's
// document ids), raised by explicit ids, saturating at Number.MAX_SAFE_INTEGER.
//
// Go forced difference: Pi is single-threaded, so its allocator needs no lock; Go callers may be on several
// goroutines (the step runs under the commit queue, but `Value` is read by readers), so a mutex guards the counter.
// Ids are int64.

// MaxSafeInteger is Number.MAX_SAFE_INTEGER, 2^53 - 1: the largest exact id, seq, version or count.
const MaxSafeInteger int64 = 1<<53 - 1

// FirstID is the first id the allocator gives: id 1 is Durable's root conversation.
const FirstID int64 = 2

// IdAllocator: Value is the next id it gives. An id raised to MaxSafeInteger is accepted, the value stays there, and
// every later Mint fails: no id past the safe range (it would stop counting exactly and repeat).
type IdAllocator struct {
	mu   sync.Mutex
	next int64
}

// NewIdAllocator is an allocator whose next id is next.
func NewIdAllocator(next int64) *IdAllocator { return &IdAllocator{next: next} }

// Value is the next id Mint gives; what each commit persists.
func (ids *IdAllocator) Value() int64 {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	return ids.next
}

// Mint takes the next id; it fails with "ID space is exhausted" once saturated (Durable's message).
func (ids *IdAllocator) Mint() (int64, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	if ids.next >= MaxSafeInteger {
		return 0, errors.New("ID space is exhausted")
	}
	id := ids.next
	ids.next++
	return id, nil
}

// Raise raises the counter above id, an id written explicitly (Durable mints ahead), saturating.
func (ids *IdAllocator) Raise(id int64) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	if id >= ids.next {
		ids.next = min(id+1, MaxSafeInteger)
	}
}
