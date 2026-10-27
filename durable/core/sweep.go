// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
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
	h.sweeps = append(h.sweeps, fn)
}

// enqueueSweep queues job on the Session line. A job that returns a *session.NeedGate (the store of held changes, while the
// host's Storage holds the commit) runs again from the start once the host answered: the Loop reruns a line job, and a
// Session-mode core reruns it on the answer.
func (h *handler) enqueueSweep(job func() error) {
	if h.loop != nil {
		h.loop.LineAfterTail(job, nil)
		return
	}
	h.sweeps = append(h.sweeps, h.sessionJob(job))
}

// sessionJob is job as a Session-mode sweep: it runs again when the host's Storage answered the commit it asked for.
func (h *handler) sessionJob(job func() error) func() {
	var run func()
	run = func() {
		if err := job(); session.IsNeedGate(err) {
			h.cg.resume = run
		}
	}
	return run
}

// runSessionSweeps runs the sweeps a Session-mode core queued, unless a host transaction holds the line.
func (h *handler) runSessionSweeps() {
	for len(h.sweeps) > 0 && h.holder == 0 {
		job := h.sweeps[0]
		h.sweeps = h.sweeps[1:]
		job()
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
	if h.cacheSent && h.cacheSizes == [5]int{cached, idle, attached, sweeps, held} {
		return
	}
	h.cacheSent, h.cacheSizes = true, [5]int{cached, idle, attached, sweeps, held}
	body := append([]byte(`{"cached":`), strconv.Itoa(cached)...)
	body = append(append(body, `,"idle":`...), strconv.Itoa(idle)...)
	body = append(append(body, `,"attached":`...), strconv.Itoa(attached)...)
	body = append(append(body, `,"sweeps":`...), strconv.Itoa(sweeps)...)
	body = append(append(body, `,"held":`...), strconv.Itoa(held)...)
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

// unloadHeld answers unloadDocuments for a Session with held changes: the store of those is a Storage commit, a job on the
// Session line that the host's Storage may hold (a *NeedGate runs it again once the Storage answered).
func (h *handler) unloadHeld(id uint32) {
	run := func() error { return h.sess.UnloadDocuments() }
	done := func(err error) {
		if err != nil {
			h.apiError(id, err)
			return
		}
		h.apiOK(id, nil)
	}
	if h.loop != nil {
		h.loop.Line(run, done)
		return
	}
	var again func()
	again = func() {
		err := run()
		if session.IsNeedGate(err) {
			h.cg.resume = again
			return
		}
		done(err)
	}
	again()
}
