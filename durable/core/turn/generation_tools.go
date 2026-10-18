// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// tools is the tools phase: wait on the round's tool tasks, starting the next call of a sequential round, or finish the round.
func (r *run) tools(cp *jv.Object) {
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
		taskID, err := createToolTask(tx, r.rt.TaskID(), assistant, next)
		if err != nil {
			return nil, err
		}
		if slots := live.Arr("tools"); slots != nil {
			for i := range slots.Len() {
				if slot := slots.Obj(i); slot != nil && slot.Get("callId") == next && !slot.Has("taskId") {
					if err := slot.Set("taskId", float64(taskID)); err != nil {
						return nil, err
					}
					break
				}
			}
		}
		all := append(append([]any{}, toolList...), float64(taskID))
		cp := jobj("phase", phaseTools, "assistant", float64(assistant), "tools", all, "pending", rest)
		return jobj("status", "waiting", "checkpoint", cp, "on", idList([]ID{taskID}), "policy", "allSettled"), nil
	})
}

// finishToolRound runs when the round's tools are terminal: it applies their controls and either ends the run at the
// final boundary (terminate, handoff, or a queued reset) or hands it to the next generation at the postTools boundary
// (spec section 8.5).
func (r *run) finishToolRound(assistant ID, toolList []any) {
	ids := make([]ID, len(toolList))
	for i, v := range toolList {
		f, _ := v.(float64)
		ids[i] = ID(f)
	}
	r.rt.Outcomes(ids, func(outcomes []*jv.Object, err error) {
		if err != nil {
			r.fail(err)
			return
		}
		controls := make([]*jv.Object, len(ids))
		for i, outcome := range outcomes {
			if str(outcome, "status") == "completed" {
				controls[i] = objField(objField(outcome, "result"), "control")
			}
		}
		r.snapshot(LiveDef, func(live *jv.Object) {
			var slots []*jv.Object
			var results []any
			if list, ok := arrayField(live, "tools"); ok {
				slots = objectsOf(list)
			}
			for _, slot := range slots {
				if slot.Has("entry") {
					e, _ := slot.Get("entry")
					results = append(results, e)
				}
			}
			if results == nil {
				results = []any{}
			}
			r.eachHook("afterTools", func() *jv.Object { return jobj("assistant", float64(assistant), "results", results) }, func(any) bool { return false }, func(err error) {
				if err != nil {
					r.fail(err)
					return
				}
				controlOf := map[ID]*jv.Object{}
				for i, id := range ids {
					controlOf[id] = controls[i]
				}
				// Every call of the round, including those answered without a task, must ask to terminate.
				terminate := len(slots) > 0
				for _, slot := range slots {
					ctl := controlOf[ID(num(slot, "taskId"))]
					if !slot.Has("taskId") || ctl == nil || !mustTrue(ctl, "terminate") {
						terminate = false
					}
				}
				var added []any
				var handoff any
				for _, ctl := range controls {
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
				r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
					return r.finishCommit(tx, assistant, terminate, handoff, added)
				})
			})
		})
	})
}

func mustTrue(o *jv.Object, key string) bool {
	v, _ := o.Get(key)
	return v == true
}

func (r *run) finishCommit(tx *session.Tx, assistant ID, terminate bool, handoff any, added []any) (*jv.Object, error) {
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
	if terminate || handoff != nil {
		if handoff != nil {
			message := jobj("role", "user", "content", handoff, "timestamp", now)
			fields := jobj("head", "self", "model", []any{message})
			entry, err := tx.AppendEntry(r.conv, entryDraft(KindReset, fields))
			if err != nil {
				return nil, err
			}
			boundary.Head, boundary.HasHead = idField(entry, "id"), true
		}
		applied, err := applyBoundary(tx, boundary, true, now)
		if err != nil {
			return nil, err
		}
		if err := EndRun(tx, live, r.rt.TaskID(), jobj("status", "done", "answer", float64(assistant))); err != nil {
			return nil, err
		}
		if len(applied.Users) > 0 {
			if err := awaitStartRun(tx, r.conv, live, applied.Users); err != nil {
				return nil, err
			}
		}
	} else {
		applied, err := applyBoundary(tx, boundary, false, now)
		if err != nil {
			return nil, err
		}
		if applied.Reset {
			// The queued reset cut the run's context before an answer.
			if err := EndRun(tx, live, r.rt.TaskID(), jobj("status", "unanswered", "reason", "reset")); err != nil {
				return nil, err
			}
			if len(applied.Users) > 0 {
				if err := awaitStartRun(tx, r.conv, live, applied.Users); err != nil {
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
			next, err := createGeneration(tx, r.conv)
			if err != nil {
				return nil, err
			}
			handOver(live, r.rt.TaskID(), next)
		}
	}
	return jobj("status", "terminal", "outcome", jobj("status", "completed", "result", jobj("entryId", float64(assistant)))), nil
}

// abort runs in a fresh invocation after an abort mark and commits a terminal outcome: a partial becomes an aborted
// entry, calls of a sequential round never started get aborted results (spec section 8.5), and the run ends.
func (g *Generation) abort(rt *session.Runtime, task *jv.Object, done func(error)) {
	r := g.newRun(rt, done)
	cp := checkpointOf(task)
	h := g.Host
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
				if _, err := AppendToolResult(tx, r.conv, call, HarnessError("aborted", "Tool "+str(call, "name")+" was aborted"), r.now(), nil); err != nil {
					return nil, err
				}
			}
			if err := EndRun(tx, live, rt.TaskID(), jobj("status", "unanswered", "reason", "aborted")); err != nil {
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
		r.entry(idField(cp, "assistant"), func(entry *jv.Object) {
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
	if phase == phasePoll {
		ref := modelRefOf(objField(cp, "model"))
		if _, ok := h.ContextWindow(ref); ok {
			h.CancelDeferred(ref, objField(cp, "handle"), rt.Signal(), func(err error) {
				if err != nil {
					_ = rt.Report(err, session.ReportFrom{Callback: "models"})
				}
				readUnstarted()
			})
			return
		}
	}
	readUnstarted()
}

func idOf(o *jv.Object) any {
	v, _ := o.Get("id")
	return v
}
