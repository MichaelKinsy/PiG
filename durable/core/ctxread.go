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

// shadowReads are the host Storage reads (hook storageRead) issued while the core ran a line job, a host transaction operation or
// an api request: the core answers from its own state at once, and the completion of what ran (the job's publication, line
// release and done, or the operation's answer) waits until the host's Storage has finished every read, so a Storage that
// holds, counts or fails them sees Pi's reads and the line stays held for as long as Pi's would.
type shadowReads struct {
	n       int
	waiters []func()
}

// after runs fn once no read is in flight.
func (s *shadowReads) after(fn func()) { s.waiters = append(s.waiters, fn) }

// gateRead asks the host's Storage to run one Pi Storage read. Methods the host's Storage does not override are not asked.
func (h *handler) gateRead(method string, args ...any) error {
	if h.inTxOp || !h.gated(method) {
		return nil
	}
	h.shadow.n++
	body := jv.NewObject()
	body.Set("name", "storageRead")
	body.Set("handler", float64(0))
	body.Set("conversationId", float64(0))
	body.Set("taskId", float64(0))
	body.Set("payload", jobj("method", method, "args", append([]any(nil), args...)))
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
	if h.sched == nil || h.loop == nil || (rt == nil && !h.gatesContextReads()) {
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
	keptHead, keptTail, kept := h.sched.KeptContext(conv)
	var tail, head session.ID
	var hasBounds bool
	bounds := func() error {
		h.job.readLine = true
		hasBounds, tail, head = false, 0, 0
		// The bounds probe (scanEntries limit 1, or entry for an `at`) and the head marker read (context.ts captureContextBounds).
		if at == 0 {
			h.read(hopcost.ReadScanEntries)
		} else {
			h.read(hopcost.ReadEntryIn)
		}
		h.read(hopcost.ReadHeadMarker)
		var marker *jv.Object
		if at == 0 {
			if err := h.gateRead("scanEntries", jobj("conversationId", float64(conv)), float64(1), nil); err != nil {
				return err
			}
			page, err := h.mir.ScanEntries(session.EntryQuery{ConversationID: conv}, 1, 0)
			if err != nil {
				return err
			}
			if len(page.Items) == 0 {
				return nil
			}
			tail = session.ID(numOf(page.Items[0], "id"))
		} else {
			if err := h.gateRead("entry", float64(conv), float64(at)); err != nil {
				return err
			}
			found, _, err := h.mir.EntryIn(conv, at)
			if err != nil {
				return err
			}
			if found == nil {
				return entryNotVisible("Entry " + itoa(int64(at)) + " is not visible from conversation " + itoa(int64(conv)))
			}
			tail = at
		}
		if err := h.gateRead("findLatestHeadMarker", float64(conv), float64(tail)); err != nil {
			return err
		}
		marker, err := h.mir.LatestHeadMarker(conv, tail)
		if err != nil {
			return err
		}
		if marker != nil {
			head = session.ID(numOf(marker, "id"))
		}
		hasBounds = true
		return nil
	}
	finish := func() {
		if hasBounds {
			h.sched.KeepContext(conv, head, tail, ended())
		}
		h.apiOK(reqID, contextAnswer{int64(conv), int64(head), int64(tail), hasBounds})
	}
	afterBounds := func(err error) {
		if err != nil {
			h.apiError(reqID, err)
			return
		}
		if !hasBounds {
			finish()
			return
		}
		query := jobj("conversationId", float64(conv))
		switch {
		case !kept || keptHead != head:
			if head != 0 {
				h.rangeMin(query, head)
			}
			query.Set("maxEntryId", float64(tail))
		case tail <= keptTail:
			finish()
			return
		default:
			query.Set("minEntryId", float64(keptTail+1))
			query.Set("maxEntryId", float64(tail))
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

// rangeMin sets the lower bound of a range scan to the head marker's head: the entry the marker keeps from.
func (h *handler) rangeMin(query *jv.Object, marker session.ID) {
	if e := h.mir.entry(marker); e != nil {
		query.Set("minEntryId", numOf(e, "head"))
	}
}

// entryNotVisible is the Error context.ts captureContextBounds throws for an entry the conversation cannot see.
type entryNotVisible string

func (e entryNotVisible) Error() string { return string(e) }

// turnContext is runtime.context for the built-in tasks (generation, compaction). Like the task runtime's read it captures
// the bounds on the Session line (a readOnLine job charged the bounds probe and the head marker read) and scans the range
// off the line when the kept range cannot serve it (context.ts readContextFrom). It returns the view and the hops from the end
// of the bounds job until the caller resumes (contextReturnHops).
func (h *handler) turnContext(conv, at turn.ID, prev turn.KeptRange) (*history.View, int, func(bool) error, error) {
	h.chargeBounds(at)
	view, err := h.contextView(int64(conv), int64(at))
	if err != nil {
		return nil, 0, nil, err
	}
	var tail, head session.ID
	if at == 0 {
		page, perr := h.mir.ScanEntries(session.EntryQuery{ConversationID: session.ID(conv)}, 1, 0)
		if perr != nil {
			return view, 0, nil, perr
		}
		if len(page.Items) == 0 {
			// No bounds: readContextFrom returns the empty view at once, through the kept range's return (context.ts :76).
			return view, keptReturnHops, nil, nil
		}
		tail = session.ID(numOf(page.Items[0], "id"))
	} else {
		tail = session.ID(at)
	}
	marker, merr := h.mir.LatestHeadMarker(session.ID(conv), tail)
	if merr != nil {
		return nil, 0, nil, merr
	}
	if marker != nil {
		head = session.ID(numOf(marker, "id"))
	}
	scan, keep := h.keepRange(conv, at, head, tail, prev, view)
	return view, scan, keep, nil
}

// chargeBounds charges the bounds capture of a built-in task's runtime.context as a readOnLine job: the bounds probe
// (scanEntries limit 1, or entry for an `at`) and the head marker read (context.ts captureContextBounds).
func (h *handler) chargeBounds(at turn.ID) {
	h.job.readLine = true
	if at == 0 {
		h.read(hopcost.ReadScanEntries)
	} else {
		h.read(hopcost.ReadEntryIn)
	}
	h.read(hopcost.ReadHeadMarker)
}

// keepRange is the part of a built-in task's runtime.context that follows the bounds job: the hops of the scan the range
// needs, when the range kept as the read was called cannot serve the bounds, and of the return of the read to the caller
// (contextReturnHops). keep stores the range once the read returns, after
// the scan (scheduler.ts:1231-1252).
func (h *handler) keepRange(conv, at turn.ID, head, tail session.ID, prev turn.KeptRange, view *history.View) (int, func(bool) error) {
	scan := contextReturnHops(prev, head, tail, h.readHops(hopcost.ReadScanEntries))
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
		h.sched.KeepContext(session.ID(conv), head, tail, ended)
		return nil
	}
}

// keptRange is the range the scheduler keeps for the conversation as runtime.context is called (turn.Host.KeptRange).
func (h *handler) keptRange(conv turn.ID) turn.KeptRange {
	head, tail, ok := h.sched.KeptContext(session.ID(conv))
	return turn.KeptRange{Head: head, Tail: tail, Ok: ok}
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
	h.chargeBounds(at)
	var tail, head session.ID
	var headFrom int64
	if at == 0 {
		page, err := h.mir.ScanEntries(session.EntryQuery{ConversationID: session.ID(conv)}, 1, 0)
		if err != nil {
			return nil, 0, nil, err
		}
		if len(page.Items) == 0 {
			ctx, _ := h.hostContextOf(int64(conv), 0, 0, 0, false)
			// No bounds: readContextFrom returns the empty view at once, through the kept range's return (context.ts :76).
			return ctx, keptReturnHops, nil, nil
		}
		tail = session.ID(numOf(page.Items[0], "id"))
	} else {
		found, _, err := h.mir.EntryIn(session.ID(conv), session.ID(at))
		if err != nil {
			return nil, 0, nil, err
		}
		if found == nil {
			return nil, 0, nil, &history.NotVisibleError{Entry: int64(at), Conv: int64(conv)}
		}
		tail = session.ID(at)
	}
	marker, err := h.mir.LatestHeadMarker(session.ID(conv), tail)
	if err != nil {
		return nil, 0, nil, err
	}
	if marker != nil {
		head, headFrom = session.ID(numOf(marker, "id")), int64(numOf(marker, "head"))
	}
	compaction, _ := boolOf(objOf(objOf(h.open, "settings"), "compaction"), "enabled")
	ctx, ok := h.hostContextOf(int64(conv), int64(tail), int64(head), headFrom, compaction)
	if !ok {
		return nil, 0, nil, h.mir.lazy.err
	}
	scan, keep := h.keepRange(conv, at, head, tail, prev, nil)
	return ctx, scan, keep, nil
}

// rangeReturnHops is the hops from the Storage scan's resumption to the caller's: scanRange returns, readContextFrom's
// await resumes and returns, the scheduler's context op resumes and returns, `#read` (async, returns a promise still
// pending) resolves one reaction later, and generation's `await runtime.context` resumes (context.ts:90, 243;
// scheduler.ts:1242, 1269; dcore-trace E23 task 25 and 26 chains: scan +44 -> context resume +48).
const rangeReturnHops = 4
