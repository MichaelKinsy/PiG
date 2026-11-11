// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// A task runtime's context read (scheduler.ts runtime.context, context.ts readContextFrom). The core derives the view from
// its own transcript store; what it reproduces from Pi is the read pattern against the host's Storage and the range cache
// that decides it: bounds on the Session line (one scanEntries row or one entry, then the newest head marker), then, off
// the line, a scan of the whole range when the conversation has no kept range or its head marker changed, a scan of the
// entries after the kept tail when it grew, and no scan otherwise. A Storage that overrides those reads sees them; the
// kept range and its retention timer exist in every case (session.Scheduler.KeepContext).

// A host Storage read of a line job (hook storageRead) is an await of Pi's callback: the job parks like a *session.NeedRead, and its body
// runs again from the start once the host's Storage answered, the reads answered so far returning their answers at once (gateReads). The
// read of an api request outside a line job, and of a host transaction operation, is answered on the spot from the core's state; the
// request completes once the host's Storage has finished every read it issued (shadowReads), so a Storage that holds, counts or fails them
// sees Pi's reads.
type shadowReads struct {
	n       int
	waiters []func()
}

// after runs fn once no read is in flight.
func (s *shadowReads) after(fn func()) { s.waiters = append(s.waiters, fn) }

// gateReads are the host Storage reads of the running line job: how many its body made in this run, the answers of the reads asked in
// earlier runs, and whether one is in flight.
type gateReads struct {
	n       int
	results []error
	asking  bool
	// finishing: the asking read was made by a host transaction's end, which is finished again once it answered.
	finishing bool
}

// beginGateReads is the start of a run of a line job's body.
func (h *handler) beginGateReads() { h.gating.n = 0 }

// endGateReads is the end of a line job: its next run is another job's.
func (h *handler) endGateReads() { h.gating = gateReads{results: h.gating.results[:0]} }

// needHostRead is the error of a body that waits for a read of the host's Storage.
//
//go:noinline
func needHostRead() error { return &session.NeedRead{} }

// gateRead asks the host's Storage to run one Pi Storage read. Methods the host's Storage does not override are not asked. A line
// job's body gets a *session.NeedRead until the Storage answered, then the Storage's error or nil.
func (h *handler) gateRead(method string, args ...any) error {
	if h.inTxOp || !h.gated(method) {
		return nil
	}
	body := jv.NewObject()
	body.Set("name", "storageRead")
	body.Set("handler", float64(0))
	body.Set("conversationId", float64(0))
	body.Set("taskId", float64(0))
	body.Set("payload", jobj("method", method, "args", append([]any(nil), args...)))
	if h.sess.InJob() || h.finishing {
		g := &h.gating
		n := g.n
		g.n++
		if n < len(g.results) {
			return g.results[n]
		}
		if g.asking {
			return needHostRead()
		}
		g.asking = true
		// A host transaction's end that waits for the read is finished again once it answered; a line job's body runs again.
		g.finishing = h.finishing
		h.startEffect(abi.EffectHook, jv.Append(nil, body), &pending{done: func(outcome uint8, payload []byte) {
			g.asking = false
			g.results = append(g.results, answerErr(outcome, payload))
			if g.finishing {
				g.finishing = false
				h.finishAgain()
				return
			}
			h.resumeWait = true
		}})
		return needHostRead()
	}
	h.shadow.n++
	h.startEffect(abi.EffectHook, jv.Append(nil, body), &pending{done: func(outcome uint8, payload []byte) {
		h.shadow.n--
		if h.shadow.n > 0 {
			return
		}
		waiters := h.shadow.waiters
		h.shadow.waiters = nil
		for _, fn := range waiters {
			fn()
		}
	}})
	return nil
}

// answerAfterReads runs answer now, or once the host's Storage finished the reads the request issued.
func (h *handler) answerAfterReads(answer func()) {
	if h.shadow.n == 0 {
		answer()
		return
	}
	h.shadow.after(answer)
}

