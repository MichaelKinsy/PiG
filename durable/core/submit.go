// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"cmp"
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// submissionDraft reads the draft of a submit event or of the transaction operation `submit`.
func submissionDraft(req *jv.Object) inbox.Draft {
	draft := inbox.Draft{Type: strOf(req, "type"), WhenBusy: strOf(req, "whenBusy"), WhenIdle: strOf(req, "whenIdle")}
	if rid, has := req.Get("requestId"); has {
		if s, isString := rid.(string); isString {
			draft.RequestID = &s
		}
	}
	if content, has := req.Get("content"); has {
		draft.Content = content
	}
	draft.Entry = objOf(req, "entry")
	// The submission's span (submissions.ts submit): its carrier is stored on the record, and the commit's events go to it.
	draft.Span = uint32(numOf(req, "span"))
	if trace, has := req.Get("trace"); has {
		draft.Trace = trace
	}
	draft.Propagation, _ = boolOf(req, "propagates")
	return draft
}

// queueModes are the steering and follow-up modes of the Harness settings, which the host sends at open over their
// defaults (resolveSettings).
func (h *handler) queueModes() inbox.QueueModes {
	modes := inbox.QueueModes{Steering: inbox.ModeOne, FollowUp: inbox.ModeOne}
	if settings := objOf(h.open, "settings"); settings != nil {
		if v := strOf(settings, "steeringMode"); v != "" {
			modes.Steering = v
		}
		if v := strOf(settings, "followUpMode"); v != "" {
			modes.FollowUp = v
		}
	}
	return modes
}

// onSubmit admits a submission (ABI section 5, submit): {id, conversationId, type, content (with the entry {kind, data?} the input is placed as) | entry, requestId, whenBusy?}.
// The answer is an api_result notice for id with {id: submissionId}, after the commit's published notice.
//
//go:noinline
func (h *handler) onSubmit(ev *abi.Event) error {
	v, err := jv.Parse(ev.Payload)
	if err != nil {
		return err
	}
	req, ok := v.(*jv.Object)
	if !ok {
		return reject("TypeError", "submit: the payload is not an object")
	}
	if h.sched == nil {
		return reject("Error", "submit needs a Harness; this handle is a Session")
	}
	// Submissions.submit queues through Session.commitWith (submissions.ts :54-60), whose #enqueue starts the job a tick after the call on
	// an idle line (session.ts :530-531).
	id := uint32(numOf(req, "id"))
	j := &submitJob{h: h, id: id, draft: submissionDraft(req), conversationID: session.ID(numOf(req, "conversationId"))}
	j.bound = h.bound(req, id)
	h.loop.LineRunnerAfterTail(j)
	return nil
}

// submitJob is the line job of one submit: its body, its answer and the state between them in one allocation.
type submitJob struct {
	h              *handler
	id             uint32
	draft          inbox.Draft
	conversationID session.ID
	bound          *session.Cancel
	admitted       inbox.Admission
}

// RunJob admits the submission.
func (j *submitJob) RunJob() error {
	h := j.h
	// The check is the job's start (session.ts #runCommit): the body runs again when it waited for a publication.
	if !h.loop.Restarted() {
		if err := j.bound.Err(); err != nil {
			return err
		}
	}
	_, err := h.sess.RunCommit(j.change, session.TxScope{Span: j.draft.Span, Trace: j.draft.Trace})
	return err
}

func (j *submitJob) change(tx *session.Tx) error {
	h := j.h
	return inbox.AdmitStatus(tx, turn.Runs{}, j.conversationID, j.draft, h.now, h.queueModes(), &j.admitted)
}

// DoneJob answers the submit once its commit's outcome is delivered.
func (j *submitJob) DoneJob(err error) {
	h, id, draft, admitted := j.h, j.id, &j.draft, &j.admitted
	if err != nil {
		h.apiError(id, err)
		return
	}
	answer := jv.NewObject()
	answer.Set("id", float64(admitted.ID))
	if draft.Span != 0 {
		// The span's result (`submission.id`, `result`) and the carrier of the run it links to.
		// Constants: a status the core wrote is one of three.
		switch admitted.Status {
		case "queued":
			answer.Set("status", "queued")
		case "placed":
			answer.Set("status", "placed")
		default:
			answer.Set("status", "done")
		}
		if admitted.Link != nil {
			answer.Set("link", admitted.Link)
		}
	}
	// Submissions.submit returns the span of "pi.durable.submit" (submissions.ts, Pi 2ec55d80): the no-op span's startSpan is an async
	// function that returns the callback's promise, and the async submit returns that one, two adoptions on top of the commit's return.
	h.holdLate(submitSpanHops, deferAnswer, id, answer)
}

// lateAnswer is an answer to an api request that waits for its microtask (deferAnswer).
type lateAnswer struct {
	id  uint32
	val any
}

// deferAnswer is the work parked on a hops chain (holdLate) that answers the api request id with val as a microtask: hopsThen for an
// answer, without a closure. The handler is the Resumer of every such microtask, and they run in the order they were queued.
func deferAnswer(h *handler, id uint32, val any) {
	h.lateAnswers = append(h.lateAnswers, lateAnswer{id, val})
	h.loop.DeferResumer(h)
}

