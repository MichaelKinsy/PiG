package host

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// 1,000 endings of one identity made in a row (each as end/remove makes one: awaitAfter of the identity's last ending, then
// host.ending) start one after the other, in the order they were made: the head only, then each when its predecessor
// finished. A teardown that did not wait for its predecessor would start all 1,000 at once.
func TestEndingsOfOneIdentityTearDownInTheOrderTheyWereMadeOneAtATime(t *testing.T) {
	const count = 1000
	s := rawLinked(t, func(FacetHost) error { return nil }, rawLinkedOptions{Parent: true})
	h := s.Server.(*chordHost)
	loop := h.loop().(*Loop)
	var (
		mu         sync.Mutex
		started    []int
		running    atomic.Int64
		overlapped atomic.Bool
	)
	gates := make([]chan struct{}, count+1)
	opened := make([]sync.Once, count+1)
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	let := func(i int) { opened[i].Do(func() { close(gates[i]) }) }
	// Also on a failed assertion: the host's close, a cleanup that runs after this function, waits for every ending.
	defer func() {
		for i := range gates {
			let(i)
		}
	}()
	h.enter(func() {
		for i := 1; i <= count; i++ {
			torn := awaitAfter(h, h.endingOf("identity"), func() (struct{}, error) {
				if running.Add(1) != 1 {
					overlapped.Store(true)
				}
				mu.Lock()
				started = append(started, i)
				mu.Unlock()
				// A real wait, as a native's close: the task parks until the test lets it go.
				_, _ = chordctx.Receive(chordctx.Background(), gates[i])
				running.Add(-1)
				return struct{}{}, nil
			})
			h.ending("identity", torn)
		}
	})
	loop.quiesce()
	startedCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(started)
	}
	if got := startedCount(); got != 1 {
		t.Fatalf("endings started with none finished = %d, want 1 (the head)", got)
	}
	for i := 1; i <= count; i++ {
		let(i)
		loop.quiesce()
		if got, want := startedCount(), min(i+1, count); got != want {
			t.Fatalf("endings started once %d finished = %d, want %d", i, got, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for i, got := range started {
		if got != i+1 {
			t.Fatalf("ending %s started in place %d: teardown order is not the order the endings were made", strconv.Itoa(got), i+1)
		}
	}
	if overlapped.Load() {
		t.Fatal("two endings of one identity ran at once")
	}
}
