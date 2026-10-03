package sqlite

// Ports packages/durable/src/storage/sqlite/storage.ts

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/ops"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/writes"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

type storedTask = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

type tableName string

const (
	tableConversation tableName = "conversation"
	tableEntry        tableName = "entry"
	tableTask         tableName = "task"
	tableSubmission   tableName = "submission"
	tableDocument     tableName = "document"
)

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER, the end of the durable ID space.
const maxSafeInteger = 1<<53 - 1

type documentAction struct {
	create  *durable.DocumentCreate
	copy    *durable.DocumentCopySource
	content *durable.DocumentContent
	retire  bool
}

// documentActions keeps the per-document actions of one batch in first-mention order.
type documentActions struct {
	order   []durable.DocumentId
	actions map[durable.DocumentId]*documentAction
}

func (actions *documentActions) get(id durable.DocumentId) *documentAction {
	action := actions.actions[id]
	if action == nil {
		action = &documentAction{}
		actions.actions[id] = action
		actions.order = append(actions.order, id)
	}
	return action
}

func (actions *documentActions) has(id durable.DocumentId) bool {
	_, ok := actions.actions[id]
	return ok
}

type scopeColumns struct {
	scopeKind string
	ownerId   int64
}

// encodeJSON serializes a stored value with JavaScript string semantics, so lone UTF-16 surrogates survive.
func encodeJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		var unsupported *json.UnsupportedValueError
		if errors.As(err, &unsupported) && strings.Contains(unsupported.Str, "cycle") {
			return "", fmt.Errorf("Converting circular structure to JSON: %w", err)
		}
		return "", err
	}
	return string(encoded), nil
}

func parseJSON[T any](text string) (T, error) {
	var value T
	err := json.Unmarshal([]byte(text), &value)
	return value, err
}

// encodeIndexedString keeps indexed identities lossless: some SQLite bindings replace lone UTF-16 surrogates, while
// their JSON escapes survive.
func encodeIndexedString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("durable sqlite: string is not JSON encodable: %v", err))
	}
	return string(encoded)
}

func cursorId(cursor durable.Cursor) (int64, bool, error) {
	if cursor == nil {
		return 0, false, nil
	}
	value, present := cursor["after"]
	if !present {
		return 0, false, nil
	}
	var after int64
	switch typed := value.(type) {
	case int64:
		after = typed
	case int:
		after = int64(typed)
	case float64:
		if typed != math.Trunc(typed) || math.Abs(typed) > maxSafeInteger {
			return 0, false, errInvalidCursor
		}
		after = int64(typed)
	default:
		return 0, false, errInvalidCursor
	}
	if after > maxSafeInteger || after < -maxSafeInteger {
		return 0, false, errInvalidCursor
	}
	return after, true, nil
}

var errInvalidCursor = errors.New("Invalid storage cursor")

func cursorStart(cursor durable.Cursor) (int64, error) {
	after, ok, err := cursorId(cursor)
	if err != nil || !ok {
		return -1, err
	}
	return after, nil
}

func page[T any](values []T, limit int, id func(T) int64) durable.Page[T, durable.Cursor] {
	count := min(max(limit, 0), len(values))
	items := values[:count:count]
	if len(values) <= limit {
		return durable.Page[T, durable.Cursor]{Items: items}
	}
	var after int64
	if count > 0 {
		after = id(items[count-1])
	}
	next := durable.Cursor{"after": after}
	return durable.Page[T, durable.Cursor]{Items: items, Next: &next}
}

func scopeColumnsOf(scope durable.DocumentRecordScope) scopeColumns {
	switch scope.Kind {
	case durable.ScopeConversation:
		return scopeColumns{scopeKind: string(scope.Kind), ownerId: int64(scope.ConversationId)}
	case durable.ScopeTask:
		return scopeColumns{scopeKind: string(scope.Kind), ownerId: int64(scope.TaskId)}
	default:
		return scopeColumns{scopeKind: string(scope.Kind)}
	}
}

type addressParts struct {
	kind string
	scopeColumns
	family   int64
	keyValue string
}

func addressPartsOf(kind string, scope durable.DocumentRecordScope, key *string) addressParts {
	parts := addressParts{kind: encodeIndexedString(kind), scopeColumns: scopeColumnsOf(scope)}
	keyValue := ""
	if key != nil {
		parts.family = 1
		keyValue = *key
	}
	parts.keyValue = encodeIndexedString(keyValue)
	return parts
}

func isAliveAt(record durable.DocumentRecord, at durable.DocumentPoint) bool {
	if at.Current {
		return record.RetiredAt == nil
	}
	return record.CreatedAt <= at.Seq && (record.RetiredAt == nil || at.Seq < *record.RetiredAt)
}

func isCurrentOnly(record durable.DocumentRecord) bool {
	return record.Scope.Kind != durable.ScopeConversation || record.History == durable.HistoryLatest
}

func writeId(write durable.StorageWrite) (int64, bool) {
	switch typed := write.(type) {
	case durable.ConversationWrite:
		return int64(typed.Value.Id), true
	case durable.EntryWrite:
		return int64(typed.Value.Id), true
	case durable.TaskWrite:
		return int64(typed.Value.Id), true
	case durable.SubmissionWrite:
		return int64(typed.Value.Id), true
	case durable.DocumentCreateWrite:
		return int64(typed.Record.Id), true
	case durable.DocumentCopyWrite:
		return int64(typed.Record.Id), true
	default:
		return 0, false
	}
}

