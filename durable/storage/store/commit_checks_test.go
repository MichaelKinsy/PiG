package store

// pi: packages/durable/src/storage/store/commit.ts
// Durable's checks of one commit (the id claims, the commit's content commands, and the copies' sources) are the checks of Pi's
// `DurableCommit`, which its public entry point does not export; the cases below drive each through StoreStorage.Commit and assert
// Durable's own message, as store-storage-commit.test.ts and the conformance suite do for the cases they cover.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

func TestStoreStorageRefusesAnIDWrittenTwiceInOneCommitAsDurableDoes(t *testing.T) {
	root := durable.ROOT_CONVERSATION_ID
	task := func(id durable.TaskId, status durable.TaskStatus) durable.StorageWrite {
		return durable.TaskWrite{Value: storedTask{
			Id: id, ConversationId: root, Kind: "k", Version: 1,
			State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: status},
		}}
	}
	t.Run("a conversation or an entry written twice, a document created twice", func(t *testing.T) {
		made := counted(t, ":memory:")
		conversation := mintID[durable.ConversationId](t, made.storage)
		entry := mintID[durable.EntryId](t, made.storage)
		document := mintID[durable.DocumentId](t, made.storage)
		create := func(id durable.DocumentId) durable.StorageWrite {
			return durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: id, Kind: "k", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}}, Content: base(1)}
		}
		for _, c := range []struct {
			name   string
			want   string
			writes []durable.StorageWrite
		}{
			{"conversation", fmt.Sprintf("ID %d is written more than once", conversation), []durable.StorageWrite{
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: conversation}},
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: conversation}},
			}},
			{"entry", fmt.Sprintf("ID %d is written more than once", entry), []durable.StorageWrite{entryWrite(entry), entryWrite(entry, "again")}},
			{"document", fmt.Sprintf("Document %d has more than one content command", document), []durable.StorageWrite{create(document), create(document)}},
		} {
			_, err := commitWrites(made.storage, c.writes...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s written twice = %v, want %q", c.name, err, c.want)
			}
		}
		must(t, made.store.Close(background))
	})

	t.Run("a task written twice as a task is one record, as two record types is refused", func(t *testing.T) {
		made := counted(t, ":memory:")
		id := mintID[durable.TaskId](t, made.storage)
		// Pi allows the same type twice for a task or a submission (the later row replaces the earlier one).
		if _, err := commitWrites(made.storage, task(id, durable.TaskPending), task(id, durable.TaskTerminal)); err != nil {
			t.Fatalf("a task written twice = %v, want stored", err)
		}
		if got := value(made.storage.Task(background, id)); got == nil || got.State.Status != durable.TaskTerminal {
			t.Fatalf("Task = %+v, want the later record (terminal)", got)
		}
		other := mintID[durable.TaskId](t, made.storage)
		_, err := commitWrites(made.storage, task(other, durable.TaskPending), durable.SubmissionWrite{Value: durable.SubmissionRecord{
			Id: durable.SubmissionId(other), ConversationId: root, Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued,
		}})
		if want := fmt.Sprintf("ID %d is written as two record types", other); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("a task and a submission of one id = %v, want %q", err, want)
		}
		must(t, made.store.Close(background))
	})
}

func TestStoreStorageRecordsACreatedDocumentsIDAsADocument(t *testing.T) {
	made := counted(t, ":memory:")
	id := mintID[durable.DocumentId](t, made.storage)
	value(commitWrites(made.storage, durable.DocumentCreateWrite{
		Record:  durable.DocumentCreate{Id: id, Kind: "k", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
		Content: base(1),
	}))
	// record_ids says which record type owns the id: a task of that id is refused with the type the document's claim recorded.
	_, err := commitWrites(made.storage, durable.TaskWrite{Value: storedTask{Id: durable.TaskId(id), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "k", Version: 1}})
	if want := fmt.Sprintf("ID %d already belongs to document", id); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("a task with a document's id = %v, want %q", err, want)
	}
	// A copy claims its id as a document too.
	child := mintID[durable.ConversationId](t, made.storage)
	source := mintID[durable.DocumentId](t, made.storage)
	copied := mintID[durable.DocumentId](t, made.storage)
	record := func(id durable.DocumentId, conversation durable.ConversationId) durable.DocumentCreate {
		return durable.DocumentCreate{
			Id: id, Kind: "c", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversation},
			History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
		}
	}
	value(commitWrites(made.storage, durable.DocumentCreateWrite{Record: record(source, durable.ROOT_CONVERSATION_ID), Content: base(1)}))
	value(commitWrites(made.storage,
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: child}},
		durable.DocumentCopyWrite{Record: record(copied, child), Source: durable.DocumentCopySource{Id: source, At: durable.CurrentPoint}},
	))
	_, err = commitWrites(made.storage, durable.EntryWrite{Value: durable.EntryRecord{Id: durable.EntryId(copied), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "m"}})
	if want := fmt.Sprintf("ID %d already belongs to document", copied); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("an entry with a copy's id = %v, want %q", err, want)
	}
	must(t, made.store.Close(background))
}