// gatesContextReads is whether the host's Storage overrides a Pi read the model context makes: a read of a handle then goes through
// the Storage as a task runtime's does (context.ts readContextFrom reads the bounds and the range through the Session's Storage).
func (h *handler) gatesContextReads() bool {
	return h.gated("scanEntries") || h.gated("entry") || h.gated("findLatestHeadMarker")
}

// runtimeContext serves api op "context" of a task runtime ({taskId, conversationId, at?}). It reports false for a read that
// is not a running invocation's, which the plain api serves.
func (h *handler) runtimeContext(reqID uint32, req *jv.Object) bool {
	rt := h.runtimes[session.ID(numOf(req, "taskId"))]
	if h.sched == nil || (rt == nil && !h.gatesContextReads()) {
		return false
	}
	conv, at := session.ID(numOf(req, "conversationId")), session.ID(numOf(req, "at"))
	// A read of a handle outside any invocation (Conversation.context) has no invocation to end.
	ended := func() bool { return rt != nil && rt.Ended() }
	if rt != nil && rt.Ended() {
		h.apiError(reqID, rt.EndedError())
		return true
	}
	// readContextFrom receives the kept range as an argument when runtime.context is called, before its bounds job.
	prev := h.keptRange(turn.ID(conv))
	var b ctxBounds
	bounds := func() error {
		var err error
		b, err = h.captureBounds(conv, at, prev, true)
		return err
	}
	finish := func() {
		if b.has {
			h.sched.KeepContext(conv, session.ContextRange{Head: b.head, From: b.from, Tail: b.tail, Current: rangeCurrent(at, b.head, b.tail, prev)}, ended())
		}
		h.apiOK(reqID, contextAnswer{int64(conv), int64(b.head), int64(b.tail), b.has})
	}
	afterBounds := func(err error) {
		if err != nil {
			h.apiError(reqID, err)
			return
		}
		if !b.has {
			finish()
			return
		}
		query := jobj("conversationId", float64(conv))
		switch {
		case !prev.Ok || prev.Head != b.head:
			if b.head != 0 {
				query.Set("minEntryId", float64(b.from))
			}
			query.Set("maxEntryId", float64(b.tail))
		case b.tail <= prev.Tail:
			finish()
			return
		default:
			query.Set("minEntryId", float64(prev.Tail+1))
			query.Set("maxEntryId", float64(b.tail))
		}
		if !h.gated("scanEntries") {
			// The range scan runs off the line: scanRange awaits the Storage scan and returns (context.ts:75-90). The api's
			// answer already spends keptReturnHops after the bounds job, which a kept range returns through alone.
			h.scanThen(h.readHops(hopcost.ReadScanEntries)+rangeReturnHops-keptReturnHops, finish)
			return
		}
		h.hookEffect("storageScanRange", 0, 0, 0, jobj("query", query), nil, func(_ any, err error) {
			if err != nil {
				h.apiError(reqID, err)
				return
			}
			finish()
		})
	}
	// The bounds are captured on the Session line (context.ts readContextFrom: session.readOnLine(captureContextBounds)).
	h.loop.ReadLine(bounds, afterBounds)
	return true
}

// ctxBounds are the bounds of a context read: the head marker entry (0: none), the entry the context starts from, and the newest entry
// it reads (context.ts ContextBounds); has is false for a conversation without entries.
type ctxBounds struct {
	head, from, tail session.ID
	has              bool
}

