// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// The Session's document sweep (session/retention.go) runs as a job on the Session line. In Harness mode that line is the Loop. A
// Session-mode core has no Loop: its line is the host's transactions, so a sweep waits for the open transaction and runs at the end
// of the step, as the job Pi queues behind the commit that holds the line.

// sweepMicrotask runs fn as a microtask: on the Loop in Harness mode, at the end of the step otherwise.
func (h *handler) sweepMicrotask(fn func()) {
	if h.loop != nil {
		h.loop.Defer(fn)
		return
	}
	h.microtasks = append(h.microtasks, fn)
}

// enqueueSweep queues job on the Session line. A job that returns a *session.NeedGate (the store of held changes, while the
// host's Storage holds the commit) runs again from the start once the host answered: the Loop reruns a line job, and a
// Session-mode core keeps it first in its queue (runSessionSweeps).
func (h *handler) enqueueSweep(job func() error) {
	if h.loop != nil {
		h.loop.LineAfterTail(job, nil)
		return
	}
	h.sweeps = append(h.sweeps, job)
}

// runSessionSweeps runs the sweeps a Session-mode core queued, unless a host transaction holds the line.
func (h *handler) runSessionSweeps() {
	for (len(h.microtasks) > 0 || len(h.sweeps) > 0) && h.holder == 0 {
		if len(h.microtasks) > 0 {
			fn := h.microtasks[0]
			h.microtasks = h.microtasks[1:]
			fn()
			continue
		}
		if err := h.sweeps[0](); session.IsNeedGate(err) {
			// The host's Storage holds the commit the job made: it runs again, first, once the Storage answered.
			h.cg.retry = retrySweeps
			return
		}
		h.sweeps = h.sweeps[1:]
	}
}

// publishDocCache tells the host the size of the document cache when it changed: the host's documentCacheSizes is a
// synchronous getter (session.ts) and replicates this state. It runs after every event and does nothing in nearly every step:
// out of line, so that Binaryen does not inline it through Handle into Session.dispatch, which would take dispatch from
// under 1 KB to 2.7 KB (functionsize.test.ts keeps every hot function of the TinyGo core under its V8 whole-function-compile
// budget; dispatch's is 2048 bytes).
//
//go:noinline
func (h *handler) publishDocCache() {
	h.publishIndexSizes()
	if h.sess == nil {
		return
	}
	cached, idle := h.sess.DocumentCacheSizes()
	sweeps := h.sess.SweepJobs
	attached := h.sess.DocumentsAttached()
	held := h.sess.HeldDocuments()
	// The documents whose value the host holds and the cache dropped: the host forgets that value (tx.go sentValues), and the core sends the
	// value of the next change.
	var dropped []byte
	for _, doc := range h.sess.TakeDropped() {
		h.sent.remove(doc.ID)
		dropped = strconv.AppendInt(append(dropped, `,{"id":`...), int64(doc.ID), 10)
		dropped = append(jv.AppendString(append(dropped, `,"address":`...), doc.Address), '}')
	}
	if h.cacheSent && h.cacheSizes == [5]int{cached, idle, attached, sweeps, held} && dropped == nil {
		return
	}
	h.cacheSent, h.cacheSizes = true, [5]int{cached, idle, attached, sweeps, held}
	body := append([]byte(`{"cached":`), strconv.Itoa(cached)...)
	body = append(append(body, `,"idle":`...), strconv.Itoa(idle)...)
	body = append(append(body, `,"attached":`...), strconv.Itoa(attached)...)
	body = append(append(body, `,"sweeps":`...), strconv.Itoa(sweeps)...)
	body = append(append(body, `,"held":`...), strconv.Itoa(held)...)
	if dropped != nil {
		body = append(append(append(body, `,"dropped":[`...), dropped[1:]...), ']')
	}
	h.b.Notice(abi.NoticeDocCache, append(body, '}'))
}

// publishIndexSizes tells the host the sizes of the scheduler's ownership indexes when they changed (scheduler.ts indexSizes,
// which the host's synchronous getter replicates). Out of line for the reason publishDocCache is: it runs after every event
// and inlined it adds 1.1 KB to Session.dispatch.
//
//go:noinline
func (h *handler) publishIndexSizes() {
	if h.sched == nil {
		return
	}
	z := h.sched.IndexSizes()
	sizes := [7]int{z.Live, z.Settled, z.Edges, z.Below, z.Roots, z.Unloaded, z.DropQueue}
	if h.indexSent && h.indexSizes == sizes {
		return
	}
	h.indexSent, h.indexSizes = true, sizes
	body := []byte(`{`)
	for i, name := range [7]string{"live", "settled", "edges", "below", "roots", "unloaded", "dropQueue"} {
		if i > 0 {
			body = append(body, ',')
		}
		body = append(append(append(append(body, '"'), name...), `":`...), strconv.Itoa(sizes[i])...)
	}
	h.b.Notice(abi.NoticeIndexSizes, append(body, '}'))
}
