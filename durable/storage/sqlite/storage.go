package sqlite

// Ports packages/durable/src/storage/sqlite/storage.ts

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/ordered"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/ops"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/scan"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/writes"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
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

// encodeJSON serializes a stored value as JSON.stringify does (storage.ts encodeJson): lone UTF-16 surrogates survive, and <, >, & and U+2028/U+2029 stay literal.
func encodeJSON(value any) (string, error) {
	buffer := encodeBuffers.Get().(*[]byte)
	encoded, err := jsstring.AppendMarshalJSON((*buffer)[:0], value)
	return finishEncoding(buffer, encoded, err)
}

// encodeEntryJSON is encodeJSON of an entry record without boxing it: the record writes its own text.
func encodeEntryJSON(entry durable.EntryRecord) (string, error) {
	buffer := encodeBuffers.Get().(*[]byte)
	encoded, err := entry.AppendStringify((*buffer)[:0])
	return finishEncoding(buffer, encoded, err)
}

// encodeBuffers holds the buffers stored values are encoded into. The text leaves as a string copy, so one buffer serves
// every row a commit encodes.
var encodeBuffers = sync.Pool{New: func() any { return new([]byte) }}

// maxPooledEncodeBuffer bounds the buffer kept between calls (golang/go#23199): a larger one is left to the collector.
const maxPooledEncodeBuffer = 64 << 10

// finishEncoding returns buffer to the pool and the encoded text as a string, or the error encodeJSON reports.
func finishEncoding(buffer *[]byte, encoded []byte, err error) (string, error) {
	var text string
	if err == nil {
		text = string(encoded)
	}
	if cap(encoded) <= maxPooledEncodeBuffer {
		*buffer = encoded[:0]
	} else {
		*buffer = nil
	}
	encodeBuffers.Put(buffer)
	if err != nil {
		var unsupported *json.UnsupportedValueError
		if errors.As(err, &unsupported) && strings.Contains(unsupported.Str, "cycle") {
			return "", fmt.Errorf("Converting circular structure to JSON: %w", err)
		}
		return "", err
	}
	return text, nil
}

func parseJSON[T any](text string) (T, error) {
	var value T
	if entry, ok := any(&value).(*durable.EntryRecord); ok {
		// EntryRecord's decoder validates the text itself, so the generic decoder's extra scan is skipped.
		if err := entry.UnmarshalJSON(stringBytes(text)); err != nil {
			return value, err
		}
		return value, nil
	}
	if err := jsstring.UnmarshalKeepingSurrogates(stringBytes(text), &value); err != nil {
		return value, err
	}
	if ordered.HasDynamic(reflect.TypeFor[T]()) {
		// A JsonValue member holds the stored text's objects in order, as JSON.parse gives Pi.
		tree, err := chordjson.DecodeUnquoting(stringBytes(text), unquoteJS)
		if err != nil {
			return value, err
		}
		ordered.Restore(&value, tree)
	}
	return value, nil
}

// unquoteJS decodes a JSON string with JavaScript string semantics: a lone surrogate escape becomes its WTF-8 form.
func unquoteJS(raw []byte, text *string) error { return jsstring.UnmarshalKeepingSurrogates(raw, text) }

// parseOps decodes a stored operation batch with its objects in document order.
func parseOps(text string) ([]durable.Op, error) {
	value, err := chordjson.DecodeUnquoting([]byte(text), unquoteJS)
	if err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("stored operations are %T, not an array", value)
	}
	ops := make([]durable.Op, len(items))
	for index, item := range items {
		op, isOp := item.([]any)
		if !isOp {
			return nil, fmt.Errorf("stored operation %d is %T, not an array", index, item)
		}
		ops[index] = op
	}
	return ops, nil
}

