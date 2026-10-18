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
		return &Rejection{"TypeError", "submit: the payload is not an object"}
	}
	if h.loop == nil {
		return &Rejection{"Error", "submit needs a Harness; this handle is a Session"}
	}
	id := uint32(numOf(req, "id"))
	draft := inbox.Draft{Type: strOf(req, "type"), WhenBusy: strOf(req, "whenBusy")}
	if rid, has := req.Get("requestId"); has {
		if s, isString := rid.(string); isString {
			draft.RequestID = &s
		}
	}
	if content, has := req.Get("content"); has {
		draft.Content = content
	}
	draft.Entry = objOf(req, "entry")
	conversationID := session.ID(numOf(req, "conversationId"))
	bound := h.bound(req)
	var submission session.ID
	// Submissions.submit queues through Session.commitWith (submissions.ts :54-60), whose #enqueue starts the job a tick after the call on
	// an idle line (session.ts :530-531).
	h.loop.LineAfterTail(func() error {
		if err := bound.Err(); err != nil {
			return err
		}
		_, err := h.sess.RunCommit(func(tx *session.Tx) error {
			var e error
			submission, e = inbox.Admit(tx, turn.Runs{}, conversationID, draft, h.now, h.queueModes())
			return e
		}, session.TxScope{})
		return err
	}, func(err error) {
		if err != nil {
			h.apiError(id, err)
			return
		}
		answer := jv.NewObject()
		answer.Set("id", float64(submission))
		h.apiOK(id, answer)
	})
	return nil
}

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
		return &Rejection{"TypeError", "abort: the payload is not an object"}
	}
	if h.loop == nil {
		return &Rejection{"Error", "abort needs a Harness; this handle is a Session"}
	}
	id := uint32(numOf(req, "requestId"))
	switch {
	case req.Has("taskId"):
		// abortTask: mark the task and wait for its abort invocation; the answer is "marked" or "terminal".
		keepRestart, _ := boolOf(req, "keepRestart")
		h.sched.Abort(session.ID(numOf(req, "taskId")), keepRestart, h.bound(req), func(result string, err error) {
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
		h.sched.AbortConversation(session.ID(numOf(req, "conversationId")), background, h.bound(req), func(err error) {
			if err != nil {
				h.apiError(id, err)
				return
			}
			h.apiOK(id, nil)
		})
		return nil
	}
	submission, conversationID := session.ID(numOf(req, "submissionId")), session.ID(numOf(req, "conversationId"))
	bound := h.bound(req)
	var result inbox.AbortResult
	// Submissions.abort returns `Session.commitWith` (submissions.ts :95-111), queued through #enqueue (session.ts :530-531).
	h.loop.LineAfterTail(func() error {
		if err := bound.Err(); err != nil {
			return err
		}
		_, err := h.sess.RunCommit(func(tx *session.Tx) error {
			var e error
			result, e = inbox.Abort(tx, submission, conversationID)
			return e
		}, session.TxScope{})
		return err
	}, func(err error) {
		if err != nil {
			h.apiError(id, err)
			return
		}
		h.apiOK(id, string(result))
	})
	return nil
}

// bound is the signal of a submit or abort that came through an invocation-bound handle: the event names the calling
// invocation's task as invocation (PROTOCOL.md, invocation-bound operations), and the operation's line job checks the
// signal at start. Unbound operations carry none.
func (h *handler) bound(req *jv.Object) *session.Cancel {
	if !req.Has("invocation") || h.sched == nil {
		return nil
	}
	return h.sched.Bound(session.ID(numOf(req, "invocation")))
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
	if strOf(req, "query") != "harness" || h.sched == nil || h.loop == nil {
		return &Rejection{"Error", "inspect: only a Harness answers query harness"}
	}
	id := uint32(numOf(req, "requestId"))
	var out *jv.Object
	h.loop.ClientReadLine(func() error {
		// harness.ts inspect :249-258, a readOnLine job: `await this.#tasks.inspect(snapshot)` (scheduler.ts :847-858: the await of
		// loadScopes(false), whose chain reads are charged by the Store adapter, then the return) and two scanAll scans of one page, each a
		// single-statement Storage read awaited by scanAll and again by the job (ReadRow + 1).
		h.job.readLine = true
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
	})
	return nil
}

// inspectedTask is a TaskInspection on the wire: {record, state: {kind, ...}}; an error crosses as {name, message}.
func inspectedTask(t session.TaskInspection) *jv.Object {
	state := jv.NewObject()
	state.Set("kind", t.Kind)
	switch t.Kind {
	case "ready":
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