// Resume answers the oldest request deferAnswer queued.
func (h *handler) Resume() {
	a := h.lateAnswers[0]
	n := copy(h.lateAnswers, h.lateAnswers[1:])
	h.lateAnswers[n] = lateAnswer{}
	h.lateAnswers = h.lateAnswers[:n]
	h.apiOK(a.id, a.val)
}

// resumeHop is the work parked on a hops chain (holdLate) that resumes the Resumer val where the chain ended (Loop.Hops).
func resumeHop(_ *handler, _ uint32, val any) { val.(session.Resumer).Resume() }

// submitSpanHops is the ticks the "pi.durable.submit" span adds to the return of Submissions.submit (Pi 6c384aa3, F-ticks-gate: cold submit 105
// against 103).
const submitSpanHops = 2

// onAbort serves the abort event (ABI section 5, kind 15): {requestId, submissionId, conversationId?} withdraws a queued
// submission, {requestId, taskId} aborts a task, {requestId, conversationId, background?} aborts a conversation. The answer
// is an api_result notice for requestId (a number the host allocates), which may follow later steps.
//
//go:noinline
func (h *handler) onAbort(ev *abi.Event) error {
	v, err := jv.Parse(ev.Payload)
	if err != nil {
		return err
	}
	req, ok := v.(*jv.Object)
	if !ok {
		return reject("TypeError", "abort: the payload is not an object")
	}
	if h.sched == nil {
		return reject("Error", "abort needs a Harness; this handle is a Session")
	}
	id := uint32(numOf(req, "requestId"))
	switch {
	case req.Has("taskId"):
		// abortTask: mark the task and wait for its abort invocation; the answer is "marked" or "terminal".
		keepRestart, _ := boolOf(req, "keepRestart")
		h.sched.Abort(session.ID(numOf(req, "taskId")), keepRestart, h.bound(req, id), func(result string, err error) {
			if err != nil {
				h.apiError(id, err)
				return
			}
			h.apiOK(id, result)
		})
		return nil
	case !req.Has("submissionId"):
		// Conversation.abort(): withdraw queued inputs, mark the scope's tasks and wait until the conversation is idle.
		background, _ := boolOf(req, "background")
		h.sched.AbortConversation(session.ID(numOf(req, "conversationId")), background, h.bound(req, id), func(err error) {
			if err != nil {
				h.apiError(id, err)
				return
			}
			h.apiOK(id, nil)
		})
		return nil
	}
	// Submissions.abort returns `Session.commitWith` (submissions.ts :95-111), queued through #enqueue (session.ts :530-531).
	h.loop.LineRunnerAfterTail(&abortJob{h: h, id: id, bound: h.bound(req, id), submission: session.ID(numOf(req, "submissionId")), conversationID: session.ID(numOf(req, "conversationId"))})
	return nil
}

// abortJob is the line job of one Submissions.abort: its body, its answer and the state between them in one allocation.
type abortJob struct {
	h              *handler
	id             uint32
	bound          *session.Cancel
	submission     session.ID
	conversationID session.ID
	result         inbox.AbortResult
}

// RunJob withdraws the submission.
func (j *abortJob) RunJob() error {
	if !j.h.loop.Restarted() {
		if err := j.bound.Err(); err != nil {
			return err
		}
	}
	_, err := j.h.sess.RunCommit(j.change, session.TxScope{})
	return err
}

func (j *abortJob) change(tx *session.Tx) error {
	var err error
	j.result, err = inbox.Abort(tx, j.submission, j.conversationID)
	return err
}

// DoneJob answers the abort once its commit's outcome is delivered.
func (j *abortJob) DoneJob(err error) {
	if err != nil {
		j.h.apiError(j.id, err)
		return
	}
	j.h.apiOK(j.id, string(j.result))
}

// bound is the signal of a submit or abort: the operation's line job checks it at start (session.ts #runCommit throwIfAborted). An
// operation that came through an invocation-bound handle names the calling invocation's task as invocation (PROTOCOL.md,
// invocation-bound operations); the signal is the invocation's. A caller's own context that can abort is `cancellable`: its signal
// is the request's (`cancel {request}` aborts it, until the answer), which the host sends when the context aborts. Other
// operations carry none.
func (h *handler) bound(req *jv.Object, id uint32) *session.Cancel {
	if h.sched == nil {
		return nil
	}
	if req.Has("invocation") {
		return h.sched.Bound(session.ID(numOf(req, "invocation")))
	}
	if cancellable, _ := boolOf(req, "cancellable"); !cancellable {
		return nil
	}
	if h.cancels == nil {
		h.cancels = map[uint32]*session.Cancel{}
	}
	c := &session.Cancel{}
	h.cancels[id] = c
	return c
}

