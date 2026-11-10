package host

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// pi: packages/chord/src/host/reference.ts (`watch`, `firstValues`)
// Own tests of the turn a member watch is opened in. Pi's `async watch(member, ctx)` waits for `firstValues` and opens the watch
// in the microtask that resumes after it, so the watch is open in the turn that binds the reference, before any section a link's
// frame starts; a caller's `.then(start)` runs in that turn too, and every change of the member after the bind is a frame of
// the watch. A Go caller that resumed from a goroutine of its own opened it after whatever section came between, and that
// section's change was folded into the watch's value, which a follower that only listens never reads (the language follower's
// pick, lost for good).

// afterTheBindOf runs, inside the loop, a microtask queued after every one already waiting for ref's first values: the next
// turn's first look at the reference once it is bound, as a frame of a link is. It reports how many watches the reference
// followed then.
func afterTheBindOf(ref *Reference) <-chan int {
	seen := make(chan int, 1)
	ref.binding.loop.Run(func() {
		ref.binding.firstValues(background).then(func(struct{}, error) { seen <- len(ref.binding.watches) })
	})
	return seen
}

// A watch of a member of a reference that is not bound yet is open when the turn that binds the reference ends, whether its caller
// blocks for it (Watch, from a goroutine of its own) or goes on (WatchThen).
func TestAMemberWatchOfAReferenceNotBoundYetIsOpenInTheTurnThatBindsIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		watch func(ref *Reference, opened chan<- chord.WatchHandle[delta.JsonValue])
	}{
		{"Watch", func(ref *Reference, opened chan<- chord.WatchHandle[delta.JsonValue]) {
			go func() {
				watch, err := ref.Member("n").Watch(background)
				if err != nil {
					t.Errorf("watch: %v", err)
				}
				opened <- watch
			}()
		}},
		{"WatchThen", func(ref *Reference, opened chan<- chord.WatchHandle[delta.JsonValue]) {
			ref.Member("n").WatchThen(background, func(watch chord.WatchHandle[delta.JsonValue], err error) {
				if err != nil {
					t.Errorf("watch: %v", err)
				}
				opened <- watch
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := make(chan struct{})
			var closes, opens seenList[string]
			host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return gatedSvc(h, gate, &closes, &opens) }, loadedOptions{})
			ref, err := host.Hold(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
			must(t, err)
			waiting := func() (n int) {
				ref.binding.loop.Run(func() { n = len(ref.binding.waiters) })
				return n
			}
			before := waiting()
			opened := make(chan chord.WatchHandle[delta.JsonValue], 1)
			tc.watch(ref, opened)
			scopeEventually(t, "the watch waits for the first values", func() bool { return waiting() == before+1 })
			seen := afterTheBindOf(ref)
			close(gate)
			if got := <-seen; got != 1 {
				t.Errorf("watches the reference followed in the turn after its bind = %d, want 1: the watch is opened after that turn", got)
			}
			if watch := <-opened; watch == nil {
				t.Fatal("no watch")
			} else {
				_, _ = watch.Stop()
			}
		})
	}
}

// WatchThen's callback gets the error, as a microtask and with no watch, of a reference released before its first value (Pi's
// `.then(start, () => {})` rejection reaction), and of a context aborted before it.
func TestAMemberWatchThenOfAReferenceEndedBeforeItsFirstValueGetsTheError(t *testing.T) {
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	var closes, opens seenList[string]
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return gatedSvc(h, gate, &closes, &opens) }, loadedOptions{})
	ref, err := host.Hold(background, hostSvc, jsonObject("id", float64(1)), GetOptions{})
	must(t, err)
	type outcome struct {
		watch chord.WatchHandle[delta.JsonValue]
		err   error
	}
	got := make(chan outcome, 2)
	record := func(watch chord.WatchHandle[delta.JsonValue], err error) { got <- outcome{watch, err} }

	aborted, abort := context.WithCancelCause(background)
	ref.Member("n").WatchThen(aborted, record)
	abort(errors.New("test: aborted"))
	if first := <-got; first.watch != nil || first.err == nil || first.err.Error() != "test: aborted" {
		t.Fatalf("aborted: watch = %v, err = %v, want no watch and the abort", first.watch, first.err)
	}

	ref.Member("n").WatchThen(background, record)
	ref.Release()
	first := <-got
	if first.watch != nil {
		t.Fatalf("released: got a watch")
	}
	wantChordError(t, first.err, data.CodeReleased, "", "")
}