// encodeIndexedString keeps indexed identities lossless: some SQLite bindings replace lone UTF-16 surrogates, while
// their JSON escapes survive. The text is JSON.stringify's (storage.ts encodeIndexedString), since a query matches the
// stored column byte for byte and either implementation may have written it.
func encodeIndexedString(value string) string {
	var scratch [64]byte
	return string(jsstring.AppendString(scratch[:0], value))
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

var errInvalidCursor error = durable.NewStorageRequestError("Invalid storage cursor")

func cursorStart(cursor durable.Cursor) (int64, error) {
	after, ok, err := cursorId(cursor)
	if err != nil || !ok {
		return -1, err
	}
	return after, nil
}

func page[T any](values []T, limit int, order durable.ScanOrder, id func(T) int64) durable.Page[T, durable.Cursor] {
	count := min(max(limit, 0), len(values))
	items := values[:count:count]
	if len(values) <= limit {
		return durable.Page[T, durable.Cursor]{Items: items}
	}
	var after int64
	if count > 0 {
		after = id(items[count-1])
	}
	next := scan.NextCursor(after, order)
	return durable.Page[T, durable.Cursor]{Items: items, Next: &next}
}

// scanSql is the ID condition, its parameter, and the ORDER BY direction of a table scan.
func scanSql(start scan.Start) (clause string, param int64, direction string) {
	if start.Order == durable.ScanAscending {
		param = -1
		if start.HasAfter {
			param = start.After
		}
		return "id > ?", param, "ASC"
	}
	param = maxSafeInteger
	if start.HasAfter {
		param = start.After
	}
	return "id < ?", param, "DESC"
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

	retained *workingSet

	schema        SqliteSchema
	statements    *commitStatements
	mu            sync.Mutex
	nextId        int64
	closed        bool
	closeDone     chan struct{}
	closeErr      error
	admittedReads int
	readsDrained  chan struct{}
}

var _ durable.Storage = (*SqliteStorage)(nil)

// SqliteStorageOptions configures a SqliteStorage (Pi's SqliteStorageOptions, which has Prefix only; WorkingSetBytes is additive, D104).
type SqliteStorageOptions struct {
	// Prefix namespaces every table and index of the durable schema, for a database shared with other tables. It must
	// be a SQLite identifier and must not use SQLite's reserved `sqlite_` prefix. Existing data is not migrated when it
	// changes. Nil leaves the names unprefixed; a present empty prefix is invalid.
	Prefix *string

	// WorkingSetBytes bounds the decoded entries the storage retains for context reads: zero selects
	// DefaultWorkingSetBytes and a negative value retains nothing.
	WorkingSetBytes int64
}

// Open initializes storage over an owned SQLite database facade. A failure closes the database.
func Open(db SqliteDatabase, options ...SqliteStorageOptions) (*SqliteStorage, error) {
	var prefix *string
	if len(options) > 0 {
		prefix = options[0].Prefix
	}
	storage, err := open(db, prefix)
	if err != nil {
		// Preserve the initialization failure.
		_ = db.Close()
		return nil, err
	}
	budget := int64(DefaultWorkingSetBytes)
	if len(options) > 0 && options[0].WorkingSetBytes != 0 {
		budget = max(options[0].WorkingSetBytes, 0)
	}
	storage.retained = newWorkingSet(budget, storage.schema)
	return storage, nil
}

func open(db SqliteDatabase, prefix *string) (*SqliteStorage, error) {
	schema, err := CreateSqliteSchema(prefix)
	if err != nil {
		return nil, err
	}
	if err := ApplySqliteMigrationsForSchema(db, schema, nil); err != nil {
		return nil, err
	}
	metadata, err := db.Get("SELECT next_id, next_seq FROM " + schema.Tables.DurableMetadata + " WHERE singleton = 1")
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
	return &SqliteStorage{db: db, schema: schema, statements: newCommitStatements(schema.Tables), nextId: nextId}, nil
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

// databaseCalls records whether a database call of a batch's checks failed.
type databaseCalls struct{ failed bool }

// watchedExecutor is an executor that sets calls.failed when one of its calls returns an error: a failing check is
// then the database's, not one of Durable's own.
type watchedExecutor struct {
	executor SqliteExecutor
	calls    *databaseCalls
}

func (watched watchedExecutor) watch(err error) error {
	if err != nil {
		watched.calls.failed = true
	}
	return err
}

func (watched watchedExecutor) Exec(sql string) error {
	return watched.watch(watched.executor.Exec(sql))
}

func (watched watchedExecutor) Run(sql string, params ...SqliteValue) error {
	return watched.watch(watched.executor.Run(sql, params...))
}

func (watched watchedExecutor) RunCounted(sql string, params ...SqliteValue) (int64, error) {
	count, err := watched.executor.RunCounted(sql, params...)
	return count, watched.watch(err)
}

func (watched watchedExecutor) Get(sql string, params ...SqliteValue) (SqliteRow, error) {
	row, err := watched.executor.Get(sql, params...)
	return row, watched.watch(err)
}

func (watched watchedExecutor) All(sql string, params ...SqliteValue) ([]SqliteRow, error) {
	rows, err := watched.executor.All(sql, params...)
	return rows, watched.watch(err)
}

// Commit commits batches in one SQL transaction, each with its own seq, in a few statements whatever their number
// (commit.go). Each batch is checked in memory before it is accepted, against the state the earlier ones left, and the
// first one refused ends the commit: the transaction commits the ones before it. No savepoints, which Durable Objects do
// not allow: the checks run before the writes instead.
func (storage *SqliteStorage) Commit(
	_ context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq),
) (durable.CommitResult, error) {
	if err := storage.assertOpen(); err != nil {
		return durable.CommitResult{}, err
	}
	// Pointer-form writes are normalized once, so every check below reads the value forms.
	values := make([][]durable.StorageWrite, len(batches))
	for i, batch := range batches {
		values[i] = writes.Values(batch)
	}
	if slices.ContainsFunc(values, hasEntryWrite) {
		storage.retained.mu.Lock()
		defer storage.retained.mu.Unlock()
	}
	var seqs []durable.Seq
	var committedEntries []committedEntry
	var refused error
	candidateNextId := storage.candidateNextId(nil)
	err := storage.db.Transaction(func(transaction SqliteExecutor) error {
		seqs = seqs[:0]
		committedEntries = nil
		refused = nil
		candidateNextId = storage.candidateNextId(nil)
		storedNextId, nextSeq, err := storage.readMetadata(transaction)
		if err != nil {
			return err
		}
		next := nextSeq
		// A failure of the database fails the call and rolls back the batches before it with the transaction.
		state, err := storage.readCommitState(transaction, values)
		if err != nil {
			return err
		}
		pending := &pendingWrites{}
		for _, batch := range values {
			actions, refusal, err := storage.checkBatch(transaction, state, pending, batch)
			if err != nil {
				return err
			}
			if refusal != nil {
				refused = refusal
				break
			}
			seq := durable.Seq(next)
			next++
			state.apply(batch, actions, seq)
			if err := pending.add(batch, actions, seq, state); err != nil {
				return err
			}
			seqs = append(seqs, seq)
			candidateNextId = max(candidateNextId, storage.candidateNextId(batch))
		}
		if err := pending.flush(transaction, storage.statements); err != nil {
			return err
		}
		committedEntries = append(committedEntries, pending.stored...)
		if len(seqs) > 0 {
			return storage.writeMetadata(transaction, max(storedNextId, candidateNextId), next)
		}
		return nil
	})
	if err != nil {
		return durable.CommitResult{}, err
	}
	storage.mu.Lock()
	storage.nextId = max(storage.nextId, candidateNextId)
	storage.mu.Unlock()
	if len(committedEntries) > 0 {
		storage.retained.retainCommitted(committedEntries)
	}
	// COMMIT returned: the stored batches are durable.
	for index, seq := range seqs {
		receive(index, seq)
	}
	return durable.CommitResult{Stored: len(seqs), Refused: refused}, nil
}

// readMetadata reads next_id and next_seq.
func (storage *SqliteStorage) readMetadata(executor SqliteExecutor) (nextId, nextSeq int64, err error) {
	metadata, err := executor.Get("SELECT next_id, next_seq FROM " + storage.schema.Tables.DurableMetadata + " WHERE singleton = 1")
	if err != nil {
		return 0, 0, err
	}
	if metadata == nil {
		return 0, 0, errors.New("Durable SQLite metadata is missing")
	}
	if nextSeq, err = rowInt(metadata, "next_seq"); err != nil {
		return 0, 0, err
	}
	if nextId, err = metadataNextId(metadata); err != nil {
		return 0, 0, err
	}
	return nextId, nextSeq, nil
}

func (storage *SqliteStorage) writeMetadata(executor SqliteExecutor, nextId, nextSeq int64) error {
	return executor.Run(
		"UPDATE "+storage.schema.Tables.DurableMetadata+" SET next_id = ?, next_seq = ? WHERE singleton = 1",
		strconv.FormatInt(nextId, 10), nextSeq,
	)
}

// checkBatch is Durable's own checks of one batch, copy resolution included, before it is accepted: the refusal, or the
// batch's document actions. Only these checks refuse; a failure of a database call is the error. A copy reads its source
// from the database, so the batches accepted before it are written first: a call of many fork batches costs a few
// statements per fork, not per write.
func (storage *SqliteStorage) checkBatch(
	executor SqliteExecutor, state *commitState, pending *pendingWrites, batch []durable.StorageWrite,
) (actions *documentActions, refusal error, err error) {
	actions, refusal = prepareDocumentActions(batch)
	if refusal == nil {
		refusal = state.check(batch, actions)
	}
	if refusal != nil {
		return nil, refusal, nil
	}
	copies := false
	for _, id := range actions.order {
		copies = copies || actions.actions[id].copy != nil
	}
	if !copies {
		return actions, nil, nil
	}
	if err := pending.flush(executor, storage.statements); err != nil {
		return nil, nil, err
	}
	// Whether a database call of the copies' resolution failed: then the failure is an unknown outcome, not a refusal.
	calls := &databaseCalls{}
	if err := storage.resolveCopies(watchedExecutor{executor: executor, calls: calls}, actions); err != nil {
		if calls.failed {
			return nil, nil, err
		}
		return nil, err, nil
	}
	return actions, nil, nil
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
	return getRecord[durable.ConversationRecord](storage.db, "SELECT record FROM "+storage.schema.Tables.Conversations+" WHERE id = ?", id)
}

// recordColumn names the one column of the record queries, for an executor that builds rows as maps.
var recordColumn = []string{"record"}

// entryColumns and revisionColumns name the columns of the queries that read them by position.
var (
	entryColumns    = []string{"record", "commit_seq"}
	revisionColumns = []string{"seq", "kind", "version", "content"}
)

// errFirstRow ends a read after the first row it wanted.
var errFirstRow = errors.New("first row read")

func getRecord[T any](executor SqliteExecutor, sqlText string, params ...SqliteValue) (*T, error) {
	var found *T
	err := eachRow(executor, true, sqlText, params, recordColumn, func(values []SqliteValue) error {
		text, err := valueText(values[0], "record")
		if err != nil {
			return err
		}
		record, err := parseJSON[T](text)
		if err != nil {
			return err
		}
		found = &record
		return errFirstRow
	})
	if err != nil && !errors.Is(err, errFirstRow) {
		return nil, err
	}
	return found, nil
}

func allRecords[T any](executor SqliteExecutor, sqlText string, params ...SqliteValue) ([]T, error) {
	records := []T{}
	err := eachRow(executor, false, sqlText, params, recordColumn, func(values []SqliteValue) error {
		text, err := valueText(values[0], "record")
		if err != nil {
			return err
		}
		record, err := parseJSON[T](text)
		if err != nil {
			return err
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func conversationIdOf(record durable.ConversationRecord) int64 { return int64(record.Id) }
func entryIdOf(record durable.EntryRecord) int64               { return int64(record.Id) }
func taskIdOf(record storedTask) int64                         { return int64(record.Id) }
func submissionIdOf(record durable.SubmissionRecord) int64     { return int64(record.Id) }
func documentIdOf(record durable.DocumentRecord) int64         { return int64(record.Id) }

// ScanConversations scans conversations by ID in query.Order, ascending by default.
func (storage *SqliteStorage) ScanConversations(
	_ context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	if err := storage.assertOpen(); err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	clause, param, direction := scanSql(start)
	clauses := []string{clause}
	params := []SqliteValue{param}
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
		"SELECT record FROM "+storage.schema.Tables.Conversations+" WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id "+direction+" LIMIT ?",
		params...,
	)
	if err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	return page(records, limit, start.Order, conversationIdOf), nil
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

// ScanEntries scans the inclusive visible range in query.Order (descending by default), returning at most limit
// entries.
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
			return nil, durable.NewStorageRequestError(fmt.Sprintf("Unknown conversation: %d", *conversationId))
		}
	}
	var entry durable.EntryRecord
	var commitSeq int64
	found := false
	err := eachRow(storage.db, true, "SELECT record, commit_seq FROM "+storage.schema.Tables.Entries+" WHERE id = ?", []SqliteValue{int64(id)}, entryColumns,
		func(values []SqliteValue) (err error) {
			text, err := valueText(values[0], "record")
			if err != nil {
				return err
			}
			if entry, err = parseJSON[durable.EntryRecord](text); err != nil {
				return err
			}
			if commitSeq, err = valueInt(values[1], "commit_seq"); err != nil {
				return err
			}
			found = true
			return errFirstRow
		})
	if err != nil && !errors.Is(err, errFirstRow) {
		return nil, err
	}
	if !found {
		return nil, nil
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
				return nil, errMissingParent
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
		return nil, durable.NewStorageRequestError(fmt.Sprintf("Unknown conversation: %d", conversationId))
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
				"SELECT record FROM "+storage.schema.Tables.Entries+" WHERE conversation_id = ? AND head IS NOT NULL ORDER BY id DESC LIMIT 1",
				int64(conversation.Id),
			)
		} else {
			marker, err = getRecord[durable.EntryRecord](
				storage.db,
				"SELECT record FROM "+storage.schema.Tables.Entries+" WHERE conversation_id = ? AND head IS NOT NULL AND id <= ? ORDER BY id DESC LIMIT 1",
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
			return nil, errMissingParent
		}
	}
}

func (storage *SqliteStorage) readEntries(
	query durable.EntryQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	empty := durable.Page[durable.EntryRecord, durable.Cursor]{}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanDescending)
	if err != nil {
		return empty, err
	}
	if start.Order == durable.ScanAscending {
		return storage.readEntriesAscending(query, limit, start)
	}
	conversation, err := storage.readConversation(query.ConversationId)
	if err != nil {
		return empty, err
	}
	if conversation == nil {
		return empty, durable.NewStorageRequestError(fmt.Sprintf("Unknown conversation: %d", query.ConversationId))
	}
	after, hasAfter := start.After, start.HasAfter
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
			"SELECT record FROM "+storage.schema.Tables.Entries+" WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id DESC LIMIT ?",
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
			return empty, errMissingParent
		}
	}
	return page(values, limit, durable.ScanDescending, entryIdOf), nil
}

