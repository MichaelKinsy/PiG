package sqlite_test

// pi: packages/durable/src/storage/sqlite/rows.ts

// Additive to packages/durable/test/sqlite-storage.test.ts, which has no case for the stored columns of documents.
//
// What SqliteStorage stores in its `documents` rows and keeps in `document_revisions` for each scope and history, read
// back from the SQLite file with SQL: the address columns (kind, family, key, scope, owner) as Durable's indexed strings,
// the created and retired seqs in their own columns, and the revisions a document keeps (a current-only document keeps
// its latest base only; a rewindable conversation document keeps every revision).

import (
	"encoding/json"
	"fmt"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// storedDocumentRow is one row of `documents` as SQLite holds it.
type storedDocumentRow struct {
	ID, Family, OwnerID, CreatedAt int64
	RetiredAt                      *int64
	Kind, Key, ScopeKind, Record   string
}

func storedDocumentRows(t *testing.T, path string) map[int64]storedDocumentRow {
	t.Helper()
	db := openInspector(t, path, true)
	defer func() { mustDo(t, db.Close()) }()
	rows := must(must(db.Prepare("SELECT id, kind, family, key_value, scope_kind, owner_id, created_at, retired_at, record FROM documents ORDER BY id")).All())
	stored := map[int64]storedDocumentRow{}
	for _, row := range rows {
		item := storedDocumentRow{
			ID: row["id"].(int64), Kind: row["kind"].(string), Family: row["family"].(int64), Key: row["key_value"].(string),
			ScopeKind: row["scope_kind"].(string), OwnerID: row["owner_id"].(int64), CreatedAt: row["created_at"].(int64), Record: row["record"].(string),
		}
		if retired, ok := row["retired_at"].(int64); ok {
			item.RetiredAt = &retired
		} else if row["retired_at"] != nil {
			t.Fatalf("retired_at of document %d is %T, want an integer or NULL", item.ID, row["retired_at"])
		}
		stored[item.ID] = item
	}
	return stored
}

func TestSqliteStorageStoresEachDocumentsAddressScopeAndLifetimeInItsColumns(t *testing.T) {
	storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
	createRoot(t, storage)
	taskID := mint[durable.TaskId](t, storage)
	commit(t, storage, durable.TaskWrite{Value: pendingTask(taskID)})
	singleton, emptyKey, quoted, rewindable, latest, ofTask := mint[durable.DocumentId](t, storage), mint[durable.DocumentId](t, storage),
		mint[durable.DocumentId](t, storage), mint[durable.DocumentId](t, storage), mint[durable.DocumentId](t, storage), mint[durable.DocumentId](t, storage)
	empty, quote, lone := "", "q\"\n", "\xed\xa0\x80"
	conversation := durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID}
	created := commit(t, storage,
		durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: singleton, Kind: "single", Scope: sessionScope}, Content: base(chorddelta.JsonObjectOf("n", 0))},
		durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: emptyKey, Kind: "single", Key: &empty, Scope: sessionScope}, Content: base(chorddelta.JsonObjectOf("n", 0))},
		durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: quoted, Kind: lone, Key: &quote, Scope: sessionScope}, Content: base(chorddelta.JsonObjectOf("n", 0))},
		durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: rewindable, Kind: "conv", Key: &quote, Scope: conversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf}, Content: base(chorddelta.JsonObjectOf("n", 0))},
		durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: latest, Kind: "conv", Scope: conversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent}, Content: base(chorddelta.JsonObjectOf("n", 0))},
		durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: ofTask, Kind: "owned", Key: &empty, Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: taskID}}, Content: base(chorddelta.JsonObjectOf("n", 0))},
	)
	retired := commit(t, storage, durable.DocumentRetireWrite{Id: latest}, durable.DocumentRetireWrite{Id: ofTask})
	if retired <= created {
		t.Fatalf("retirement commit %d does not follow the creation commit %d", retired, created)
	}

	type expectation struct {
		kind, key, scopeKind string
		family, owner        int64
		retired              *int64
	}
	retiredSeq := int64(retired)
	want := map[durable.DocumentId]expectation{
		singleton:  {kind: `"single"`, key: `""`, scopeKind: "session", family: 0},
		emptyKey:   {kind: `"single"`, key: `""`, scopeKind: "session", family: 1},
		quoted:     {kind: `"\ud800"`, key: `"q\"\n"`, scopeKind: "session", family: 1},
		rewindable: {kind: `"conv"`, key: `"q\"\n"`, scopeKind: "conversation", family: 1, owner: int64(durable.ROOT_CONVERSATION_ID)},
		latest:     {kind: `"conv"`, key: `""`, scopeKind: "conversation", family: 0, owner: int64(durable.ROOT_CONVERSATION_ID), retired: &retiredSeq},
		ofTask:     {kind: `"owned"`, key: `""`, scopeKind: "task", family: 1, owner: int64(taskID), retired: &retiredSeq},
	}
	stored := storedDocumentRows(t, path)
	if len(stored) != len(want) {
		t.Fatalf("documents holds %d rows, want %d: %+v", len(stored), len(want), stored)
	}
	for id, expected := range want {
		row, ok := stored[int64(id)]
		if !ok {
			t.Fatalf("document %d has no row", id)
		}
		if row.Kind != expected.kind || row.Key != expected.key || row.ScopeKind != expected.scopeKind || row.Family != expected.family || row.OwnerID != expected.owner {
			t.Errorf("document %d columns = kind %s key %s scope %s family %d owner %d, want kind %s key %s scope %s family %d owner %d",
				id, row.Kind, row.Key, row.ScopeKind, row.Family, row.OwnerID, expected.kind, expected.key, expected.scopeKind, expected.family, expected.owner)
		}
		if row.CreatedAt != int64(created) {
			t.Errorf("document %d created_at = %d, want %d", id, row.CreatedAt, created)
		}
		switch {
		case expected.retired == nil && row.RetiredAt != nil:
			t.Errorf("document %d retired_at = %d, want NULL", id, *row.RetiredAt)
		case expected.retired != nil && (row.RetiredAt == nil || *row.RetiredAt != *expected.retired):
			t.Errorf("document %d retired_at = %v, want %d", id, row.RetiredAt, *expected.retired)
		}
		var record struct {
			Id        int64  `json:"id"`
			CreatedAt int64  `json:"createdAt"`
			RetiredAt *int64 `json:"retiredAt"`
		}
		mustDo(t, json.Unmarshal([]byte(row.Record), &record))
		if record.Id != int64(id) || record.CreatedAt != row.CreatedAt || (record.RetiredAt == nil) != (row.RetiredAt == nil) || (record.RetiredAt != nil && *record.RetiredAt != *row.RetiredAt) {
			t.Errorf("document %d record = %s disagrees with its columns", id, row.Record)
		}
	}

	// The columns are what the reads match: each address finds its own incarnation, and none finds another's.
	for id, address := range map[durable.DocumentId]durable.DocumentAddress{
		singleton:  {Kind: "single", Scope: sessionScope},
		emptyKey:   {Kind: "single", Key: &empty, Scope: sessionScope},
		quoted:     {Kind: lone, Key: &quote, Scope: sessionScope},
		rewindable: {Kind: "conv", Key: &quote, Scope: conversation},
	} {
		found := must(storage.FindDocument(testContext, address, durable.CurrentPoint))
		if found == nil || found.Id != id {
			t.Errorf("FindDocument(%+v) = %+v, want document %d", address, found, id)
		}
	}
	if found := must(storage.FindDocument(testContext, durable.DocumentAddress{Kind: "owned", Key: &empty, Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: taskID}}, durable.CurrentPoint)); found != nil {
		t.Errorf("FindDocument of a retired task document = %+v, want none", found)
	}
	if found := must(storage.FindDocument(testContext, durable.DocumentAddress{Kind: "owned", Key: &empty, Scope: durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: taskID}}, durable.AtSeq(created))); found == nil || found.Id != ofTask {
		t.Errorf("FindDocument of the task document at its creation = %+v, want document %d", found, ofTask)
	}
}

