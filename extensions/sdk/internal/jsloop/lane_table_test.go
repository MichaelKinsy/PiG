package jsloop

import (
	"reflect"
	"sync"
	"testing"
)

// laneBehaviors is the behavior table of Lane. The root module and the SDK module each carry a verbatim copy of this file and of lane.go (TestSDKCopy), so one table pins both implementations.
var laneBehaviors = []struct {
	name string
	run  func(t *testing.T)
}{
	{"a place reserved into caller storage orders like one Reserve made", func(t *testing.T) {
		var lane Lane
		var first, second Place
		p1, p2 := lane.ReserveInto(&first), lane.ReserveInto(&second)
		if p1 != &first || p2 != &second {
			t.Fatal("ReserveInto must return the storage it was given")
		}
		var rec orderRecorder
		var done sync.WaitGroup
		done.Go(func() {
			p2.Wait()
			rec.action("second")()
			p2.Release()
		})
		rec.action("first")()
		p1.Release()
		done.Wait()
		if got := rec.names(); !reflect.DeepEqual(got, []string{"first", "second"}) {
			t.Fatalf("order %v", got)
		}
	}},
	{"a place waits for every earlier place to release, whatever order the goroutines start in", func(t *testing.T) {
		const n = 6
		for shift := range n {
			var lane Lane
			places := make([]*Place, n)
			for i := range places {
				places[i] = lane.Reserve()
			}
			var rec orderRecorder
			var done sync.WaitGroup
			for k := range n {
				i := (k + shift) % n
				done.Go(func() {
					places[i].Wait()
					rec.action(string(rune('a' + i)))()
					places[i].Release()
				})
			}
			done.Wait()
			if want := []string{"a", "b", "c", "d", "e", "f"}; !reflect.DeepEqual(rec.names(), want) {
				t.Fatalf("shift %d: calls issued in order %v, want %v", shift, rec.names(), want)
			}
		}
	}},
	{"the first place does not wait", func(t *testing.T) {
		var lane Lane
		lane.Reserve().Wait()
	}},
	{"a later place stays blocked until the earlier one releases", func(t *testing.T) {
		var lane Lane
		first, second := lane.Reserve(), lane.Reserve()
		passed := make(chan struct{})
		go func() { second.Wait(); close(passed) }()
		select {
		case <-passed:
			t.Fatal("the second place passed before the first released")
		default:
		}
		first.Release()
		awaitSignal(t, passed, "the second place after the first released")
	}},
	{"Release is idempotent and does not wait for earlier places", func(t *testing.T) {
		var lane Lane
		first, second, third := lane.Reserve(), lane.Reserve(), lane.Reserve()
		second.Release()
		second.Release()
		// third waits only for second: Release handed the place on without waiting for first.
		third.Wait()
		first.Release()
	}},
	{"Pass waits for the earlier places and then hands the place on", func(t *testing.T) {
		var lane Lane
		first, second, third := lane.Reserve(), lane.Reserve(), lane.Reserve()
		passed := make(chan struct{})
		go func() { second.Pass(); close(passed) }()
		thirdWaiting := make(chan struct{})
		go func() { third.Wait(); close(thirdWaiting) }()
		select {
		case <-passed:
			t.Fatal("Pass returned before the earlier place released")
		case <-thirdWaiting:
			t.Fatal("a place behind Pass proceeded before the earlier place released")
		default:
		}
		first.Release()
		awaitSignal(t, passed, "Pass after the earlier place released")
		awaitSignal(t, thirdWaiting, "the place behind Pass")
		second.Pass()
	}},
	{"lanes do not order each other", func(t *testing.T) {
		var a, b Lane
		a.Reserve()
		b.Reserve().Wait()
	}},
	{"every place after one waits for it, and the place that follows a released place does not wait", func(t *testing.T) {
		first := After(nil)
		const waiting = 5
		var woken sync.WaitGroup
		blocked := make(chan struct{}, waiting)
		passed := make(chan struct{}, waiting)
		for range waiting {
			follower := After(first)
			woken.Go(func() {
				blocked <- struct{}{}
				follower.Wait()
				passed <- struct{}{}
			})
		}
		for range waiting {
			awaitSignal(t, blocked, "a follower reaching its Wait")
		}
		select {
		case <-passed:
			t.Fatal("a follower passed before the place it follows was released")
		default:
		}
		first.Release()
		woken.Wait()
		if len(passed) != waiting {
			t.Fatalf("%d followers passed, want %d", len(passed), waiting)
		}
		after := make(chan struct{})
		go func() { After(first).Wait(); close(after) }()
		awaitSignal(t, after, "the place after a released one")
	}},
	{"a place keeps no link to the place it waited for or that it replaced", func(t *testing.T) {
		var lane Lane
		first, second, third := lane.Reserve(), lane.Reserve(), lane.Reserve()
		first.Release()
		second.Wait()
		if second.previous.Load() != nil {
			t.Fatal("a place that waited still holds the place before it, so a long-lived lane would keep every place it ever made")
		}
		third.Release()
		if third.previous.Load() != nil {
			t.Fatal("a released place still holds the place before it")
		}
		fourth := lane.Reserve()
		if fourth.previous.Load() != nil {
			t.Fatal("a place reserved behind a released place holds it")
		}
	}},
	{"a call that makes no contention allocates its place and nothing else", func(t *testing.T) {
		var lane Lane
		allocs := testing.AllocsPerRun(100, func() {
			place := lane.Reserve()
			place.Wait()
			place.Release()
		})
		if allocs > 1 {
			t.Fatalf("an uncontended trip through a lane allocated %v objects, want its place only", allocs)
		}
	}},
	{"a nil place is no reservation", func(t *testing.T) {
		var place *Place
		place.Wait()
		place.Release()
		place.Pass()
	}},
}

func TestLaneBehaviors(t *testing.T) {
	for _, tc := range laneBehaviors {
		t.Run(tc.name, tc.run)
	}
}
