package session_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Ports packages/durable/test/session-tables.test.ts

type workHooks struct{}

var workTask = durable.DefineTask(durable.TaskDefinition[obj, obj, obj, workHooks]{
	Name:    "test.work",
	Version: 1,
	Initial: func(obj) obj { return obj{"phase": "start"} },
	Phases: map[string]durable.PhaseHandler[obj, obj, obj, workHooks]{
		"start": func(_ ctxT, _ durable.RunningTask[obj, obj, obj], _ durable.TaskRuntime[obj, obj, obj, workHooks]) error {
			return nil
		},
		"next": func(_ ctxT, _ durable.RunningTask[obj, obj, obj], _ durable.TaskRuntime[obj, obj, obj, workHooks]) error {
			return nil
		},
	},
	Abort: func(_ ctxT, _ durable.RunningTask[obj, obj, obj], _ durable.TaskRuntime[obj, obj, obj, workHooks]) error {
		return nil
	},
})

var tablesProgressDoc = defineDoc("test.progress", 1, taskScope, func() obj { return obj{"lines": []any{}} })

var tablesStepDoc = defineFamily("test.step", 1, taskScope, func(any) obj { return obj{"lines": []any{}} })

var tablesNotesDoc = defineDoc("test.notes", 1, rewindableScope(durable.ForkAsOf), func() obj { return obj{"text": ""} })

func conversationOwned(conversationId durable.ConversationId) durable.TaskOptions {
	return durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, ConversationId: &conversationId}
}

func terminalTask(task session.AnyTaskRecord) session.AnyTaskRecord {
	var result any = obj{"ok": true}
	return session.AnyTaskRecord{
		Id: task.Id, ConversationId: task.ConversationId, Kind: task.Kind, Version: task.Version, Input: task.Input,
		Background: task.Background, AbortRequested: task.AbortRequested,
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{
			Status:  durable.TaskTerminal,
			Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result},
		},
	}
}

func pendingTask(id durable.TaskId, conversationId durable.ConversationId, path string, abortRequested bool) session.AnyTaskRecord {
	var checkpoint any = obj{"phase": "start"}
	return session.AnyTaskRecord{
		Id: id, ConversationId: conversationId, Kind: "test.work", Version: 1, Input: obj{"path": path},
		AbortRequested: abortRequested,
		State:          durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint},
	}
}

func createWorkTask(t *testing.T, harness sessiontest.Harness, conversationId durable.ConversationId, withDocument bool) durable.TaskId {
	t.Helper()
	var taskId durable.TaskId
	commit(t, harness.Session, func(tx durable.Tx) error {
		id, err := durable.CreateTask(tx, workTask, obj{"path": "a"}, conversationOwned(conversationId))
		taskId = id
		if err != nil || !withDocument {
			return err
		}
		_, err = mustDoc(t, tx, tablesProgressDoc, id).Array("lines").Push("started")
		return err
	})
	return taskId
}

func readTask(t *testing.T, tx *session.Transaction, id durable.TaskId) session.AnyTaskRecord {
	t.Helper()
	task, err := tx.Task(id)
	if err != nil || task == nil {
		t.Fatalf("task %d: %v", id, err)
	}
	return *task
}

func isReadAfterWrite(err error) bool {
	var target *durable.ReadAfterWrite
	return errors.As(err, &target)
}

