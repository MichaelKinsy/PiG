// Ports the parked-work helpers of packages/durable/src/harness/scheduler.ts (parked, asleep, reserved, pendingAgain,
// closeSignal) and the two predicates of src/harness/tool.ts the Harness hands the scheduler (gatedCall, keepsChildren),
// spec suspendable §2.

package harness

import (
	"context"
	"maps"

	"github.com/MichaelKinsy/PiG/durable"
)

// parked reports whether a live task holds no invocation while it waits: sleeping, waiting, or holding its outcome.
func parked(record anyTaskRecord) bool {
	status := record.State.Status
	return status == durable.TaskSleeping || status == durable.TaskWaiting || status == durable.TaskCompleting
}

// asleepAt reports a sleeping record that cannot wake yet: no abort mark, no listed key with an unused value, until not
// reached.
func asleepAt(record anyTaskRecord, now float64) bool {
	state := record.State
	return state.Status == durable.TaskSleeping && !record.AbortRequested && !durable.SleepDue(state, record.Memos, now)
}

// asleepLocked reports a sleeping record that cannot wake yet (asleepAt), unless a gate wake runs it before its time.
func (s *TaskScheduler) asleepLocked(record anyTaskRecord, now float64) bool {
	if !asleepAt(record, now) {
		return false
	}
	_, woken := s.gateWakes[record.Id]
	return !woken
}

// wakeGateLocked wakes a call parked at a gate, so its gates are asked again; a sleep whose deadline passed takes the
// deadline path when reserved (wokeLocked). Other records are left alone.
func (s *TaskScheduler) wakeGateLocked(record anyTaskRecord) {
	if !s.options.Gated(record) {
		return
	}
	s.gateWakes[record.Id] = struct{}{}
	s.runnable.Add(record.Id)
}

// reserved is the record a reservation writes: running at the checkpoint, with the wake that ends its wait or sleep and the
// signal memos it marks; a task written back as pending keeps the wake it woke with.
func reserved(record anyTaskRecord, woke *woke) anyTaskRecord {
	state := record.State
	if woke != nil {
		memos := make(map[string]durable.JsonValue, len(record.Memos)+len(woke.memos))
		maps.Copy(memos, record.Memos)
		maps.Copy(memos, woke.memos)
		woken := record
		woken.Memos = memos
		return withState(woken, anyTaskState{Status: durable.TaskRunning, Checkpoint: state.Checkpoint, Wake: &woke.wake})
	}
	if state.Status == durable.TaskPending || state.Status == durable.TaskRunning {
		state.Status = durable.TaskRunning
		return withState(record, state)
	}
	return withState(record, anyTaskState{Status: durable.TaskRunning, Checkpoint: state.Checkpoint})
}

// pendingAgain is the record written back as pending (open, close, handover): its checkpoint, and the wake it woke with,
// if any.
func pendingAgain(record anyTaskRecord, running anyTaskState) anyTaskRecord {
	running.Status = durable.TaskPending
	return withState(record, running)
}

// closeSignalKey carries a Harness's close signal on the context of its task invocations.
type closeSignalKey struct{}

// withCloseSignal makes closed the signal closeSignal returns for every task runtime whose Signal derives from ctx: a
// WeakMap from a runtime to the Harness's signal upstream (scheduler.ts closeSignals). A Go runtime reaches a handler
// wrapped by its task definition's types, so the association rides the runtime's Signal, as providerSessionIds does.
func withCloseSignal(ctx, closed context.Context) context.Context {
	return context.WithValue(ctx, closeSignalKey{}, closed)
}

// closeSignal is cancelled once the Harness running runtime's task closes, or its Session fails, unlike the runtime's own
// Signal, which an abort mark cancels too: for cleanup a run does after its own abort mark, such as a tool's Cancel.
func closeSignal(runtime interface{ Signal() context.Context }) context.Context {
	closed, ok := runtime.Signal().Value(closeSignalKey{}).(context.Context)
	if !ok {
		panic("durable/harness: the task runtime has no close signal")
	}
	return closed
}

// gatedCall reports whether a sleeping task is a tool call parked at its gates, before its intent (spec suspendable §5.2):
// the registry, and every open, wake it, so the gates installed then decide (tool.ts gatedCall).
func gatedCall(record anyTaskRecord) bool {
	return record.Kind == toolTaskKind && record.State.Status == durable.TaskSleeping && phaseOf(record.State.Checkpoint) == string(toolPhaseCall)
}

// keepsChildren reports whether a task's AbandonOnRestart children survive a restart (spec suspendable §2.5): it is a tool
// call that was not cut off while running, parked or not started yet, whatever its status (tool.ts keepsChildren).
func keepsChildren(record anyTaskRecord) bool {
	if record.Kind != toolTaskKind {
		return false
	}
	// A tool task's checkpoint; absent once it holds or has its outcome.
	phase := phaseOf(record.State.Checkpoint)
	return phase == string(toolPhaseCall) || phase == "parked"
}
