package ai

import (
	"runtime"
	"slices"
	"sync"
	"testing"
)

// pull's contract is iter.Pull's, whichever build (iter.Pull itself, or the race build's goroutine pair) provides it.
func TestPullFollowsIterPullContract(t *testing.T) {
	counting := func(n int, ended *bool) func(func(int) bool) {
		return func(yield func(int) bool) {
			defer func() { *ended = true }()
			for i := range n {
				if !yield(i) {
					return
				}
			}
		}
	}
	t.Run("a drained sequence ends and stays ended", func(t *testing.T) {
		var ended bool
		next, stop := pull(counting(3, &ended))
		var got []int
		for {
			v, ok := next()
			if !ok {
				break
			}
			got = append(got, v)
		}
		if !slices.Equal(got, []int{0, 1, 2}) || !ended {
			t.Fatalf("got %v, ended %v, want [0 1 2] and ended", got, ended)
		}
		if v, ok := next(); ok || v != 0 {
			t.Fatalf("next after the end = %d, %v", v, ok)
		}
		stop()
	})
	t.Run("stop ends a sequence after its first value and next then yields nothing", func(t *testing.T) {
		var ended bool
		next, stop := pull(counting(3, &ended))
		if v, ok := next(); !ok || v != 0 {
			t.Fatalf("first next = %d, %v", v, ok)
		}
		stop()
		if !ended {
			t.Fatal("stop did not end the sequence before returning")
		}
		if _, ok := next(); ok {
			t.Fatal("next after stop yielded a value")
		}
		stop()
	})
	t.Run("a sequence that is stopped before the first next never runs", func(t *testing.T) {
		var ended bool
		started := false
		_, stop := pull(func(yield func(int) bool) {
			started = true
			counting(1, &ended)(yield)
		})
		stop()
		if started || ended {
			t.Fatalf("started %v, ended %v, want neither", started, ended)
		}
	})
	t.Run("a panic reaches the caller of next, then the sequence is over", func(t *testing.T) {
		next, stop := pull(func(yield func(int) bool) {
			yield(1)
			panic("boom")
		})
		if v, ok := next(); !ok || v != 1 {
			t.Fatalf("first next = %d, %v", v, ok)
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != "boom" {
					t.Fatalf("recovered %v, want boom", recovered)
				}
			}()
			next()
		}()
		if _, ok := next(); ok {
			t.Fatal("next after the panic yielded a value")
		}
		stop()
	})
	t.Run("a panic while stopping reaches the caller of stop", func(t *testing.T) {
		next, stop := pull(func(yield func(int) bool) {
			defer func() { panic("cleanup failed") }()
			yield(1)
		})
		next()
		defer func() {
			if recovered := recover(); recovered != "cleanup failed" {
				t.Fatalf("recovered %v, want cleanup failed", recovered)
			}
		}()
		stop()
		t.Fatal("stop returned")
	})
	t.Run("a Goexit in the sequence ends the caller's goroutine", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		reached := false
		go func() {
			defer wg.Done()
			next, _ := pull(func(yield func(int) bool) { runtime.Goexit() })
			next()
			reached = true
		}()
		wg.Wait()
		if reached {
			t.Fatal("next returned after the sequence called runtime.Goexit")
		}
	})
}