// readEntriesAscending reads oldest first: the fork chain's segments from the root conversation forward, each up to its
// fork point.
func (storage *SqliteStorage) readEntriesAscending(
	query durable.EntryQuery, limit int, start scan.Start,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	empty := durable.Page[durable.EntryRecord, durable.Cursor]{}
	type segment struct {
		conversationId durable.ConversationId
		upper          *int64
	}
	segments := []segment{}
	conversation, err := storage.readConversation(query.ConversationId)
	if err != nil {
		return empty, err
	}
	if conversation == nil {
		return empty, durable.NewStorageRequestError(fmt.Sprintf("Unknown conversation: %d", query.ConversationId))
	}
	var upper *int64
	if query.MaxEntryId != nil {
		value := int64(*query.MaxEntryId)
		upper = &value
	}
	for {
		segments = append(segments, segment{conversationId: conversation.Id, upper: upper})
		if conversation.Parent == nil {
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
			return empty, errMissingParent
		}
	}
	var lower *int64
	if query.MinEntryId != nil {
		value := int64(*query.MinEntryId)
		lower = &value
	}
	if start.HasAfter {
		value := start.After + 1
		if lower != nil {
			value = max(*lower, value)
		}
		lower = &value
	}
	values := []durable.EntryRecord{}
	for _, current := range slices.Backward(segments) {
		clauses := []string{"conversation_id = ?"}
		params := []SqliteValue{int64(current.conversationId)}
		if lower != nil {
			clauses = append(clauses, "id >= ?")
			params = append(params, *lower)
		}
		if current.upper != nil {
			clauses = append(clauses, "id <= ?")
			params = append(params, *current.upper)
		}
		params = append(params, int64(limit+1-len(values)))
		records, err := allRecords[durable.EntryRecord](
			storage.db,
			"SELECT record FROM "+storage.schema.Tables.Entries+" WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id ASC LIMIT ?",
			params...,
		)
		if err != nil {
			return empty, err
		}
		values = append(values, records...)
		if len(values) > limit {
			break
		}
	}
	return page(values, limit, durable.ScanAscending, entryIdOf), nil
}