func TestStoreStorageRefusesACopyWhoseRecordDoesNotMatchItsSource(t *testing.T) {
	rewindable := func(conversation durable.ConversationId) durable.DocumentCreate {
		return durable.DocumentCreate{
			Id: 0, Kind: "c", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversation},
			History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
		}
	}
	key := func(text string) *string { return &text }
	cases := []struct {
		name   string
		source func(create durable.DocumentCreate) durable.DocumentCreate // the stored source, made from the copy's record
		change func(create *durable.DocumentCreate)                       // what the copy's record differs in
		ok     bool
	}{
		{"a matching record is copied", func(c durable.DocumentCreate) durable.DocumentCreate { return c }, func(*durable.DocumentCreate) {}, true},
		{"a matching keyed record is copied", func(c durable.DocumentCreate) durable.DocumentCreate { c.Key = key("a"); return c },
			func(c *durable.DocumentCreate) { c.Key = key("a") }, true},
		{"another kind", func(c durable.DocumentCreate) durable.DocumentCreate { return c }, func(c *durable.DocumentCreate) { c.Kind = "d" }, false},
		{"a source without a key, a copy with one", func(c durable.DocumentCreate) durable.DocumentCreate { return c },
			func(c *durable.DocumentCreate) { c.Key = key("a") }, false},
		{"a source with a key, a copy without one", func(c durable.DocumentCreate) durable.DocumentCreate { c.Key = key("a"); return c },
			func(c *durable.DocumentCreate) {}, false},
		{"another key", func(c durable.DocumentCreate) durable.DocumentCreate { c.Key = key("a"); return c },
			func(c *durable.DocumentCreate) { c.Key = key("b") }, false},
		{"another history", func(c durable.DocumentCreate) durable.DocumentCreate { return c },
			func(c *durable.DocumentCreate) { c.History = durable.HistoryLatest }, false},
		{"another fork", func(c durable.DocumentCreate) durable.DocumentCreate { return c },
			func(c *durable.DocumentCreate) { c.Fork = durable.ForkCurrent }, false},
		{"a session source", func(c durable.DocumentCreate) durable.DocumentCreate {
			c.Scope, c.History, c.Fork = durable.DocumentRecordScope{Kind: durable.ScopeSession}, "", ""
			return c
		}, func(c *durable.DocumentCreate) { c.History, c.Fork = "", "" }, false},
		{"a session copy", func(c durable.DocumentCreate) durable.DocumentCreate { return c },
			func(c *durable.DocumentCreate) { c.Scope = durable.DocumentRecordScope{Kind: durable.ScopeSession} }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			made := counted(t, ":memory:")
			child := mintID[durable.ConversationId](t, made.storage)
			value(commitWrites(made.storage, durable.ConversationWrite{Value: durable.ConversationRecord{Id: child}}))
			source := mintID[durable.DocumentId](t, made.storage)
			copied := mintID[durable.DocumentId](t, made.storage)
			stored := c.source(rewindable(durable.ROOT_CONVERSATION_ID))
			stored.Id = source
			value(commitWrites(made.storage, durable.DocumentCreateWrite{Record: stored, Content: base(1)}))
			create := rewindable(child)
			create.Id = copied
			c.change(&create)
			_, err := commitWrites(made.storage, durable.DocumentCopyWrite{Record: create, Source: durable.DocumentCopySource{Id: source, At: durable.CurrentPoint}})
			if c.ok {
				if err != nil {
					t.Fatalf("copy = %v, want stored", err)
				}
			} else {
				messages := strings.Join(chain(err), " | ")
				if err == nil || !strings.Contains(messages, fmt.Sprintf("Document copy %d was rejected", copied)) ||
					!strings.Contains(messages, fmt.Sprintf("Fork source document %d does not match the copied record", source)) {
					t.Fatalf("copy = %v, want Document copy %d was rejected: Fork source document %d does not match the copied record", messages, copied, source)
				}
				if found := value(made.storage.Document(background, copied, durable.CurrentPoint)); found != nil {
					t.Fatalf("a refused copy stored %+v", found)
				}
			}
			must(t, made.store.Close(background))
		})
	}
}
