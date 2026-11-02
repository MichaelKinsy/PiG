package goroutineid

import (
	"iter"
	"sync"
	"testing"
)

func TestCurrentIdentifiesTheCallingGoroutine(t *testing.T) {
	here := Current()
	if here == 0 || Current() != here {
		t.Fatalf("Current() = %d, not stable and non-zero on one goroutine", here)
	}
	other := make(chan uint64)
	go func() { other <- Current() }()
	if id := <-other; id == 0 || id == here {
		t.Fatalf("another goroutine has ID %d, this one %d", id, here)
	}
}

// Current must return the ID runtime.Stack prints for the calling goroutine on every goroutine, whatever its stack depth and
// however many are alive at once, because callers compare IDs taken on different goroutines.
func TestCurrentEqualsTheStackHeaderID(t *testing.T) {
	var deep func(n int) (uint64, uint64)
	deep = func(n int) (uint64, uint64) {
		if n == 0 {
			return Current(), stackID()
		}
		return deep(n - 1)
	}
	const goroutines = 64
	var wg sync.WaitGroup
	seen := make(map[uint64]bool)
	var mu sync.Mutex
	release := make(chan struct{})
	for i := range goroutines {
		wg.Go(func() {
			got, want := deep(i * 7)
			<-release
			again := Current()
			mu.Lock()
			defer mu.Unlock()
			if got != want || again != want {
				t.Errorf("goroutine %d: Current() = %d then %d, stack header %d", i, got, again, want)
			}
			if seen[want] {
				t.Errorf("goroutine %d: ID %d is shared with another live goroutine", i, want)
			}
			seen[want] = true
		})
	}
	close(release)
	wg.Wait()
}

// A coroutine of iter.Pull runs on a goroutine of its own, and the iterator body's goroutine ID is the one its stack header
// prints, not the consumer's.
func TestCurrentOnACoroutineEqualsItsStackHeaderID(t *testing.T) {
	next, stop := iter.Pull(func(yield func(uint64) bool) {
		for range 3 {
			if Current() != stackID() || !yield(Current()) {
				return
			}
		}
	})
	defer stop()
	consumer := Current()
	for range 3 {
		id, ok := next()
		if !ok || id == 0 || id == consumer {
			t.Fatalf("coroutine yielded %d, ok=%v, consumer is %d", id, ok, consumer)
		}
		if consumer != Current() || consumer != stackID() {
			t.Fatalf("the consumer's ID changed across a coroutine switch: %d, now %d (header %d)", consumer, Current(), stackID())
		}
	}
}

// Proving ends once the proof has seen its distinct goroutines (and is never true where Current always formats the header).
func TestProvingEndsOnceDistinctGoroutinesHaveCalledCurrent(t *testing.T) {
	for range 32 {
		var done sync.WaitGroup
		done.Go(func() { Current() })
		done.Wait()
	}
	if Proving() {
		t.Fatal("Proving() is still true after 32 distinct goroutines called Current")
	}
}

func BenchmarkCurrent(b *testing.B) {
	var deep func(n int)
	deep = func(n int) {
		if n > 0 {
			deep(n - 1)
			return
		}
		b.ReportAllocs()
		for b.Loop() {
			Current()
		}
	}
	deep(40)
}
