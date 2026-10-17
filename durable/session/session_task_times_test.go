package session_test

import (
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Task lifecycle times (packages/durable/src/session/transaction.ts #stampTimes, Pi 1.1.0): the Session stamps
// StartedAt on the first change to running and EndedAt on the change to terminal; a time already set carries over from
// the replaced record, and from an earlier staged candidate of the same transaction.
func TestSessionStampsTaskLifecycleTimes(t *testing.T) {
	var clock atomic.Int64
	clock.Store(10)
	controlled := sessiontest.NewControlledStorage()
	harness := sessiontest.Harness{
		Storage:      controlled,
		Session:      session.CreateSession(controlled, session.CreateSessionOptions{Now: func() float64 { return float64(clock.Load()) }}),
		Publications: &sessiontest.Publications{},
	}
	conversationId := createConversation(t, harness)
	taskId := createWorkTask(t, harness, conversationId, false)
	stored := func() session.AnyTaskRecord {
		var record session.AnyTaskRecord
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			record = readTask(t, tx, taskId)
			return nil
		})
		return record
	}
	times := func(record session.AnyTaskRecord) [2]float64 {
		started, ended := -1.0, -1.0
		if record.StartedAt != nil {
			started = *record.StartedAt
		}
		if record.EndedAt != nil {
			ended = *record.EndedAt
		}
		return [2]float64{started, ended}
	}
	withState := func(record session.AnyTaskRecord, state durable.TaskState[durable.JsonValue, durable.JsonValue]) session.AnyTaskRecord {
		record.State = state
		return record
	}
	if got := times(stored()); got != [2]float64{-1, -1} {
		t.Fatalf("a pending task has times %v", got)
	}

	// The first change to running stamps StartedAt; a pending-to-pending replacement stamps nothing.
	pending := stored()
	commitWith(t, harness.Session, func(tx *session.Transaction) error {
		return tx.SetTask(withState(pending, durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning}))
	})
	if got := times(stored()); got != [2]float64{10, -1} {
		t.Fatalf("running task times %v, want startedAt 10", got)
	}

	// A replacement built from the stored record keeps its times: a later run does not restart the span.
	running := stored()
	clock.Store(30)
	commitWith(t, harness.Session, func(tx *session.Transaction) error {
		return tx.SetTask(withState(running, durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning}))
	})
	if got := times(stored()); got != [2]float64{10, -1} {
		t.Fatalf("a record that carries its times has %v, want startedAt 10", got)
	}

	// Two changes in one transaction: the second carries the candidate's StartedAt and stamps EndedAt.
	clock.Store(40)
	fresh := pendingTask(taskId, conversationId, "again", false)
	commitWith(t, harness.Session, func(tx *session.Transaction) error {
		if err := tx.SetTask(withState(fresh, durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning})); err != nil {
			return err
		}
		clock.Store(50)
		return tx.SetTask(withState(fresh, durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status:  durable.TaskTerminal,
			Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted},
		}))
	})
	if got := times(stored()); got != [2]float64{40, 50} {
		t.Fatalf("times after two staged changes %v, want startedAt 40 (the candidate's) and endedAt 50", got)
	}
}
