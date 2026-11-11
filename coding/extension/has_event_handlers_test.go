package extension

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
)

func noopHandler(...any) (any, error) { return nil, nil }

// HasEventHandlers answers what len(EventHandlers(event)) > 0 answers, for every way a handler set changes, on the Extension and on the copies a runner holds.
func TestHasEventHandlersMatchesEventHandlers(t *testing.T) {
	events := []string{"a", "b", "c"}
	for seed := range uint64(40) {
		rng := rand.New(rand.NewPCG(seed, 7))
		ext := &Extension{Name: "subject"}
		if seed%2 == 0 {
			ext.InitializeEventHandlers()
		}
		copyOfExt := *ext
		var live []struct {
			event string
			id    int
		}
		next := 1
		for step := range 60 {
			op := rng.IntN(6)
			event := events[rng.IntN(len(events))]
			switch op {
			case 0, 1:
				ext.AddEventHandler(event, next, noopHandler)
				live = append(live, struct {
					event string
					id    int
				}{event, next})
				next++
			case 2:
				ext.AddJSONEventHandler(event, next, noopHandler)
				live = append(live, struct {
					event string
					id    int
				}{event, next})
				next++
			case 3, 4:
				if len(live) > 0 {
					i := rng.IntN(len(live))
					ext.RemoveEventHandler(live[i].event, live[i].id)
					live = append(live[:i], live[i+1:]...)
				}
			case 5:
				source := &Extension{Name: "source"}
				source.InitializeEventHandlers()
				live = live[:0]
				for _, e := range events {
					if rng.IntN(2) == 0 {
						source.AddEventHandler(e, next, noopHandler)
						live = append(live, struct {
							event string
							id    int
						}{e, next})
						next++
					}
				}
				ext.ReplaceEventHandlers(source)
			}
			if ext.handlerState != nil && copyOfExt.handlerState == nil {
				// The runner's copy shares the registry only once it exists.
				copyOfExt = *ext
			}
			for _, e := range events {
				want := len(ext.EventHandlers(e)) > 0
				if got := ext.HasEventHandlers(e); got != want {
					t.Fatalf("seed %d step %d op %d: HasEventHandlers(%q) = %v, EventHandlers has %d", seed, step, op, e, got, len(ext.EventHandlers(e)))
				}
				if ext.handlerState != nil {
					if got := copyOfExt.HasEventHandlers(e); got != want {
						t.Fatalf("seed %d step %d op %d: a copy sharing the registry says HasEventHandlers(%q) = %v, want %v", seed, step, op, e, got, want)
					}
				}
			}
		}
	}
}

// Handlers set directly on an Extension nobody initialized are what EventHandlers dispatches, so they are what HasEventHandlers reports.
func TestHasEventHandlersLegacyHandlers(t *testing.T) {
	ext := Extension{Handlers: map[string][]HandlerFn{"present": {noopHandler}, "empty": {}}}
	if !ext.HasEventHandlers("present") {
		t.Fatal("a legacy handler is not reported")
	}
	for _, event := range []string{"empty", "absent"} {
		if ext.HasEventHandlers(event) {
			t.Fatalf("HasEventHandlers(%q) = true for an event without handlers", event)
		}
	}
	if (&Extension{}).HasEventHandlers("any") {
		t.Fatal("an Extension without handlers reports one")
	}
}

// The answer a reader publishes must never outlive the change that made it stale: after a registration returns, the goroutine that made it sees it, however many readers race the publication.
func TestHasEventHandlersSeesEveryCompletedChange(t *testing.T) {
	ext := &Extension{Name: "racing"}
	ext.InitializeEventHandlers()
	ext.AddEventHandler("stable", 1, noopHandler)
	var stop atomic.Bool
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				_ = ext.HasEventHandlers("toggled")
				_ = ext.HasEventHandlers("stable")
			}
		}()
	}
	for i := range 3000 {
		ext.AddEventHandler("toggled", 100+i, noopHandler)
		if !ext.HasEventHandlers("toggled") {
			t.Fatalf("iteration %d: a handler added before the call is not reported", i)
		}
		ext.RemoveEventHandler("toggled", 100+i)
		if ext.HasEventHandlers("toggled") {
			t.Fatalf("iteration %d: a handler removed before the call is still reported", i)
		}
		if !ext.HasEventHandlers("stable") {
			t.Fatalf("iteration %d: an untouched event lost its handler", i)
		}
	}
	stop.Store(true)
	readers.Wait()
}

// A replaced registry is seen at once by the copies that share it, including a copy that asked before the replacement.
func TestHasEventHandlersFollowsReplacement(t *testing.T) {
	ext := &Extension{Name: "restarting"}
	ext.InitializeEventHandlers()
	ext.AddEventHandler("before", 1, noopHandler)
	runnerCopy := *ext
	if !runnerCopy.HasEventHandlers("before") || runnerCopy.HasEventHandlers("after") {
		t.Fatal("initial registry misreported")
	}
	fresh := &Extension{Name: "restarting"}
	fresh.InitializeEventHandlers()
	fresh.AddEventHandler("after", 2, noopHandler)
	ext.ReplaceEventHandlers(fresh)
	if runnerCopy.HasEventHandlers("before") || !runnerCopy.HasEventHandlers("after") {
		t.Fatal(fmt.Sprint("the copy did not follow the replacement: before=", runnerCopy.HasEventHandlers("before"), " after=", runnerCopy.HasEventHandlers("after")))
	}
}
