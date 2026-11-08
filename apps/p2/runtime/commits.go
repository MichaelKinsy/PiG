package runtime

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
)

// CommitSignal ports apps/p2/src/runtime/commits.ts: it wakes a catch-up task after relevant commits. Subscribe (build it)
// before the task's initial scan to avoid missing a race. Advance runs synchronously on the Session line, so it only
// records the commit; a waiter is a goroutine of the task, woken through a channel that each relevant commit closes.
type CommitSignal struct {
	mu          sync.Mutex
	version     int
	wake        chan struct{}
	observation durable.CommitObservation
}

// NewCommitSignal observes the commits of session (a Harness is one) and counts those accepts takes.
func NewCommitSignal(session interface {
	ObserveCommits(durable.CommitObserver) durable.CommitObservation
}, accepts func(durable.CommitPublication) bool) *CommitSignal {
	signal := &CommitSignal{wake: make(chan struct{})}
	signal.observation = session.ObserveCommits(commitSignalObserver{signal: signal, accepts: accepts})
	return signal
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
	close(o.signal.wake)
	o.signal.wake = make(chan struct{})
	o.signal.mu.Unlock()
}

// End: the Session closed or failed; its owner (the worker's Harness) ends with it.
func (commitSignalObserver) End(durable.WatchEnd) {}

// Version is the number of relevant commits seen so far: read it before a scan, then Next(ctx, version) waits for a commit
// after the scan.
func (s *CommitSignal) Version() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Next returns once a relevant commit has been seen after version seen (at once if one has), or with ctx's cause when ctx
// ends first.
func (s *CommitSignal) Next(ctx context.Context, seen int) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	s.mu.Lock()
	if s.version != seen {
		s.mu.Unlock()
		return nil
	}
	wake := s.wake
	s.mu.Unlock()
	select {
	case <-wake:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Close detaches from the Session.
func (s *CommitSignal) Close() { s.observation.Detach() }
