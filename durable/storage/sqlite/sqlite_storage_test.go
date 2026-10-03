package sqlite_test

// Ports packages/durable/test/sqlite-storage.test.ts

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

var testContext = context.Background()

type storedTask = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

// must returns value or fails the test by panicking with err; Go cannot pass a multi-value call next to t.
func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func expectError(t *testing.T, err error, messageIncludes string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), messageIncludes) {
		t.Fatalf("error = %v, want one including %q", err, messageIncludes)
	}
}

func expectEqual(t *testing.T, actual, expected any) {
	t.Helper()
	if !reflect.DeepEqual(durabletest.Normalize(actual), durabletest.Normalize(expected)) {
		t.Fatalf("got %s, want %s", durabletest.Describe(actual), durabletest.Describe(expected))
	}
}

// createSqliteStorage opens file-backed storage in a temporary directory that the test removes.
func createSqliteStorage(t *testing.T, options node.NodeSqliteStorageOptions) (*sqlite.SqliteStorage, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "storage.sqlite")
	storage := must(node.OpenNodeSqliteStorage(path, options))
	t.Cleanup(func() { _ = storage.Close(testContext) })
	return storage, path
}

func reopen(t *testing.T, path string) *sqlite.SqliteStorage {
	t.Helper()
	storage := must(node.OpenNodeSqliteStorage(path, node.NodeSqliteStorageOptions{}))
	t.Cleanup(func() { _ = storage.Close(testContext) })
	return storage
}

// reopeningStorage closes and reopens the database after every commit, so every read observes persisted state.
type reopeningStorage struct {
	current *sqlite.SqliteStorage
	path    string
	options node.NodeSqliteStorageOptions
	closed  bool
}

func (storage *reopeningStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	if storage.closed {
		return storage.current.Commit(ctx, writes)
	}
	seq, err := storage.current.Commit(ctx, writes)
	if closeErr := storage.current.Close(testContext); closeErr != nil && err == nil {
		err = closeErr
	}
	reopened, openErr := node.OpenNodeSqliteStorage(storage.path, storage.options)
	if openErr != nil {
		return 0, openErr
	}
	storage.current = reopened
	return seq, err
}

func (storage *reopeningStorage) MintId() (int64, error) { return storage.current.MintId() }

func (storage *reopeningStorage) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	return storage.current.Conversation(ctx, id)
}

func (storage *reopeningStorage) ScanConversations(ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	return storage.current.ScanConversations(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	return storage.current.Entry(ctx, id)
}

func (storage *reopeningStorage) VisibleEntry(ctx context.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	return storage.current.VisibleEntry(ctx, conversationId, id)
}

func (storage *reopeningStorage) FindLatestHeadMarker(ctx context.Context, conversationId durable.ConversationId, at *durable.EntryId) (*durable.EntryRecord, error) {
	return storage.current.FindLatestHeadMarker(ctx, conversationId, at)
}

func (storage *reopeningStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	return storage.current.ScanEntries(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) Task(ctx context.Context, id durable.TaskId) (*storedTask, error) {
	return storage.current.Task(ctx, id)
}

func (storage *reopeningStorage) ScanTasks(ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[storedTask, durable.Cursor], error) {
	return storage.current.ScanTasks(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	return storage.current.Submission(ctx, id)
}

func (storage *reopeningStorage) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	return storage.current.ScanSubmissions(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) SubmissionByRequest(ctx context.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	return storage.current.SubmissionByRequest(ctx, conversationId, requestId)
}

func (storage *reopeningStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	return storage.current.FindDocument(ctx, address, at)
}

func (storage *reopeningStorage) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	return storage.current.Document(ctx, id, at)
}

func (storage *reopeningStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	return storage.current.ScanDocuments(ctx, query, limit, cursor)
}

func (storage *reopeningStorage) Close(ctx context.Context) error {
	if storage.closed {
		return nil
	}
	storage.closed = true
	return storage.current.Close(ctx)
}

func TestSqliteStorageConformance(t *testing.T) {
	durabletest.RegisterStorageConformance(t, "SqliteStorage", func(use func(durable.Storage) error) error {
		storage, err := node.OpenNodeSqliteStorage(filepath.Join(t.TempDir(), "storage.sqlite"), node.NodeSqliteStorageOptions{})
		if err != nil {
			return err
		}
		defer func() { _ = storage.Close(testContext) }()
		return use(storage)
	})

	durabletest.RegisterStorageConformance(t, "SqliteStorage across reopen", func(use func(durable.Storage) error) error {
		path := filepath.Join(t.TempDir(), "storage.sqlite")
		created, err := node.OpenNodeSqliteStorage(path, node.NodeSqliteStorageOptions{})
		if err != nil {
			return err
		}
		if err := created.Close(testContext); err != nil {
			return err
		}
		current, err := node.OpenNodeSqliteStorage(path, node.NodeSqliteStorageOptions{})
		if err != nil {
			return err
		}
		storage := &reopeningStorage{current: current, path: path}
		defer func() { _ = storage.Close(testContext) }()
		return use(storage)
	})
}

func entry(id durable.EntryId, conversationId durable.ConversationId, data durable.JsonValue) durable.EntryRecord {
	return durable.EntryRecord{Id: id, ConversationId: conversationId, Kind: "message", Data: data}
}

func createRoot(t *testing.T, storage durable.Storage) durable.Seq {
	t.Helper()
	return must(storage.Commit(testContext, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
	}))
}

func pendingTask(id durable.TaskId) storedTask {
	var phase durable.JsonValue = map[string]any{"phase": "ready"}
	return storedTask{
		Id:             id,
		ConversationId: durable.ROOT_CONVERSATION_ID,
		Kind:           "test.task",
		Version:        1,
		Input:          nil,
		State:          durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &phase},
	}
}

