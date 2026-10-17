// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// The built-in pi.live document of src/harness/live.ts: run control and presentation of the current generation and tool
// round. Its value is {run?, generation?, tools?, compactions?}.

// Task kinds of the built-in tasks.
const (
	KindGeneration = "pi.generation"
	KindTool       = "pi.tool"
	KindCompaction = "pi.compaction"
)

// LiveDef is LiveDoc. A complete base is stored whenever nothing runs (spec section 8.2): no generation and no running
// tool slot. That holds while idle, in the commit handing a generation over to its tool round, and between tools, so the
// delta chain spans at most one generation or the overlapping execution of one round's tools. A slot holds output only
// while running, so every base is small. There is deliberately no delta-count bound.
// Read-only: assigned once at initialization.
var LiveDef = &session.DocDef{
	Kind:    "pi.live",
	Version: 1,
	Scope:   session.ScopeConversation,
	History: session.HistoryLatest,
	Fork:    session.ForkInitial,
	Initial: func(any) *jv.Object { return jv.NewObject() },
	CheckpointWhen: func(value *jv.Object, _ []delta.Op, _ int) bool {
		if _, ok := value.Get("generation"); ok {
			return false
		}
		return !anySlotRunning(value)
	},
}

func anySlotRunning(value *jv.Object) bool {
	for _, key := range [...]string{"tools", "nestedTools"} {
		tools, _ := value.Get(key)
		list, _ := tools.([]any)
		for _, s := range list {
			if slot, ok := s.(*jv.Object); ok {
				if status, _ := slot.Get("status"); status == "running" {
					return true
				}
			}
		}
	}
	return false
}

// EndRun is endRun: it settles each input of the run owned by taskID with settlement and removes `run`, and always
// removes `generation` and `tools`, whose presentation belongs to the ending run.
func EndRun(tx *session.Tx, live *delta.Obj, taskID session.ID, settlement *jv.Object) error {
	if run := live.Obj("run"); run != nil && sameID(run.Get("taskId"), taskID) {
		if inputs := run.Arr("inputs"); inputs != nil {
			for i := range inputs.Len() {
				id, _ := inputs.Get(i).(float64)
				if err := tx.SettleSubmission(session.ID(id), settlement); err != nil {
					return err
				}
			}
		}
		live.Delete("run")
	}
	live.Delete("generation")
	live.Delete("tools")
	live.Delete("nestedTools")
	return nil
}

// AddCompactionStatus is addCompactionStatus: statuses stay in task ID order because a created task has the newest ID.
func AddCompactionStatus(live *delta.Obj, status *jv.Object) error {
	list := live.Arr("compactions")
	if list == nil {
		if err := live.Set("compactions", []any{}); err != nil {
			return err
		}
		list = live.Arr("compactions")
	}
	_, err := list.Push(status)
	return err
}

// CompactionStatusOf is compactionStatus: the status of compaction task taskID, or nil.
func CompactionStatusOf(live *delta.Obj, taskID session.ID) *delta.Obj {
	return findByTask(live.Arr("compactions"), taskID)
}

// RemoveCompactionStatus is removeCompactionStatus: it removes the status of compaction task taskID, and the list once empty.
func RemoveCompactionStatus(live *delta.Obj, taskID session.ID) error {
	list := live.Arr("compactions")
	if list == nil {
		return nil
	}
	for i := range list.Len() {
		if o := list.Obj(i); o != nil && sameID(o.Get("taskId"), taskID) {
			if _, err := list.Splice(i, 1); err != nil {
				return err
			}
			break
		}
	}
	if list.Len() == 0 {
		live.Delete("compactions")
	}
	return nil
}

// FailCompactionStatus is failCompactionStatus: it removes the status of compaction task taskID and records that it failed with
// message (pi.live.compactionFailure).
func FailCompactionStatus(live *delta.Obj, taskID session.ID, message string) error {
	if status := CompactionStatusOf(live, taskID); status != nil {
		if err := live.Set("compactionFailure", jobj("taskId", float64(taskID), "reason", status.Get("reason"), "error", message)); err != nil {
			return err
		}
	}
	return RemoveCompactionStatus(live, taskID)
}

// ToolSlotOf is toolSlot: the slot of tool task taskID, model-issued in the current round or nested, or nil. Nested slots are
// found by binary search: a tool may make thousands of nested calls, each of which looks up its slot in several commits.
// Their task IDs ascend: each slot is appended in the commit that creates its task, and removal keeps the order.
func ToolSlotOf(live *delta.Obj, taskID session.ID) *delta.Obj {
	if slot := findByTask(live.Arr("tools"), taskID); slot != nil {
		return slot
	}
	nested := live.Arr("nestedTools")
	if nested == nil {
		return nil
	}
	low, high := 0, nested.Len()-1
	for low <= high {
		middle := (low + high) >> 1
		candidate := nested.Obj(middle)
		id, _ := candidate.Get("taskId").(float64)
		switch {
		case id == float64(taskID):
			return candidate
		case id < float64(taskID):
			low = middle + 1
		default:
			high = middle - 1
		}
	}
	return nil
}

