// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// The Session's document sweep (session/retention.go) runs as a job on the Session line, the Loop.

// sweepMicrotask runs fn as a microtask of the Loop.
func (h *handler) sweepMicrotask(fn func()) { h.loop.Defer(fn) }

// enqueueSweep queues job on the Session line (session.ts #maintain). A job that returns a *session.NeedPublication (the store of held
// changes, while the part that writes them is in the writer) runs again from the start once it is published.
func (h *handler) enqueueSweep(job func() error) { h.loop.LineMaintain(job) }

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
	taskDocuments, tails := h.sess.TaskDocumentsSize(), h.sess.TailsSize()
	// The documents whose value the host holds and the cache dropped: the host forgets that value (tx.go sentValues), and the core sends the
	// value of the next change.
	var dropped []byte
	for _, doc := range h.sess.TakeDropped() {
		h.sent.remove(doc.ID)
		dropped = strconv.AppendInt(append(dropped, `,{"id":`...), int64(doc.ID), 10)
		dropped = append(jv.AppendString(append(dropped, `,"address":`...), doc.Address), '}')
	}
	if h.cacheSent && h.cacheSizes == [7]int{cached, idle, attached, sweeps, held, taskDocuments, tails} && dropped == nil {
		return
	}
	h.cacheSent, h.cacheSizes = true, [7]int{cached, idle, attached, sweeps, held, taskDocuments, tails}
	body := append([]byte(`{"cached":`), strconv.Itoa(cached)...)
	body = append(append(body, `,"idle":`...), strconv.Itoa(idle)...)
	body = append(append(body, `,"attached":`...), strconv.Itoa(attached)...)
	body = append(append(body, `,"sweeps":`...), strconv.Itoa(sweeps)...)
	body = append(append(body, `,"held":`...), strconv.Itoa(held)...)
	body = append(append(body, `,"taskDocuments":`...), strconv.Itoa(taskDocuments)...)
	body = append(append(body, `,"tails":`...), strconv.Itoa(tails)...)
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
	sizes := [8]int{z.Live, z.Settled, z.Edges, z.Below, z.Roots, z.Unloaded, z.DropQueue, z.Pins}
	if h.indexSent && h.indexSizes == sizes {
		return
	}
	h.indexSent, h.indexSizes = true, sizes
	body := []byte(`{`)
	for i, name := range [8]string{"live", "settled", "edges", "below", "roots", "unloaded", "dropQueue", "pins"} {
		if i > 0 {
			body = append(body, ',')
		}
		body = append(append(append(append(body, '"'), name...), `":`...), strconv.Itoa(sizes[i])...)
	}
	h.b.Notice(abi.NoticeIndexSizes, append(body, '}'))
}
