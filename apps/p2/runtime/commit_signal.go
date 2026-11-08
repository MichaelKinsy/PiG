package runtime

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/src/runtime/commits.ts: a signal that wakes a catch-up task after the commits it accepts. Subscribe
// (NewCommitSignal) before the task's initial scan to avoid missing a race.
type CommitSignal struct {
	mu          sync.Mutex
	version     int
	waiters     map[chan struct{}]struct{}
	observation durable.CommitObservation
}

type commitSignalObserver struct {
	signal  *CommitSignal
	accepts func(durable.CommitPublication) bool
}

func (o commitSignalObserver) Advance(_ context.Context, publication durable.CommitPublication) {
	if !o.accepts(publication) {
		return
	}
	o.signal.mu.Lock()
	o.signal.version++
	for wake := range o.signal.waiters {
		close(wake)
		delete(o.signal.waiters, wake)
	}
	o.signal.mu.Unlock()
}

// End is the Session's close or failure: the signal's owner (the worker's Harness) ends with it.
func (commitSignalObserver) End(chord.WatchEnd) {}

// NewCommitSignal observes session's commits; accepts picks the ones that count.
func NewCommitSignal(session durable.Session, accepts func(durable.CommitPublication) bool) *CommitSignal {
	signal := &CommitSignal{waiters: map[chan struct{}]struct{}{}}
	signal.observation = session.ObserveCommits(commitSignalObserver{signal: signal, accepts: accepts})
	return signal
}

// Version counts the commits accepted so far.
func (s *CommitSignal) Version() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Next returns once an accepted commit happened after the version seen, or with ctx's error when it ends first.
func (s *CommitSignal) Next(ctx context.Context, seen int) error {
	s.mu.Lock()
	if s.version != seen {
		s.mu.Unlock()
		return nil
	}
	wake := make(chan struct{})
	s.waiters[wake] = struct{}{}
	s.mu.Unlock()
	select {
	case <-wake:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.waiters, wake)
		s.mu.Unlock()
		return ctx.Err()
	}
}

// Close detaches the observer.
func (s *CommitSignal) Close() { s.observation.Detach() }