// captureBounds captures the bounds of the context of a conversation, or of the context cut off at entry at, as a readOnLine job
// (context.ts captureContextBounds). The newest entry and head marker come from what the Session knows of its commits, and cost no
// Storage read; an entry of the range kept as the read was called is visible without one. gate asks the host's Storage for the reads
// the core makes. A cut off at an entry the conversation cannot see is a history.NotVisibleError.
func (h *handler) captureBounds(conv, at session.ID, prev turn.KeptRange, gate bool) (ctxBounds, error) {
	var b ctxBounds
	if at == 0 {
		if known, ok := h.sess.LookupLatestEntryID(conv); ok {
			h.job.change += hopcost.KnownTail
			b.tail = known
		} else {
			h.read(hopcost.ReadScanEntries)
			h.job.change += h.sess.ColdEntryAwaits()
			if gate {
				if err := h.gateRead("scanEntries", jobj("conversationId", float64(conv)), float64(1), nil); err != nil {
					return b, err
				}
			}
			page, err := h.mir.ScanEntries(session.EntryQuery{ConversationID: conv}, 1, 0)
			if err != nil {
				return b, err
			}
			if len(page.Items) != 0 {
				b.tail = session.ID(numOf(page.Items[0], "id"))
			}
			h.sess.NoteLatestEntryID(conv, b.tail)
		}
		if b.tail == 0 {
			return b, nil
		}
	} else {
		visible := false
		if prev.Ok && at >= prev.From && at <= prev.Tail {
			found, _, err := h.mir.EntryIn(conv, at)
			if err != nil {
				return b, err
			}
			visible = found != nil
		}
		if !visible {
			h.read(hopcost.ReadEntryIn)
			if gate {
				if err := h.gateRead("entry", float64(conv), float64(at)); err != nil {
					return b, err
				}
			}
			found, _, err := h.mir.EntryIn(conv, at)
			if err != nil {
				return b, err
			}
			if found == nil {
				return b, &history.NotVisibleError{Entry: int64(at), Conv: int64(conv)}
			}
		}
		b.tail = at
	}
	marker, known := h.sess.LookupHeadMarker(conv, b.tail)
	if known {
		h.job.change += hopcost.KnownTail
	} else {
		h.read(hopcost.ReadHeadMarker)
		h.job.change += h.sess.ColdMarkerAwaits()
		if gate {
			if err := h.gateRead("findLatestHeadMarker", float64(conv), float64(b.tail)); err != nil {
				return b, err
			}
		}
		var err error
		if marker, err = h.mir.LatestHeadMarker(conv, b.tail); err != nil {
			return b, err
		}
		h.sess.NoteHeadMarker(conv, b.tail, marker)
	}
	if marker != nil {
		b.head, b.from = session.ID(numOf(marker, "id")), session.ID(numOf(marker, "head"))
	}
	b.has = true
	return b, nil
}

// rangeCurrent is whether the range a read returned is kept current (context.ts readContextFrom): a read at the newest entry is; one
// cut off at an entry is only the range it started from, returned unchanged.
func rangeCurrent(at, head, tail session.ID, prev turn.KeptRange) bool {
	return at == 0 || (prev.Ok && prev.Head == head && prev.Tail == tail && prev.Current)
}

// turnContext is runtime.context for the built-in tasks (generation, compaction). Like the task runtime's read it captures
// the bounds on the Session line (a readOnLine job charged the bounds probe and the head marker read) and scans the range
// off the line when the kept range cannot serve it (context.ts readContextFrom). It returns the view and the hops from the end
// of the bounds job until the caller resumes (contextReturnHops).
func (h *handler) turnContext(conv, at turn.ID, prev turn.KeptRange) (*history.View, int, func(bool) error, error) {
	b, err := h.captureBounds(session.ID(conv), session.ID(at), prev, false)
	if err != nil {
		return nil, 0, nil, err
	}
	view, err := h.contextView(int64(conv), int64(at))
	if err != nil {
		return nil, 0, nil, err
	}
	if !b.has {
		// No bounds: readContextFrom returns the empty view at once, through the kept range's return (context.ts :76).
		return view, keptReturnHops, nil, nil
	}
	scan, keep := h.keepRange(conv, at, b, prev, view)
	return view, scan, keep, nil
}

