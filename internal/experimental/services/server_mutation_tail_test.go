package services

import (
	"context"
	"slices"
	"sync"
	"testing"
)

// An admitted call's ticket belongs to one tail. A member whose context carries it may run work on another tail first; that
// work must not consume or release the reservation, or a later call in the first tail would overtake this one.
func TestMutationTicketIsIgnoredByAnotherTail(t *testing.T) {
	var first, other serviceMutationTail
	held, next := first.reserve(), first.reserve()
	heldCtx := context.WithValue(t.Context(), mutationTicketKey{}, held)
	nextCtx := context.WithValue(t.Context(), mutationTicketKey{}, next)
	if err := other.run(heldCtx, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	record := func(name string) func() error {
		return func() error { mu.Lock(); order = append(order, name); mu.Unlock(); return nil }
	}
	var later sync.WaitGroup
	later.Go(func() { _ = first.run(nextCtx, record("second")) })
	if err := first.run(heldCtx, record("first")); err != nil {
		t.Fatal(err)
	}
	held.release()
	later.Wait()
	if !slices.Equal(order, []string{"first", "second"}) {
		t.Fatalf("mutation order = %v, want [first second]", order)
	}
}
