// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// The run-end tasks of the `afterRun` hook (generation.ts finish and drain, spec section 8.3): a run that answered, or whose tool
// round asked to terminate, keeps `pi.live.run` while the hooks of its agent are asked for tasks. Their tasks are created as the
// generation's children, which the generation waits for in `drain`, and then the run ends at the final boundary.

// ThrownError is what host code (a tool, a hook, a section) threw: its text is the error's message, as errorText in harness/tool.ts
// reads it, and its name survives for a caller that classifies. Errors are an AggregateError's errors.
type ThrownError struct {
	Name, Message string
	Errors        []*ThrownError
}

func (e *ThrownError) Error() string { return e.Message }

// Thrown reads the {name, message} a host sends for a throw; a missing name is "Error".
func Thrown(o *jv.Object) *ThrownError {
	name := str(o, "name")
	if name == "" {
		name = "Error"
	}
	return &ThrownError{Name: name, Message: str(o, "message")}
}

// completedTerminal is the terminal state of a generation that completed with its answer entry.
func completedTerminal(answer ID) *jv.Object {
	return jobj("status", "terminal", "outcome", jobj("status", "completed", "result", jobj("entryId", float64(answer))))
}

// asksAfterRun is generation.ts asksAfterRun, an async function its caller awaits: whether the phase's agent selects an `afterRun` hook,
// so a run that answers waits for its tasks.
func (r *run) asksAfterRun(then func(bool)) {
	r.agent(func(agent *Agent) {
		asks := false
		for _, table := range agent.HookHandlers {
			if _, ok := table["afterRun"]; ok {
				asks = true
				break
			}
		}
		r.hops(1, func() { then(asks) })
	})
}

// holdRun is generation.ts holdRun, commit A of a run that answered with `afterRun` hooks selected: settle the run's inputs `done` with
// the answer and keep `pi.live.run` for the after-run tasks, then ask the hooks in `finish`. The final boundary waits for the run's end,
// so queued items stay queued. It reports false when the generation does not own the run.
func (r *run) holdRun(tx *session.Tx, live *delta.Obj, reason string, answer, last ID) (*jv.Object, bool, error) {
	run := live.Obj("run")
	if run == nil || !sameID(run.Get("taskId"), r.rt.TaskID()) {
		return nil, false, nil
	}
	if inputs := run.Arr("inputs"); inputs != nil {
		for i := range inputs.Len() {
			id, _ := inputs.Get(i).(float64)
			if err := tx.SettleSubmission(session.ID(id), jobj("status", "done", "answer", float64(answer))); err != nil {
				return nil, false, err
			}
		}
	}
	if err := run.Set("inputs", []any{}); err != nil {
		return nil, false, err
	}
	if err := run.Set("afterRun", jv.NewObject()); err != nil {
		return nil, false, err
	}
	live.Delete("generation")
	live.Delete("tools")
	live.Delete("nestedTools")
	// A run started before `first` was recorded has its answer as its first entry.
	var first any = float64(answer)
	if recorded := run.Get("first"); recorded != nil {
		first = recorded
	}
	summary := jobj("reason", reason, "answer", float64(answer), "last", float64(last), "first", first)
	return jobj("status", "running", "checkpoint", jobj("phase", phaseFinish, "run", summary)), true, nil
}

// preparedTask is a task an `afterRun` hook asked for, checked by the host and ready to create: the installed definition's name and
// version, the input, and the checkpoint its `initial()` returned.
type preparedTask struct {
	name       string
	version    int
	input      any
	checkpoint any
}

