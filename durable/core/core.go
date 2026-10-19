// SPDX-License-Identifier: MIT

// Package core is PiG's synchronous Durable core (docs/plan/durable-core, ADR-0001): one deterministic state machine
// per Session that takes an ABI 1 event and returns a step of commits, reads, notices and effects. The same source
// builds natively, as a Go wasip1 reactor and as a TinyGo wasip1 reactor (-scheduler=none); cmd/corewasm is the Wasm
// binding and sqlhost the native host.
//
// Rules for every package under durable/core except cmd, host, contract and the abi/spec and abi/abitest tooling
// (purity_test.go enforces them): no reflection, no encoding/json, no fmt, no os, time, sync or net, no goroutines,
// no package-level mutable state (a package-level var must be documented as read-only), no I/O.
//
// Layout and ownership (docs/plan/durable-core/LAYOUT.md):
//
//	abi       event, step, value types, codecs, Builder, abi_id         integrator
//	jv        ordered JSON values (ECMAScript property order)          dcore-session
//	delta     Chord document ops: tracker, apply                       dcore-session
//	rec       task, submission, conversation, document records        dcore-store
//	history   transcript arena, index, JSON scan/encode, context,      dcore-store, with dcore-loop
//	          forks, compaction ranges and summaries
//	store     SQL table, Pi rows, cold open, sidecar                   dcore-store
//	session   line, scheduler, tasks, documents, inbox, forks, ...     dcore-session
//	turn      generation and tool tasks                                dcore-loop
//	core      event routing and read deferral (this package)           integrator
//	cmd/corewasm, sqlhost, driver, host/cf, probe, budget (tooling)    dcore-host
//
// Imports only point down this list (abi at the bottom, core at the top).
package core

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// ABIID is abi_id (ABI section 1).
const ABIID = abi.ID

// SQLTable returns the sql_table export.
func SQLTable() []string { return store.SQL() }

// Session is one ABI handle.
type Session struct {
	b        *abi.Builder
	st       *store.Store
	sess     Handler
	deferred []abi.Event // events waiting for rows, in arrival order; payloads copied
	closed   bool
	dead     bool // a fatal step was returned; the host must discard the handle
	// hopEv is the hopped event being handled. Handlers take an event by pointer through an interface, so a local would be copied
	// to the heap on every step. A hopped event costs nothing in the common case, so it uses this one; the handlers of other events
	// capture their event in callbacks that run in later steps (api.go reads ev.ID after a hops chain), so those keep a copy of
	// their own. onHopped reads the effect ID and keeps nothing.
	hopEv abi.Event
}

// NewSession returns a handle. uuidv7 is the host's process-level pi-ai uuidv7() generator (ABI section 4.2).
func NewSession(uuidv7 func() string) *Session {
	b := abi.NewBuilder()
	st := store.New(store.Options{EmptyNew: true})
	return &Session{
		b:    b,
		st:   st,
		sess: newHandler(b, st, uuidv7),
	}
}

// Reader runs one read statement of the SQL table at once and returns its rows (ABI section 4.2, import pig.read). The
// rows' text and blob values must not alias host memory the next call reuses.
type Reader = func(sql uint16, params []abi.Value) ([][]abi.Value, error)

// SetReader gives the handle the host's synchronous read. With a reader, open loads the live state and the core reads
// settled records when a step needs them; without one it loads every record at open.
func (s *Session) SetReader(read Reader) {
	if h, ok := s.sess.(*handler); ok {
		h.reader = read
	}
}

// Step processes one event. The returned step is valid until the next Step call.
func (s *Session) Step(ev abi.Event) *abi.Step {
	s.b.Reset()
	if r, ok := s.sess.(interface{ StepStart() }); ok {
		r.StepStart()
	}
	if s.dead {
		s.b.Fatal(abi.ErrorJSON("HandleDiscarded", "a fatal step was returned; discard the handle"))
		return s.b.Step()
	}
	defer func() {
		s.dead = s.b.Step().Status == abi.StatusFatal
	}()
	if s.closed {
		s.b.Reject(abi.ErrorJSON("SessionClosed", "the handle is closed"))
		return s.b.Step()
	}
	if ev.Kind == abi.EventRows {
		rows := ev // a copy of its own: taking &ev would move the parameter to the heap on every step
		s.rows(&rows)
		s.flushOrigins()
		return s.b.Step()
	}
	if len(s.deferred) != 0 {
		// ABI section 3 invariant 3: a host answers every read before delivering another event.
		s.b.Fatal(abi.ErrorJSON("ProtocolError", "an event arrived while reads were unanswered"))
		return s.b.Step()
	}
	if ev.Kind == abi.EventHopped {
		s.hopEv = ev
		s.dispatch(&s.hopEv)
	} else {
		own := ev
		s.dispatch(&own)
	}
	if s.b.Step().Status == abi.StatusOK {
		// A handler that closes behind its line job reports it when the job ran, in a later step.
		if c, ok := s.sess.(interface{ Closed() bool }); ok {
			s.closed = s.closed || c.Closed()
		} else if ev.Kind == abi.EventClose {
			s.closed = true
		}
	}
	s.flushOrigins()
	return s.b.Step()
}