func mint[I durable.Id](t *testing.T, storage durable.Storage) I {
	t.Helper()
	return durable.IdFromNumber[I](must(storage.MintId()))
}

func commit(t *testing.T, storage durable.Storage, writes ...durable.StorageWrite) durable.Seq {
	t.Helper()
	return must(storage.Commit(testContext, writes))
}

func openInspector(t *testing.T, path string, readOnly bool) *node.DatabaseSync {
	t.Helper()
	return must(node.NewDatabaseSync(path, node.DatabaseSyncOptions{ReadOnly: readOnly}))
}

func scalar(t *testing.T, db *node.DatabaseSync, sqlText string) int64 {
	t.Helper()
	row := must(must(db.Prepare(sqlText)).Get())
	return row["value"].(int64)
}

func revisionCount(t *testing.T, path string, documentId durable.DocumentId) int64 {
	t.Helper()
	db := openInspector(t, path, true)
	defer func() { mustDo(t, db.Close()) }()
	statement := must(db.Prepare("SELECT count(*) AS count FROM document_revisions WHERE document_id = ?"))
	return must(statement.Get(int64(documentId)))["count"].(int64)
}

func base(value durable.JsonObject) durable.DocumentContent {
	return durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: value}
}

func delta(ops ...durable.Op) durable.DocumentContent {
	return durable.DocumentContent{Kind: durable.ContentDelta, Version: 1, Ops: ops}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	return must(os.Stat(path)).Size()
}

var sessionScope = durable.DocumentRecordScope{Kind: durable.ScopeSession}