// finish asks the `afterRun` hooks for tasks and creates them, as the generation's children, in the commit that moves to `drain`;
// without any, it ends the run now.
func (r *run) finish(cp *jv.Object) {
	summary := objField(cp, "run")
	answer := idField(summary, "answer")
	if r.cutOffTooOften(r.task) {
		r.endAnsweredCutOff(answer)
		return
	}
	var prepared []preparedTask
	r.eachHook("afterRun", func() *jv.Object { return jobj("run", summary) }, func(result any) bool {
		asked, _ := result.(*jv.Object)
		if asked == nil {
			return false
		}
		tasks, _ := arrayField(asked, "tasks")
		for _, t := range tasks {
			if o, ok := t.(*jv.Object); ok {
				input, _ := o.Get("input")
				checkpoint, _ := o.Get("checkpoint")
				prepared = append(prepared, preparedTask{name: str(o, "task"), version: int(num(o, "version")), input: input, checkpoint: checkpoint})
			}
		}
		// The host skipped the broken requests; the hook's handler failed with them, which hooks.each reports against its extension.
		if rejected, ok := arrayField(asked, "rejected"); ok && len(rejected) > 0 {
			errs := make([]*ThrownError, 0, len(rejected))
			for _, e := range rejected {
				o, _ := e.(*jv.Object)
				errs = append(errs, Thrown(o))
			}
			err := error(errs[0])
			if len(errs) > 1 {
				err = &ThrownError{Name: "AggregateError", Message: itoa(int64(len(errs))) + " afterRun task requests were rejected", Errors: errs}
			}
			_ = r.rt.Report(err, session.ReportFrom{Extension: r.hookExtension("afterRun")})
		}
		return false
	}, func(err error) {
		if err != nil {
			r.fail(err)
			return
		}
		if len(prepared) == 0 {
			r.endAnsweredRun(answer)
			return
		}
		r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
			var tasks []ID
			for _, p := range prepared {
				checkpoint := p.checkpoint
				def := &session.TaskDef{Name: p.name, Version: p.version, Initial: func(any) any { return checkpoint }}
				id, err := tx.CreateTask(def, p.input, session.TaskOptions{OwnerTask: r.rt.TaskID()})
				if err != nil {
					return nil, err
				}
				tasks = append(tasks, id)
			}
			live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
			if err != nil {
				return nil, err
			}
			if run := live.Obj("run"); run != nil && sameID(run.Get("taskId"), r.rt.TaskID()) {
				if after := run.Obj("afterRun"); after != nil {
					if err := after.Set("tasks", idList(tasks)); err != nil {
						return nil, err
					}
				}
			}
			next := jobj("phase", phaseDrain, "answer", float64(answer), "tasks", idList(tasks))
			return jobj("status", "waiting", "checkpoint", next, "on", idList(tasks), "policy", "allSettled"), nil
		})
	})
}

// drain runs once the after-run tasks are terminal, whatever their outcomes: it ends the run.
func (r *run) drain(cp *jv.Object) {
	answer := idField(cp, "answer")
	if r.cutOffTooOften(r.task) {
		r.endAnsweredCutOff(answer)
		return
	}
	r.endAnsweredRun(answer)
}

// endAnsweredRun is commit B of a run that answered (spec section 8.3): its after-run tasks are terminal, or there were none. It
// places what the final boundary selects, ends the run, and starts the next one for selected user items.
func (r *run) endAnsweredRun(answer ID) {
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		boundary, err := prepareBoundary(tx, r.conv, r.g.Host.Settings().Queue)
		if err != nil {
			return nil, err
		}
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		applied, err := applyBoundary(tx, boundary, true, r.now())
		if err != nil {
			return nil, err
		}
		if err := EndRun(tx, live, r.rt.TaskID(), Answered(answer)); err != nil {
			return nil, err
		}
		if len(applied.Users) > 0 {
			if err := awaitStartRun(tx, r.conv, live, applied.Start(), false); err != nil {
				return nil, err
			}
		}
		return completedTerminal(answer), nil
	})
}

// endAnsweredCutOff ends a run that answered once its `finish` or `drain` phase was cut off settings.restartLimit times (spec section
// 5.4): the run answered, so it ends `completed`, without a failure and without the tasks it would still have waited for, marked so the
// scheduler reports it `task_interrupted`. Queued items stay queued, as after a failed run.
func (r *run) endAnsweredCutOff(answer ID) {
	cp := checkpointOf(r.task)
	text := "The generation was interrupted " + cutOffCount(r.task) + " in its " + str(cp, "phase") + " phase"
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		if err := EndRun(tx, live, r.rt.TaskID(), Answered(answer)); err != nil {
			return nil, err
		}
		r.rt.MarkEndedByCrashes(text)
		return completedTerminal(answer), nil
	})
}
