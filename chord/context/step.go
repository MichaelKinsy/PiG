package chordctx

import (
	"context"
	"sync"
)

// A step is Go's stand-in for one synchronous turn of Pi's single thread: Durable publishes a commit to all its
// observers in one turn, and a followed source's observer (chord follow.ts:3-5) groups what it is told in that turn into
// one batch on the next microtask. A Go publisher runs the observers one after the other on its own goroutine, so it opens a
// step on the context it hands them, and the observers that want the calls of the turn together (one host's followed
// sources) leave them with StepBatch; they are delivered together when the step ends, before the publisher goes on.
type step struct {
	mu     sync.Mutex
	ended  bool
	groups []*stepGroup
}

type stepGroup struct {
	key   any
	items []func()
	flush func(items []func())
}

var stepKey = NewKey[*step]("chord.step")

// WithStep derives a context that carries a new step, and the function that ends it: end hands every group its items, in
// the order they were left, through the flush of the group's first item (a group's flush may start further work, but
// leaves nothing in this step any more). end is idempotent; items left after it are refused. A nested step is its own.
func WithStep(parent context.Context) (context.Context, func()) {
	s := &step{}
	return WithValue(parent, stepKey, s), s.end
}

func (s *step) end() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	groups := s.groups
	s.groups = nil
	s.mu.Unlock()
	for _, group := range groups {
		group.flush(group.items)
	}
}

// StepBatch leaves item in the step ctx carries, under key, and reports whether it did: false when ctx carries no step or
// its step ended, and the caller then runs the item itself. All items under one key reach the flush of the first of them
// together, in order, when the step ends. key must be comparable.
func StepBatch(ctx context.Context, key any, item func(), flush func(items []func())) bool {
	s, ok := Value(ctx, stepKey)
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return false
	}
	for _, group := range s.groups {
		if group.key == key {
			group.items = append(group.items, item)
			return true
		}
	}
	s.groups = append(s.groups, &stepGroup{key: key, items: []func(){item}, flush: flush})
	return true
}