// Task looks up the latest complete record for one task.
func (storage *SqliteStorage) Task(_ context.Context, id durable.TaskId) (*storedTask, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	return getRecord[storedTask](storage.db, "SELECT record FROM "+storage.schema.Tables.Tasks+" WHERE id = ?", int64(id))
}

// ScanTasks scans task records matching every supplied filter by ID in query.Order, ascending by default.
func (storage *SqliteStorage) ScanTasks(
	_ context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor,
) (durable.Page[storedTask, durable.Cursor], error) {
	if err := storage.assertOpen(); err != nil {
		return durable.Page[storedTask, durable.Cursor]{}, err
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return durable.Page[storedTask, durable.Cursor]{}, err
	}
	clause, param, direction := scanSql(start)
	clauses := []string{clause}
	params := []SqliteValue{param}
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
		"SELECT record FROM "+storage.schema.Tables.Tasks+" WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id "+direction+" LIMIT ?",
		params...,
	)
	if err != nil {
		return durable.Page[storedTask, durable.Cursor]{}, err
	}
	return page(records, limit, start.Order, taskIdOf), nil
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
	return getRecord[durable.SubmissionRecord](storage.db, "SELECT record FROM "+storage.schema.Tables.Submissions+" WHERE id = ?", int64(id))
}

