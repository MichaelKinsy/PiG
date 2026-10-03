// Ports packages/durable/src/harness/live.ts.

package harness

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// ToolSlotStatus is the presentation status of one tool call of the current round.
type ToolSlotStatus string

const (
	ToolSlotPending ToolSlotStatus = "pending"
	ToolSlotRunning ToolSlotStatus = "running"
	ToolSlotDone    ToolSlotStatus = "done"
)

// ToolSlot is the presentation of one tool call of the current round (live.ts:11-31).
type ToolSlot struct {
	CallId string `json:"callId"`
	Name   string `json:"name"`
	// TaskId is nil for a call not started yet (sequential round) and for a call its request did not offer, which
	// starts done with the entry generation wrote.
	TaskId *durable.TaskId `json:"taskId,omitempty"`
	Status ToolSlotStatus  `json:"status"`
	// Output is the retained running output; DroppedBytes and DroppedLines are what the bounds dropped.
	Output       *string `json:"output,omitempty"`
	DroppedBytes *int    `json:"droppedBytes,omitempty"`
	DroppedLines *int    `json:"droppedLines,omitempty"`
	// Details is the last details() value; nil when absent.
	Details *durable.JsonValue `json:"details,omitempty"`
	// Diagnostics are those recorded through api.Diagnostic.
	Diagnostics []durable.ToolDiagnostic `json:"diagnostics,omitempty"`
	// Entry is the result entry once done; nil when the tool task faulted or was orphaned.
	Entry *durable.EntryId `json:"entry,omitempty"`
}

// RetryStatus is a durable backoff before the next attempt.
type RetryStatus struct {
	At    float64 `json:"at"`
	Error string  `json:"error"`
}

// CompactionStatus is the presentation of one live compaction task (spec §8.7, live.ts:34-43).
type CompactionStatus struct {
	TaskId durable.TaskId           `json:"taskId"`
	Reason durable.CompactionReason `json:"reason"`
	// Blocking reports whether a generation waits for it: a compaction the generation owns.
	Blocking bool `json:"blocking"`
	Attempt  int  `json:"attempt"`
	// Retry is the durable backoff before the next summarization attempt.
	Retry *RetryStatus `json:"retry,omitempty"`
}

// LiveRun is run control: the task that settles the run's inputs, and those inputs.
type LiveRun struct {
	TaskId durable.TaskId         `json:"taskId"`
	Inputs []durable.SubmissionId `json:"inputs"`
}

// DeferredStatus is a provider-side deferred response being polled.
type DeferredStatus struct {
	PollAt float64 `json:"pollAt"`
}

// LiveGeneration is the presentation of the current generation attempt.
type LiveGeneration struct {
	Attempt int `json:"attempt"`
	// Message is the committed throttled partial of the in-flight response, as JSON.
	Message durable.JsonObject `json:"message,omitempty"`
	// Retry is the durable backoff before the next attempt.
	Retry *RetryStatus `json:"retry,omitempty"`
	// Deferred is set while a provider-side deferred response is polled.
	Deferred *DeferredStatus `json:"deferred,omitempty"`
}

// LiveState is the built-in live conversation state: run control and presentation of the current generation and
// tool round (live.ts:46-62).
type LiveState struct {
	// Run is present exactly while busy.
	Run        *LiveRun        `json:"run,omitempty"`
	Generation *LiveGeneration `json:"generation,omitempty"`
	// Tools is the current round in call order, from the tool-calling answer until the generation's tools phase ends
	// it; never empty while present.
	Tools []ToolSlot `json:"tools,omitempty"`
	// Compactions lists live compaction tasks in task ID order; never empty while present.
	Compactions []CompactionStatus `json:"compactions,omitempty"`
}

// LiveDoc is the built-in pi.live document (live.ts:64-77). A complete base is stored whenever nothing runs (spec
// §8.2): no generation and no running tool slot. That bounds the delta chain to one generation or one round's tools,
// and a slot holds output only while running, so every base is small.
var LiveDoc = durable.DefineDoc(durable.DocDefinition[LiveState]{
	CommonDocDefinition: durable.CommonDocDefinition[LiveState]{
		Kind:    "pi.live",
		Version: 1,
		CheckpointWhen: func(value LiveState, _ []durable.Op, _ durable.CheckpointInfo) bool {
			if value.Generation != nil {
				return false
			}
			for _, slot := range value.Tools {
				if slot.Status == ToolSlotRunning {
					return false
				}
			}
			return true
		},
	},
	DocumentSemantics: durable.DocumentSemantics{
		Scope:   durable.ScopeConversation,
		History: durable.HistoryLatest,
		Fork:    durable.ForkInitial,
	},
	Initial: func() LiveState { return LiveState{} },
})

// Built-in task kinds that can own pi.live.run (live.ts:80-82).
var runTaskKinds = map[string]bool{"pi.generation": true}

const (
	toolTaskKind       = "pi.tool"
	compactionTaskKind = "pi.compaction"
)

// liveRunOf returns the run control of a pi.live draft; nil while idle.
func liveRunOf(live *delta.Object) (*LiveRun, error) {
	return decodeAt[LiveRun](live, "run")
}

// runTaskOf returns the task holding run control; ok is false while idle.
func runTaskOf(live *delta.Object) (durable.TaskId, bool) {
	run := live.Object("run")
	if run == nil {
		return 0, false
	}
	id, ok := run.Get("taskId").(float64)
	return durable.TaskId(id), ok
}

