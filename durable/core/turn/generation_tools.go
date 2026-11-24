// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// tools is the tools phase: wait on the round's tool tasks, starting the next call of a sequential round, or finish the round.
func (r *run) tools(cp *jv.Object) {
	if r.cutOffTooOften(r.task) {
		r.endCutOff(cp)
		return
	}
	assistant := idField(cp, "assistant")
	toolList, _ := arrayField(cp, "tools")
	pending, _ := arrayField(cp, "pending")
	if len(pending) == 0 {
		r.finishToolRound(assistant, toolList)
		return
	}
	next := jsString(pending[0])
	rest := append([]any{}, pending[1:]...)
	// Sequential round: start the next call and wait for it.
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		var slot *delta.Obj
		if slots := live.Arr("tools"); slots != nil {
			for i := range slots.Len() {
				if s := slots.Obj(i); s != nil && s.Get("callId") == next && !s.Has("taskId") {
					slot = s
					break
				}
			}
		}
		unoffered := slot != nil && slot.Get("unoffered") == true
		taskID, err := createToolTask(tx, r.rt.TaskID(), assistant, next, unoffered)
		if err != nil {
			return nil, err
		}
		if slot != nil {
			if err := slot.Set("taskId", float64(taskID)); err != nil {
				return nil, err
			}
		}
		all := append(append([]any{}, toolList...), float64(taskID))
		cp := jobj("phase", phaseTools, "assistant", float64(assistant), "tools", all, "pending", rest)
		return jobj("status", "waiting", "checkpoint", cp, "on", idList([]ID{taskID}), "policy", "allSettled"), nil
	})
}

// finishToolRound runs when the round's tools are terminal: it applies their controls, appends a handoff's reset, and then
// ends the run at the final boundary (terminate), ends it at a queued reset, or hands it to the next generation at the
// postTools boundary, in the handoff's new context when there is one (spec section 8.5).
type roundEnd struct {
	r         *run
	assistant ID
	ids       []ID
	results   []any
	controls  []*jv.Object
	live      *jv.Object
	terminate bool
	newest    ID
	added     []any
	handoff   any
	maxTurns  int
	afterRun  bool
}

func (r *run) finishToolRound(assistant ID, toolList []any) {
	ids := make([]ID, len(toolList))
	for i, v := range toolList {
		f, _ := v.(float64)
		ids[i] = ID(f)
	}
	e := &roundEnd{r: r, assistant: assistant, ids: ids}
	// Pi v1.6.0 generation.ts finishToolRound: roundSlots (snapshot + 1 async hop), then afterTools, then endToolRound
	// (outcomes, then roundSlots again: snapshot + 1 async hop).
	r.snapshot(LiveDef, e.gotSlotsFirst)
}

func (e *roundEnd) gotSlotsFirst(live *jv.Object) {
	e.live = live
	e.r.hops(1, e.runAfterTools)
}

func (e *roundEnd) runAfterTools() {
	var results []any
	if list, ok := arrayField(e.live, "tools"); ok {
		for _, slot := range objectsOf(list) {
			if slot.Has("entry") {
				v, _ := slot.Get("entry")
				results = append(results, v)
			}
		}
	}
	if results == nil {
		results = []any{}
	}
	e.results = results
	e.r.eachHook("afterTools", e.afterToolsPayload, ignoreHookResult, e.afterToolsDone)
}

func ignoreHookResult(any) bool { return false }

func (e *roundEnd) afterToolsPayload() *jv.Object {
	return jobj("assistant", float64(e.assistant), "results", e.results)
}

func (e *roundEnd) afterToolsDone(err error) {
	if err != nil {
		e.r.fail(err)
		return
	}
	e.r.outcomes(e.ids, e.gotOutcomes)
}

func (e *roundEnd) gotOutcomes(outcomes []*jv.Object, err error) {
	if err != nil {
		e.r.fail(err)
		return
	}
	controls := make([]*jv.Object, len(e.ids))
	for i, outcome := range outcomes {
		if str(outcome, "status") == "completed" {
			controls[i] = objField(objField(outcome, "result"), "control")
		}
	}
	e.controls = controls
	e.r.snapshot(LiveDef, e.gotSlotsSecond)
}

func (e *roundEnd) gotSlotsSecond(live *jv.Object) {
	e.live = live
	e.r.hops(1, e.finishEnd)
}

