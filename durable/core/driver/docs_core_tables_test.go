package driver_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
)

// Ports of packages/durable/test/session-tables.test.ts through driver.Session over the REAL core (docs_core_test.go rig).

var tablesNotesDoc = durable.DefineDoc(durable.DocDefinition[obj]{
	CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "test.notes", Version: 1, Initial: func() obj { return delta.JsonObjectOf("text", "") }, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
})

func (r *coreRig) createConversation(t *testing.T) durable.ConversationId {
	t.Helper()
	var id durable.ConversationId
	r.commit(t, func(tx durable.Tx) error {
		record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		id = record.Id
		return err
	})
	return id
}

func isReadAfterWrite(err error) bool {
	var target *durable.ReadAfterWrite
	return errors.As(err, &target)
}

// session-tables.test.ts:82 "allows table reads only before the first table write".
func TestCoreTableReadsAreAllowedOnlyBeforeTheFirstTableWrite(t *testing.T) {
	r := openCoreDB(t)
	conversationId := r.createConversation(t)
	r.commit(t, func(tx durable.Tx) error {
		conversation, err := tx.Conversation(conversationId)
		if err != nil || conversation == nil || conversation.Id != conversationId {
			t.Fatalf("conversation = %+v, %v", conversation, err)
		}
		conversations, err := tx.ScanConversations(durable.ConversationQuery{}, 1, nil)
		if err != nil || len(conversations.Items) != 1 || conversations.Items[0].Id != conversationId || conversations.Next != nil {
			t.Fatalf("scanConversations = %+v, %v", conversations, err)
		}
		tasks, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &conversationId}, 10, nil)
		if err != nil || len(tasks.Items) != 0 || tasks.Next != nil {
			t.Fatalf("scanTasks = %+v, %v", tasks, err)
		}
		entries, err := tx.ScanEntries(durable.EntryQuery{ConversationId: conversationId}, 10, nil)
		if err != nil || len(entries.Items) != 0 || entries.Next != nil {
			t.Fatalf("scanEntries = %+v, %v", entries, err)
		}
		if _, err := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "note"}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Conversation(conversationId); !isReadAfterWrite(err) {
			t.Fatalf("conversation after write: %v", err)
		}
		if _, err := tx.Task(1); err == nil || !strings.Contains(err.Error(), "Tx.task() cannot read tables after the first table write") {
			t.Fatalf("task after write: %v", err)
		}
		if _, err := tx.Entry(1); !isReadAfterWrite(err) {
			t.Fatalf("entry after write: %v", err)
		}
		if _, err := tx.ScanConversations(durable.ConversationQuery{}, 10, nil); !isReadAfterWrite(err) {
			t.Fatalf("scanConversations after write: %v", err)
		}
		if _, err := tx.ScanEntries(durable.EntryQuery{ConversationId: conversationId}, 10, nil); !isReadAfterWrite(err) {
			t.Fatalf("scanEntries after write: %v", err)
		}
		// Document access remains available after table writes.
		o, err := tx.Doc(tablesNotesDoc, conversationId)
		if err != nil {
			return err
		}
		return o.Set("text", "after write")
	})
}

// session-tables.test.ts:104 "passes caller-selected limits and cursors through table scans".
func TestCoreTableScansPassLimitsAndCursors(t *testing.T) {
	r := openCoreDB(t)
	ids := []durable.ConversationId{r.createConversation(t), r.createConversation(t), r.createConversation(t)}
	r.commit(t, func(tx durable.Tx) error {
		first, err := tx.ScanConversations(durable.ConversationQuery{}, 2, nil)
		if err != nil || len(first.Items) != 2 || first.Items[0].Id != ids[0] || first.Items[1].Id != ids[1] || first.Next == nil {
			t.Fatalf("first page = %+v, %v", first, err)
		}
		second, err := tx.ScanConversations(durable.ConversationQuery{}, 2, *first.Next)
		if err != nil || len(second.Items) != 1 || second.Items[0].Id != ids[2] || second.Next != nil {
			t.Fatalf("second page = %+v, %v", second, err)
		}
		return nil
	})
}

// A core error named after a Pi error class matches the same Go type the in-process Session returns (errors.ts instanceof).
func TestRemoteErrorsMatchPiErrorClasses(t *testing.T) {
	var rw *durable.ReadAfterWrite
	if err := error(&driver.CoreError{Name: "ReadAfterWrite", Message: "Tx.scanEntries() cannot read tables after the first table write"}); !errors.As(err, &rw) || rw.Method != "scanEntries" {
		t.Fatalf("ReadAfterWrite: %v %+v", err, rw)
	}
	var busy *durable.ConversationBusy
	if err := error(&driver.CoreError{Name: "ConversationBusy", Message: "Conversation 7 is busy"}); !errors.As(err, &busy) || busy.ConversationId != 7 {
		t.Fatalf("ConversationBusy: %v %+v", err, busy)
	}
	// The structured conversationId wins over the message.
	id := int64(9)
	if err := error(&driver.CoreError{Name: "ConversationBusy", Message: "changed text", ConversationID: &id}); !errors.As(err, &busy) || busy.ConversationId != 9 {
		t.Fatalf("ConversationBusy by field: %v %+v", err, busy)
	}
	if err := error(&driver.CoreError{Name: "Error", Message: "x"}); errors.As(err, &rw) && rw.Method == "x" {
		t.Fatal("a plain Error is not a ReadAfterWrite")
	}
}

