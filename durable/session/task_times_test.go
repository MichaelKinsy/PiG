package session_test

import (
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// packages/durable/src/session/transaction.ts #stampTimes (Pi 1.1.0, #10549): the Session stamps startedAt on the first change to
// running and endedAt on the change to terminal with the clock createSession was given; a value already set carries over from
// the transaction's candidate, then from the record being replaced.
func TestSetTaskStampsLifecycleTimesFromTheSessionClock(t *testing.T) {
	var clock atomic.Int64
	clock.Store(100)
	kernel := session.CreateSession(storage.NewMemoryStorage(), session.CreateSessionOptions{Now: func() float64 { return float64(clock.Load()) }})
	conversationId := sessiontest.CreateConversation(t, kernel)
	var taskId durable.TaskId
	commit(t, kernel, func(tx durable.Tx) error {
		id, err := durable.CreateTask(tx, workTask, delta.JsonObjectOf("path", "a"), conversationOwned(conversationId))
		taskId = id
		return err
	})
	read := func() session.AnyTaskRecord {
		var record session.AnyTaskRecord
		commitWith(t, kernel, func(tx *session.Transaction) error {
			record = readTask(t, tx, taskId)
			return nil
		})
		return record
	}
	if record := read(); record.StartedAt != nil || record.EndedAt != nil {
		t.Fatalf("a pending task has times %v %v", value(record.StartedAt), value(record.EndedAt))
	}

	// Two writes in one transaction: running stamps startedAt, and the terminal write keeps it and stamps endedAt.
	commitWith(t, kernel, func(tx *session.Transaction) error {
		record := readTask(t, tx, taskId)
		record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning}
		if err := tx.SetTask(record); err != nil {
			return err
		}
		clock.Store(200)
		record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}
		return tx.SetTask(record)
	})
	at := func(value float64) *float64 { return &value }
	record := read()
	if !reflect.DeepEqual([]*float64{record.StartedAt, record.EndedAt}, []*float64{at(100), at(200)}) {
		t.Fatalf("times %v %v, want 100 and 200", value(record.StartedAt), value(record.EndedAt))
	}

	// transaction.ts:955-956 `candidate?.startedAt ?? value.startedAt`: within one transaction, the time of the candidate
	// the write replaces wins over a time the written value carries.
	clock.Store(300)
	var second durable.TaskId
	commit(t, kernel, func(tx durable.Tx) error {
		id, err := durable.CreateTask(tx, workTask, delta.JsonObjectOf("path", "b"), conversationOwned(conversationId))
		second = id
		return err
	})
	commitWith(t, kernel, func(tx *session.Transaction) error {
		record := readTask(t, tx, second)
		record.State = durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning}
		if err := tx.SetTask(record); err != nil {
			return err
		}
		record.StartedAt = at(1)
		return tx.SetTask(record)
	})
	var replaced session.AnyTaskRecord
	commitWith(t, kernel, func(tx *session.Transaction) error {
		replaced = readTask(t, tx, second)
		return nil
	})
	if !reflect.DeepEqual(replaced.StartedAt, at(300)) {
		t.Fatalf("after a second write carrying another startedAt: %v, want the candidate's 300", value(replaced.StartedAt))
	}
}

// value prints a lifecycle time, or nil when it is unset.
func value(time *float64) any {
	if time == nil {
		return nil
	}
	return *time
}
