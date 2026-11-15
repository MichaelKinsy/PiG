package store

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/internal/storeseam"
)

// The test seam storeseam.WrapAdopt: the adoption of every commit that takes its place in the commit queue passes through
// the wrapper of its store, in queue order, and the wrapped adoption runs where the fence's did.

// wrapping installs a wrapper on store that records, into events, "wrap n" as the n-th commit takes its place and "before n"
// and "after n" around that commit's own adoption. A replacing wrapper does not call it.
func wrapping(store *ChordStore, events *[]string, replacing bool) {
	enlisted := 0
	storeseam.WrapAdopt(store, func(adopt storeseam.Adopt) storeseam.Adopt {
		enlisted++
		n := enlisted
		*events = append(*events, fmt.Sprintf("wrap %d", n))
		return func(seq int64, ids []int64, rows int) error {
			*events = append(*events, fmt.Sprintf("before %d", n))
			var err error
			if !replacing {
				err = adopt(seq, ids, rows)
			}
			*events = append(*events, fmt.Sprintf("after %d", n))
			return err
		}
	})
}

func TestWrapAdopt(t *testing.T) {
	t.Run("wraps every commit as it takes its place, the joiners of a group included, and runs the wrapped adoption between the adoptions of the group's commits", func(t *testing.T) {
		ctl := controlledStore(t, true)
		var events []string
		wrapping(ctl.store, &events, false)
		gate := ctl.arm()
		running := startCommit(ctl.store, traced(&events, "a", true), created("a"))
		<-gate.Entered()
		var queued []<-chan groupOutcome
		for _, key := range []string{"b", "c", "d"} {
			queued = append(queued, startCommit(ctl.store, traced(&events, key, true), created(key)))
		}
		// c and d join the group b leads, so the queue's tail changes only for b: every wrapper has run before the step goes on.
		sameEvents(t, events, "wrap 1", "admit a", "wrap 2", "wrap 3", "wrap 4")
		gate.Release()
		for index, settled := range append([]<-chan groupOutcome{running}, queued...) {
			if seq := seqOf(t, settled); seq != int64(index+1) {
				t.Fatalf("commit %d: seq = %d, want %d", index, seq, index+1)
			}
		}
		sameEvents(t, events,
			"wrap 1", "admit a", "wrap 2", "wrap 3", "wrap 4",
			"before 1", "adopt a 1 2", "after 1",
			"admit b", "admit c", "admit d",
			"before 2", "adopt b 2 3", "after 2",
			"before 3", "adopt c 3 4", "after 3",
			"before 4", "adopt d 4 5", "after 4")
		if got := ctl.transactions(); got != 2 {
			t.Fatalf("transactions = %d, want 2: the three commits queued behind the held one are one group", got)
		}
	})

	t.Run("runs a wrapper's adoption in place of the fence's when it does not call it", func(t *testing.T) {
		ctl := controlledStore(t, true)
		var events []string
		wrapping(ctl.store, &events, true)
		seq := seqOf(t, startCommit(ctl.store, traced(&events, "a", true), created("a")))
		if seq != 1 {
			t.Fatalf("seq = %d, want 1", seq)
		}
		sameEvents(t, events, "wrap 1", "admit a", "before 1", "after 1")
	})

	t.Run("wraps only the commits that take their place while it is set, and only those of its own store", func(t *testing.T) {
		ctl, other := controlledStore(t, true), controlledStore(t, true)
		var events []string
		before := seqOf(t, startCommit(ctl.store, traced(&events, "before", true), created("before")))
		wrapping(ctl.store, &events, false)
		wrapped := seqOf(t, startCommit(ctl.store, traced(&events, "wrapped", true), created("wrapped")))
		seqOf(t, startCommit(other.store, traced(&events, "other", true), created("other")))
		storeseam.WrapAdopt(ctl.store, nil)
		after := seqOf(t, startCommit(ctl.store, traced(&events, "after", true), created("after")))
		if before != 1 || wrapped != 2 || after != 3 {
			t.Fatalf("seqs = [%d, %d, %d], want [1, 2, 3]", before, wrapped, after)
		}
		sameEvents(t, events,
			"admit before", "adopt before 1 2",
			"wrap 1", "admit wrapped", "before 1", "adopt wrapped 2 3", "after 1",
			"admit other", "adopt other 1 2",
			"admit after", "adopt after 3 4")
	})
}
