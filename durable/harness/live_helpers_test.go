package harness

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// live.ts:84-163 are the pi.live helpers every Harness phase uses on a draft of the document. The cases below are read from those lines.

type settleRecorder struct {
	durable.Tx
	settled    []durable.SubmissionId
	settlement durable.SubmissionSettlement
}

func (recorder *settleRecorder) SettleSubmission(id durable.SubmissionId, settlement durable.SubmissionSettlement) error {
	recorder.settled = append(recorder.settled, id)
	recorder.settlement = settlement
	return nil
}

func liveDraftOf(members ...any) *delta.Object {
	return delta.Track(delta.JsonObjectOf(members...)).BeginChange().State()
}

func busyLive() *delta.Object {
	return liveDraftOf(
		"run", delta.JsonObjectOf("taskId", float64(5), "inputs", []any{float64(11), float64(12)}),
		"generation", delta.JsonObjectOf("attempt", float64(1)),
		"tools", []any{delta.JsonObjectOf("callId", "c1", "name", "read", "status", "running")},
	)
}

// endRun (live.ts:88-95): the run's inputs settle and `run` goes only when taskId owns the run, but `generation` and `tools` always go.
//
// mutation-checked: ending any task's run, keeping generation, keeping tools, skipping the settlement and keeping run each fail a case.
func TestEndRunSettlesAndRemovesTheRunOnlyForItsOwner(t *testing.T) {
	settlement := durable.SubmissionSettlement{Status: durable.SubmissionUnanswered, Reason: "faulted"}
	for _, tc := range []struct {
		name        string
		taskId      durable.TaskId
		wantSettled []durable.SubmissionId
		wantRun     bool
	}{
		{"the owner", 5, []durable.SubmissionId{11, 12}, false},
		{"another task", 9, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live, tx := busyLive(), &settleRecorder{}
			if err := EndRun(tx, live, tc.taskId, settlement); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(tx.settled, tc.wantSettled) || (len(tx.settled) > 0 && tx.settlement != settlement) {
				t.Fatalf("settled %v with %+v, want %v with %+v", tx.settled, tx.settlement, tc.wantSettled, settlement)
			}
			if live.Has("run") != tc.wantRun {
				t.Fatalf("run present = %v, want %v", live.Has("run"), tc.wantRun)
			}
			if live.Has("generation") || live.Has("tools") {
				t.Fatalf("generation or tools survived the end of a run: generation=%v tools=%v", live.Has("generation"), live.Has("tools"))
			}
		})
	}
}

// compactionStatus (live.ts:105-107) finds the status of the given task, not the first one.
//
// mutation-checked: matching any status fails it.
func TestFindCompactionStatusMatchesTheTaskID(t *testing.T) {
	live := liveDraftOf("compactions", []any{
		delta.JsonObjectOf("taskId", float64(3), "attempt", float64(1)),
		delta.JsonObjectOf("taskId", float64(7), "attempt", float64(2)),
	})
	status := FindCompactionStatus(live, 7)
	if status == nil || status.Get("attempt") != float64(2) {
		t.Fatalf("status of task 7 = %v, want the second one", status)
	}
	if FindCompactionStatus(live, 8) != nil {
		t.Fatal("a task with no status finds one")
	}
	if FindCompactionStatus(liveDraftOf(), 7) != nil {
		t.Fatal("a document without compactions finds one")
	}
}

// clearProgress (live.ts:127-133) removes exactly what a running tool published and nothing else of the slot.
//
// mutation-checked: dropping any one of the five deletions fails it.
func TestClearProgressRemovesWhatARunningToolPublished(t *testing.T) {
	slot := liveDraftOf(
		"callId", "c1", "name", "read", "taskId", float64(4), "status", "running", "entry", float64(9),
		"output", "text", "droppedBytes", float64(3), "droppedLines", float64(2), "details", delta.JsonObjectOf("k", "v"), "diagnostics", []any{},
	)
	ClearProgress(slot)
	for _, key := range []string{"output", "droppedBytes", "droppedLines", "details", "diagnostics"} {
		if slot.Has(key) {
			t.Errorf("%s survived ClearProgress", key)
		}
	}
	for _, key := range []string{"callId", "name", "taskId", "status", "entry"} {
		if !slot.Has(key) {
			t.Errorf("%s was removed by ClearProgress", key)
		}
	}
}

// docTx serves one pi.live draft and counts how often the document was opened.
type docTx struct {
	settleRecorder
	live  *delta.Object
	opens int
}

func (tx *docTx) Doc(durable.AnyDocToken, ...any) (*delta.Object, error) {
	tx.opens++
	return tx.live, nil
}

func schedulerRecord(kind string, id durable.TaskId) durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	return durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{Id: id, ConversationId: 1, Kind: kind}
}

// settleSchedulerOutcome (live.ts:136-164): a faulted or orphaned task of a kind the Harness does not own never opens pi.live; a generation that does not own the run
// leaves the run, the generation and the tools alone; a compaction task's status is removed.
//
// mutation-checked: treating any kind as a run task, ignoring who owns the run, and not removing the compaction status each fail a case.
func TestSettleSchedulerOutcomeOnlyTouchesWhatTheTaskOwns(t *testing.T) {
	outcome := durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeFaulted}
	t.Run("a task of another kind never opens pi.live", func(t *testing.T) {
		tx := &docTx{live: busyLive()}
		if err := SettleSchedulerOutcome(tx, schedulerRecord("user.task", 5), outcome); err != nil {
			t.Fatal(err)
		}
		if tx.opens != 0 || len(tx.settled) != 0 || !tx.live.Has("run") || !tx.live.Has("generation") {
			t.Fatalf("opens %d, settled %v, live run/generation present %v/%v", tx.opens, tx.settled, tx.live.Has("run"), tx.live.Has("generation"))
		}
	})
	t.Run("a generation that does not own the run changes nothing", func(t *testing.T) {
		tx := &docTx{live: busyLive()}
		if err := SettleSchedulerOutcome(tx, schedulerRecord("pi.generation", 9), outcome); err != nil {
			t.Fatal(err)
		}
		if len(tx.settled) != 0 || !tx.live.Has("run") || !tx.live.Has("generation") || !tx.live.Has("tools") {
			t.Fatalf("settled %v; run/generation/tools present %v/%v/%v", tx.settled, tx.live.Has("run"), tx.live.Has("generation"), tx.live.Has("tools"))
		}
	})
	t.Run("a compaction task's status is removed with the empty list", func(t *testing.T) {
		tx := &docTx{live: liveDraftOf("compactions", []any{delta.JsonObjectOf("taskId", float64(7), "attempt", float64(1))})}
		if err := SettleSchedulerOutcome(tx, schedulerRecord("pi.compaction", 7), outcome); err != nil {
			t.Fatal(err)
		}
		if tx.live.Has("compactions") {
			t.Fatal("the status of the faulted compaction task is still listed")
		}
	})
}