// ScanSubmissions scans submissions matching every supplied filter by ID in query.Order, ascending by default.
func (storage *SqliteStorage) ScanSubmissions(
	_ context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	if err := storage.assertOpen(); err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	clause, param, direction := scanSql(start)
	clauses := []string{clause}
	params := []SqliteValue{param}
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
		"SELECT record FROM "+storage.schema.Tables.Submissions+" WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id "+direction+" LIMIT ?",
		params...,
	)
	if err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	return page(records, limit, start.Order, submissionIdOf), nil
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
		"SELECT record FROM "+storage.schema.Tables.Submissions+" WHERE conversation_id = ? AND request_id = ?",
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
		return getRecord[durable.DocumentRecord](storage.db, `SELECT record FROM `+storage.schema.Tables.Documents+`
					WHERE kind = ? AND scope_kind = ? AND owner_id = ? AND family = ? AND key_value = ?
					AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`, params...)
	}
	params = append(params, int64(at.Seq), int64(at.Seq))
	return getRecord[durable.DocumentRecord](storage.db, `SELECT record FROM `+storage.schema.Tables.Documents+`
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
		stored, err = storage.materializeDocument(transaction, id, at)
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
		"SELECT record FROM "+storage.schema.Tables.Documents+" WHERE "+strings.Join(clauses, " AND ")+" ORDER BY id LIMIT ?",
		params...,
	)
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	return page(records, limit, durable.ScanAscending, documentIdOf), nil
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
	storage.retained.release()
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
	return getRecord[durable.ConversationRecord](storage.db, "SELECT record FROM "+storage.schema.Tables.Conversations+" WHERE id = ?", int64(id))
}

func (storage *SqliteStorage) materializeDocument(
	executor SqliteExecutor, id durable.DocumentId, at durable.DocumentPoint,
) (*durable.StoredDocument, error) {
	record, err := getRecord[durable.DocumentRecord](executor, "SELECT record FROM "+storage.schema.Tables.Documents+" WHERE id = ?", int64(id))
	if err != nil || record == nil {
		return nil, err
	}
	if !at.Current && isCurrentOnly(*record) {
		return nil, durable.NewStorageRequestError(fmt.Sprintf("Document %d does not retain historical content", id))
	}
	if !isAliveAt(*record, at) {
		return nil, nil
	}
	upper := int64(maxSafeInteger)
	if !at.Current {
		upper = int64(at.Seq)
	}
	var baseSeq, baseVersion int64
	var value any
	found := false
	err = eachRow(executor, true, `SELECT seq, kind, version, content FROM `+storage.schema.Tables.DocumentRevisions+`
				WHERE document_id = ? AND kind = 'base' AND seq <= ? ORDER BY seq DESC LIMIT 1`, []SqliteValue{int64(id), upper}, revisionColumns,
		func(values []SqliteValue) (err error) {
			if baseSeq, err = valueInt(values[0], "seq"); err != nil {
				return err
			}
			if baseVersion, err = valueInt(values[2], "version"); err != nil {
				return err
			}
			content, err := valueText(values[3], "content")
			if err != nil {
				return err
			}
			if value, err = chordjson.DecodeUnquoting(stringBytes(content), unquoteJS); err != nil {
				return err
			}
			found = true
			return errFirstRow
		})
	if err != nil && !errors.Is(err, errFirstRow) {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("Document %d is missing a required base", id)
	}
	batches := [][]durable.Op{}
	err = eachRow(executor, false, `SELECT seq, kind, version, content FROM `+storage.schema.Tables.DocumentRevisions+`
				WHERE document_id = ? AND seq > ? AND seq <= ? ORDER BY seq`, []SqliteValue{int64(id), baseSeq, upper}, revisionColumns,
		func(values []SqliteValue) error {
			kind, err := valueText(values[1], "kind")
			if err != nil {
				return err
			}
			version, err := valueInt(values[2], "version")
			if err != nil {
				return err
			}
			if kind != string(durable.ContentDelta) || version != baseVersion {
				return fmt.Errorf("Document %d crosses a stored version boundary without a base", id)
			}
			content, err := valueText(values[3], "content")
			if err != nil {
				return err
			}
			batch, err := parseOps(content)
			if err != nil {
				return err
			}
			batches = append(batches, batch)
			return nil
		})
	if err != nil {
		return nil, err
	}
	if value, err = ops.ApplyBatches(value, batches); err != nil {
		return nil, err
	}
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return nil, fmt.Errorf("Document %d does not materialize to an object", id)
	}
	return &durable.StoredDocument{
		Record: *record, Version: int(baseVersion), Value: object, DeltasSinceBase: len(batches),
	}, nil
}

func hasEntryWrite(batch []durable.StorageWrite) bool {
	for _, write := range batch {
		if _, ok := write.(durable.EntryWrite); ok {
			return true
		}
	}
	return false
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

func (storage *SqliteStorage) resolveCopy(
	executor SqliteExecutor, id durable.DocumentId, create durable.DocumentCreate, source durable.DocumentCopySource,
) (*durable.DocumentContent, error) {
	stored, err := storage.materializeDocument(executor, source.Id, source.At)
	if err == nil && stored == nil {
		err = fmt.Errorf("Fork source document %d cannot be read", source.Id)
	}
	if err == nil && !copyMatches(stored.Record, create) {
		err = fmt.Errorf("Fork source document %d does not match the copied record", source.Id)
	}
	if err != nil {
		return nil, &scan.CausedError{Message: fmt.Sprintf("Document copy %d was rejected", id), Cause: err}
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

// resolveCopies resolves each copy to a base of its source's state before this batch, which may not change the source.
func (storage *SqliteStorage) resolveCopies(executor SqliteExecutor, actions *documentActions) error {
	for _, id := range actions.order {
		action := actions.actions[id]
		if action.copy == nil {
			continue
		}
		content, err := storage.resolveCopy(executor, id, *action.create, *action.copy)
		if err != nil {
			return err
		}
		action.content = content
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

// errMissingParent is the TypeError Pi's fork walks throw when a parent conversation is not stored: the next step reads
// `.id` of the undefined record (sqlite/storage.ts readEntry, readLatestHeadMarker, readEntries, readEntriesAscending).
// A commit does not check that a parent exists.
var errMissingParent = errors.New("Cannot read properties of undefined (reading 'id')")