// session-tables.test.ts:132 "creates conversations, entries, and tasks with minted IDs", without the task: the real core
// schedules a created task and runs its phases on the host, which this rig does not provide.
func TestCoreCreatesConversationsAndEntriesWithMintedIDs(t *testing.T) {
	r := openCoreDB(t)
	var conversation durable.ConversationRecord
	var first, headed durable.EntryRecord
	r.commit(t, func(tx durable.Tx) error {
		var err error
		if conversation, err = tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}); err != nil {
			return err
		}
		if first, err = tx.AppendEntry(conversation.Id, durable.EntryDraft{Kind: "note", Data: "one"}); err != nil {
			return err
		}
		headed, err = tx.AppendEntry(conversation.Id, durable.EntryDraft{Kind: "summary", HeadSelf: true})
		return err
	})
	if ids := map[int64]bool{int64(conversation.Id): true, int64(first.Id): true, int64(headed.Id): true}; len(ids) != 3 {
		t.Fatal("minted IDs are distinct")
	}
	if headed.Head == nil || *headed.Head != headed.Id {
		t.Fatalf("head self is the entry's own ID: %+v", headed)
	}
	if first.ConversationId != conversation.Id || first.Kind != "note" || first.Data != "one" {
		t.Fatalf("first entry = %+v", first)
	}
	r.commit(t, func(tx durable.Tx) error {
		stored, err := tx.Entry(headed.Id)
		if err != nil || stored == nil || stored.Id != headed.Id || stored.Kind != "summary" || stored.Head == nil || *stored.Head != headed.Id {
			t.Fatalf("stored entry = %+v, %v", stored, err)
		}
		return nil
	})
	err := func() error {
		_, err := r.s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
			_, err := tx.AppendEntry(12345, durable.EntryDraft{Kind: "note"})
			return nil, err
		})
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "Conversation 12345 does not exist") {
		t.Fatalf("append to a missing conversation: %v", err)
	}
}

type workHooks struct{}

var workTask = durable.DefineTask(durable.TaskDefinition[obj, obj, obj, workHooks]{
	Name: "test.work", Version: 1,
	Initial: func(obj) obj { return delta.JsonObjectOf("phase", "start") },
	Phases: map[string]durable.PhaseHandler[obj, obj, obj, workHooks]{
		"start": func(context.Context, durable.RunningTask[obj, obj, obj], durable.TaskRuntime[obj, obj, obj, workHooks]) error {
			return nil
		},
	},
})

// holdPhases parks every phase until the test ends: the real core schedules a created task and the host runs its phases, so a
// table test that reads the task back needs a handler that does not settle it.
func holdPhases(ctx context.Context, _ driver.PhaseEffect) error {
	<-ctx.Done()
	return nil
}

func conversationOwned(id durable.ConversationId) durable.TaskOptions {
	return durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &id}
}

// session-tables.test.ts:188 "creates a conversation with an explicitly staged task owner" (the setTask half needs the internal Tx).
func TestCoreCreatesAConversationWithAnExplicitlyStagedTaskOwner(t *testing.T) {
	r := openCoreDBWith(t, holdPhases)
	parentId := r.createConversation(t)
	var supervisorId durable.TaskId
	var child durable.ConversationRecord
	r.commit(t, func(tx durable.Tx) error {
		options := conversationOwned(parentId)
		options.Background = true
		var err error
		if supervisorId, err = durable.CreateTask(tx, workTask, delta.JsonObjectOf("path", "background"), options); err != nil {
			return err
		}
		child, err = tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: supervisorId}})
		return err
	})
	if child.Owner == nil || child.Owner.ConversationId != parentId || child.Owner.TaskId != supervisorId {
		t.Fatalf("child owner = %+v, want conversation %d task %d", child.Owner, parentId, supervisorId)
	}
	r.commit(t, func(tx durable.Tx) error {
		stored, err := tx.Conversation(child.Id)
		if err != nil || stored == nil || !reflect.DeepEqual(*stored, child) {
			t.Fatalf("stored conversation = %+v, %v; want %+v", stored, err, child)
		}
		return nil
	})
}

var tablesProgressDoc = durable.DefineDoc(durable.DocDefinition[obj]{
	CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "test.progress", Version: 1, Initial: func() obj { return delta.JsonObjectOf("lines", []any{}) }, CheckpointWhen: func(obj, []durable.Op, durable.CheckpointInfo) bool { return false }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeTask},
})

