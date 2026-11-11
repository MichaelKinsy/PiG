package host

import "slices"

// Ports packages/chord/src/next/remote/calls.ts. The call window (§3, decision log "Flow control: an ack-clocked
// window"): at most the peer's N calls in flight on a link from this host; further calls wait in one FIFO queue, sent in
// order as replies free slots.

// queuedCall is a call waiting for a slot: its hold (a rebind or release takes it out) and what sends it. A call must be
// comparable (a pointer): the window takes one out by identity.
type queuedCall interface {
	comparable
	holdID() int
	send()
}

// callWindow is the calls of one link's consumer side. limit is the peer's N from its `hello` (0 before it, so every
// call waits). A relay forwards its downstream calls at once, so only this host's own calls are counted here. Pi's
// `Set<C>` is a slice in insertion order: a call is in it once, and a call queued while the window drains is visited by
// the drain, as a JavaScript Set iteration visits it.
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

// opened: the peer's window arrived with its `hello`: waiting calls go out.
func (w *callWindow[C]) opened(limit int) {
	w.limit = limit
	w.drain()
}

// freed: a call in flight ended (its reply, its abort sent, its hold released or gone): its slot is free.
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