// origins is the handler's count of what holds each host async context (origin.go); a handler without one has none.
type origins interface {
	HoldOrigin(origin uint32)
	ReleaseOrigin(origin uint32)
	FlushOrigins()
}

// holdOrigin keeps origin o for an event that waits for rows.
//
//go:noinline
func (s *Session) holdOrigin(o uint32) {
	if h, ok := s.sess.(origins); ok {
		h.HoldOrigin(o)
	}
}

//go:noinline
func (s *Session) releaseOrigin(o uint32) {
	if h, ok := s.sess.(origins); ok {
		h.ReleaseOrigin(o)
	}
}

// flushOrigins tells the host which origins nothing holds any more, in a step that asks for no reads.
//
//go:noinline
func (s *Session) flushOrigins() {
	if len(s.deferred) != 0 {
		return
	}
	if h, ok := s.sess.(origins); ok {
		h.FlushOrigins()
	}
}

//go:noinline
func (s *Session) rows(ev *abi.Event) {
	if len(s.deferred) == 0 {
		s.b.Fatal(abi.ErrorJSON("ProtocolError", "rows without pending reads"))
		return
	}
	switch err := s.st.Rows(s.b, ev.Rows); {
	case errors.Is(err, store.ErrPending):
		return
	case err != nil:
		s.fail(err)
		return
	}
	queue := s.deferred
	s.deferred = nil
	for i := range queue {
		if len(s.deferred) != 0 {
			// An earlier re-delivered event needs more rows; keep the rest behind it.
			s.deferred = append(s.deferred, queue[i:]...)
			return
		}
		s.dispatch(&queue[i])
		s.releaseOrigin(queue[i].Origin)
		if s.b.Step().Status != abi.StatusOK {
			for _, rest := range queue[i+1:] {
				s.releaseOrigin(rest.Origin)
			}
			return
		}
	}
}

// dispatch hands one event to the session and turns its outcome into the step's status.
//
//go:noinline
func (s *Session) dispatch(ev *abi.Event) {
	err := s.sess.Handle(ev)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrPending):
		s.park(ev)
	default:
		s.fail(err)
	}
}

// park keeps an event that needs rows until they arrive, and the origin it runs in.
//
//go:noinline
func (s *Session) park(ev *abi.Event) {
	s.holdOrigin(ev.Origin)
	s.deferred = append(s.deferred, detach(ev))
}

func (s *Session) fail(err error) {
	var rej *Rejection
	switch {
	case errors.As(err, &rej):
		s.b.Reject(abi.ErrorJSON(rej.Name, rej.Message))
	case errors.Is(err, ErrNotImplemented):
		s.b.Reject(abi.ErrorJSON("NotImplemented", err.Error()))
	default:
		s.b.Fatal(abi.ErrorJSON("CoreError", err.Error()))
	}
}

// detach copies the parts of ev that alias the host's input buffer.
//
//go:noinline
func detach(ev *abi.Event) abi.Event {
	out := *ev
	out.Payload = append([]byte(nil), ev.Payload...)
	out.Rows = nil
	return out
}

// MemStats reports the mem_stats export: arena, index, derived, documents, scratch, total live, heap in use, linear
// memory size. Total live is the documents plus the record text the mirror holds of what this handle wrote, so it stays
// constant across turns that leave no live work. The binding fills the last two from its runtime.
func (s *Session) MemStats() [8]uint64 {
	docs := s.st.Footprint()
	var arena, index, held uint64
	if h, ok := s.sess.(*handler); ok {
		arena, index = h.historyBytes()
		if h.mir != nil {
			held = h.mir.heldBytes()
		}
	}
	return [8]uint64{arena, index, 0, docs, 0, arena + index + docs + held, 0, 0}
}