var tablesStepDoc = durable.DefineDocFamily(durable.DocFamilyDefinition[obj, any]{
	Kind:              "test.step",
	Version:           1,
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeTask},
	Initial:           func(any) obj { return delta.JsonObjectOf("lines", []any{}) },
	Family:            true,
	CheckpointWhen:    func(obj, []durable.Op, durable.CheckpointInfo) bool { return false },
})

// session-tables.test.ts:354 "creates a task and then its document in one transaction without ReadAfterWrite".
func TestCoreCreatesATaskAndThenItsDocumentsInOneTransaction(t *testing.T) {
	r := openCoreDBWith(t, holdPhases)
	conversationId := r.createConversation(t)
	var taskId durable.TaskId
	r.commit(t, func(tx durable.Tx) error {
		var err error
		if taskId, err = durable.CreateTask(tx, workTask, delta.JsonObjectOf("path", "a"), conversationOwned(conversationId)); err != nil {
			return err
		}
		// Validation uses the candidate task record, not a caller table read.
		o, err := tx.Doc(tablesProgressDoc, taskId)
		if err != nil {
			return err
		}
		if _, err := o.Array("lines").Push("created"); err != nil {
			return err
		}
		step, err := tx.Doc(tablesStepDoc, taskId, "one", nil)
		if err != nil {
			return err
		}
		_, err = step.Array("lines").Push("step")
		return err
	})
	got, err := durable.Snapshot[obj](t.Context(), r.s, tablesProgressDoc, taskId)
	if err != nil || got == nil || fmt.Sprint((*got).Value("lines")) != "[created]" {
		t.Fatalf("progress = %v, %v", got, err)
	}
	// A later commit mixes a table write and a task document write.
	r.commit(t, func(tx durable.Tx) error {
		if _, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}); err != nil {
			return err
		}
		o, err := tx.Doc(tablesProgressDoc, taskId)
		if err != nil {
			return err
		}
		_, err = o.Array("lines").Push("committed task")
		return err
	})
	got, err = durable.Snapshot[obj](t.Context(), r.s, tablesProgressDoc, taskId)
	if err != nil || got == nil || fmt.Sprint((*got).Value("lines")) != "[created committed task]" {
		t.Fatalf("progress after = %v, %v", got, err)
	}
}

// session-tables.test.ts:213 "rejects missing, terminal, and abort-marked conversation owners atomically", the parts reachable
// through the public Tx: a missing owner task and an owner task that finished. (The staged setTask variants need the internal Tx.)
func TestCoreRejectsMissingAndTerminalConversationOwners(t *testing.T) {
	phases := func(context.Context, driver.PhaseEffect) error { return nil } // every phase completes: the task finishes
	r := openCoreDBWith(t, phases)
	parentId := r.createConversation(t)
	err := r.fail(t, func(tx durable.Tx) error {
		_, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: 999}})
		return err
	})
	if !strings.Contains(err.Error(), "Conversation owner task 999 does not exist") {
		t.Fatalf("missing owner: %v", err)
	}
	var taskId durable.TaskId
	r.commit(t, func(tx durable.Tx) error {
		var cerr error
		taskId, cerr = durable.CreateTask(tx, workTask, delta.JsonObjectOf("path", "done"), conversationOwned(parentId))
		return cerr
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := r.s.WaitForTask(ctx, taskId); err != nil {
		t.Fatalf("the task does not finish: %v", err)
	}
	err = r.fail(t, func(tx durable.Tx) error {
		_, cerr := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskId}})
		return cerr
	})
	if !strings.Contains(err.Error(), "is terminal") {
		t.Fatalf("terminal owner: %v", err)
	}
}

// Pi's TaskDefinition requires abort(); a Go definition can leave it nil. Aborting such a task faults it with a named error (the
// native core dereferenced the missing handler and the session died: "the core panicked: invalid memory address").
func TestCoreAbortingATaskWithoutAnAbortHandlerFaultsIt(t *testing.T) {
	r := openCoreDBWith(t, holdPhases)
	parentId := r.createConversation(t)
	var taskId durable.TaskId
	r.commit(t, func(tx durable.Tx) error {
		var err error
		options := conversationOwned(parentId)
		options.Background = true
		taskId, err = durable.CreateTask(tx, workTask, delta.JsonObjectOf("path", "aborting"), options)
		return err
	})
	if _, err := r.s.AbortTask(t.Context(), taskId); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	settled, err := r.s.WaitForTask(ctx, taskId)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if settled.State.Outcome == nil || settled.State.Outcome.Error == nil {
		t.Fatalf("settled = %+v", settled)
	}
	if got := fmt.Sprintf("%+v", *settled.State.Outcome.Error); !strings.Contains(got, "has no abort handler") {
		t.Fatalf("settled = %s", got)
	}
}
