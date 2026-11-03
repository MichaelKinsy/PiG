package driver_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
)

// session.ts observeCommits: every observer sees every publication in commit order, on the Session's one delivery goroutine and
// never on the owner goroutine, so a slow observer never delays a commit and publications that pile up behind it still arrive in order. The burst test above
// never queues behind a blocked observer; this one holds the first Advance until all the commits are stored.
func TestObserveCommitsQueuedPublicationsArriveInCommitOrderBehindASlowObserver(t *testing.T) {
	s := openCore(t)
	hold := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var seen []durable.Seq
	s.ObserveCommits(commitsFunc(func(_ context.Context, p durable.CommitPublication) {
		once.Do(func() {
			close(entered)
			<-hold
		})
		mu.Lock()
		seen = append(seen, p.Seq)
		mu.Unlock()
	}))
	var wire []durable.Seq
	var wireMu sync.Mutex
	s.ObserveCommits(commitsFunc(func(_ context.Context, p durable.CommitPublication) {
		wireMu.Lock()
		wire = append(wire, p.Seq)
		wireMu.Unlock()
	}))

	writeNotes(t, s, "first")
	<-entered
	// The observer is held inside its first Advance: these commits succeed regardless and queue behind it.
	const queued = 6
	for i := range queued {
		writeNotes(t, s, string(rune('a'+i)))
	}
	close(hold)

	const total = queued + 1
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		got := len(seen)
		mu.Unlock()
		wireMu.Lock()
		other := len(wire)
		wireMu.Unlock()
		if got >= total && other >= total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("publications arrived %d and %d of %d", got, other, total)
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	wireMu.Lock()
	defer wireMu.Unlock()
	if !slices.IsSorted(seen) || len(slices.Compact(slices.Clone(seen))) != total {
		t.Fatalf("a slow observer saw %v, want %d strictly increasing sequence numbers", seen, total)
	}
	if !slices.Equal(seen, wire) {
		t.Fatalf("the observers saw %v and %v, want the same publications in the same order", seen, wire)
	}
}

// Observation.Detach: no Advance follows it, not even for publications already queued for the observer. The observer that
// detaches inside its first Advance gets exactly that one; the other observer still gets all of them.
func TestObserveCommitsDetachInsideAdvanceDropsTheQueuedPublications(t *testing.T) {
	s := openCore(t)
	entered := make(chan struct{})
	hold := make(chan struct{})
	var observation durable.CommitObservation
	var once sync.Once
	var mu sync.Mutex
	var detaching []durable.Seq
	var staying []durable.Seq
	observation = s.ObserveCommits(commitsFunc(func(_ context.Context, p durable.CommitPublication) {
		mu.Lock()
		detaching = append(detaching, p.Seq)
		mu.Unlock()
		once.Do(func() {
			close(entered)
			<-hold
			observation.Detach()
		})
	}))
	s.ObserveCommits(commitsFunc(func(_ context.Context, p durable.CommitPublication) {
		mu.Lock()
		staying = append(staying, p.Seq)
		mu.Unlock()
	}))

	writeNotes(t, s, "first")
	<-entered
	const queued = 4
	for i := range queued {
		writeNotes(t, s, string(rune('a'+i)))
	}
	close(hold)

	const total = queued + 1
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		got := len(staying)
		mu.Unlock()
		if got >= total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the staying observer saw %d of %d publications", got, total)
		}
		time.Sleep(time.Millisecond)
	}
	// Anything the detached observer is still going to get would have been handed out before the staying one finished.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(detaching) != 1 {
		t.Fatalf("the detached observer saw %v, want only the publication it detached in", detaching)
	}
	if len(staying) != total || !slices.IsSorted(staying) || staying[0] != detaching[0] {
		t.Fatalf("the staying observer saw %v, want %d publications in order starting at %d", staying, total, detaching[0])
	}
}