// IsNestedSlot is whether a slot names its parent task, which only a nested call's does.
func IsNestedSlot(slot *delta.Obj) bool { return slot.Get("parentTaskId") != nil }

// RemoveNestedSlots removes the nested slots below tool task taskID, transitively; the list holds parents before their
// children.
func RemoveNestedSlots(live *delta.Obj, taskID session.ID) error {
	nested := live.Arr("nestedTools")
	if nested == nil {
		return nil
	}
	removed := map[float64]bool{float64(taskID): true}
	for i := range nested.Len() {
		slot := nested.Obj(i)
		if parent, _ := slot.Get("parentTaskId").(float64); removed[parent] {
			id, _ := slot.Get("taskId").(float64)
			removed[id] = true
		}
	}
	for i := nested.Len() - 1; i >= 0; i-- {
		if parent, _ := nested.Obj(i).Get("parentTaskId").(float64); removed[parent] {
			if _, err := nested.Splice(i, 1); err != nil {
				return err
			}
		}
	}
	if nested.Len() == 0 {
		live.Delete("nestedTools")
	}
	return nil
}

func findByTask(list *delta.Arr, taskID session.ID) *delta.Obj {
	if list == nil {
		return nil
	}
	for i := range list.Len() {
		if o := list.Obj(i); o != nil && sameID(o.Get("taskId"), taskID) {
			return o
		}
	}
	return nil
}

// FinishSlot is finishSlot (live.ts:197-204): it marks a slot done and clears its running output; the caller sets a
// model-issued call's result entry, or a nested call's summary.
func FinishSlot(slot *delta.Obj) error {
	if err := slot.Set("status", "done"); err != nil {
		return err
	}
	ClearProgress(slot)
	return nil
}

// ClearProgress is clearProgress: it removes what a tool published while running; its result entry or a rerun replaces it.
func ClearProgress(slot *delta.Obj) {
	for _, key := range [...]string{"output", "droppedBytes", "droppedLines", "details", "diagnostics"} {
		slot.Delete(key)
	}
}

func sameID(v any, id session.ID) bool {
	f, ok := v.(float64)
	return ok && f == float64(id)
}

// SettleSchedulerOutcome is settleSchedulerOutcome (harness/live.ts): the Harness side of a task the scheduler settled
// with an outcome it wrote itself (faulted or orphaned), in the settling transaction. A tool task finishes its slot; a
// compaction task drops its status; the generation that holds `run` converts its partial, and its inputs settle
// unanswered with the reason (faulted carries the error message as detail).
func SettleSchedulerOutcome(tx *session.Tx, record, outcome *jv.Object) error {
	kind, _ := record.Get("kind")
	taskID, _ := record.Get("id")
	conv, _ := record.Get("conversationId")
	id, _ := taskID.(float64)
	c, _ := conv.(float64)
	switch kind {
	case KindTool, KindCompaction, KindGeneration:
	default:
		return nil
	}
	live, err := tx.Doc(LiveDef, session.DocArgs{Owner: session.ID(c)})
	if err != nil {
		return err
	}
	switch kind {
	case KindTool:
		if slot := ToolSlotOf(live, session.ID(id)); slot != nil {
			if err := FinishSlot(slot); err != nil {
				return err
			}
		}
		return RemoveNestedSlots(live, session.ID(id))
	case KindCompaction:
		message := ""
		if status, _ := outcome.Get("status"); status == "faulted" {
			message = str(objField(outcome, "error"), "message")
		} else {
			message, _ = stringField(outcome, "reason")
		}
		return FailCompactionStatus(live, session.ID(id), message)
	}
	if run := live.Obj("run"); run == nil || !sameID(run.Get("taskId"), session.ID(id)) {
		return nil
	}
	if err := convertPartial(tx, live, session.ID(c)); err != nil {
		return err
	}
	status, _ := outcome.Get("status")
	settlement := jobj("status", "unanswered")
	if status == "faulted" {
		settlement.Set("reason", "faulted")
		if e, ok := outcome.Get("error"); ok {
			if eo, ok := e.(*jv.Object); ok {
				if m, ok := eo.Get("message"); ok {
					settlement.Set("detail", m)
				}
			}
		}
	} else if reason, ok := outcome.Get("reason"); ok {
		settlement.Set("reason", reason)
	}
	return EndRun(tx, live, session.ID(id), settlement)
}