// SqliteStorage is the portable SQLite implementation of durable.Storage over a SqliteDatabase facade.
type SqliteStorage struct {
	db SqliteDatabase

	mu            sync.Mutex
	nextId        int64
	closed        bool
	closeDone     chan struct{}
	closeErr      error
	admittedReads int
	readsDrained  chan struct{}
}

var _ durable.Storage = (*SqliteStorage)(nil)

// Open initializes storage over an owned SQLite database facade. A failure closes the database.
func Open(db SqliteDatabase) (*SqliteStorage, error) {
	storage, err := open(db)
	if err != nil {
		// Preserve the initialization failure.
		_ = db.Close()
		return nil, err
	}
	return storage, nil
}

func open(db SqliteDatabase) (*SqliteStorage, error) {
	if err := ApplySqliteMigrations(db, nil); err != nil {
		return nil, err
	}
	metadata, err := db.Get("SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1")
	if err != nil {
		return nil, err
	}
	if metadata == nil {
		return nil, errors.New("Durable SQLite metadata is missing")
	}
	nextId, err := metadataNextId(metadata)
	if err != nil {
		return nil, err
	}
	return &SqliteStorage{db: db, nextId: nextId}, nil
}

func metadataNextId(metadata SqliteRow) (int64, error) {
	text, err := rowText(metadata, "next_id")
	if err != nil {
		return 0, err
	}
	if value, err := strconv.ParseInt(text, 10, 64); err == nil {
		return value, nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("Durable SQLite next_id is not a number: %q", text)
	}
	return int64(value), nil
}

// Commit atomically persists one batch and returns its sequence.
func (storage *SqliteStorage) Commit(_ context.Context, batch []durable.StorageWrite) (durable.Seq, error) {
	if err := storage.assertOpen(); err != nil {
		return 0, err
	}
	batch = writes.Values(batch)
	actions, err := prepareDocumentActions(batch)
	if err != nil {
		return 0, err
	}
	candidateNextId := storage.candidateNextId(batch)
	var committedSeq durable.Seq
	err = storage.db.Transaction(func(transaction SqliteExecutor) error {
		metadata, err := transaction.Get("SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1")
		if err != nil {
			return err
		}
		if metadata == nil {
			return errors.New("Durable SQLite metadata is missing")
		}
		nextSeq, err := rowInt(metadata, "next_seq")
		if err != nil {
			return err
		}
		storedNextId, err := metadataNextId(metadata)
		if err != nil {
			return err
		}
		seq := durable.Seq(nextSeq)
		if err := checkGlobalIds(transaction, batch); err != nil {
			return err
		}
		if err := checkDocumentActions(transaction, actions); err != nil {
			return err
		}
		for _, write := range batch {
			if err := applyTableWrite(transaction, write, seq); err != nil {
				return err
			}
		}
		if err := applyDocumentActions(transaction, actions, seq); err != nil {
			return err
		}
		if err := transaction.Run(
			"UPDATE durable_metadata SET next_id = ?, next_seq = ? WHERE singleton = 1",
			strconv.FormatInt(max(storedNextId, candidateNextId), 10),
			int64(seq)+1,
		); err != nil {
			return err
		}
		committedSeq = seq
		return nil
	})
	if err != nil {
		return 0, err
	}
	storage.mu.Lock()
	storage.nextId = max(storage.nextId, candidateNextId)
	storage.mu.Unlock()
	return committedSeq, nil
}

// MintId returns a fresh candidate from the Session-global numeric ID namespace.
func (storage *SqliteStorage) MintId() (int64, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.closed {
		return 0, errClosed
	}
	if storage.nextId > maxSafeInteger {
		return 0, errors.New("ID space is exhausted")
	}
	id := storage.nextId
	storage.nextId++
	return id, nil
}

// Conversation looks up one conversation by exact ID.
func (storage *SqliteStorage) Conversation(
	_ context.Context, id durable.ConversationId,
) (*durable.ConversationRecord, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	return getRecord[durable.ConversationRecord](storage.db, "SELECT record FROM conversations WHERE id = ?", id)
}

