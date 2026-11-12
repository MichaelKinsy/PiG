package host

import (
	"math"
	"slices"
)

// Ports packages/chord/src/remote/calls.ts. The call window (§3, decision log "Flow control: an ack-clocked
// window"; decision "Call windows only for untrusted links"): at most the peer's N calls of one origin in flight on a
// link; further calls wait in one FIFO queue, sent in order as replies free slots. A peer that announced no window
// takes every call at once.

// queuedCall is a call waiting for a slot: its hold (a rebind or release takes it out) and what sends it. A call must be
// comparable (a pointer): the window takes one out by identity.
type queuedCall interface {
	comparable
	holdID() int
	send()
}

// callWindow is the calls of one origin on one link: a link's consumer side for this host's own calls, or a relay's queue
// of one downstream origin's calls towards one upstream link. limit is the peer's N from its `hello`: 0 before it (so
// every call waits), math.MaxInt when it announced none (a trusted link: every call goes at once). Pi's `Set<C>` is a
// slice in insertion order: a call is in it once, and a call queued while the window drains is visited by the drain, as
// a JavaScript Set iteration visits it.
type callWindow[C queuedCall] struct {
	limit    int
	inFlight int
	waiting  []C
}

// submit sends call now when a slot is free and nothing waits, otherwise queues it behind the earlier calls.
func (w *callWindow[C]) submit(call C) {
	if len(w.waiting) == 0 && w.inFlight < w.limit {
		w.inFlight++
		call.send()
		return
	}
	if !slices.Contains(w.waiting, call) {
		w.waiting = append(w.waiting, call)
	}
}

// opened: the peer's window arrived with its `hello` (0: none announced, a window is at least 1): waiting calls go out.
func (w *callWindow[C]) opened(limit int) {
	if limit == 0 {
		limit = math.MaxInt
	}
	w.limit = limit
	w.drain()
}

// freed: a call in flight ended (its reply, its abort sent, its hold released or gone): its slot is free. A caller taking
// waiting calls out at the same time takes them out first (remove, removeHold), so the slot never sends one.
func (w *callWindow[C]) freed() {
	w.inFlight--
	w.drain()
}

// remove takes a waiting call out (its caller cancelled); nothing happens for one that is not waiting.
func (w *callWindow[C]) remove(call C) {
	if at := slices.Index(w.waiting, call); at >= 0 {
		w.waiting = slices.Delete(w.waiting, at, at+1)
	}
}

// removeHold takes every waiting call of hold out and returns them (its hold rebinds or was released).
func (w *callWindow[C]) removeHold(hold int) []C {
	var removed []C
	kept := w.waiting[:0:0]
	for _, call := range w.waiting {
		if call.holdID() == hold {
			removed = append(removed, call)
		} else {
			kept = append(kept, call)
		}
	}
	w.waiting = kept
	return removed
}

// clear takes every waiting call out and returns them (the link closed).
func (w *callWindow[C]) clear() []C {
	removed := w.waiting
	w.waiting = nil
	return removed
}

// drain sends waiting calls in order while slots are free.
func (w *callWindow[C]) drain() {
	for len(w.waiting) > 0 {
		if w.inFlight >= w.limit {
			return
		}
		call := w.waiting[0]
		w.waiting = w.waiting[1:]
		w.inFlight++
		call.send()
	}
}