func (e *roundEnd) finishEnd() {
	var slots []*jv.Object
	var results []any
	if list, ok := arrayField(e.live, "tools"); ok {
		slots = objectsOf(list)
	}
	for _, slot := range slots {
		if slot.Has("entry") {
			v, _ := slot.Get("entry")
			results = append(results, v)
		}
	}
	// In call order, the slots' order; a slot without a task (faulted or orphaned before it got one) has no control.
	controlOf := map[ID]*jv.Object{}
	for i, id := range e.ids {
		controlOf[id] = e.controls[i]
	}
	ordered := make([]*jv.Object, len(slots))
	for i, slot := range slots {
		if slot.Has("taskId") {
			ordered[i] = controlOf[ID(num(slot, "taskId"))]
		}
	}
	// Every call of the round must ask to terminate.
	terminate := len(slots) > 0
	for _, ctl := range ordered {
		if ctl == nil || !mustTrue(ctl, "terminate") {
			terminate = false
		}
	}
	var added []any
	var handoff any
	for _, ctl := range ordered {
		if ctl == nil {
			continue
		}
		if a, ok := arrayField(ctl, "addTools"); ok {
			added = append(added, a...)
		}
		// The last handoff in call order wins.
		if v, ok := ctl.Get("handoff"); ok && v != nil {
			handoff = v
		}
	}
	newest := e.assistant
	for _, v := range results {
		if id, _ := v.(float64); ID(id) > newest {
			newest = ID(id)
		}
	}
	e.terminate, e.newest, e.added, e.handoff = terminate, newest, added, handoff
	// Read from the phase's agent, as every agent field; a `configure()` during the round applies at its end.
	e.r.agent(e.gotAgent)
}

func (e *roundEnd) gotAgent(agent *Agent) {
	e.maxTurns = agent.MaxTurns
	if e.terminate {
		e.r.asksAfterRun(e.commitFinish)
		return
	}
	e.commitFinish(false)
}

func (e *roundEnd) commitFinish(afterRun bool) {
	e.afterRun = afterRun
	e.r.commitDone(e.commitTx)
}

func (e *roundEnd) commitTx(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
	return e.r.finishCommit(tx, e.assistant, e.newest, e.terminate, e.afterRun, e.maxTurns, e.handoff, e.added)
}

func mustTrue(o *jv.Object, key string) bool {
	v, _ := o.Get(key)
	return v == true
}

func (r *run) finishCommit(tx *session.Tx, assistant, newest ID, terminate, afterRun bool, maxTurns int, handoff any, added []any) (*jv.Object, error) {
	h := r.g.Host
	boundary, err := prepareBoundary(tx, r.conv, h.Settings().Queue)
	if err != nil {
		return nil, err
	}
	if len(added) > 0 {
		if err := h.AddTools(tx, r.conv, added); err != nil {
			return nil, err
		}
	}
	live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
	if err != nil {
		return nil, err
	}
	now := r.now()
	// The run's last entry so far: the newest result, or the assistant when no result has an entry.
	last := newest
	if handoff != nil {
		// The handoff's reset precedes every queued item, so queued writes and selected steers land in its context.
		message := jobj("role", "user", "content", handoff, "timestamp", now)
		fields := jobj("head", "self", "model", []any{message})
		entry, err := tx.AppendEntry(r.conv, entryDraft(KindReset, fields))
		if err != nil {
			return nil, err
		}
		boundary.Head, boundary.HasHead = idField(entry, "id"), true
		last = idField(entry, "id")
	}
	turns := 0
	if run := live.Obj("run"); run != nil && sameID(run.Get("taskId"), r.rt.TaskID()) {
		t, _ := run.Get("turns").(float64)
		turns = int(t)
	}
	if afterRun {
		held, ok, err := r.holdRun(tx, live, "terminate", assistant, last)
		if err != nil {
			return nil, err
		}
		if ok {
			return held, nil
		}
	}
	switch {
	case terminate:
		applied, err := applyBoundary(tx, boundary, true, now)
		if err != nil {
			return nil, err
		}
		if err := EndRun(tx, live, r.rt.TaskID(), Answered(assistant)); err != nil {
			return nil, err
		}
		if len(applied.Users) > 0 {
			if err := awaitStartRun(tx, r.conv, live, applied.Start(), false); err != nil {
				return nil, err
			}
		}
	case maxTurns > 0 && turns >= maxTurns:
		// The turn limit ends the run like a failure: `afterFailedRun` decides the queued items, and a continue resumes it on the round's
		// results. The table was read for the boundary already, before any write.
		failed := failure{policy: h.Settings().AfterFailedRun}
		if failed.policy == "continue" {
			failed.boundary = boundary
		}
		detail := itoa(int64(turns)) + " turns"
		if turns == 1 {
			detail = "1 turn"
		}
		if err := r.endFailedRun(tx, live, failed, UnansweredWith("turn_limit", detail)); err != nil {
			return nil, err
		}
	default:
		applied, err := applyBoundary(tx, boundary, false, now)
		if err != nil {
			return nil, err
		}
		if applied.Reset {
			// The queued reset cut the run's context before an answer.
			if err := EndRun(tx, live, r.rt.TaskID(), Unanswered("reset")); err != nil {
				return nil, err
			}
			if len(applied.Users) > 0 {
				if err := awaitStartRun(tx, r.conv, live, applied.Start(), true); err != nil {
					return nil, err
				}
			}
		} else {
			live.Delete("tools")
			live.Delete("nestedTools")
			if run := live.Obj("run"); run != nil && run.Get("taskId") == float64(r.rt.TaskID()) {
				if inputs := run.Arr("inputs"); inputs != nil {
					if _, err := inputs.Push(idList(applied.Users)...); err != nil {
						return nil, err
					}
				}
			}
			next, err := createGeneration(tx, r.conv, nil)
			if err != nil {
				return nil, err
			}
			handOver(live, r.rt.TaskID(), next)
		}
	}
	return completedTerminal(assistant), nil
}