func getRecord[T any](executor SqliteExecutor, sqlText string, params ...SqliteValue) (*T, error) {
	row, err := executor.Get(sqlText, params...)
	if err != nil || row == nil {
		return nil, err
	}
	text, err := rowText(row, "record")
	if err != nil {
		return nil, err
	}
	record, err := parseJSON[T](text)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func allRecords[T any](executor SqliteExecutor, sqlText string, params ...SqliteValue) ([]T, error) {
	rows, err := executor.All(sqlText, params...)
	if err != nil {
		return nil, err
	}
	records := make([]T, 0, len(rows))
	for _, row := range rows {
		text, err := rowText(row, "record")
		if err != nil {
			return nil, err
		}
		record, err := parseJSON[T](text)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func conversationIdOf(record durable.ConversationRecord) int64 { return int64(record.Id) }
func entryIdOf(record durable.EntryRecord) int64               { return int64(record.Id) }
func taskIdOf(record storedTask) int64                         { return int64(record.Id) }
func submissionIdOf(record durable.SubmissionRecord) int64     { return int64(record.Id) }
func documentIdOf(record durable.DocumentRecord) int64         { return int64(record.Id) }

// ScanConversations scans conversations in ascending ID order.
func (storage *SqliteStorage) ScanConversations(
	_ context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	if err := storage.assertOpen(); err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	after, err := cursorStart(cursor)
	if err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	clauses := []string{"id > ?"}
	params := []SqliteValue{after}
	if query.OwnerConversationId != nil {
		clauses = append(clauses, "owner_conversation_id = ?")
		params = append(params, int64(*query.OwnerConversationId))
	}
	if query.OwnerTaskId != nil {
		clauses = append(clauses, "owner_task_id = ?")
		params = append(params, int64(*query.OwnerTaskId))
	}
	params = append(params, int64(limit)+1)
	records, err := allRecords[durable.ConversationRecord](
		storage.db,
		"SELECT record FROM conversations WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id LIMIT ?",
		params...,
	)
	if err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	return page(records, limit, conversationIdOf), nil
}

// Entry looks up one global entry and the sequence of the commit that persisted it.
func (storage *SqliteStorage) Entry(_ context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	var found *durable.EntryAt
	err := storage.admitRead(func() (err error) {
		found, err = storage.readEntry(nil, id)
		return err
	})
	return found, err
}

// VisibleEntry looks up one entry only when it is visible through the requested conversation's ancestry.
func (storage *SqliteStorage) VisibleEntry(
	_ context.Context, conversationId durable.ConversationId, id durable.EntryId,
) (*durable.EntryAt, error) {
	var found *durable.EntryAt
	err := storage.admitRead(func() (err error) {
		found, err = storage.readEntry(&conversationId, id)
		return err
	})
	return found, err
}

// FindLatestHeadMarker returns the newest visible entry with a head at or below the optional inclusive cutoff.
func (storage *SqliteStorage) FindLatestHeadMarker(
	_ context.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId,
) (*durable.EntryRecord, error) {
	var found *durable.EntryRecord
	err := storage.admitRead(func() (err error) {
		found, err = storage.readLatestHeadMarker(conversationId, atOrBeforeEntryId)
		return err
	})
	return found, err
}

// ScanEntries scans the inclusive visible range newest-first, returning at most limit entries.
func (storage *SqliteStorage) ScanEntries(
	_ context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	var found durable.Page[durable.EntryRecord, durable.Cursor]
	err := storage.admitRead(func() (err error) {
		found, err = storage.readEntries(query, limit, cursor)
		return err
	})
	return found, err
}

func (storage *SqliteStorage) readEntry(
	conversationId *durable.ConversationId, id durable.EntryId,
) (*durable.EntryAt, error) {
	var conversation *durable.ConversationRecord
	if conversationId != nil {
		var err error
		conversation, err = storage.readConversation(*conversationId)
		if err != nil {
			return nil, err
		}
		if conversation == nil {
			return nil, fmt.Errorf("Unknown conversation: %d", *conversationId)
		}
	}
	row, err := storage.db.Get("SELECT record, commit_seq FROM entries WHERE id = ?", int64(id))
	if err != nil || row == nil {
		return nil, err
	}
	text, err := rowText(row, "record")
	if err != nil {
		return nil, err
	}
	entry, err := parseJSON[durable.EntryRecord](text)
	if err != nil {
		return nil, err
	}
	commitSeq, err := rowInt(row, "commit_seq")
	if err != nil {
		return nil, err
	}
	if conversation != nil {
		upperEntryId := int64(math.MaxInt64)
		for conversation.Id != entry.ConversationId {
			if conversation.Parent == nil {
				return nil, nil
			}
			upperEntryId = min(upperEntryId, int64(conversation.Parent.At))
			conversation, err = storage.readConversation(conversation.Parent.ConversationId)
			if err != nil {
				return nil, err
			}
			if conversation == nil {
				return nil, errors.New("Fork parent conversation is missing")
			}
		}
		if int64(entry.Id) > upperEntryId {
			return nil, nil
		}
	}
	return &durable.EntryAt{Entry: entry, CommitSeq: durable.Seq(commitSeq)}, nil
}

func (storage *SqliteStorage) readLatestHeadMarker(
	conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId,
) (*durable.EntryRecord, error) {
	conversation, err := storage.readConversation(conversationId)
	if err != nil {
		return nil, err
	}
	if conversation == nil {
		return nil, fmt.Errorf("Unknown conversation: %d", conversationId)
	}
	var upper *int64
	if atOrBeforeEntryId != nil {
		value := int64(*atOrBeforeEntryId)
		upper = &value
	}
	for {
		var marker *durable.EntryRecord
		if upper == nil {
			marker, err = getRecord[durable.EntryRecord](
				storage.db,
				"SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL ORDER BY id DESC LIMIT 1",
				int64(conversation.Id),
			)
		} else {
			marker, err = getRecord[durable.EntryRecord](
				storage.db,
				"SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1",
				int64(conversation.Id),
				*upper,
			)
		}
		if err != nil || marker != nil {
			return marker, err
		}
		if conversation.Parent == nil {
			return nil, nil
		}
		at := int64(conversation.Parent.At)
		if upper == nil || at < *upper {
			upper = &at
		}
		conversation, err = storage.readConversation(conversation.Parent.ConversationId)
		if err != nil {
			return nil, err
		}
		if conversation == nil {
			return nil, errors.New("Fork parent conversation is missing")
		}
	}
}

func (storage *SqliteStorage) readEntries(
	query durable.EntryQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	empty := durable.Page[durable.EntryRecord, durable.Cursor]{}
	conversation, err := storage.readConversation(query.ConversationId)
	if err != nil {
		return empty, err
	}
	if conversation == nil {
		return empty, fmt.Errorf("Unknown conversation: %d", query.ConversationId)
	}
	after, hasAfter, err := cursorId(cursor)
	if err != nil {
		return empty, err
	}
	var upper *int64
	if query.MaxEntryId != nil {
		value := int64(*query.MaxEntryId)
		upper = &value
	}
	if hasAfter {
		value := int64(maxSafeInteger)
		if upper != nil {
			value = *upper
		}
		value = min(value, after-1)
		upper = &value
	}
	values := []durable.EntryRecord{}
	for {
		clauses := []string{"conversation_id = ?"}
		params := []SqliteValue{int64(conversation.Id)}
		if query.MinEntryId != nil {
			clauses = append(clauses, "id >= ?")
			params = append(params, int64(*query.MinEntryId))
		}
		if upper != nil {
			clauses = append(clauses, "id <= ?")
			params = append(params, *upper)
		}
		params = append(params, int64(limit+1-len(values)))
		records, err := allRecords[durable.EntryRecord](
			storage.db,
			"SELECT record FROM entries WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id DESC LIMIT ?",
			params...,
		)
		if err != nil {
			return empty, err
		}
		values = append(values, records...)
		if len(values) > limit || conversation.Parent == nil {
			break
		}
		at := int64(conversation.Parent.At)
		if upper == nil || at < *upper {
			upper = &at
		}
		if query.MinEntryId != nil && *upper < int64(*query.MinEntryId) {
			break
		}
		conversation, err = storage.readConversation(conversation.Parent.ConversationId)
		if err != nil {
			return empty, err
		}
		if conversation == nil {
			return empty, errors.New("Fork parent conversation is missing")
		}
	}
	return page(values, limit, entryIdOf), nil
}

// Task looks up the latest complete record for one task.
func (storage *SqliteStorage) Task(_ context.Context, id durable.TaskId) (*storedTask, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	return getRecord[storedTask](storage.db, "SELECT record FROM tasks WHERE id = ?", int64(id))
}

// ScanTasks scans task records matching every supplied filter.
func (storage *SqliteStorage) ScanTasks(
	_ context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor,
) (durable.Page[storedTask, durable.Cursor], error) {
	if err := storage.assertOpen(); err != nil {
		return durable.Page[storedTask, durable.Cursor]{}, err
	}
	after, err := cursorStart(cursor)
	if err != nil {
		return durable.Page[storedTask, durable.Cursor]{}, err
	}
	clauses := []string{"id > ?"}
	params := []SqliteValue{after}
	if query.ConversationId != nil {
		clauses = append(clauses, "conversation_id = ?")
		params = append(params, int64(*query.ConversationId))
	}
	if query.Kind != nil {
		clauses = append(clauses, "kind = ?")
		params = append(params, encodeIndexedString(*query.Kind))
	}
	if query.Status != nil {
		clauses = append(clauses, "status = ?")
		params = append(params, string(*query.Status))
	}
	if query.AbortRequested != nil {
		clauses = append(clauses, "abort_requested = ?")
		params = append(params, boolInt(*query.AbortRequested))
	}
	if query.Background != nil {
		clauses = append(clauses, "background = ?")
		params = append(params, boolInt(*query.Background))
	}
	params = append(params, int64(limit)+1)
	records, err := allRecords[storedTask](
		storage.db,
		"SELECT record FROM tasks WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id LIMIT ?",
		params...,
	)
	if err != nil {
		return durable.Page[storedTask, durable.Cursor]{}, err
	}
	return page(records, limit, taskIdOf), nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// Submission looks up the latest complete record for one admitted submission.
func (storage *SqliteStorage) Submission(
	_ context.Context, id durable.SubmissionId,
) (*durable.SubmissionRecord, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	return getRecord[durable.SubmissionRecord](storage.db, "SELECT record FROM submissions WHERE id = ?", int64(id))
}

// ScanSubmissions scans submissions matching every supplied filter in ascending ID order.
func (storage *SqliteStorage) ScanSubmissions(
	_ context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	if err := storage.assertOpen(); err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	after, err := cursorStart(cursor)
	if err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	clauses := []string{"id > ?"}
	params := []SqliteValue{after}
	if query.ConversationId != nil {
		clauses = append(clauses, "conversation_id = ?")
		params = append(params, int64(*query.ConversationId))
	}
	if query.Status != nil {
		clauses = append(clauses, "status = ?")
		params = append(params, string(*query.Status))
	}
	params = append(params, int64(limit)+1)
	records, err := allRecords[durable.SubmissionRecord](
		storage.db,
		"SELECT record FROM submissions WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id LIMIT ?",
		params...,
	)
	if err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	return page(records, limit, submissionIdOf), nil
}

// SubmissionByRequest finds a submission by its conversation-scoped host deduplication key.
func (storage *SqliteStorage) SubmissionByRequest(
	_ context.Context, conversationId durable.ConversationId, requestId string,
) (*durable.SubmissionRecord, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	return getRecord[durable.SubmissionRecord](
		storage.db,
		"SELECT record FROM submissions WHERE conversation_id = ? AND request_id = ?",
		int64(conversationId),
		encodeIndexedString(requestId),
	)
}

// FindDocument resolves the incarnation occupying one exact logical address at the selected point.
func (storage *SqliteStorage) FindDocument(
	_ context.Context, address durable.DocumentAddress, at durable.DocumentPoint,
) (*durable.DocumentRecord, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	parts := addressPartsOf(address.Kind, address.Scope, address.Key)
	params := []SqliteValue{parts.kind, parts.scopeKind, parts.ownerId, parts.family, parts.keyValue}
	if at.Current {
		return getRecord[durable.DocumentRecord](storage.db, `SELECT record FROM documents
					WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ?
					AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`, params...)
	}
	params = append(params, int64(at.Seq), int64(at.Seq))
	return getRecord[durable.DocumentRecord](storage.db, `SELECT record FROM documents
					WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ?
					AND created_at <= ? AND (retired_at IS NULL OR retired_at > ?)
					ORDER BY created_at DESC LIMIT 1`, params...)
}

// Document materializes one specific incarnation by ID at the selected point without following a replacement at its
// address.
func (storage *SqliteStorage) Document(
	_ context.Context, id durable.DocumentId, at durable.DocumentPoint,
) (*durable.StoredDocument, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	// The record and revision queries must observe one committed state; a commit between them can replace the base.
	var stored *durable.StoredDocument
	err := storage.db.Transaction(func(transaction SqliteExecutor) (err error) {
		stored, err = materializeDocument(transaction, id, at)
		return err
	})
	return stored, err
}

// ScanDocuments scans incarnations alive in one exact scope at the selected point.
func (storage *SqliteStorage) ScanDocuments(
	_ context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	if err := storage.assertOpen(); err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	after, err := cursorStart(cursor)
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	scope := scopeColumnsOf(query.Scope)
	clauses := []string{"scope_kind = ?", "owner_id = ?", "id > ?"}
	params := []SqliteValue{scope.scopeKind, scope.ownerId, after}
	if query.Kind != nil {
		clauses = append(clauses, "kind = ?")
		params = append(params, encodeIndexedString(*query.Kind))
	}
	if query.At.Current {
		clauses = append(clauses, "retired_at IS NULL")
	} else {
		clauses = append(clauses, "created_at <= ?", "(retired_at IS NULL OR retired_at > ?)")
		params = append(params, int64(query.At.Seq), int64(query.At.Seq))
	}
	params = append(params, int64(limit)+1)
	records, err := allRecords[durable.DocumentRecord](
		storage.db,
		"SELECT record FROM documents WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id LIMIT ?",
		params...,
	)
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	return page(records, limit, documentIdOf), nil
}

// Close waits for admitted multi-query reads, then closes the database. Every call returns the same result once the
// database is closed.
func (storage *SqliteStorage) Close(_ context.Context) error {
	storage.mu.Lock()
	if storage.closeDone != nil {
		done := storage.closeDone
		storage.mu.Unlock()
		<-done
		return storage.closeErr
	}
	storage.closed = true
	done := make(chan struct{})
	storage.closeDone = done
	var drained chan struct{}
	if storage.admittedReads > 0 {
		drained = make(chan struct{})
		storage.readsDrained = drained
	}
	storage.mu.Unlock()
	if drained != nil {
		<-drained
	}
	storage.closeErr = storage.db.Close()
	close(done)
	return storage.closeErr
}

// admitRead runs a read that issues several queries. Close waits for admitted reads, so their later queries never
// reach a closed database. Single-query reads and transactions are already ordered before close by the database.
func (storage *SqliteStorage) admitRead(read func() error) error {
	storage.mu.Lock()
	if storage.closed {
		storage.mu.Unlock()
		return errClosed
	}
	storage.admittedReads++
	storage.mu.Unlock()
	defer func() {
		storage.mu.Lock()
		storage.admittedReads--
		if storage.admittedReads == 0 && storage.readsDrained != nil {
			close(storage.readsDrained)
			storage.readsDrained = nil
		}
		storage.mu.Unlock()
	}()
	return read()
}

func (storage *SqliteStorage) readConversation(id durable.ConversationId) (*durable.ConversationRecord, error) {
	return getRecord[durable.ConversationRecord](storage.db, "SELECT record FROM conversations WHERE id = ?", int64(id))
}

func materializeDocument(
	executor SqliteExecutor, id durable.DocumentId, at durable.DocumentPoint,
) (*durable.StoredDocument, error) {
	record, err := getRecord[durable.DocumentRecord](executor, "SELECT record FROM documents WHERE id = ?", int64(id))
	if err != nil || record == nil {
		return nil, err
	}
	if !at.Current && isCurrentOnly(*record) {
		return nil, fmt.Errorf("Document %d does not retain historical content", id)
	}
	if !isAliveAt(*record, at) {
		return nil, nil
	}
	upper := int64(maxSafeInteger)
	if !at.Current {
		upper = int64(at.Seq)
	}
	base, err := executor.Get(`SELECT seq, kind, version, content FROM document_revisions
				WHERE document_id = ? AND kind = 'base' AND seq <= ? ORDER BY seq DESC LIMIT 1`, int64(id), upper)
	if err != nil {
		return nil, err
	}
	if base == nil {
		return nil, fmt.Errorf("Document %d is missing a required base", id)
	}
	baseSeq, err := rowInt(base, "seq")
	if err != nil {
		return nil, err
	}
	baseVersion, err := rowInt(base, "version")
	if err != nil {
		return nil, err
	}
	baseContent, err := rowText(base, "content")
	if err != nil {
		return nil, err
	}
	value, err := parseJSON[durable.JsonValue](baseContent)
	if err != nil {
		return nil, err
	}
	tail, err := executor.All(`SELECT seq, kind, version, content FROM document_revisions
				WHERE document_id = ? AND seq > ? AND seq <= ? ORDER BY seq`, int64(id), baseSeq, upper)
	if err != nil {
		return nil, err
	}
	batches := make([][]durable.Op, 0, len(tail))
	for _, revision := range tail {
		kind, err := rowText(revision, "kind")
		if err != nil {
			return nil, err
		}
		version, err := rowInt(revision, "version")
		if err != nil {
			return nil, err
		}
		if kind != string(durable.ContentDelta) || version != baseVersion {
			return nil, fmt.Errorf("Document %d crosses a stored version boundary without a base", id)
		}
		content, err := rowText(revision, "content")
		if err != nil {
			return nil, err
		}
		batch, err := parseJSON[[]durable.Op](content)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	if value, err = ops.ApplyBatches(value, batches); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Document %d does not materialize to an object", id)
	}
	return &durable.StoredDocument{
		Record: *record, Version: int(baseVersion), Value: object, DeltasSinceBase: len(tail),
	}, nil
}

func (storage *SqliteStorage) candidateNextId(batch []durable.StorageWrite) int64 {
	storage.mu.Lock()
	nextId := storage.nextId
	storage.mu.Unlock()
	for _, write := range batch {
		if id, ok := writeId(write); ok {
			nextId = max(nextId, id+1)
		}
	}
	return nextId
}

// writeTable returns the table a creating write claims its ID in; ok is false for document change and retirement.
func writeTable(write durable.StorageWrite) (int64, tableName, bool) {
	switch write.(type) {
	case durable.ConversationWrite:
		id, _ := writeId(write)
		return id, tableConversation, true
	case durable.EntryWrite:
		id, _ := writeId(write)
		return id, tableEntry, true
	case durable.TaskWrite:
		id, _ := writeId(write)
		return id, tableTask, true
	case durable.SubmissionWrite:
		id, _ := writeId(write)
		return id, tableSubmission, true
	case durable.DocumentCreateWrite, durable.DocumentCopyWrite:
		id, _ := writeId(write)
		return id, tableDocument, true
	default:
		return 0, "", false
	}
}

func checkGlobalIds(executor SqliteExecutor, batch []durable.StorageWrite) error {
	claimed := map[int64]tableName{}
	for _, write := range batch {
		id, table, ok := writeTable(write)
		if !ok {
			continue
		}
		row, err := executor.Get("SELECT record_type FROM record_ids WHERE id = ?", id)
		if err != nil {
			return err
		}
		var existing tableName
		if row != nil {
			text, err := rowText(row, "record_type")
			if err != nil {
				return err
			}
			existing = tableName(text)
		}
		earlier := claimed[id]
		if table == tableConversation || table == tableEntry || table == tableDocument {
			if existing != "" {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" {
				return fmt.Errorf("ID %d is written more than once", id)
			}
		} else {
			if existing != "" && existing != table {
				return fmt.Errorf("ID %d already belongs to %s", id, existing)
			}
			if earlier != "" && earlier != table {
				return fmt.Errorf("ID %d is written as two record types", id)
			}
		}
		claimed[id] = table
	}
	return nil
}

func prepareDocumentActions(batch []durable.StorageWrite) (*documentActions, error) {
	actions := &documentActions{actions: map[durable.DocumentId]*documentAction{}}
	for _, write := range batch {
		switch typed := write.(type) {
		case durable.DocumentCreateWrite:
			action := actions.get(typed.Record.Id)
			if action.create != nil || action.content != nil || action.copy != nil {
				return nil, fmt.Errorf("Document %d has more than one content command", typed.Record.Id)
			}
			action.create = &typed.Record
			action.content = &typed.Content
		case durable.DocumentCopyWrite:
			action := actions.get(typed.Record.Id)
			if action.create != nil || action.content != nil || action.copy != nil {
				return nil, fmt.Errorf("Document %d has more than one content command", typed.Record.Id)
			}
			action.create = &typed.Record
			action.copy = &typed.Source
		case durable.DocumentChangeWrite:
			action := actions.get(typed.Id)
			if action.content != nil || action.copy != nil {
				return nil, fmt.Errorf("Document %d has more than one content command", typed.Id)
			}
			action.content = &typed.Content
		case durable.DocumentRetireWrite:
			action := actions.get(typed.Id)
			if action.retire {
				return nil, fmt.Errorf("Document %d is retired more than once", typed.Id)
			}
			action.retire = true
		}
	}
	return actions, nil
}

func checkDocumentActions(executor SqliteExecutor, actions *documentActions) error {
	liveCounts := map[addressParts]int{}
	var liveOrder []addressParts
	for _, id := range actions.order {
		action := actions.actions[id]
		if action.copy != nil && actions.has(action.copy.Id) {
			return durable.NewStorageRejected(
				fmt.Sprintf("Document copy %d source is changed in the copy batch", id), nil,
			)
		}
		existing, err := getRecord[durable.DocumentRecord](executor, "SELECT record FROM documents WHERE id = ?", int64(id))
		if err != nil {
			return err
		}
		if action.create == nil && existing == nil {
			return fmt.Errorf("Unknown document: %d", id)
		}
		if action.create != nil && existing != nil {
			return fmt.Errorf("Document %d already exists", id)
		}
		if existing != nil && existing.RetiredAt != nil {
			return fmt.Errorf("Document %d is retired", id)
		}
		if action.content != nil && action.content.Kind == durable.ContentDelta {
			previous, err := executor.Get(
				"SELECT version FROM document_revisions WHERE document_id = ? ORDER BY seq DESC LIMIT 1", int64(id),
			)
			if err != nil {
				return err
			}
			if previous == nil {
				return fmt.Errorf("Document %d delta has no base", id)
			}
			version, err := rowInt(previous, "version")
			if err != nil {
				return err
			}
			if version != int64(action.content.Version) {
				return fmt.Errorf("Document %d version transition requires a base", id)
			}
		}
		var parts addressParts
		if action.create != nil {
			parts = addressPartsOf(action.create.Kind, action.create.Scope, action.create.Key)
		} else {
			parts = addressPartsOf(existing.Kind, existing.Scope, existing.Key)
		}
		live, counted := liveCounts[parts]
		if !counted {
			liveOrder = append(liveOrder, parts)
			current, err := currentDocumentId(executor, parts)
			if err != nil {
				return err
			}
			if current {
				live = 1
			}
		}
		if action.retire && existing != nil {
			live--
		}
		if action.create != nil && !action.retire {
			live++
		}
		liveCounts[parts] = live
	}
	for _, parts := range liveOrder {
		if liveCounts[parts] > 1 {
			return errors.New("Document address already has a current incarnation")
		}
	}
	return nil
}

// currentDocumentId reports whether an incarnation is current at the address.
func currentDocumentId(executor SqliteExecutor, parts addressParts) (bool, error) {
	row, err := executor.Get(`SELECT id FROM documents
				WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ? AND retired_at IS NULL
				LIMIT 1`, parts.kind, parts.scopeKind, parts.ownerId, parts.family, parts.keyValue)
	return row != nil, err
}

func applyTableWrite(executor SqliteExecutor, write durable.StorageWrite, seq durable.Seq) error {
	switch typed := write.(type) {
	case durable.ConversationWrite:
		value := typed.Value
		record, err := encodeJSON(value)
		if err != nil {
			return err
		}
		if err := claimId(executor, int64(value.Id), tableConversation); err != nil {
			return err
		}
		var ownerConversation, ownerTask SqliteValue
		if value.Owner != nil {
			ownerConversation = int64(value.Owner.ConversationId)
			ownerTask = int64(value.Owner.TaskId)
		}
		return executor.Run(
			"INSERT INTO conversations (id, owner_conversation_id, owner_task_id, record) VALUES (?, ?, ?, ?)",
			int64(value.Id), ownerConversation, ownerTask, record,
		)
	case durable.EntryWrite:
		value := typed.Value
		if err := claimId(executor, int64(value.Id), tableEntry); err != nil {
			return err
		}
		var head SqliteValue
		if value.Head != nil {
			head = int64(*value.Head)
		}
		record, err := encodeJSON(value)
		if err != nil {
			return err
		}
		return executor.Run(
			"INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, ?, ?)",
			int64(value.Id), int64(value.ConversationId), head, int64(seq), record,
		)
	case durable.TaskWrite:
		value := typed.Value
		if err := claimId(executor, int64(value.Id), tableTask); err != nil {
			return err
		}
		record, err := encodeJSON(value)
		if err != nil {
			return err
		}
		return executor.Run(
			`INSERT INTO tasks (id, conversation_id, kind, status, abort_requested, background, record)
						VALUES (?, ?, ?, ?, ?, ?, ?)
						ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id, kind = excluded.kind,
						status = excluded.status, abort_requested = excluded.abort_requested,
						background = excluded.background, record = excluded.record`,
			int64(value.Id),
			int64(value.ConversationId),
			encodeIndexedString(value.Kind),
			string(value.State.Status),
			boolInt(value.AbortRequested),
			boolInt(value.Background),
			record,
		)
	case durable.SubmissionWrite:
		value := typed.Value
		if err := claimId(executor, int64(value.Id), tableSubmission); err != nil {
			return err
		}
		var requestId SqliteValue
		if value.RequestId != nil {
			requestId = encodeIndexedString(*value.RequestId)
		}
		record, err := encodeJSON(value)
		if err != nil {
			return err
		}
		return executor.Run(
			`INSERT INTO submissions (id, conversation_id, request_id, status, record) VALUES (?, ?, ?, ?, ?)
						ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id,
						request_id = excluded.request_id, status = excluded.status, record = excluded.record`,
			int64(value.Id), int64(value.ConversationId), requestId, string(value.Status), record,
		)
	default:
		return nil
	}
}

func claimId(executor SqliteExecutor, id int64, table tableName) error {
	return executor.Run("INSERT OR IGNORE INTO record_ids (id, record_type) VALUES (?, ?)", id, string(table))
}

func resolveCopy(
	executor SqliteExecutor, id durable.DocumentId, create durable.DocumentCreate, source durable.DocumentCopySource,
) (*durable.DocumentContent, error) {
	stored, err := materializeDocument(executor, source.Id, source.At)
	if err == nil && stored == nil {
		err = fmt.Errorf("Fork source document %d cannot be read", source.Id)
	}
	if err == nil && !copyMatches(stored.Record, create) {
		err = fmt.Errorf("Fork source document %d does not match the copied record", source.Id)
	}
	if err != nil {
		if _, ok := errors.AsType[*durable.StorageRejected](err); ok {
			return nil, err
		}
		return nil, durable.NewStorageRejected(fmt.Sprintf("Document copy %d was rejected", id), err)
	}
	return &durable.DocumentContent{Kind: durable.ContentBase, Version: stored.Version, Value: stored.Value}, nil
}

func copyMatches(source durable.DocumentRecord, create durable.DocumentCreate) bool {
	sameKey := (source.Key == nil && create.Key == nil) ||
		(source.Key != nil && create.Key != nil && *source.Key == *create.Key)
	return source.Scope.Kind == durable.ScopeConversation &&
		create.Scope.Kind == durable.ScopeConversation &&
		source.Kind == create.Kind &&
		sameKey &&
		source.History == create.History &&
		source.Fork == create.Fork
}

func applyDocumentActions(executor SqliteExecutor, actions *documentActions, seq durable.Seq) error {
	for _, id := range actions.order {
		action := actions.actions[id]
		content := action.content
		if action.copy != nil {
			var err error
			if content, err = resolveCopy(executor, id, *action.create, *action.copy); err != nil {
				return err
			}
		}
		var record durable.DocumentRecord
		if action.create != nil {
			record = durable.DocumentRecord{
				Id:        action.create.Id,
				Kind:      action.create.Kind,
				Key:       action.create.Key,
				CreatedAt: seq,
				Scope:     action.create.Scope,
				History:   action.create.History,
				Fork:      action.create.Fork,
			}
			var retiredAt SqliteValue
			if action.retire {
				retired := seq
				record.RetiredAt = &retired
				retiredAt = int64(seq)
			}
			parts := addressPartsOf(record.Kind, record.Scope, record.Key)
			if err := claimId(executor, int64(id), tableDocument); err != nil {
				return err
			}
			encoded, err := encodeJSON(record)
			if err != nil {
				return err
			}
			if err := executor.Run(
				`INSERT INTO documents
						(id, kind, family, key_value, scope_kind, owner_id, created_at, retired_at, record)
						VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				int64(id), parts.kind, parts.family, parts.keyValue, parts.scopeKind, parts.ownerId, int64(seq),
				retiredAt, encoded,
			); err != nil {
				return err
			}
		} else {
			existing, err := getRecord[durable.DocumentRecord](
				executor, "SELECT record FROM documents WHERE id = ?", int64(id),
			)
			if err != nil {
				return err
			}
			if existing == nil {
				return fmt.Errorf("Unknown document: %d", id)
			}
			record = *existing
		}

		if content != nil {
			if content.Kind == durable.ContentBase && isCurrentOnly(record) {
				if err := executor.Run("DELETE FROM document_revisions WHERE document_id = ?", int64(id)); err != nil {
					return err
				}
			}
			var encoded string
			var err error
			if content.Kind == durable.ContentBase {
				encoded, err = encodeJSON(content.Value)
			} else {
				encoded, err = encodeJSON(content.Ops)
			}
			if err != nil {
				return err
			}
			if err := executor.Run(
				"INSERT INTO document_revisions (document_id, seq, kind, version, content) VALUES (?, ?, ?, ?, ?)",
				int64(id), int64(seq), string(content.Kind), int64(content.Version), encoded,
			); err != nil {
				return err
			}
		}

		if action.retire {
			if action.create == nil {
				retired := seq
				record.RetiredAt = &retired
				encoded, err := encodeJSON(record)
				if err != nil {
					return err
				}
				if err := executor.Run(
					"UPDATE documents SET retired_at = ?, record = ? WHERE id = ?", int64(seq), encoded, int64(id),
				); err != nil {
					return err
				}
			}
			if isCurrentOnly(record) {
				if err := executor.Run("DELETE FROM document_revisions WHERE document_id = ?", int64(id)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var errClosed = errors.New("SqliteStorage is closed")

func (storage *SqliteStorage) assertOpen() error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.closed {
		return errClosed
	}
	return nil
}
