// Package parking is the hand-off of a method handler's first suspension (Chord v2 host, section "handler authoring").
//
// Pi runs an async method handler synchronously, inside the section that admits its call, up to its first `await`; the
// rest of the admitting section runs after that. A Go handler is a blocking function, so the section lends itself to the
// handler: the handler runs as a coroutine (host.Loop.Lend) that the section's goroutine drives, and meanwhile the loop's
// owner is the handler's goroutine, so the handler's calls into the loop run inline, as Pi's nested synchronous calls do.
// A handler parks when it is about to wait for something that is not the host's section (a promise, a transaction line, a
// timer, a channel it awaits with chordctx.Receive): the host primitives that wait call Park first, and Raise yields the
// coroutine, which gives the section back.
//
// The Signal of a call travels on the handler's ctx under Key (the handler's ctx answers Value(Key)), so a primitive that
// is given the handler's ctx, or a context derived from it, parks without knowing anything of the host.
package parking

import (
	"context"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// Key is the context key a handler's ctx answers with its call's *Signal. It is not a chord.Key: the ctx's String and its
// values are Pi's, and this one is host-internal.
var Key key

type key struct{}

// Signal is the once-only "the handler returned or parked" of one call.
type Signal struct {
	raised   atomic.Bool
	returned atomic.Bool
	done     chan struct{}
	// owner is the loop's owner, which the call's section lent to the handler's goroutine; Raise hands it back to section.
	// owner is nil for a signal that lent nothing (a call admitted outside a section).
	owner   *atomic.Uint64
	section uint64
	// yield gives the section's goroutine back the control of the handler's coroutine, which runs on goroutine gid; nil for
	// a signal that is not bound to one.
	yield func()
	gid   uint64
}

// New returns an unraised Signal that lends nothing.
func New() *Signal { return &Signal{done: make(chan struct{})} }

// Lend makes Raise hand owner back to section, the goroutine of the section that drives the handler. It is called once,
// before s is shared.
func (s *Signal) Lend(owner *atomic.Uint64, section uint64) {
	s.owner, s.section = owner, section
}

// Bind makes Raise yield the handler's coroutine, which runs on goroutine gid, by calling yield. Only a Raise on gid counts:
// the section's goroutine is blocked until the coroutine yields, so a goroutine the handler started cannot give the section
// back, and its Park leaves the handler's synchronous part to the handler's own next one. It is called once, on gid, before
// the handler can share s.
func (s *Signal) Bind(gid uint64, yield func()) {
	s.gid, s.yield = gid, yield
}

// Raise ends the handler's first synchronous part: the loop's owner goes back to the section, the handler's coroutine
// yields (when bound) and the section goes on. Only the first Raise counts; the rest is one atomic load.
func (s *Signal) Raise() { s.raise(false) }

// Return is Raise for a handler that returned: what is left of the handler is its settlement, which the host runs as soon as
// the section is over, on the goroutine that ended it. A handler that parked before it returned raised long ago, and this is
// then one atomic load.
func (s *Signal) Return() { s.raise(true) }

func (s *Signal) raise(returned bool) {
	if s.raised.Load() {
		return
	}
	if s.yield != nil && goroutineid.Current() != s.gid {
		return
	}
	if !s.raised.CompareAndSwap(false, true) {
		return
	}
	s.returned.Store(returned)
	if s.owner != nil {
		s.owner.Store(s.section)
	}
	close(s.done)
	if s.yield != nil {
		s.yield()
	}
}

// Raised reports whether Raise ran.
func (s *Signal) Raised() bool { return s.raised.Load() }

// Returned reports whether the first Raise was Return: the handler finished before it parked.
func (s *Signal) Returned() bool { return s.returned.Load() }

// Wait blocks until Raise.
func (s *Signal) Wait() { <-s.done }

// Park raises the Signal ctx carries, if any: the caller is about to wait for something the host's section cannot give it,
// or to continue after the section that admitted its handler. It must come right before the wait, after the caller's own
// synchronous work, and the next thing the caller does after the wait must not touch the host's state outside a section.
func Park(ctx context.Context) {
	if signal, _ := ctx.Value(Key).(*Signal); signal != nil {
		signal.Raise()
	}
}