// onCancel serves api op "cancel" {request}: the context of the submit or abort `request` aborted. A request whose line job has
// started ignores it, as Pi's does; the others reject at start, or while waiting for the run they marked.
func (h *handler) onCancel(req *jv.Object) {
	if c := h.cancels[uint32(numOf(req, "request"))]; c != nil {
		c.AbortWith(reject("AbortError", "This operation was aborted"))
	}
}

// inspectDiagnostic answers a diagnostic the host's synchronous test getters read, in the step that carries the query (session.ts
// lineSizes and knownLatestEntryId, scheduler.ts idleState): the api_result notice of requestId.
//
//go:noinline
func (h *handler) inspectDiagnostic(query string, req *jv.Object, id uint32) error {
	out := jv.NewObject()
	switch query {
	case "lineSizes":
		out.Set("outstanding", float64(h.loop.Outstanding()))
		out.Set("unpublished", float64(h.sess.Writer().Unpublished()))
	case "knownLatestEntryId":
		// session.ts knownLatestEntryId: the newest entry ID as the Session's commits know it right now; null for a conversation without
		// entries, known false when it takes a read.
		latest, known := h.sess.KnownLatestEntryID(session.ID(numOf(req, "conversationId")))
		out.Set("known", known)
		if latest != 0 {
			out.Set("id", float64(latest))
		} else {
			out.Set("id", nil)
		}
	default:
		if h.sched == nil {
			return &Rejection{"Error", "inspect: only a Harness answers query idleState"}
		}
		contexts, idle, mismatches, full := h.sched.IdleState()
		out.Set("contexts", float64(contexts))
		out.Set("idleContexts", float64(idle))
		out.Set("idleMismatches", float64(mismatches))
		out.Set("fullIdleChecks", float64(full))
	}
	h.apiOK(id, out)
	return nil
}

// onInspect answers Harness.inspect (harness.ts inspect, a readOnLine job): {query: "harness", requestId}. The answer is
// an api_result notice for requestId with {scheduling, tasks: [{record, state}], submissions}: the live tasks with the
// state the scheduler derives, and the queued then placed submissions in ID order.
//
//go:noinline
func (h *handler) onInspect(ev *abi.Event) error {
	v, err := jv.Parse(ev.Payload)
	if err != nil {
		return err
	}
	req, _ := v.(*jv.Object)
	if query := strOf(req, "query"); query == "lineSizes" || query == "idleState" || query == "knownLatestEntryId" {
		return h.inspectDiagnostic(query, req, uint32(numOf(req, "requestId")))
	}
	if strOf(req, "query") != "harness" || h.sched == nil {
		return &Rejection{"Error", "inspect: only a Harness answers query harness"}
	}
	id := uint32(numOf(req, "requestId"))
	var out *jv.Object
	h.loop.ReadLineLead(func() error {
		// harness.ts inspect :249-258, a readOnLine job: `await this.#tasks.inspect(snapshot)` (scheduler.ts :2124-2132: the await of
		// #published and of loadScopes(false), which Scheduler.Inspect charges, whose chain reads are charged by the Store adapter, then the
		// return) and two scanAll scans of one page, each a single-statement Storage read awaited by scanAll and again by the job (ReadRow + 1).
		h.job.change += 2 + 2*(h.readHops(hopcost.ReadRow)+1)
		scheduling, live, err := h.sched.Inspect(h.snap)
		if err != nil {
			return err
		}
		tasks := make([]any, len(live))
		for i, t := range live {
			tasks[i] = inspectedTask(t)
		}
		var submissions []any
		for _, status := range [...]string{"queued", "placed"} {
			page, err := h.mir.ScanSubmissions(session.SubmissionQuery{Status: status}, 1<<30, 0)
			if err != nil {
				return err
			}
			for _, s := range page.Items {
				submissions = append(submissions, s)
			}
		}
		slices.SortStableFunc(submissions, func(a, b any) int { return cmp.Compare(numOf(a.(*jv.Object), "id"), numOf(b.(*jv.Object), "id")) })
		out = jv.NewObject()
		out.Set("scheduling", scheduling)
		out.Set("tasks", tasks)
		out.Set("submissions", submissions)
		return nil
	}, func(err error) {
		if err != nil {
			h.apiError(id, err)
			return
		}
		h.apiOK(id, out)
	}, hostReadLead)
	return nil
}

// inspectedTask is a TaskInspection on the wire: {record, state: {kind, ...}}; an error crosses as {name, message}.
func inspectedTask(t session.TaskInspection) *jv.Object {
	state := jv.NewObject()
	state.Set("kind", t.Kind)
	switch t.Kind {
	case "pending":
		state.Set("migrates", t.Migrates)
	case "sleeping":
		state.Set("until", t.Until)
	case "waiting":
		on := make([]any, len(t.On))
		for i, id := range t.On {
			on[i] = float64(id)
		}
		state.Set("on", on)
	case "blocked":
		state.Set("reason", t.Reason)
		if t.Err != nil {
			e := jv.NewObject()
			e.Set("name", "Error")
			e.Set("message", t.Err.Error())
			state.Set("error", e)
		}
	}
	o := jv.NewObject()
	o.Set("record", t.Record)
	o.Set("state", state)
	return o
}
