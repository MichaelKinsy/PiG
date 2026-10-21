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