func TestSessionTransactionTables(t *testing.T) {
	t.Run("allows table reads only before the first table write", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		commit(t, harness.Session, func(tx durable.Tx) error {
			conversation, err := tx.Conversation(conversationId)
			must(t, err)
			expectEqual(t, *conversation, durable.ConversationRecord{Id: conversationId})
			conversations, err := tx.ScanConversations(durable.ConversationQuery{}, 1, nil)
			must(t, err)
			expectEqual(t, conversations.Items, []durable.ConversationRecord{{Id: conversationId}})
			if conversations.Next != nil {
				t.Fatal("a complete scan has no continuation")
			}
			tasks, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &conversationId}, 10, nil)
			must(t, err)
			entries, err := tx.ScanEntries(durable.EntryQuery{ConversationId: conversationId}, 10, nil)
			must(t, err)
			if len(tasks.Items) != 0 || len(entries.Items) != 0 || tasks.Next != nil || entries.Next != nil {
				t.Fatal("no tasks or entries yet")
			}
			_, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "note"})
			must(t, err)
			if _, err := tx.Conversation(conversationId); !isReadAfterWrite(err) {
				t.Fatalf("conversation after write: %v", err)
			}
			_, err = tx.Task(1)
			expectErrorContains(t, err, "Tx.task() cannot read tables after the first table write")
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
			return mustDoc(t, tx, tablesNotesDoc, conversationId).Set("text", "after write")
		})
		expectEqual(t, snapshot(t, harness.Session, tablesNotesDoc, conversationId), obj{"text": "after write"})
	})

	t.Run("passes caller-selected limits and cursors through table scans", func(t *testing.T) {
		harness := open()
		ids := []durable.ConversationId{createConversation(t, harness), createConversation(t, harness), createConversation(t, harness)}
		commit(t, harness.Session, func(tx durable.Tx) error {
			first, err := tx.ScanConversations(durable.ConversationQuery{}, 2, nil)
			must(t, err)
			expectEqual(t, []durable.ConversationId{first.Items[0].Id, first.Items[1].Id}, ids[:2])
			if first.Next == nil {
				t.Fatal("a partial scan continues")
			}
			second, err := tx.ScanConversations(durable.ConversationQuery{}, 2, *first.Next)
			must(t, err)
			if len(second.Items) != 1 || second.Items[0].Id != ids[2] || second.Next != nil {
				t.Fatalf("second page %+v", second)
			}
			return nil
		})
	})

	t.Run("treats synchronous setTask as the first table write", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		taskId := createWorkTask(t, harness, conversationId, false)
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			must(t, tx.SetTask(readTask(t, tx, taskId)))
			if _, err := tx.Task(taskId); !isReadAfterWrite(err) {
				t.Fatalf("task after setTask: %v", err)
			}
			return nil
		})
	})

	t.Run("creates conversations, entries, and tasks with minted IDs", func(t *testing.T) {
		harness := open()
		var conversation durable.ConversationRecord
		var first, headed durable.EntryRecord
		var taskId durable.TaskId
		commit(t, harness.Session, func(tx durable.Tx) error {
			var err error
			conversation, err = tx.CreateConversation(ownerless())
			must(t, err)
			first, err = tx.AppendEntry(conversation.Id, durable.EntryDraft{Kind: "note", Data: "one"})
			must(t, err)
			headed, err = tx.AppendEntry(conversation.Id, durable.EntryDraft{Kind: "summary", HeadSelf: true})
			must(t, err)
			options := conversationOwned(conversation.Id)
			options.Background = true
			taskId, err = durable.CreateTask(tx, workTask, obj{"path": "x"}, options)
			return err
		})
		ids := map[int64]bool{int64(conversation.Id): true, int64(first.Id): true, int64(headed.Id): true, int64(taskId): true}
		if len(ids) != 4 {
			t.Fatal("minted IDs are distinct")
		}
		if headed.Head == nil || *headed.Head != headed.Id {
			t.Fatal("head self is the entry's own ID")
		}
		expectEqual(t, first, durable.EntryRecord{Id: first.Id, ConversationId: conversation.Id, Kind: "note", Data: "one"})
		stored, err := harness.Storage.Entry(ctx, headed.Id)
		must(t, err)
		expectEqual(t, stored.Entry, headed)
		task, err := harness.Storage.Task(ctx, taskId)
		must(t, err)
		var checkpoint any = obj{"phase": "start"}
		expectEqual(t, *task, session.AnyTaskRecord{
			Id: taskId, ConversationId: conversation.Id, Kind: "test.work", Version: 1, Input: obj{"path": "x"},
			Background: true, State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &checkpoint},
		})
		flush(harness)
		changes := harness.Publications.Last().Changes
		if len(changes) != 4 {
			t.Fatalf("changes %d", len(changes))
		}
		admitted := harness.Storage.AdmittedCommits()
		last := admitted[len(admitted)-1]
		kinds := map[string]int{}
		for _, change := range changes {
			kinds[durable.CommitChangeType(change)]++
			found := false
			for _, write := range last {
				if equal(write, change) {
					found = true
				}
			}
			if !found {
				t.Fatalf("change %T is not an admitted write", change)
			}
		}
		expectEqual(t, kinds, map[string]int{"conversation": 1, "entry": 2, "task": 1})
		for _, change := range changes {
			switch typed := change.(type) {
			case durable.ConversationWrite:
				expectEqual(t, typed.Value, conversation)
			case durable.EntryWrite:
				if !equal(typed.Value, first) && !equal(typed.Value, headed) {
					t.Fatal("entry change is a created entry")
				}
			}
		}
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := durable.CreateTask(tx, workTask, obj{"path": "x"}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
			return err
		})
		expectErrorContains(t, err, "requires options.conversationId")
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.AppendEntry(12345, durable.EntryDraft{Kind: "note"})
			return err
		})
		expectErrorContains(t, err, "Conversation 12345 does not exist")
	})

	t.Run("creates a conversation with an explicitly staged task owner", func(t *testing.T) {
		harness := open()
		parentId := createConversation(t, harness)
		var supervisorId durable.TaskId
		var child durable.ConversationRecord
		commit(t, harness.Session, func(tx durable.Tx) error {
			options := conversationOwned(parentId)
			options.Background = true
			var err error
			supervisorId, err = durable.CreateTask(tx, workTask, obj{"path": "background"}, options)
			must(t, err)
			child, err = tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: supervisorId}})
			return err
		})
		expectEqual(t, *child.Owner, durable.ConversationOwner{ConversationId: parentId, TaskId: supervisorId})
		stored, err := harness.Storage.Conversation(ctx, child.Id)
		must(t, err)
		expectEqual(t, *stored, child)
		err = tryCommitWith(harness.Session, func(tx *session.Transaction) error {
			supervisor := readTask(t, tx, supervisorId)
			supervisor.ConversationId = child.Id
			return tx.SetTask(supervisor)
		})
		expectErrorContains(t, err, fmt.Sprintf("Task %d cannot change conversations", supervisorId))
		task, err := harness.Storage.Task(ctx, supervisorId)
		must(t, err)
		if task.ConversationId != parentId {
			t.Fatal("the rejected move is not persisted")
		}
	})

	t.Run("rejects missing, terminal, and abort-marked conversation owners atomically", func(t *testing.T) {
		harness := open()
		parentId := createConversation(t, harness)
		var movedStagedTaskId durable.TaskId
		err := tryCommitWith(harness.Session, func(tx *session.Transaction) error {
			id, err := durable.CreateTask(tx, workTask, obj{"path": "move"}, conversationOwned(parentId))
			must(t, err)
			movedStagedTaskId = id
			return tx.SetTask(pendingTask(id, 998, "move", false))
		})
		expectErrorContains(t, err, "cannot change conversations")
		if task, _ := harness.Storage.Task(ctx, movedStagedTaskId); task != nil {
			t.Fatal("the rejected task is not persisted")
		}

		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: 999}})
			return err
		})
		expectErrorContains(t, err, "Conversation owner task 999 does not exist")

		var rejectedChildId durable.ConversationId
		err = tryCommitWith(harness.Session, func(tx *session.Transaction) error {
			supervisorId, err := durable.CreateTask(tx, workTask, obj{"path": "aborting"}, conversationOwned(parentId))
			must(t, err)
			child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: supervisorId}})
			must(t, err)
			rejectedChildId = child.Id
			return tx.SetTask(pendingTask(supervisorId, parentId, "aborting", true))
		})
		expectErrorContains(t, err, "is abort-marked")
		if rejectedChildId == 0 {
			t.Fatal("expected a rejected child ID")
		}
		if conversation, _ := harness.Storage.Conversation(ctx, rejectedChildId); conversation != nil {
			t.Fatal("the rejected child is not persisted")
		}

		err = tryCommitWith(harness.Session, func(tx *session.Transaction) error {
			supervisorId, err := durable.CreateTask(tx, workTask, obj{"path": "terminal"}, conversationOwned(parentId))
			must(t, err)
			_, err = tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: supervisorId}})
			must(t, err)
			return tx.SetTask(terminalTask(pendingTask(supervisorId, parentId, "terminal", false)))
		})
		expectErrorContains(t, err, "is terminal")

		terminalOwnerId := createWorkTask(t, harness, parentId, false)
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			return tx.SetTask(terminalTask(readTask(t, tx, terminalOwnerId)))
		})
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: terminalOwnerId}})
			return err
		})
		expectErrorContains(t, err, "is terminal")
	})

	t.Run("takes ownership of table JSON and rejects non-strict values", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		nested := obj{"value": 1}
		payload := obj{"nested": nested}
		var entry durable.EntryRecord
		commit(t, harness.Session, func(tx durable.Tx) error {
			var err error
			entry, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "data", Data: payload})
			nested["value"] = 2
			return err
		})
		stored, err := harness.Storage.Entry(ctx, entry.Id)
		must(t, err)
		expectEqual(t, stored.Entry.Data, obj{"nested": obj{"value": 1}})
		var omitted durable.EntryRecord
		commit(t, harness.Session, func(tx durable.Tx) error {
			var err error
			omitted, err = tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "omitted"})
			return err
		})
		if omitted.Data != nil {
			t.Fatal("absent data stays absent")
		}
		storedOmitted, err := harness.Storage.Entry(ctx, omitted.Id)
		must(t, err)
		if storedOmitted.Entry.Data != nil {
			t.Fatal("absent data stays absent in Storage")
		}
		commits := len(harness.Storage.Commits())
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.AppendEntry(conversationId, durable.EntryDraft{Kind: "invalid", Data: math.NaN()})
			return err
		})
		expectStrictJSON(t, err)
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("a rejected append writes nothing")
		}
	})

	t.Run("replaces task records completely", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		taskId := createWorkTask(t, harness, conversationId, false)
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			task := readTask(t, tx, taskId)
			var checkpoint any = obj{"phase": "next", "step": 2}
			return tx.SetTask(session.AnyTaskRecord{
				Id: task.Id, ConversationId: task.ConversationId, Kind: task.Kind, Version: task.Version, Input: task.Input,
				Background: task.Background, AbortRequested: task.AbortRequested,
				State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &checkpoint},
				Memos: map[string]durable.JsonValue{"choice": "b"},
			})
		})
		task, err := harness.Storage.Task(ctx, taskId)
		must(t, err)
		expectEqual(t, task.State.Status, durable.TaskRunning)
		expectEqual(t, *task.State.Checkpoint, obj{"phase": "next", "step": 2})
		expectEqual(t, task.Memos, map[string]any{"choice": "b"})
	})

	t.Run("creates a task and then its document in one transaction without ReadAfterWrite", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		var taskId durable.TaskId
		commit(t, harness.Session, func(tx durable.Tx) error {
			var err error
			taskId, err = durable.CreateTask(tx, workTask, obj{"path": "a"}, conversationOwned(conversationId))
			must(t, err)
			// Validation uses the candidate task record, not a caller table read.
			_, err = mustDoc(t, tx, tablesProgressDoc, taskId).Array("lines").Push("created")
			must(t, err)
			_, err = mustDoc(t, tx, tablesStepDoc, taskId, "one", nil).Array("lines").Push("step")
			return err
		})
		expectEqual(t, snapshot(t, harness.Session, tablesProgressDoc, taskId), obj{"lines": []any{"created"}})
		flush(harness)
		publication := harness.Publications.Last()
		hasTask := false
		for _, change := range publication.Changes {
			if task, ok := change.(durable.TaskWrite); ok && task.Value.Id == taskId {
				hasTask = true
			}
		}
		if !hasTask {
			t.Fatal("the publication carries the task")
		}
		documents := sessiontest.DocumentChanges(publication)
		if len(documents) != 2 {
			t.Fatalf("documents %d", len(documents))
		}
		// Task documents derive their conversation from the task record.
		for _, document := range documents {
			if document.ConversationId == nil || *document.ConversationId != conversationId {
				t.Fatal("task document publication names its task's conversation")
			}
		}
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := tx.CreateConversation(ownerless())
			must(t, err)
			_, err = mustDoc(t, tx, tablesProgressDoc, taskId).Array("lines").Push("committed task")
			return err
		})
		flush(harness)
		document := sessiontest.DocumentChanges(harness.Publications.Last())[0]
		if document.ConversationId == nil || *document.ConversationId != conversationId {
			t.Fatal("a committed task's document names its conversation")
		}
	})

	t.Run("rejects task documents after a terminal candidate", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		taskId := createWorkTask(t, harness, conversationId, true)
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			task := readTask(t, tx, taskId)
			progress := mustDoc(t, tx, tablesProgressDoc, taskId)
			must(t, tx.SetTask(terminalTask(task)))
			_, err := tx.Doc(tablesProgressDoc, taskId)
			expectErrorContains(t, err, fmt.Sprintf("Task %d is terminal", taskId))
			_, err = tx.Doc(tablesStepDoc, taskId, "late", nil)
			expectErrorContains(t, err, fmt.Sprintf("Task %d is terminal", taskId))
			expectErrorContains(t, tx.SetTask(task), "terminal candidate")
			_, err = progress.Array("lines").Push("final")
			return err
		})
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(tablesProgressDoc, taskId)
			return err
		})
		expectErrorContains(t, err, fmt.Sprintf("Task %d is terminal", taskId))
	})

	t.Run("retires task documents at terminal settlement, including documents created in the same transaction", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		taskId := createWorkTask(t, harness, conversationId, true)
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := mustDoc(t, tx, tablesStepDoc, taskId, "committed", nil).Array("lines").Push("x")
			return err
		})
		flush(harness)
		published := harness.Publications.Len()
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			task := readTask(t, tx, taskId)
			_, err := mustDoc(t, tx, tablesStepDoc, taskId, "new", nil).Array("lines").Push("created then retired")
			must(t, err)
			return tx.SetTask(terminalTask(task))
		})
		writes := harness.Storage.LastCommit()
		if len(writes) != 5 || len(writesOfType(writes, "task")) != 1 {
			t.Fatalf("writes %d", len(writes))
		}
		creations := writesOfType(writes, "document.create")
		retirements := writesOfType(writes, "document.retire")
		if len(creations) != 1 || len(retirements) != 3 {
			t.Fatalf("creations %d retirements %d", len(creations), len(retirements))
		}
		created := creations[0].(durable.DocumentCreateWrite).Record.Id
		retiredCreated := false
		for _, retirement := range retirements {
			if retirement.(durable.DocumentRetireWrite).Id == created {
				retiredCreated = true
			}
		}
		if !retiredCreated {
			t.Fatal("the same-transaction document retires")
		}
		flush(harness)
		if harness.Publications.Len() != published+1 {
			t.Fatal("one publication")
		}
		publication := harness.Publications.Last()
		tasks := 0
		for _, change := range publication.Changes {
			if _, ok := change.(durable.TaskWrite); ok {
				tasks++
			}
		}
		documents := sessiontest.DocumentChanges(publication)
		if tasks != 1 || len(documents) != 3 {
			t.Fatalf("tasks %d documents %d", tasks, len(documents))
		}
		for _, document := range documents {
			if document.Value != nil || len(document.Ops) != 0 {
				t.Fatal("retirement publishes null with no ops")
			}
			if document.ConversationId == nil || *document.ConversationId != conversationId {
				t.Fatal("retirement names the task's conversation")
			}
		}
		if snapshot(t, harness.Session, tablesProgressDoc, taskId) != nil || snapshot(t, harness.Session, tablesStepDoc, taskId, "committed") != nil {
			t.Fatal("retired task documents are absent")
		}
		alive, err := harness.Storage.ScanDocuments(ctx, durable.DocumentQuery{Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: taskId}, At: durable.CurrentPoint}, 10, nil)
		must(t, err)
		if len(alive.Items) != 0 {
			t.Fatal("no task document stays alive")
		}
		err = tryCommitWith(harness.Session, func(tx *session.Transaction) error {
			return tx.SetTask(terminalTask(readTask(t, tx, taskId)))
		})
		expectErrorContains(t, err, fmt.Sprintf("Task %d is already terminal", taskId))
	})

	t.Run("validates document owners", func(t *testing.T) {
		harness := open()
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(tablesProgressDoc, durable.TaskId(4242))
			return err
		})
		expectErrorContains(t, err, "Task 4242 does not exist")
		err = tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(tablesNotesDoc, durable.ConversationId(4242))
			return err
		})
		expectErrorContains(t, err, "Conversation 4242 does not exist")
	})
}