// EndRun ends the run owned by taskId: it settles each of its inputs and removes run. It always removes generation and
// tools, whose presentation belongs to the ending run (live.ts:88-95).
func EndRun(tx durable.Tx, live *delta.Object, taskId durable.TaskId, settlement durable.SubmissionSettlement) error {
	if owner, ok := runTaskOf(live); ok && owner == taskId {
		run, err := liveRunOf(live)
		if err != nil {
			return err
		}
		for _, id := range run.Inputs {
			if err := tx.SettleSubmission(id, settlement); err != nil {
				return err
			}
		}
		live.Delete("run")
	}
	live.Delete("generation")
	live.Delete("tools")
	return nil
}

// AddCompactionStatus adds the status of a compaction task created in this commit; statuses stay in task ID order.
func AddCompactionStatus(live *delta.Object, status CompactionStatus) error {
	statuses := live.Array("compactions")
	if statuses == nil {
		return setJSON(live, "compactions", []CompactionStatus{status})
	}
	return pushJSON(statuses, status)
}

// FindCompactionStatus returns the status draft of compaction task taskId, or nil when not listed.
func FindCompactionStatus(live *delta.Object, taskId durable.TaskId) *delta.Object {
	statuses := live.Array("compactions")
	if statuses == nil {
		return nil
	}
	for index := range statuses.Len() {
		status := statuses.Object(index)
		if status != nil && status.Get("taskId") == float64(taskId) {
			return status
		}
	}
	return nil
}

// RemoveCompactionStatus removes the status of compaction task taskId, and the list once empty.
func RemoveCompactionStatus(live *delta.Object, taskId durable.TaskId) error {
	statuses := live.Array("compactions")
	if statuses == nil {
		return nil
	}
	for index := range statuses.Len() {
		status := statuses.Object(index)
		if status != nil && status.Get("taskId") == float64(taskId) {
			if _, err := statuses.Splice(index, 1); err != nil {
				return err
			}
			break
		}
	}
	if statuses.Len() == 0 {
		live.Delete("compactions")
	}
	return nil
}

// FindToolSlot returns the slot draft of tool task taskId in the current round, or nil when the round no longer
// lists it.
func FindToolSlot(live *delta.Object, taskId durable.TaskId) *delta.Object {
	slots := live.Array("tools")
	if slots == nil {
		return nil
	}
	for index := range slots.Len() {
		slot := slots.Object(index)
		if slot != nil && slot.Get("taskId") == float64(taskId) {
			return slot
		}
	}
	return nil
}

// FinishSlot marks a slot done: the result entry, if any, now carries its running output, details, and diagnostics.
func FinishSlot(slot *delta.Object, entry *durable.EntryId) error {
	if err := slot.Set("status", string(ToolSlotDone)); err != nil {
		return err
	}
	if entry != nil {
		if err := slot.Set("entry", float64(*entry)); err != nil {
			return err
		}
	}
	ClearProgress(slot)
	return nil
}

// ClearProgress removes what a tool published while running; its result entry or a rerun replaces it.
func ClearProgress(slot *delta.Object) {
	slot.Delete("output")
	slot.Delete("droppedBytes")
	slot.Delete("droppedLines")
	slot.Delete("details")
	slot.Delete("diagnostics")
}

// SettleSchedulerOutcome is the Harness cleanup for a terminal outcome the scheduler writes itself (faulted or
// orphaned), passed to the scheduler, which does not know task kinds (spec §5.4, live.ts:136-164). A run task ends its
// run; a tool task's slot is marked done without an entry, and context derivation synthesizes the missing result; a
// compaction task's status is removed. Other kinds are ignored so it never creates pi.live elsewhere. A committed
// generation partial becomes an aborted assistant entry, exactly as in the generation abort handler, so the
// transcript keeps what the model produced and pi.usage counts its spend.
func SettleSchedulerOutcome(tx durable.Tx, record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], outcome durable.TaskOutcome[durable.JsonValue]) error {
	switch {
	case record.Kind == toolTaskKind:
		live, err := docDraft(tx, LiveDoc, record.ConversationId)
		if err != nil {
			return err
		}
		if slot := FindToolSlot(live, record.Id); slot != nil {
			return FinishSlot(slot, nil)
		}
		return nil
	case record.Kind == compactionTaskKind:
		live, err := docDraft(tx, LiveDoc, record.ConversationId)
		if err != nil {
			return err
		}
		return RemoveCompactionStatus(live, record.Id)
	case !runTaskKinds[record.Kind]:
		return nil
	}
	live, err := docDraft(tx, LiveDoc, record.ConversationId)
	if err != nil {
		return err
	}
	if owner, ok := runTaskOf(live); !ok || owner != record.Id {
		return nil
	}
	if err := ConvertPartial(tx, live, record.ConversationId); err != nil {
		return err
	}
	settlement := durable.SubmissionSettlement{Status: durable.SubmissionUnanswered}
	if outcome.Status == durable.OutcomeFaulted {
		settlement.Reason = "faulted"
		if outcome.Error != nil {
			settlement.Detail = outcome.Error.Message
		}
	} else if outcome.Reason != nil {
		settlement.Reason = *outcome.Reason
	}
	return EndRun(tx, live, record.Id, settlement)
}
