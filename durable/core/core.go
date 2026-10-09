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
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// ABIID is abi_id (ABI section 1).
const ABIID = abi.ID

// SQLTable returns the sql_table export.
func SQLTable() []string { return store.SQL() }

// Session is one ABI handle.
type Session struct {
	b        *abi.Builder
	st       *store.Store
	sess     *session.Session
	deferred []abi.Event // events waiting for rows, in arrival order; payloads copied
	closed   bool
	dead     bool // a fatal step was returned; the host must discard the handle
}

// NewSession returns a handle. uuidv7 is the host's process-level pi-ai uuidv7() generator (ABI section 4.2).
func NewSession(uuidv7 func() string) *Session {
	b := abi.NewBuilder()
	st := store.New(store.Options{})
	return &Session{
		b:  b,
		st: st,
		sess: session.New(session.Config{
			Store: st, Builder: b, History: history.NewStore(), Machines: turn.Machines(), UUIDv7: uuidv7,
		}),
	}
}

// Step processes one event. The returned step is valid until the next Step call.
func (s *Session) Step(ev abi.Event) *abi.Step {
	s.b.Reset()
	if s.dead {
		s.b.Fatal(abi.ErrorJSON("HandleDiscarded", "a fatal step was returned; discard the handle"))
		return s.b.Step()
	}
	defer func() { s.dead = s.b.Step().Status == abi.StatusFatal }()
	if s.closed {
		s.b.Reject(abi.ErrorJSON("SessionClosed", "the handle is closed"))
		return s.b.Step()
	}
	if ev.Kind == abi.EventRows {
		s.rows(&ev)
		return s.b.Step()
	}
	if len(s.deferred) != 0 {
		// ABI section 3 invariant 3: a host answers every read before delivering another event.
		s.b.Fatal(abi.ErrorJSON("ProtocolError", "an event arrived while reads were unanswered"))
		return s.b.Step()
	}
	s.dispatch(&ev)
	if ev.Kind == abi.EventClose && s.b.Step().Status == abi.StatusOK {
		s.closed = true
	}
	return s.b.Step()
}

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
		if s.b.Step().Status != abi.StatusOK {
			return
		}
	}
}

// dispatch hands one event to the session and turns its outcome into the step's status.
func (s *Session) dispatch(ev *abi.Event) {
	err := s.sess.Handle(ev)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrPending):
		s.deferred = append(s.deferred, detach(ev))
	default:
		s.fail(err)
	}
}

func (s *Session) fail(err error) {
	var rej *session.Rejection
	switch {
	case errors.As(err, &rej):
		s.b.Reject(abi.ErrorJSON(rej.Name, rej.Message))
	case errors.Is(err, session.ErrNotImplemented):
		s.b.Reject(abi.ErrorJSON("NotImplemented", err.Error()))
	default:
		s.b.Fatal(abi.ErrorJSON("CoreError", err.Error()))
	}
}

// detach copies the parts of ev that alias the host's input buffer.
func detach(ev *abi.Event) abi.Event {
	out := *ev
	out.Payload = append([]byte(nil), ev.Payload...)
	out.Rows = nil
	return out
}

// MemStats reports the mem_stats export: arena, index, derived, documents, scratch, total live, heap in use, linear
// memory size. The binding fills the last two from its runtime.
func (s *Session) MemStats() [8]uint64 {
	docs := s.st.Footprint()
	return [8]uint64{0, 0, 0, docs, 0, docs, 0, 0}
}