func TestSqliteStorageKeepsRevisionsOfRewindableConversationDocumentsOnly(t *testing.T) {
	conversation := durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID}
	cases := []struct {
		name    string
		scope   func(durable.TaskId) durable.DocumentRecordScope
		history durable.DocumentHistory
		fork    durable.DocumentFork
		// kept is whether the document keeps its history: revisions after the replacing base and after the retirement,
		// and whether a read at its creation commit succeeds.
		kept bool
	}{
		{"session", func(durable.TaskId) durable.DocumentRecordScope { return sessionScope }, "", "", false},
		{"task", func(task durable.TaskId) durable.DocumentRecordScope {
			return durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: task}
		}, "", "", false},
		{"latest conversation", func(durable.TaskId) durable.DocumentRecordScope { return conversation }, durable.HistoryLatest, durable.ForkCurrent, false},
		{"rewindable conversation", func(durable.TaskId) durable.DocumentRecordScope { return conversation }, durable.HistoryRewindable, durable.ForkAsOf, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
			createRoot(t, storage)
			taskID := mint[durable.TaskId](t, storage)
			commit(t, storage, durable.TaskWrite{Value: pendingTask(taskID)})
			id := mint[durable.DocumentId](t, storage)
			created := commit(t, storage, durable.DocumentCreateWrite{
				Record:  durable.DocumentCreate{Id: id, Kind: "history", Scope: c.scope(taskID), History: c.history, Fork: c.fork},
				Content: base(chorddelta.JsonObjectOf("count", 0)),
			})
			for count := 1; count <= 3; count++ {
				commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"s", []any{"count"}, count})})
			}
			if got := revisionCount(t, path, id); got != 4 {
				t.Fatalf("revisions after three deltas = %d, want 4", got)
			}
			commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: base(chorddelta.JsonObjectOf("count", 4))})
			wantAfterBase := int64(1)
			if c.kept {
				wantAfterBase = 5
			}
			if got := revisionCount(t, path, id); got != wantAfterBase {
				t.Fatalf("revisions after a replacing base = %d, want %d", got, wantAfterBase)
			}
			at, err := storage.Document(testContext, id, durable.AtSeq(created))
			if c.kept {
				mustDo(t, err)
				if at == nil || fmt.Sprint(at.Value.Value("count")) != "0" {
					t.Fatalf("Document at its creation = %+v, want count 0", at)
				}
			} else {
				expectError(t, err, "does not retain historical content")
			}
			commit(t, storage, durable.DocumentRetireWrite{Id: id})
			wantAfterRetire := int64(0)
			if c.kept {
				wantAfterRetire = 5
			}
			if got := revisionCount(t, path, id); got != wantAfterRetire {
				t.Fatalf("revisions after retirement = %d, want %d", got, wantAfterRetire)
			}
		})
	}
}
