// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import "github.com/MichaelKinsy/PiG/durable/core/abi"

// Origins (ABI section 5.4). A host call that reaches the core carries the tag of the async context (an AsyncLocalStorage
// snapshot) it was made in. Pi runs each continuation in the context it was queued in: a microtask in the context of the code that
// called queueMicrotask, a line job in its enqueuer's, an `await` in its function's. The core keeps one current origin, the context
// of the code that is running; the Loop captures it when it queues a microtask or a job and makes it current again when it runs
// them, and the core records it where work waits for the host (a started effect, a hops chain), so the completion that wakes that
// work runs in it. Every effect and notice leaves stamped with the current origin, and the host starts the effect's handler and
// runs the notice's listeners in that context. So the context a store set around `submit` reaches is the one Pi's scheduler runs
// the turn in: the kicker's of an idle scheduler (scheduler.ts #kick queues its drain as a microtask), and an already draining
// scheduler keeps its own.
//
// The host keeps the context of an origin until the core says it is done with it. The core counts what holds an origin: an event
// from its arrival to the end of its step (and while it waits for rows), a queued line job, a started effect until its completion,
// a hops chain until it ends, and the microtasks a parked Loop keeps across steps. An origin whose count reached zero when a step
// ended goes to the host in that step's origin_free notice.

// originRefs counts what holds each origin the host named.
type originRefs struct {
	n map[uint32]uint32
	// maybe holds the origins whose count reached zero since the last flush: a step decides at its end, when nothing that
	// took another hold in the meantime can be wrong.
	maybe []uint32
	// free holds the origins the host is to be told about; a rejected step carries no notice, so they wait for the next step.
	free []uint32
}

// origin is the origin of the code that is running.
func (h *handler) origin() uint32 {
	if h.loop != nil {
		return h.loop.Origin
	}
	return h.bareOrigin
}

func (h *handler) setOrigin(o uint32) {
	if h.loop != nil {
		h.loop.Origin = o
	} else {
		h.bareOrigin = o
	}
}

// holdOrigin counts one more hold on origin o; 0 is no origin.
//
//go:noinline
func (h *handler) holdOrigin(o uint32) {
	if o == 0 {
		return
	}
	if h.refs.n == nil {
		h.refs.n = map[uint32]uint32{}
	}
	h.refs.n[o]++
}

// dropOrigin drops one hold on origin o. The origin is freed at the end of the step if nothing took a hold again.
//
//go:noinline
func (h *handler) dropOrigin(o uint32) {
	if o == 0 {
		return
	}
	if c := h.refs.n[o]; c > 0 {
		h.refs.n[o] = c - 1
		if c == 1 {
			h.refs.maybe = append(h.refs.maybe, o)
		}
	}
}

// HoldOrigin is the hold of an event that waits for rows (Session.dispatch).
func (h *handler) HoldOrigin(o uint32) { h.holdOrigin(o) }

// ReleaseOrigin drops it.
func (h *handler) ReleaseOrigin(o uint32) { h.dropOrigin(o) }

// pin holds the origins of the microtasks a parked Loop keeps across steps, until the next step's pump unpins them.
//
//go:noinline
func (h *handler) pin() {
	h.loop.QueuedOrigins(func(o uint32) {
		h.holdOrigin(o)
		h.pinned = append(h.pinned, o)
	})
}

//go:noinline
func (h *handler) unpin() {
	for _, o := range h.pinned {
		h.dropOrigin(o)
	}
	h.pinned = h.pinned[:0]
}

// FlushOrigins ends a step that carries no reads: the origins nothing holds any more go to the host in an origin_free notice.
// A rejected or fatal step carries none (its builder was reset), so the list waits for the next step.
//
//go:noinline
func (h *handler) FlushOrigins() {
	for _, o := range h.refs.maybe {
		if c, ok := h.refs.n[o]; ok && c == 0 {
			delete(h.refs.n, o)
			h.refs.free = append(h.refs.free, o)
		}
	}
	h.refs.maybe = h.refs.maybe[:0]
	if len(h.refs.free) == 0 || h.b.Step().Status != abi.StatusOK {
		return
	}
	h.setOrigin(0)
	payload := abi.U32Payload(uint32(len(h.refs.free)))
	for _, o := range h.refs.free {
		payload = append(payload, abi.U32Payload(o)...)
	}
	h.refs.free = h.refs.free[:0]
	h.b.Notice(abi.NoticeOriginFree, payload)
}
