package store

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/store/store.test.ts, "settled" (Pi main f57cd9484): `settled()` resolves once every commit queued
// before the call settled (stored and adopted, or rejected), also on a closed store, and waits for no commit queued later.
//
// Go form: `Settled()` is the channel Pi's promise is; it closes once the commits queued before the call settled. "Resolves"
// is a receive; "has not resolved" is a receive that finds nothing, asked when nothing can race it (the commit queue is held
// by a transaction gate, as `holdNextCommit` holds it in Pi).

// done reports whether settled is closed.
func done(settled <-chan struct{}) bool {
	select {
	case <-settled:
		return true
	default:
		return false
	}
}

func TestSettled(t *testing.T) {
	t.Run("resolves once the commits queued before it were adopted or rejected", func(t *testing.T) {
		ctl := controlledStore(t, true)
		var events []string
		fence := func(name string, admit bool) CommitFence {
			return CommitFence{
				Admit: func() bool { return admit },
				Adopt: func(int64, []int64, int) error {
					events = append(events, "adopt "+name)
					return nil
				},
			}
		}
		gate := ctl.arm()
		running := startCommit(ctl.store, fence("a", true), created("a"))
		<-gate.Entered()
		queued := startCommit(ctl.store, fence("b", true), created("b"))
		refused := startCommit(ctl.store, fence("c", false), created("c"))
		settled := ctl.store.Settled()
		if done(settled) {
			t.Fatal("settled resolved while the first commit's step is held")
		}
		if len(events) != 0 {
			t.Fatalf("events = %v before the step is released, want none", events)
		}
		gate.Release()
		<-settled
		events = append(events, "settled")
		sameEvents(t, events, "adopt a", "adopt b", "settled")
		if got := []int64{seqOf(t, running), seqOf(t, queued)}; got[0] != 1 || got[1] != 2 {
			t.Fatalf("seqs = %v, want [1 2]", got)
		}
		_ = rejected(t, failureOf(t, refused), data.CodeReleased, data.ReasonDiscarded)
	})

	t.Run("does not wait for a commit queued after the call; resolves on a closed store", func(t *testing.T) {
		ctl := controlledStore(t, true)
		before := ctl.store.Settled()
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("a"))
		<-gate.Entered()
		if !done(before) {
			t.Fatal("settled waited for a commit queued after the call")
		}
		gate.Release()
		seqOf(t, running)
		noError(t, ctl.store.Close(background))
		if !done(ctl.store.Settled()) {
			t.Fatal("settled did not resolve on a closed store")
		}
	})
}