func TestPicoSqliteStorage(t *testing.T) {
	t.Run("persists records, sequence allocation, and global ID allocation across reopen", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		if seq := createRoot(t, storage); seq != 1 {
			t.Fatalf("root seq = %d", seq)
		}
		entryId := mint[durable.EntryId](t, storage)
		if seq := commit(t, storage, durable.EntryWrite{Value: entry(entryId, durable.ROOT_CONVERSATION_ID, nil)}); seq != 2 {
			t.Fatalf("entry seq = %d", seq)
		}
		mustDo(t, storage.Close(testContext))

		reopened := reopen(t, path)
		found := must(reopened.Entry(testContext, entryId))
		expectEqual(t, map[string]any{"entry": found.Entry, "commitSeq": found.CommitSeq},
			map[string]any{"entry": entry(entryId, durable.ROOT_CONVERSATION_ID, nil), "commitSeq": 2})
		if id := mint[durable.EntryId](t, reopened); id != entryId+1 {
			t.Fatalf("minted %d, want %d", id, entryId+1)
		}
		if seq := commit(t, reopened, durable.TaskWrite{Value: pendingTask(mint[durable.TaskId](t, reopened))}); seq != 3 {
			t.Fatalf("task seq = %d", seq)
		}
	})

	t.Run("rejects persisted metadata corruption on reopen", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		mustDo(t, storage.Close(testContext))
		database := openInspector(t, path, false)
		mustDo(t, database.Exec("DELETE FROM durable_metadata"))
		mustDo(t, database.Close())
		_, err := node.OpenNodeSqliteStorage(path, node.NodeSqliteStorageOptions{})
		expectError(t, err, "Durable SQLite metadata is missing")
	})

	t.Run("rejects a document whose required base is missing", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{
			Record:  durable.DocumentCreate{Id: id, Kind: "corrupt", Scope: sessionScope},
			Content: base(durable.JsonObject{"retained": true}),
		})
		database := openInspector(t, path, false)
		mustDo(t, must(database.Prepare("DELETE FROM document_revisions WHERE document_id = ?")).Run(int64(id)))
		mustDo(t, database.Close())
		_, err := storage.Document(testContext, id, durable.CurrentPoint)
		expectError(t, err, fmt.Sprintf("Document %d is missing a required base", id))
	})

	t.Run("replays detached root replacements and follow-up edits while rejecting corrupt operations", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{
			Record:  durable.DocumentCreate{Id: id, Kind: "replay", Scope: sessionScope},
			Content: base(durable.JsonObject{"nested": map[string]any{"value": 1}, "rows": []any{}}),
		})
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(
			durable.Op{"r", map[string]any{"nested": map[string]any{"value": 2}, "rows": []any{map[string]any{"id": 1}}}},
		)})
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(
			durable.Op{"s", []any{"nested", "value"}, 3},
			durable.Op{"p", []any{"rows"}, 1, 0, []any{map[string]any{"id": 2}}},
			durable.Op{"m", []any{"rows"}, []any{1, 0}},
		)})
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"s", []any{"nested", "value"}, 4})})

		expected := map[string]any{"nested": map[string]any{"value": 4}, "rows": []any{map[string]any{"id": 2}, map[string]any{"id": 1}}}
		first := must(storage.Document(testContext, id, durable.CurrentPoint))
		expectEqual(t, first.Value, expected)
		first.Value["nested"].(map[string]any)["value"] = 99
		first.Value["rows"].([]any)[0].(map[string]any)["id"] = 99
		expectEqual(t, must(storage.Document(testContext, id, durable.CurrentPoint)).Value, expected)

		database := openInspector(t, path, false)
		mustDo(t, must(database.Prepare(`UPDATE document_revisions SET content = ? WHERE document_id = ? AND seq =
					(SELECT max(seq) FROM document_revisions WHERE document_id = ?)`)).Run(`[["unknown"]]`, int64(id), int64(id)))
		mustDo(t, database.Close())
		_, err := storage.Document(testContext, id, durable.CurrentPoint)
		expectError(t, err, "unknown op verb")
	})

	t.Run("rolls SQL rows and sequence allocation back as one transaction", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		createRoot(t, storage)
		transientId := mint[durable.EntryId](t, storage)
		transientTaskId := mint[durable.TaskId](t, storage)
		circular := map[string]any{}
		circular["self"] = circular
		task := pendingTask(transientTaskId)
		task.Input = circular
		_, err := storage.Commit(testContext, []durable.StorageWrite{
			durable.EntryWrite{Value: entry(transientId, durable.ROOT_CONVERSATION_ID, nil)},
			durable.TaskWrite{Value: task},
		})
		expectError(t, err, "circular structure")
		if found := must(storage.Entry(testContext, transientId)); found != nil {
			t.Fatalf("transient entry = %+v", found)
		}
		if found := must(storage.Task(testContext, transientTaskId)); found != nil {
			t.Fatalf("transient task = %+v", found)
		}
		committedId := mint[durable.EntryId](t, storage)
		if seq := commit(t, storage, durable.EntryWrite{Value: entry(committedId, durable.ROOT_CONVERSATION_ID, nil)}); seq != 2 {
			t.Fatalf("seq = %d, want 2", seq)
		}

		db := openInspector(t, path, true)
		defer func() { mustDo(t, db.Close()) }()
		if count := scalar(t, db, "SELECT count(*) AS value FROM entries"); count != 1 {
			t.Fatalf("entries = %d", count)
		}
		if nextSeq := scalar(t, db, "SELECT next_seq AS value FROM durable_metadata WHERE singleton = 1"); nextSeq != 3 {
			t.Fatalf("next_seq = %d", nextSeq)
		}
	})

	t.Run("reconstructs recent and ancient rewindable points after reopen", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		record := durable.DocumentCreate{
			Id: id, Kind: "history",
			Scope:   durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID},
			History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
		}
		createdAt := commit(t, storage, durable.DocumentCreateWrite{Record: record, Content: base(durable.JsonObject{"count": 0})})
		ancientAt, recentAt := createdAt, createdAt
		for count := 1; count <= 40; count++ {
			content := delta(durable.Op{"s", []any{"count"}, count})
			if count == 20 {
				content = base(durable.JsonObject{"count": count})
			}
			recentAt = commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: content})
			if count == 5 {
				ancientAt = recentAt
			}
		}
		mustDo(t, storage.Close(testContext))
		reopened := reopen(t, path)
		expectEqual(t, must(reopened.Document(testContext, id, durable.AtSeq(ancientAt))).Value, map[string]any{"count": 5})
		expectEqual(t, must(reopened.Document(testContext, id, durable.AtSeq(recentAt))).Value, map[string]any{"count": 40})
	})

	t.Run("uses indexes for exact addresses, exact scopes, entry history, and document revision tails", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		mustDo(t, storage.Close(testContext))
		db := openInspector(t, path, true)
		defer func() { mustDo(t, db.Close()) }()
		plan := func(sqlText string, params ...any) string {
			rows := must(must(db.Prepare("EXPLAIN QUERY PLAN " + sqlText)).All(params...))
			details := make([]string, len(rows))
			for index, row := range rows {
				details[index] = row["detail"].(string)
			}
			return strings.Join(details, "\n")
		}
		details := []string{
			plan(`SELECT record FROM documents
						WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ?
						AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`, "kind", "session", 0, 0, ""),
			plan(`SELECT record FROM documents
						WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ?
						AND created_at <= ? AND (retired_at IS NULL OR retired_at > ?)
						ORDER BY created_at DESC LIMIT 1`, "kind", "conversation", 1, 0, "", 10, 10),
			plan(`SELECT record FROM documents
						WHERE scope_kind = ? AND owner_id = ? AND kind = ? AND id > ? ORDER BY id LIMIT ?`, "task", 1, "kind", 0, 10),
			plan(`SELECT record FROM entries
						WHERE conversation_id = ? AND id <= ? ORDER BY id DESC LIMIT ?`, 1, 10, 10),
			plan(`SELECT record FROM entries
						WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1`, 1, 10),
			plan("SELECT record FROM tasks WHERE status = ? AND id > ? ORDER BY id LIMIT ?", "pending", 0, 10),
			plan(`SELECT seq, kind, version, content FROM document_revisions
						WHERE document_id = ? AND kind = 'base' AND seq <= ? ORDER BY seq DESC LIMIT 1`, 1, 10),
			plan(`SELECT seq, kind, version, content FROM document_revisions
						WHERE document_id = ? AND seq > ? AND seq <= ? ORDER BY seq`, 1, 5, 10),
		}
		for index, indexName := range []string{
			"documents_by_address", "documents_by_address", "documents_by_scope_kind", "entries_by_conversation",
			"entry_heads_by_conversation", "tasks_by_status", "document_revisions_by_kind",
			"sqlite_autoindex_document_revisions_1",
		} {
			if !strings.Contains(details[index], indexName) {
				t.Fatalf("plan %d = %q, want %s", index, details[index], indexName)
			}
		}
		for index, detail := range details[:3] {
			if strings.Contains(detail, "SCAN documents") {
				t.Fatalf("plan %d scans documents: %q", index, detail)
			}
		}
		if strings.Contains(details[7], "SCAN document_revisions") {
			t.Fatalf("plan 7 scans document_revisions: %q", details[7])
		}
	})

	t.Run("reclaims current-only revisions only after a base or retirement", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{
			Record: durable.DocumentCreate{Id: id, Kind: "latest", Scope: sessionScope}, Content: base(durable.JsonObject{"count": 0}),
		})
		for count := 1; count <= 10; count++ {
			commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"s", []any{"count"}, count})})
		}
		if count := revisionCount(t, path, id); count != 11 {
			t.Fatalf("revisions = %d, want 11", count)
		}
		revisions := openInspector(t, path, true)
		row := must(must(revisions.Prepare(
			"SELECT content FROM document_revisions WHERE document_id = ? AND kind = 'delta' ORDER BY seq DESC LIMIT 1",
		)).Get(int64(id)))
		mustDo(t, revisions.Close())
		var lastOps any
		mustDo(t, json.Unmarshal([]byte(row["content"].(string)), &lastOps))
		expectEqual(t, lastOps, []any{[]any{"s", []any{"count"}, 10}})
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: base(durable.JsonObject{"count": 11})})
		if count := revisionCount(t, path, id); count != 1 {
			t.Fatalf("revisions after base = %d, want 1", count)
		}
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"r", map[string]any{"count": 12}})})
		if count := revisionCount(t, path, id); count != 2 {
			t.Fatalf("revisions after replacement delta = %d, want 2", count)
		}
		commit(t, storage, durable.DocumentRetireWrite{Id: id})
		if count := revisionCount(t, path, id); count != 0 {
			t.Fatalf("revisions after retirement = %d, want 0", count)
		}
	})

	t.Run("auto-checkpoints WAL frames and truncates the WAL on close", func(t *testing.T) {
		pages := 1
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{WalAutoCheckpointPages: &pages})
		createRoot(t, storage)
		for index := range 20 {
			commit(t, storage, durable.EntryWrite{Value: entry(mint[durable.EntryId](t, storage), durable.ROOT_CONVERSATION_ID,
				map[string]any{"text": strings.Repeat("x", 32*1024), "index": index})})
		}
		walPath := path + "-wal"
		if size := fileSize(t, walPath); size >= 512*1024 {
			t.Fatalf("WAL size = %d", size)
		}
		observer := openInspector(t, path, true)
		defer func() { mustDo(t, observer.Close()) }()
		if count := scalar(t, observer, "SELECT count(*) AS value FROM entries"); count != 20 {
			t.Fatalf("entries = %d", count)
		}
		mustDo(t, storage.Close(testContext))
		if size := fileSize(t, walPath); size != 0 {
			t.Fatalf("WAL size after close = %d, want 0", size)
		}
	})

	t.Run("reuses pages released by current-only checkpoints", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		createRoot(t, storage)
		id := mint[durable.DocumentId](t, storage)
		large := strings.Repeat("x", 512*1024)
		commit(t, storage, durable.DocumentCreateWrite{
			Record: durable.DocumentCreate{Id: id, Kind: "reuse", Scope: sessionScope}, Content: base(durable.JsonObject{"text": large}),
		})
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: base(durable.JsonObject{"text": "small"})})
		before := openInspector(t, path, true)
		pagesAfterDelete := scalar(t, before, "SELECT page_count AS value FROM pragma_page_count() ")
		freeAfterDelete := scalar(t, before, "SELECT freelist_count AS value FROM pragma_freelist_count() ")
		mustDo(t, before.Close())
		if freeAfterDelete <= 0 {
			t.Fatalf("freelist after delete = %d", freeAfterDelete)
		}
		commit(t, storage, durable.DocumentChangeWrite{Id: id, Content: base(durable.JsonObject{"text": large})})
		after := openInspector(t, path, true)
		defer func() { mustDo(t, after.Close()) }()
		pagesAfterReuse := scalar(t, after, "SELECT page_count AS value FROM pragma_page_count() ")
		freeAfterReuse := scalar(t, after, "SELECT freelist_count AS value FROM pragma_freelist_count() ")
		if pagesAfterReuse > pagesAfterDelete+2 {
			t.Fatalf("pages after reuse = %d, after delete = %d", pagesAfterReuse, pagesAfterDelete)
		}
		if freeAfterReuse >= freeAfterDelete {
			t.Fatalf("freelist after reuse = %d, after delete = %d", freeAfterReuse, freeAfterDelete)
		}
	})

	t.Run("keeps representative row and document storage bounded", func(t *testing.T) {
		storage, path := createSqliteStorage(t, node.NodeSqliteStorageOptions{})
		createRoot(t, storage)
		for index := range 100 {
			commit(t, storage, durable.EntryWrite{Value: entry(mint[durable.EntryId](t, storage), durable.ROOT_CONVERSATION_ID,
				map[string]any{"index": index, "text": strings.Repeat("x", 1_024)})})
		}
		documentId := mint[durable.DocumentId](t, storage)
		commit(t, storage, durable.DocumentCreateWrite{Record: durable.DocumentCreate{
			Id: documentId, Kind: "size.history",
			Scope:   durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID},
			History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
		}, Content: base(durable.JsonObject{"count": 0})})
		for count := 1; count <= 100; count++ {
			commit(t, storage, durable.DocumentChangeWrite{Id: documentId, Content: delta(durable.Op{"s", []any{"count"}, count})})
		}
		mustDo(t, storage.Close(testContext))
		if size := fileSize(t, path); size >= 1024*1024 {
			t.Fatalf("database size = %d", size)
		}
	})
}