// keepRange is the part of a built-in task's runtime.context that follows the bounds job: the hops of the scan the range
// needs, when the range kept as the read was called cannot serve the bounds, and of the return of the read to the caller
// (contextReturnHops). keep stores the range once the read returns, after
// the scan (scheduler.ts:1231-1252).
func (h *handler) keepRange(conv, at turn.ID, b ctxBounds, prev turn.KeptRange, view *history.View) (int, func(bool) error) {
	scan := contextReturnHops(prev, b.head, b.tail, h.readHops(hopcost.ReadScanEntries))
	return scan, func(ended bool) error {
		// The scan hops let the scheduler drop this conversation's kept range (Release) or the store reset a chain the view
		// reads, as the retention timer or another run's commit fires; Pi's read holds its own arrays, so derive them again.
		if view != nil && !view.Valid() {
			fresh, err := h.contextView(int64(conv), int64(at))
			if err != nil {
				return err
			}
			*view = *fresh
		}
		h.sched.KeepContext(session.ID(conv), session.ContextRange{Head: b.head, From: b.from, Tail: b.tail, Current: rangeCurrent(session.ID(at), b.head, b.tail, prev)}, ended)
		return nil
	}
}

// keptRange is the range the scheduler keeps for the conversation as runtime.context is called (turn.Host.KeptRange).
func (h *handler) keptRange(conv turn.ID) turn.KeptRange {
	r, ok := h.sched.KeptContext(session.ID(conv))
	return turn.KeptRange{Head: r.Head, From: r.From, Tail: r.Tail, Current: r.Current, Ok: ok}
}

// contextReturnHops is the hops from the end of runtime.context's bounds job until its caller resumes, given the range the
// scheduler keeps (context.ts readContextFrom :75-92). A range the kept one cannot serve (none kept, another head marker, a tail
// past the kept tail) is scanned off the line, scan hops, and returns through rangeReturnHops; a kept range is reused or cut
// without an await and returns through keptReturnHops.
func contextReturnHops(prev turn.KeptRange, head, tail session.ID, scan int) int {
	if !prev.Ok || prev.Head != head || tail > prev.Tail {
		return scan + rangeReturnHops
	}
	return keptReturnHops
}

// keptReturnHops is the hops from the bounds job's end to the caller's resumption when readContextFrom needs no scan: it returns
// as its bounds await resumes, the scheduler's context op resumes and returns, `#read` resolves one reaction later and the
// caller's await resumes (context.ts :75-102; scheduler.ts :1231-1256, :1269). The scan path's extra hop is scanRange's own
// return. Measured on the reference: rcf return at the bounds job's end, the caller three hops later.
const keptReturnHops = rangeReturnHops - 1

// requestContext is runtime.context for a model request (turn.Host.RequestContext). With a host that answers the core's reads
// the context is the host's: the bounds are captured here exactly as turnContext captures them, and the host derives the
// context from the stored entries (hostctx.go). Without one the core derives the view itself.
func (h *handler) requestContext(conv, at turn.ID, prev turn.KeptRange) (turn.RequestContext, int, func(bool) error, error) {
	if h.reader == nil {
		view, scan, keep, err := h.turnContext(conv, at, prev)
		if err != nil {
			return nil, 0, nil, err
		}
		return turn.ViewContext{View: view}, scan, keep, nil
	}
	b, err := h.captureBounds(session.ID(conv), session.ID(at), prev, false)
	if err != nil {
		return nil, 0, nil, err
	}
	if !b.has {
		ctx, _ := h.hostContextOf(int64(conv), 0, 0, 0, false)
		// No bounds: readContextFrom returns the empty view at once, through the kept range's return (context.ts :76).
		return ctx, keptReturnHops, nil, nil
	}
	compaction, _ := boolOf(objOf(objOf(h.open, "settings"), "compaction"), "enabled")
	ctx, ok := h.hostContextOf(int64(conv), int64(b.tail), int64(b.head), int64(b.from), compaction)
	if !ok {
		return nil, 0, nil, h.mir.lazy.err
	}
	scan, keep := h.keepRange(conv, at, b, prev, nil)
	return ctx, scan, keep, nil
}

// rangeReturnHops is the hops from the Storage scan's resumption to the caller's: scanRange returns, readContextFrom's
// await resumes and returns, the scheduler's context op resumes and returns, `#read` (async, returns a promise still
// pending) resolves one reaction later, and generation's `await runtime.context` resumes (context.ts:90, 243;
// scheduler.ts:1242, 1269; dcore-trace E23 task 25 and 26 chains: scan +44 -> context resume +48).
const rangeReturnHops = 4
