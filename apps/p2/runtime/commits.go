package runtime

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
)

// The commit filters of apps/p2/src/runtime/commits.ts that CommitSignal (commit_signal.go) takes, and the version counter its
// tasks wait on.

// VersionSignal is a counter tasks wait on: Bump counts and wakes the waiters, Next waits until it moved from the version a
// task saw.
type VersionSignal struct {
	mu      sync.Mutex
	version int
	waiters map[chan struct{}]struct{}
}

// Bump is one more change.
func (s *VersionSignal) Bump() {
	s.mu.Lock()
	s.version++
	waiters := s.waiters
	s.waiters = nil
	s.mu.Unlock()
	for wake := range waiters {
		close(wake)
	}
}

// Version is how many changes there were.
func (s *VersionSignal) Version() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Next waits until the version is not seen (a change since): it fails with ctx's cause when ctx ends first.
func (s *VersionSignal) Next(ctx context.Context, seen int) error {
	s.mu.Lock()
	if err := context.Cause(ctx); err != nil {
		s.mu.Unlock()
		return err
	}
	if s.version != seen {
		s.mu.Unlock()
		return nil
	}
	wake := make(chan struct{})
	if s.waiters == nil {
		s.waiters = map[chan struct{}]struct{}{}
	}
	s.waiters[wake] = struct{}{}
	s.mu.Unlock()
	select {
	case <-wake:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.waiters, wake)
		s.mu.Unlock()
		return context.Cause(ctx)
	}
}

// ChangesDocument is whether publication changed a document of kind (a creation, an update, a retirement or a copy): Pi's
// `change.type === "document" && change.record.kind === kind`.
func ChangesDocument(publication durable.CommitPublication, kind string) bool {
	for _, change := range publication.Changes {
		switch change := change.(type) {
		case durable.DocumentChange:
			if change.Record.Kind == kind {
				return true
			}
		case durable.DocumentRetireChange:
			if change.Record.Kind == kind {
				return true
			}
		case durable.DocumentCopyChange:
			if change.Record.Kind == kind {
				return true
			}
		}
	}
	return false
}
