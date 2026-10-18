package session

import (
	"sort"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// Runtime is the facade a task phase handler uses, bound to one invocation
// (TaskRuntime of types.ts). Every operation fails once the invocation ended.
// Operations that Pi returns as promises take a continuation that runs as a
// microtask after the operation settles.
type Runtime struct {
	s     *Scheduler
	inv   *invocation
	task  func() *TaskImpl
	snap  func() Snapshot
	agent any
}

func (s *Scheduler) newRuntime(inv *invocation, task func() *TaskImpl, snap func() Snapshot) *Runtime {
	return &Runtime{s: s, inv: inv, task: task, snap: snap}
}

func (r *Runtime) agentReset() { r.agent = nil }

// OnEnded runs fn when the invocation ends (scheduler.ts #end), or at once when it already has.
func (r *Runtime) OnEnded(fn func()) {
	if r.inv.ended {
		fn()
		return
	}
	r.inv.watches = append(r.inv.watches, fn)
}

// Extension is the extension that installed the definition this invocation runs, as of its reservation; empty for a built-in
// task.
func (r *Runtime) Extension() string { return r.inv.extension }

// TaskID is the running task.
func (r *Runtime) TaskID() ID { return r.inv.taskID }

// ConversationID is the task's conversation.
func (r *Runtime) ConversationID() ID { return r.inv.conversationID }

// Signal is aborted when the run is signalled by AbortTask, the Harness closes
// or the invocation ends.
func (r *Runtime) Signal() *Cancel { return r.inv.signal }

// Snapshot is the registry snapshot of the current phase.
func (r *Runtime) Snapshot() Snapshot { return r.snap() }

// Now is the Harness clock.
func (r *Runtime) Now() (float64, error) {
	if r.inv.ended {
		return 0, endedError(r.inv)
	}
	return r.s.env.Now(), nil
}

// Report forwards a non-fatal failure to the host with this task in its info: a task_error about the task's own code,
// or, with from, an extension_error about extension code it called, such as a section, or a host_error about a host
// callback, such as the environment builder (scheduler.ts runtime.report).
func (r *Runtime) Report(err error, from ...ReportFrom) error {
	if r.inv.ended {
		return endedError(r.inv)
	}
	if len(from) == 0 {
		r.s.report(err, aboutInvocation("task_error", r.inv))
		return nil
	}
	task := reportTask(r.inv.taskID, r.inv.kind, r.inv.conversationID)
	if f := from[0]; f.Callback != "" {
		r.s.report(err, obj("type", "host_error", "callback", f.Callback, "task", task))
		return nil
	} else {
		info := obj("type", "extension_error", "task", task)
		if f.Extension != "" {
			info.Set("extension", f.Extension)
		}
		r.s.report(err, info)
	}
	return nil
}

// ReportFrom is whose code a task's Report is about when not the task's own: an extension's (Extension, when known), or a
// host callback (Callback, a HostCallback name such as env or models).
type ReportFrom struct{ Extension, Callback string }

// Commit commits on the Session line after rereading the task. change may
// stage writes through tx and return the state the task commits for itself
// (nil leaves it unchanged). It fails when the task is terminal, the
// invocation ended, the Harness is closing or, in a run invocation, the task
// carries an abort mark.
func (r *Runtime) Commit(change func(tx *Tx, current *jv.Object) (next *jv.Object, err error), then func(error)) {
	r.gated(func(tx *Tx, current *jv.Object) error {
		next, err := change(tx, current)
		if err != nil {
			return err
		}
		u := tx.ticks
		if next == nil {
			tx.ticks = hopcost.RuntimeCommit(u, false, 0)
			return nil
		}
		overlay := stagedTasks(tx)
		err = r.s.commitState(tx, r.inv, current, next)
		tx.ticks = hopcost.RuntimeCommit(u, true, commitStateTicks(next, overlay))
		return err
	}, then)
}

// BeginHost opens a commit whose callback runs in the host, one operation per event (ABI section 8): the checks of Commit
// run now, at the moment the host's transaction takes the Session line, and the returned transaction is scoped to the
// task. The caller must hold the line until FinishHost or AbandonHost.
func (r *Runtime) BeginHost() (tx *Tx, current *jv.Object, err error) {
	s, inv := r.s, r.inv
	if inv.ended {
		return nil, nil, endedError(inv)
	}
	if s.closing {
		return nil, nil, s.closedErr()
	}
	found, ok := s.live.get(inv.taskID)
	if !ok {
		return nil, nil, errorf("Task %d is terminal", inv.taskID)
	}
	if st := taskStatus(found); st != StatusRunning {
		return nil, nil, errorf("Task %d is %s", inv.taskID, string(st))
	}
	if !inv.abortMode && getBool(found, "abortRequested") {
		return nil, nil, errorf("Task %d has a durable abort mark", inv.taskID)
	}
	tx, err = s.sess.Begin(TxScope{ConversationID: inv.conversationID, TaskID: inv.taskID})
	return tx, found, err
}

// FinishHost commits the host transaction of BeginHost: next, when not nil, is the state the task commits for itself.
func (r *Runtime) FinishHost(tx *Tx, current, next *jv.Object) (*Publication, error) {
	u := tx.ticks
	if next != nil {
		overlay := stagedTasks(tx)
		if err := r.s.commitState(tx, r.inv, current, next); err != nil {
			r.s.sess.Abandon(tx, err)
			return nil, err
		}
		tx.ticks = hopcost.RuntimeCommit(u, true, commitStateTicks(next, overlay))
	} else {
		tx.ticks = hopcost.RuntimeCommit(u, false, 0)
	}
	// The callback's Change is the wrappers of #gated (scheduler.ts :1147-1155, :1275-1297), as for Commit: the line job's Storage
	// reads the core makes to take the line are not Pi's.
	r.s.sess.lastTicks = tx.ticks
	return r.s.sess.Finish(tx)
}

// FinishHostSettled commits again the host transaction of BeginHost whose commit returned a *NeedGate (Session.FinishSettled).
func (r *Runtime) FinishHostSettled(tx *Tx) (*Publication, error) { return r.s.sess.FinishSettled(tx) }

// AbandonHost ends the host transaction of BeginHost without a commit.
func (r *Runtime) AbandonHost(tx *Tx, err error) { r.s.sess.Abandon(tx, err) }

// gated runs change as a job guarded by the invocation (scheduler.ts #gated).
func (r *Runtime) gated(change func(tx *Tx, current *jv.Object) error, then func(error)) {
	s, inv := r.s, r.inv
	if inv.ended {
		s.loop.Defer(func() { then(endedError(inv)) })
		return
	}
	s.commitQueued(func(tx *Tx) error {
		if inv.ended {
			return endedError(inv)
		}
		if s.closing {
			return s.closedErr()
		}
		found, ok := s.live.get(inv.taskID)
		if !ok {
			return errorf("Task %d is terminal", inv.taskID)
		}
		if st := taskStatus(found); st != StatusRunning {
			return errorf("Task %d is %s", inv.taskID, string(st))
		}
		if !inv.abortMode && getBool(found, "abortRequested") {
			return errorf("Task %d has a durable abort mark", inv.taskID)
		}
		return change(tx, found)
	}, TxScope{ConversationID: inv.conversationID, TaskID: inv.taskID}, then)
}

// Memo stores candidate unless a memo of that name exists and returns the
// durable winner.
func (r *Runtime) Memo(name string, candidate any, then func(winner any, err error)) {
	var winner any
	r.gated(func(tx *Tx, current *jv.Object) error {
		if w, ok := memoOf(current, name); ok {
			winner = w
			return nil
		}
		memos := jv.NewObject()
		if m := getObj(current, "memos"); m != nil {
			memos = m.Shallow()
		}
		memos.Set(name, jv.Clone(candidate))
		winner = candidate
		tx.ticks = hopcost.GatedSyncChange
		return tx.stageTask(with(current, "memos", memos))
	}, func(err error) {
		if err != nil {
			then(nil, err)
			return
		}
		then(winner, nil)
	})
}

// MemoGet reads a memo of this task.
func (r *Runtime) MemoGet(name string) (any, bool, error) {
	if r.inv.ended {
		return nil, false, endedError(r.inv)
	}
	rec, _ := r.s.live.get(r.inv.taskID)
	v, ok := memoOf(rec, name)
	return v, ok, nil
}

// GetTask returns a committed task record, or nil.
func (r *Runtime) GetTask(id ID, then func(*jv.Object, error)) {
	if r.inv.ended {
		r.s.loop.Defer(func() { then(nil, endedError(r.inv)) })
		return
	}
	var rec *jv.Object
	r.s.loop.Line(func() error {
		var err error
		rec, err = r.s.sess.store.Task(id)
		return err
	}, func(err error) { then(rec, err) })
}

// WaitForTask resolves with the task's terminal record; it fails when the
// invocation ends first.
func (r *Runtime) WaitForTask(id ID, then func(rec *jv.Object, err error, waited bool)) {
	if r.inv.ended {
		r.s.loop.Defer(func() { then(nil, endedError(r.inv), false) })
		return
	}
	r.s.WaitForTaskFound(id, r.inv.signal, then)
}

// AbortOwned aborts a task this task owns and calls then once it is terminal (scheduler.ts abortOwned).
func (r *Runtime) AbortOwned(id ID, then func(error)) {
	r.GetTask(id, func(rec *jv.Object, err error) {
		if err != nil {
			then(err)
			return
		}
		var owner any
		if rec != nil {
			owner, _ = rec.Get("owner")
		}
		if owner != float64(r.inv.taskID) {
			then(errorf("Task %d is not owned by task %d", id, r.inv.taskID))
			return
		}
		if taskStatus(rec) == StatusTerminal {
			then(nil)
			return
		}
		r.s.Abort(id, true, r.inv.signal, func(_ string, err error) {
			if err != nil {
				then(err)
				return
			}
			r.WaitForTask(id, func(_ *jv.Object, err error, _ bool) { then(err) })
		})
	})
}

// OwnedTasks calls then with copies of the live tasks this task owns directly, in ID order, whichever invocation created
// them (scheduler.ts ownedTasks): every live task is listed under its parent node, its owner task when it has one. Tasks in
// conversations it owns are not included. It fails when the invocation has ended.
func (r *Runtime) OwnedTasks(then func([]*jv.Object, error)) {
	if r.inv.ended {
		r.s.loop.Defer(func() { then(nil, endedError(r.inv)) })
		return
	}
	var out []*jv.Object
	r.s.loop.ReadLine(func() error {
		// The step that ends the invocation may have run on the line before this.
		if r.inv.ended {
			return endedError(r.inv)
		}
		children := r.s.below[taskTreeNode(r.inv.taskID)]
		if children == nil {
			return nil
		}
		children.Range(func(node treeNode) bool {
			if node.conversation {
				return true
			}
			if rec, live := r.s.live.get(node.id); live {
				if owner, _ := rec.Get("owner"); owner == float64(r.inv.taskID) {
					out = append(out, jv.Clone(rec).(*jv.Object))
				}
			}
			return true
		})
		sort.Slice(out, func(a, b int) bool { return idOfRecord(out[a]) < idOfRecord(out[b]) })
		return nil
	}, func(err error) { then(out, err) })
}

func idOfRecord(rec *jv.Object) float64 {
	id, _ := rec.Get("id")
	n, _ := id.(float64)
	return n
}

// Outcomes returns the outcomes of terminal tasks, in order.
func (r *Runtime) Outcomes(ids []ID, then func([]*jv.Object, error)) {
	if r.inv.ended {
		r.s.loop.Defer(func() { then(nil, endedError(r.inv)) })
		return
	}
	var out []*jv.Object
	r.s.loop.ReadLine(func() error {
		out = nil
		for _, id := range ids {
			rec, err := r.s.sess.store.Task(id)
			if err != nil {
				return err
			}
			if rec == nil || taskStatus(rec) != StatusTerminal {
				return errorf("Task %d is not terminal", id)
			}
			out = append(out, getObj(getObj(rec, "state"), "outcome"))
		}
		return nil
	}, func(err error) { then(out, err) })
}

// SleepUntil commits a sleep: the task holds no invocation until the Harness clock reaches until, then resumes at checkpoint.
// then runs once the sleep is stored, not when the time comes (scheduler.ts sleepUntil, a #gated #commitState).
func (r *Runtime) SleepUntil(until float64, checkpoint any, then func(error)) {
	r.gated(func(tx *Tx, current *jv.Object) error {
		tx.ticks = hopcost.GatedChange(1)
		return r.s.commitState(tx, r.inv, current, obj("status", StatusSleeping, "checkpoint", jv.Clone(checkpoint), "until", until))
	}, then)
}

// Read runs job as a read-only job on the Session line (scheduler.ts #read with Session.readOnLine): it waits behind the
// commits already queued, so a read observes the state those commits left, and then calls then with its error. It fails when
// the invocation has ended, before and after the wait.
func (r *Runtime) Read(job func() error, then func(error)) {
	inv := r.inv
	if inv.ended {
		r.s.loop.Defer(func() { then(endedError(inv)) })
		return
	}
	r.s.loop.ReadLine(func() error {
		if inv.ended {
			return endedError(inv)
		}
		return job()
	}, then)
}

// stagedTasks is the tasks the transaction has staged: the overlay #commitState reads before it stages the task's own state.
func stagedTasks(tx *Tx) int {
	n := 0
	for _, id := range tx.taskOrder {
		if tx.tasks[id].write != nil {
			n++
		}
	}
	return n
}

// commitStateTicks is the awaits of scheduler.ts #commitState beyond its own return: #validateWait for a waiting state
// (loadChain and the async call's return: 2), and for a terminal state `await #loadScopes(false)` and one loadChain per
// task the transaction staged before the state (overlay), at least one.
func commitStateTicks(next *jv.Object, overlay int) int {
	switch status, _ := getStr(next, "status"); status {
	case StatusWaiting:
		return 2
	case StatusTerminal:
		return 1 + max(1, overlay)
	}
	return 0
}
