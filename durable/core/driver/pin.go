// SPDX-License-Identifier: MIT

package driver

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/durable"
)

// ErrSuspendableWorkNotInCorePin is what every member of the suspendable-work surface (Pi 473515f9c: signals, tool parks,
// wake and checkpoint, nested calls that do not wait, keyed tool tasks, gate sleeps, hook tasks, submission notify, a task
// state that waits for signals or for any task) returns on the core backend. The core is pinned to Pi 1.4.0+e7a0df27
// (docs/plan/durable-core/CONTRACT.md section 1), which has none of it; CONTRACT.md section 8 lists each row shape a pin bump
// must add. A caller tests for it with errors.Is.
var ErrSuspendableWorkNotInCorePin = errors.New("suspendable work (signals, parks, wake, checkpoint, ExecuteToolNoWait, gates, hook tasks) " +
	"is not in the Durable core's contract pin (Pi 1.4.0+e7a0df27, docs/plan/durable-core/CONTRACT.md); a pin bump adds it")

// notInPin is ErrSuspendableWorkNotInCorePin for the named member or value.
func notInPin(what string) error { return fmt.Errorf("%s: %w", what, ErrSuspendableWorkNotInCorePin) }

// checkNextState rejects a state a task commits that only suspendable work can hold: a wake, signals, a wait for any task, a
// sleep that has no time. The core's task rows have none of them (CONTRACT.md section 8); sent anyway, the core would drop or
// misread the members.
func checkNextState(next *durable.NextTaskState[durable.JsonValue, durable.JsonValue]) error {
	switch {
	case next == nil:
		return nil
	case next.Wake != nil:
		return notInPin("TaskState.Wake")
	case len(next.Signals) > 0:
		return notInPin("TaskState.Signals")
	case next.Policy == durable.JoinAny:
		return notInPin("JoinAny")
	case next.Status == durable.TaskSleeping && next.Until == nil:
		return notInPin("a sleep without Until")
	}
	return nil
}
