// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package rec

import "github.com/MichaelKinsy/PiG/durable/core/jv"

// Task is a task record (spec §5.1 TaskRecord): `{id, conversationId, kind, version, input, owner?, background,
// abortRequested, startedAt?, endedAt?, state, memos?}` in the order a creation writes them (startedAt and endedAt are
// appended by StampTimes). Accessors read V; mutators return a modified shallow copy.
type Task struct{ V *jv.Object }

// NewTask builds a pending task in the key order of Tx.createTask (src/session/transaction.ts:411-423). owner is 0 for
// a task its conversation owns. checkpoint is the definition's initial checkpoint.
func NewTask(taskID, conv int64, kind string, version int, input JSON, owner int64, background bool, checkpoint JSON) *Task {
	o := jv.NewObjectCap(8)
	o.Set("id", id(taskID))
	o.Set("conversationId", id(conv))
	o.Set("kind", kind)
	o.Set("version", float64(version))
	o.Set("input", input)
	if owner != 0 {
		o.Set("owner", id(owner))
	}
	o.Set("background", background)
	o.Set("abortRequested", false)
	st := jv.NewObjectCap(2)
	st.Set("status", "pending")
	st.Set("checkpoint", checkpoint)
	o.Set("state", st)
	return &Task{V: o}
}

// ParseTask reads a stored task record. The fields the tasks table indexes must be present and well formed.
func ParseTask(b []byte) (*Task, error) {
	o, err := parseObject(b)
	if err != nil {
		return nil, err
	}
	return TaskOf(o)
}

// TaskOf reads a task record that is already a value: ParseTask without the text, with the same checks. The task keeps o;
// records are never changed in place (mutators return a copy), so it needs no copy of its own.
func TaskOf(o *jv.Object) (*Task, error) {
	if o == nil {
		return nil, ErrShape
	}
	t := &Task{V: o}
	if _, ok := idOf(o, "id"); !ok {
		return nil, ErrShape
	}
	if _, ok := idOf(o, "conversationId"); !ok {
		return nil, ErrShape
	}
	if _, ok := stringOf(o, "kind"); !ok {
		return nil, ErrShape
	}
	if t.Status() == 0 {
		return nil, ErrShape
	}
	return t, nil
}

// Append appends the record's JSON.stringify.
func (t *Task) Append(dst []byte) []byte { return jv.Append(dst, t.V) }

// ID is the task ID.
func (t *Task) ID() int64 { n, _ := idOf(t.V, "id"); return n }

// ConversationID is the conversation the task lives in.
func (t *Task) ConversationID() int64 { n, _ := idOf(t.V, "conversationId"); return n }

// Kind is the registered task definition name.
func (t *Task) Kind() string { s, _ := stringOf(t.V, "kind"); return s }

// Version is the definition version of the live input and checkpoint.
func (t *Task) Version() int { n, _ := idOf(t.V, "version"); return int(n) }

// Input is the original task input.
func (t *Task) Input() JSON { v, _ := t.V.Get("input"); return v }

// Owner is the owning task of a child task; ok is false for a task its conversation owns.
func (t *Task) Owner() (int64, bool) { return idOf(t.V, "owner") }

// Background reports a background task (the `background` column is 1 exactly when this is true).
func (t *Task) Background() bool { return boolOf(t.V, "background") }

// AbortRequested reports the durable abort mark.
func (t *Task) AbortRequested() bool { return boolOf(t.V, "abortRequested") }

// StartedAt is the wall-clock ms of the first change to running (main da866ada); ok is false before the task first runs
// and on records written by 1.0.4.
func (t *Task) StartedAt() (float64, bool) {
	v, _ := t.V.Get("startedAt")
	f, ok := v.(float64)
	return f, ok
}

// EndedAt is the wall-clock ms of the change to terminal; ok is false while live.
func (t *Task) EndedAt() (float64, bool) {
	v, _ := t.V.Get("endedAt")
	f, ok := v.(float64)
	return f, ok
}

// State returns the state object (`{status, checkpoint | on... | outcome}`).
func (t *Task) State() *jv.Object { return objectOf(t.V, "state") }

// Status is the persisted status; 0 when the record has none.
func (t *Task) Status() TaskStatus {
	st := t.State()
	if st == nil {
		return 0
	}
	name, _ := stringOf(st, "status")
	s, _ := ParseTaskStatus(name)
	return s
}

// Checkpoint is the state's checkpoint (pending, running, waiting); nil when absent.
func (t *Task) Checkpoint() JSON {
	if st := t.State(); st != nil {
		v, _ := st.Get("checkpoint")
		return v
	}
	return nil
}

// Memos returns the memo object kept while the task can run, or nil.
func (t *Task) Memos() *jv.Object { return objectOf(t.V, "memos") }

// Terminal reports a settled task.
func (t *Task) Terminal() bool { return t.Status() == Terminal }

// WithState returns {...t, state} (scheduler.ts withState): entering completing or terminal drops the memos.
func (t *Task) WithState(state *jv.Object) *Task {
	c := t.V.Shallow()
	if name, _ := stringOf(state, "status"); name == "completing" || name == "terminal" {
		c.Delete("memos")
	}
	c.Set("state", state)
	return &Task{V: c}
}

// WithAbortRequested returns {...t, abortRequested: true}.
func (t *Task) WithAbortRequested() *Task { return &Task{V: with(t.V, "abortRequested", true)} }

// WithMemos returns {...t, memos}.
func (t *Task) WithMemos(memos *jv.Object) *Task { return &Task{V: with(t.V, "memos", memos)} }

// StampTimes is Transaction.#stampTimes (src/session/transaction.ts:953-963): startedAt on the first change to running,
// endedAt on the change to terminal. A value already set on candidate (the transaction's latest candidate for the task)
// or on t carries over; now is the Harness clock of this commit. It returns t itself when nothing changes.
func (t *Task) StampTimes(candidate *Task, now float64) *Task {
	status := t.Status()
	started, hasStarted := t.StartedAt()
	ended, hasEnded := t.EndedAt()
	newStarted, okStarted := started, hasStarted
	newEnded, okEnded := ended, hasEnded
	if candidate != nil {
		if v, ok := candidate.StartedAt(); ok {
			newStarted, okStarted = v, true
		}
		if v, ok := candidate.EndedAt(); ok {
			newEnded, okEnded = v, true
		}
	}
	if !okStarted && status == Running {
		newStarted, okStarted = now, true
	}
	if !okEnded && status == Terminal {
		newEnded, okEnded = now, true
	}
	if okStarted == hasStarted && newStarted == started && okEnded == hasEnded && newEnded == ended {
		return t
	}
	out := t.V
	if okStarted {
		out = with(out, "startedAt", newStarted)
	}
	if okEnded {
		out = with(out, "endedAt", newEnded)
	}
	return &Task{V: out}
}
