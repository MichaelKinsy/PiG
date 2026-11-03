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
	const queued = 6
	const total = queued + 1
	slow := make(chan durable.Seq, total)
	s.ObserveCommits(commitsFunc(func(_ context.Context, p durable.CommitPublication) {
		once.Do(func() {
			close(entered)
			<-hold
		})
		slow <- p.Seq
	}))
	other := make(chan durable.Seq, total)
	s.ObserveCommits(commitsFunc(func(_ context.Context, p durable.CommitPublication) { other <- p.Seq }))

	writeNotes(t, s, "first")
	<-entered
	// The observer is held inside its first Advance: these commits succeed regardless and queue behind it.
	for i := range queued {
		writeNotes(t, s, string(rune('a'+i)))
	}
	close(hold)

	seen, wire := receiveSeqs(t, slow, total), receiveSeqs(t, other, total)
	if !slices.IsSorted(seen) || len(slices.Compact(slices.Clone(seen))) != total {
		t.Fatalf("a slow observer saw %v, want %d strictly increasing sequence numbers", seen, total)
	}
	if !slices.Equal(seen, wire) {
		t.Fatalf("the observers saw %v and %v, want the same publications in the same order", seen, wire)
	}
}

// receiveSeqs takes n sequence numbers from got, failing when one does not arrive.
func receiveSeqs(t *testing.T, got <-chan durable.Seq, n int) []durable.Seq {
	t.Helper()
	seqs := make([]durable.Seq, 0, n)
	for len(seqs) < n {
		select {
		case seq := <-got:
			seqs = append(seqs, seq)
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d publications arrived: %v", len(seqs), n, seqs)
		}
	}
	return seqs
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
	const queued = 4
	const total = queued + 1
	staying := make(chan durable.Seq, total)
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
	s.ObserveCommits(commitsFunc(func(_ context.Context, p durable.CommitPublication) { staying <- p.Seq }))

	writeNotes(t, s, "first")
	<-entered
	for i := range queued {
		writeNotes(t, s, string(rune('a'+i)))
	}
	close(hold)

	// The delivery goroutine hands each publication to the observers in attachment order, so once the staying observer has them
	// all, the detached one (attached first) has been given everything it is ever going to be given.
	got := receiveSeqs(t, staying, total)
	mu.Lock()
	defer mu.Unlock()
	if len(detaching) != 1 {
		t.Fatalf("the detached observer saw %v, want only the publication it detached in", detaching)
	}
	if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != total || got[0] != detaching[0] {
		t.Fatalf("the staying observer saw %v, want %d publications in order starting at %d", got, total, detaching[0])
	}
}
