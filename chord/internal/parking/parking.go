// Package parking is the hand-off of a method handler's first suspension (Chord v2 host, section "handler authoring").
//
// Pi runs an async method handler synchronously, inside the section that admits its call, up to its first `await`; the
// rest of the admitting section runs after that. A Go handler is a blocking function on a goroutine of its own, so the
// section lends itself to the handler: the section waits on a Signal until the handler returns or parks, and meanwhile
// the loop's owner is the handler's goroutine (host.Loop.Lend), so the handler's calls into the loop run inline, as Pi's
// nested synchronous calls do. A handler parks when it is about to wait for something that is not the host's section
// (a promise, a transaction line, a timer, a channel it awaits with chordctx.Receive): the host primitives that wait call
// Park first, and Raise gives the section back.
//
// The Signal of a call travels on the handler's ctx under Key (the handler's ctx answers Value(Key)), so a primitive that
// is given the handler's ctx, or a context derived from it, parks without knowing anything of the host.
package parking

import (
	"context"
	"sync/atomic"
)

// Key is the context key a handler's ctx answers with its call's *Signal. It is not a chord.Key: the ctx's String and its
// values are Pi's, and this one is host-internal.
var Key key

type key struct{}

// Signal is the once-only "the handler returned or parked" of one call.
type Signal struct {
	raised atomic.Bool
	done   chan struct{}
	// owner is the loop's owner, which the call's section lent to the handler's goroutine; Raise hands it back to section.
	// owner is nil for a signal that lent nothing (a call admitted outside a section).
	owner   *atomic.Uint64
	section uint64
}

// New returns an unraised Signal that lends nothing.
func New() *Signal { return &Signal{done: make(chan struct{})} }

// Lend makes Raise hand owner back to section, the goroutine of the section that waits on s. It is called once, before s is
// shared.
func (s *Signal) Lend(owner *atomic.Uint64, section uint64) {
	s.owner, s.section = owner, section
}

// Raise ends the handler's first synchronous part: the loop's owner goes back to the waiting section and the section
// wakes. Only the first Raise counts; the rest is one atomic load.
func (s *Signal) Raise() {
	if s.raised.Load() || !s.raised.CompareAndSwap(false, true) {
		return
	}
	if s.owner != nil {
		s.owner.Store(s.section)
	}
	close(s.done)
}

// Raised reports whether Raise ran.
func (s *Signal) Raised() bool { return s.raised.Load() }

// Wait blocks until Raise. Built with the chordstall tag it also reports (and goes on waiting) when that takes longer than
// StallAfter: see stall_on.go.
func (s *Signal) Wait() { s.wait() }

// Park raises the Signal ctx carries, if any: the caller is about to wait for something the host's section cannot give it,
// or to continue after the section that admitted its handler. It must come right before the wait, after the caller's own
// synchronous work, and the next thing the caller does after the wait must not touch the host's state outside a section.
func Park(ctx context.Context) {
	if signal, _ := ctx.Value(Key).(*Signal); signal != nil {
		signal.Raise()
	}
}