// abort runs in a fresh invocation after an abort mark and commits a terminal outcome: a partial becomes an aborted
// entry, calls of a sequential round never started get aborted results (spec section 8.5), and the run ends.
func (g *Generation) abort(rt *session.Runtime, task *jv.Object, done func(error)) {
	r := g.newRun(rt, done)
	cp := checkpointOf(task)
	finish := func(unstarted []*jv.Object) {
		r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
			live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
			if err != nil {
				return nil, err
			}
			if err := convertPartial(tx, live, r.conv); err != nil {
				return nil, err
			}
			for _, call := range unstarted {
				// A call its request did not offer could not have run either way.
				unoffered := false
				if slots := live.Arr("tools"); slots != nil {
					for i := range slots.Len() {
						if slot := slots.Obj(i); slot != nil && slot.Get("callId") == str(call, "id") {
							unoffered = slot.Get("unoffered") == true
							break
						}
					}
				}
				result := HarnessError("aborted", "Tool "+str(call, "name")+" was aborted")
				if unoffered {
					result = HarnessError("tool_unavailable", "Tool "+str(call, "name")+" is not available")
				}
				if _, err := AppendToolResult(tx, r.conv, call, result, r.now(), nil); err != nil {
					return nil, err
				}
			}
			if err := EndRun(tx, live, rt.TaskID(), Unanswered("aborted")); err != nil {
				return nil, err
			}
			return jobj("status", "terminal", "outcome", jobj("status", "aborted")), nil
		})
	}
	phase := str(cp, "phase")
	readUnstarted := func() {
		if phase != phaseTools {
			finish(nil)
			return
		}
		r.entry(idField(cp, "assistant"), func(entry *jv.Object, _ bool) {
			var calls []*jv.Object
			if models, ok := arrayField(entry, "model"); ok && len(models) > 0 {
				if m, ok := models[0].(*jv.Object); ok && str(m, "role") == "assistant" {
					content, _ := arrayField(m, "content")
					pending, _ := arrayField(cp, "pending")
					for _, id := range pending {
						for _, c := range content {
							if o, ok := c.(*jv.Object); ok && str(o, "type") == "toolCall" && idOf(o) == id {
								calls = append(calls, o)
								break
							}
						}
					}
				}
			}
			finish(calls)
		})
	}
	if model, handle := deferredOf(cp); handle != nil {
		r.cancelDeferred(model, handle, readUnstarted)
		return
	}
	readUnstarted()
}

func idOf(o *jv.Object) any {
	v, _ := o.Get("id")
	return v
}
