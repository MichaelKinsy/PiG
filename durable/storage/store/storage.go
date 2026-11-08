package store

// Ports packages/durable/src/storage/store/storage.ts
//
// Durable's `Storage` on Chord's store (slice 6b D1, D2, §2): opening under an owner name (claims, tables, the
// allocator), commits as one series of the store's commit queue, and the reads, each one guarded read of the store
// (several queries at one state in a read transaction), its request errors in a tagged result told after it.
//
// Async contract: Commit blocks until its series is durable and every receipt ran (in order, synchronously, after
// COMMIT and before the store's next step); a refusal of Durable's own checks is CommitResult.Refused, every other
// failure the returned error. Reads block until their guarded read of the store settled; a request error
// (*durable.StorageRequestError) is returned after the guarded read, never inside it. Close refuses new calls, waits
// for the commits underway, then releases the claims; the store, the app's, stays open.

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"sync"

	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/scan"
)

type storedTask = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

const maxSafeInteger = 1<<53 - 1

// errClosed is the error of a call on a closed storage.
var errClosed = errors.New("StoreStorage is closed")

// readResult is a guarded read's result: its data, or its request error, which is returned after the guarded read
// (slice 6b §1.4), never inside it.
type readResult[T any] struct {
	value   T
	refused string
	isError bool
}

// found is a read that found value.
func found[T any](value T) readResult[T] { return readResult[T]{value: value} }

// unknownConversation is a refused read of an unknown conversation.
func unknownConversation[T any](id durable.ConversationId) readResult[T] {
	return readResult[T]{refused: "Unknown conversation: " + itoa(int64(id)), isError: true}
}

// told is a read's data, its request error returned now that the guarded read returned (slice 6b §1.4).
func told[T any](result readResult[T], err error) (T, error) {
	if err != nil {
		var zero T
		return zero, err
	}
	if result.isError {
		var zero T
		return zero, durable.NewStorageRequestError(result.refused)
	}
	return result.value, nil
}

// scanFrom is the id condition of a table scan from start.
func scanFrom(start scan.Start) Condition {
	if start.Order == durable.ScanAscending {
		after := int64(-1)
		if start.HasAfter {
			after = start.After
		}
		return Condition{Column: "id", Op: chordstore.WhereGt, Value: after}
	}
	after := int64(maxSafeInteger)
	if start.HasAfter {
		after = start.After
	}
	return Condition{Column: "id", Op: chordstore.WhereLt, Value: after}
}

func orderOf(order durable.ScanOrder) chordstore.Order {
	if order == durable.ScanAscending {
		return ascending
	}
	return descending
}

// page is the page of values: up to limit of them, and a cursor when there are more.
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

// cursorID is the id a document scan's cursor continues after; absent: ok false.
func cursorID(cursor durable.Cursor) (int64, bool, error) {
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
			return 0, false, scan.ErrInvalidCursor
		}
		//portlint:allow numbers typed is whole and within ±(2^53-1) by the check above, which is Number.isSafeInteger (storage.ts cursorId)
		after = int64(typed)
	default:
		return 0, false, scan.ErrInvalidCursor
	}
	if after > maxSafeInteger || after < -maxSafeInteger {
		return 0, false, scan.ErrInvalidCursor
	}
	return after, true, nil
}

// StoreStorage is Durable's `Storage` on Chord's store (slice 6b D1, D2, §2): its records as declared tables under
// an owner name, its documents in the store's documents engine, its ids from the store's one allocator, each commit
// call as one series of the store's commit queue (one backend transaction, one seq per batch), published in the
// host's batch. Several may share a store under distinct owner names (claims); the app owns the store, and Close
// leaves it open.
type StoreStorage struct {
	store    *chordstore.ChordStore
	owner    string
	claimant *chordstore.NamedClaimant
	tables   *CommitTables
	queries  *StorageQueries

	mu       sync.Mutex
	closed   bool
	closing  chan struct{}
	underway int
	// drained is closed once the calls underway reach 0, when Close waits for them.
	drained chan struct{}
}

var (
	_ durable.Storage           = (*StoreStorage)(nil)
	_ durable.PublishingStorage = (*StoreStorage)(nil)
)

// Open opens Durable's storage in store under owner (a lowercase `[a-z0-9@/._~-]` name): claims its tables and its
// document namespaces (a second claim is refused `invalid/args`), creates or migrates its tables, and raises the
// store's allocator above every id it holds (one index read). A failure releases what it claimed.
func Open(ctx context.Context, store chordstore.Store, owner string) (_ *StoreStorage, err error) {
	// Chord's own: OpenStore or ImportStore made it.
	chord, ok := store.(*chordstore.ChordStore)
	if !ok {
		return nil, errors.New("StoreStorage needs a ChordStore")
	}
	claimant := &chordstore.NamedClaimant{Label: "StoreStorage(" + owner + ")"}
	defer func() {
		if err != nil {
			chord.Claims.Release(claimant)
		}
	}()
	declared, err := chordstore.NewStoreOwnerTables(chord, owner, claimant)
	if err != nil {
		return nil, err
	}
	tables := &CommitTables{}
	for _, each := range []struct {
		target     **chordstore.OwnerTable
		definition *chordstore.TableDefinition
	}{
		{&tables.RecordIds, RecordIds},
		{&tables.Conversations, Conversations},
		{&tables.Entries, Entries},
		{&tables.EntryHeads, EntryHeads},
		{&tables.Tasks, Tasks},
		{&tables.Submissions, Submissions},
	} {
		if *each.target, err = declared.Declare(each.definition); err != nil {
			return nil, err
		}
	}
	for _, namespace := range ScopeNamespaces {
		if err := chord.Claims.Documents(owner, namespace, claimant); err != nil {
			return nil, err
		}
	}
	if err := declared.Prepare(ctx); err != nil {
		return nil, err
	}
	highest, err := chordstore.Read(chord, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
		return executor.Get(`SELECT max("id") AS id FROM "` + tables.RecordIds.Sql.Table + `"`)
	}, ctx)
	if err != nil {
		return nil, err
	}
	if highest != nil && highest["id"] != nil {
		id, err := sqlite.RowInt(highest, "id")
		if err != nil {
			return nil, err
		}
		chord.Ids().Raise(id)
	}
	queries, err := NewStorageQueries(tables)
	if err != nil {
		return nil, err
	}
	return &StoreStorage{store: chord, owner: owner, claimant: claimant, tables: tables, queries: queries}, nil
}

// Commit commits batches as one series of the store (ChordStore.CommitSeries): one backend transaction, one seq per
// batch, each batch's checks (at staging, then against the state the earlier ones left) before its writes.
func (storage *StoreStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	if err := storage.enter(); err != nil {
		return durable.CommitResult{}, err
	}
	defer storage.leave()
	raise := func(id int64) { storage.store.Ids().Raise(id) }
	commits := make([]chordstore.SeriesCommit, 0, len(batches))
	for _, batch := range batches {
		commit, err := NewDurableCommit(batch, storage.owner, storage.tables, raise, ctx)
		if err != nil {
			// Staging runs Durable's checks of the batch itself: a refusal in its turn, the batches before it stored.
			refusal := err
			commits = append(commits, chordstore.SeriesCommit{Check: func(*chordstore.Step) error { return refusal }})
			break
		}
		commits = append(commits, commit.Series())
	}
	result, err := storage.store.CommitSeries(commits, func(index int, seq int64, _ []int64) error {
		receive(index, durable.SeqFromNumber(seq))
		return nil
	}, ctx)
	if err != nil {
		return durable.CommitResult{}, err
	}
	return durable.CommitResult{Stored: result.Stored, Refused: result.Refused}, nil
}

// Publishing runs a commit's publication in the attached host's batch (`batch.Within`: it joins a running batch,
// opens one, or runs next while one sends): Chord sources over Durable's views are marked in one batch per commit.
// Plainly without a host.
func (storage *StoreStorage) Publishing(ctx context.Context, publish func()) {
	batch := storage.store.Batch()
	if batch == nil {
		publish()
		return
	}
	// The batch has no locks: the host serializes every call to it. A commit's writer calls from a goroutine of its own, so
	// the publication is a section of the host's loop (inline when the caller is in one), as Pi's continuation after the
	// commit's `await` is on its one event loop.
	batch.Section(func() {
		// publish never fails: the batch's own failure is the host's (it tells its onError).
		_ = batch.Within(func() error {
			publish()
			return nil
		}, ctx)
	})
}

// MintId takes a fresh id from the store's one allocator.
func (storage *StoreStorage) MintId() (int64, error) {
	if err := storage.enter(); err != nil {
		return 0, err
	}
	defer storage.leave()
	return storage.store.MintId()
}

// Conversation looks up one conversation by exact id; nil when absent.
func (storage *StoreStorage) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	return reading(storage, ctx, func(executor sqlite.SqliteExecutor) (*durable.ConversationRecord, error) {
		return storage.conversation(executor, id, ctx)
	})
}

// ScanConversations scans conversations in query.Order (default ascending) by id.
func (storage *StoreStorage) ScanConversations(ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	var none durable.Page[durable.ConversationRecord, durable.Cursor]
	if err := storage.assertOpen(); err != nil {
		return none, err
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return none, err
	}
	conditions := []Condition{scanFrom(start)}
	if query.OwnerConversationId != nil {
		conditions = append(conditions, Condition{Column: "owner_conversation_id", Op: chordstore.WhereEq, Value: int64(*query.OwnerConversationId)})
	}
	if query.OwnerTaskId != nil {
		conditions = append(conditions, Condition{Column: "owner_task_id", Op: chordstore.WhereEq, Value: int64(*query.OwnerTaskId)})
	}
	return scanTable(storage, ctx, storage.queries.Conversations, conditions, start, limit,
		func(record durable.ConversationRecord) int64 { return int64(record.Id) })
}

// Entry looks up one global entry and the sequence of the commit that persisted it; nil when absent.
func (storage *StoreStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	return reading(storage, ctx, func(executor sqlite.SqliteExecutor) (*durable.EntryAt, error) {
		return storage.entry(executor, id, ctx)
	})
}

// VisibleEntry looks up one entry only when it is visible through the requested conversation's ancestry.
func (storage *StoreStorage) VisibleEntry(ctx context.Context, conversationID durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	return told(chordstore.Read(storage.store, func(executor sqlite.SqliteExecutor) (readResult[*durable.EntryAt], error) {
		return storage.visibleEntry(executor, conversationID, id, ctx)
	}, ctx))
}

// FindLatestHeadMarker returns the newest visible entry with a Head at or below the optional inclusive cutoff.
func (storage *StoreStorage) FindLatestHeadMarker(ctx context.Context, conversationID durable.ConversationId, atOrBeforeEntryID *durable.EntryId) (*durable.EntryRecord, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	return told(chordstore.Read(storage.store, func(executor sqlite.SqliteExecutor) (readResult[*durable.EntryRecord], error) {
		return storage.latestHeadMarker(executor, conversationID, atOrBeforeEntryID, ctx)
	}, ctx))
}

// ScanEntries scans the inclusive visible range in query.Order (default descending), returning at most limit entries.
func (storage *StoreStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	var none durable.Page[durable.EntryRecord, durable.Cursor]
	if err := storage.assertOpen(); err != nil {
		return none, err
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanDescending)
	if err != nil {
		return none, err
	}
	return told(chordstore.Read(storage.store, func(executor sqlite.SqliteExecutor) (readResult[durable.Page[durable.EntryRecord, durable.Cursor]], error) {
		if start.Order == durable.ScanAscending {
			return storage.entriesAscending(executor, query, limit, start, ctx)
		}
		return storage.entriesDescending(executor, query, limit, start, ctx)
	}, ctx))
}

// Task looks up the latest complete record for one task; nil when absent.
func (storage *StoreStorage) Task(ctx context.Context, id durable.TaskId) (*storedTask, error) {
	return byID[storedTask](storage, ctx, storage.queries.Tasks, int64(id))
}

// ScanTasks scans task records matching every supplied filter in query.Order (default ascending) by id.
func (storage *StoreStorage) ScanTasks(ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[storedTask, durable.Cursor], error) {
	var none durable.Page[storedTask, durable.Cursor]
	if err := storage.assertOpen(); err != nil {
		return none, err
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return none, err
	}
	conditions := []Condition{scanFrom(start)}
	if query.ConversationId != nil {
		conditions = append(conditions, Condition{Column: "conversation_id", Op: chordstore.WhereEq, Value: int64(*query.ConversationId)})
	}
	if query.Kind != nil {
		conditions = append(conditions, Condition{Column: "kind", Op: chordstore.WhereEq, Value: EncodeIndexed(*query.Kind)})
	}
	if query.Status != nil {
		conditions = append(conditions, Condition{Column: "status", Op: chordstore.WhereEq, Value: string(*query.Status)})
	}
	if query.AbortRequested != nil {
		conditions = append(conditions, Condition{Column: "abort_requested", Op: chordstore.WhereEq, Value: boolInt(*query.AbortRequested)})
	}
	if query.Background != nil {
		conditions = append(conditions, Condition{Column: "background", Op: chordstore.WhereEq, Value: boolInt(*query.Background)})
	}
	return scanTable(storage, ctx, storage.queries.Tasks, conditions, start, limit,
		func(record storedTask) int64 { return int64(record.Id) })
}

// Submission looks up the latest complete record for one admitted submission; nil when absent.
func (storage *StoreStorage) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	return byID[durable.SubmissionRecord](storage, ctx, storage.queries.Submissions, int64(id))
}

// ScanSubmissions scans submissions matching every supplied filter in query.Order (default ascending) by id.
func (storage *StoreStorage) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	var none durable.Page[durable.SubmissionRecord, durable.Cursor]
	if err := storage.assertOpen(); err != nil {
		return none, err
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return none, err
	}
	conditions := []Condition{scanFrom(start)}
	if query.ConversationId != nil {
		conditions = append(conditions, Condition{Column: "conversation_id", Op: chordstore.WhereEq, Value: int64(*query.ConversationId)})
	}
	if query.Status != nil {
		conditions = append(conditions, Condition{Column: "status", Op: chordstore.WhereEq, Value: string(*query.Status)})
	}
	return scanTable(storage, ctx, storage.queries.Submissions, conditions, start, limit,
		func(record durable.SubmissionRecord) int64 { return int64(record.Id) })
}

// SubmissionByRequest finds a submission by its conversation-scoped host deduplication key; nil when absent.
func (storage *StoreStorage) SubmissionByRequest(ctx context.Context, conversationID durable.ConversationId, requestID string) (*durable.SubmissionRecord, error) {
	submissions := storage.queries.Submissions
	conditions := []Condition{
		{Column: "conversation_id", Op: chordstore.WhereEq, Value: int64(conversationID)},
		{Column: "request_id", Op: chordstore.WhereEq, Value: EncodeIndexed(requestID)},
	}
	return reading(storage, ctx, func(executor sqlite.SqliteExecutor) (*durable.SubmissionRecord, error) {
		row, err := submissions.First(executor, conditions, ascending)
		if err != nil || row == nil {
			return nil, err
		}
		return recordPointer[durable.SubmissionRecord](submissions, *row, ctx)
	})
}

// FindDocument resolves the incarnation occupying one exact logical address at the selected point; nil when absent.
func (storage *StoreStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	return reading(storage, ctx, func(executor sqlite.SqliteExecutor) (*durable.DocumentRecord, error) {
		entry, err := chordstore.FindDocumentEntry(executor, AddressOf(address.Kind, address.Scope, address.Key, storage.owner), pointOf(at))
		if err != nil || entry == nil {
			return nil, err
		}
		record, err := RecordOfEntry(*entry)
		if err != nil {
			return nil, err
		}
		return &record, nil
	})
}

// Document materializes one specific incarnation by id at the selected point without following a replacement at its
// address; nil when absent.
func (storage *StoreStorage) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	// The record and its revisions must come from one committed state (S4).
	read, err := chordstore.ReadTransaction(storage.store, func(executor sqlite.SqliteExecutor) (DocumentRead, error) {
		return ReadAt(executor, id, at, storage.owner)
	}, ctx)
	if err != nil {
		return nil, err
	}
	if read.CurrentOnly {
		return nil, durable.NewStorageRequestError("Document " + itoa(int64(read.CurrentOnlyID)) + " does not retain historical content")
	}
	return read.Document, nil
}

// ScanDocuments scans incarnations alive in one exact scope at the selected point.
func (storage *StoreStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	var none durable.Page[durable.DocumentRecord, durable.Cursor]
	if err := storage.assertOpen(); err != nil {
		return none, err
	}
	after, present, err := cursorID(cursor)
	if err != nil {
		return none, err
	}
	if !present {
		after = -1
	}
	scope := chordstore.DocumentScope{Owner: storage.owner, Namespace: NamespaceOf(query.Scope)}
	if query.Kind != nil {
		kind := EncodeIndexed(*query.Kind)
		scope.Kind = &kind
	}
	entries, err := chordstore.Read(storage.store, func(executor sqlite.SqliteExecutor) ([]chordstore.DocumentEntry, error) {
		return chordstore.ListDocumentEntries(executor, scope, pointOf(query.At), after, int64(limit)+1)
	}, ctx)
	if err != nil {
		return none, err
	}
	records := make([]durable.DocumentRecord, len(entries))
	for index, entry := range entries {
		if records[index], err = RecordOfEntry(entry); err != nil {
			return none, err
		}
	}
	return page(records, limit, durable.ScanAscending, func(record durable.DocumentRecord) int64 { return int64(record.Id) }), nil
}

// Close refuses new calls, waits for its commits underway, then releases its claims; the store stays open (the app's),
// and finishes the reads underway, which change nothing.
func (storage *StoreStorage) Close(ctx context.Context) error {
	storage.mu.Lock()
	if storage.closing == nil {
		storage.closed = true
		storage.closing = make(chan struct{})
		if storage.underway > 0 {
			storage.drained = make(chan struct{})
		}
		closing, drained := storage.closing, storage.drained
		go func() {
			// The calls underway settle first, then the claims go; Close's wait on closing is the join.
			defer close(closing)
			if drained != nil {
				<-drained
			}
			storage.store.Claims.Release(storage.claimant)
		}()
	}
	closing := storage.closing
	storage.mu.Unlock()
	select {
	case <-closing:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// enter runs one commit or mint of this storage: refused once it closed, counted until leave (Close waits).
func (storage *StoreStorage) enter() error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.closed {
		return errClosed
	}
	storage.underway++
	return nil
}

func (storage *StoreStorage) leave() {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.underway--
	if storage.underway == 0 && storage.drained != nil {
		close(storage.drained)
		storage.drained = nil
	}
}

// assertOpen refuses a read once closed; a read is not waited for by Close (it changes nothing, and the store, which
// stays open, finishes it).
func (storage *StoreStorage) assertOpen() error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.closed {
		return errClosed
	}
	return nil
}

// reading is a call that is one guarded read of the store.
func reading[T any](storage *StoreStorage, ctx context.Context, read func(executor sqlite.SqliteExecutor) (T, error)) (T, error) {
	if err := storage.assertOpen(); err != nil {
		var zero T
		return zero, err
	}
	return chordstore.Read(storage.store, read, ctx)
}

// recordPointer is the record of row as a pointer.
func recordPointer[T storedRecord](queries *TableQueries, row RecordRow, ctx context.Context) (*T, error) {
	value, err := recordOf[T](queries, row, ctx)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// byID is the record with id in queries' table; nil when absent.
func byID[T storedRecord](storage *StoreStorage, ctx context.Context, queries *TableQueries, id int64) (*T, error) {
	return reading(storage, ctx, func(executor sqlite.SqliteExecutor) (*T, error) {
		row, err := queries.ByID(executor, id)
		if err != nil || row == nil {
			return nil, err
		}
		return recordPointer[T](queries, *row, ctx)
	})
}

// scanTable is a page of a table scan: up to limit records of queries' table whose conditions hold, from start.
func scanTable[T storedRecord](storage *StoreStorage, ctx context.Context, queries *TableQueries, conditions []Condition, start scan.Start, limit int, id func(T) int64) (durable.Page[T, durable.Cursor], error) {
	var none durable.Page[T, durable.Cursor]
	rows, err := chordstore.Read(storage.store, func(executor sqlite.SqliteExecutor) ([]RecordRow, error) {
		return queries.Select(executor, conditions, orderOf(start.Order), int64(limit)+1)
	}, ctx)
	if err != nil {
		return none, err
	}
	values, err := appendRecords(queries, rows, make([]T, 0, len(rows)), ctx)
	if err != nil {
		return none, err
	}
	return page(values, limit, start.Order, id), nil
}

func (storage *StoreStorage) conversation(executor sqlite.SqliteExecutor, id durable.ConversationId, ctx context.Context) (*durable.ConversationRecord, error) {
	conversations := storage.queries.Conversations
	row, err := conversations.ByID(executor, int64(id))
	if err != nil || row == nil {
		return nil, err
	}
	return recordPointer[durable.ConversationRecord](conversations, *row, ctx)
}

func (storage *StoreStorage) entry(executor sqlite.SqliteExecutor, id durable.EntryId, ctx context.Context) (*durable.EntryAt, error) {
	entries := storage.queries.Entries
	row, err := entries.ByID(executor, int64(id))
	if err != nil || row == nil {
		return nil, err
	}
	record, err := recordOf[durable.EntryRecord](entries, *row, ctx)
	if err != nil {
		return nil, err
	}
	return &durable.EntryAt{Entry: record, CommitSeq: durable.SeqFromNumber(row.CommitSeq)}, nil
}

// visibleEntry is entry id when visible through conversationID's fork chain (`SqliteStorage.readEntry`).
func (storage *StoreStorage) visibleEntry(executor sqlite.SqliteExecutor, conversationID durable.ConversationId, id durable.EntryId, ctx context.Context) (readResult[*durable.EntryAt], error) {
	conversation, err := storage.conversation(executor, conversationID, ctx)
	if err != nil {
		return readResult[*durable.EntryAt]{}, err
	}
	if conversation == nil {
		return unknownConversation[*durable.EntryAt](conversationID), nil
	}
	stored, err := storage.entry(executor, id, ctx)
	if err != nil || stored == nil {
		return readResult[*durable.EntryAt]{}, err
	}
	upper := int64(math.MaxInt64)
	for conversation.Id != stored.Entry.ConversationId {
		if conversation.Parent == nil {
			return readResult[*durable.EntryAt]{}, nil
		}
		upper = min(upper, int64(conversation.Parent.At))
		if conversation, err = storage.conversation(executor, conversation.Parent.ConversationId, ctx); err != nil {
			return readResult[*durable.EntryAt]{}, err
		}
		if conversation == nil {
			return readResult[*durable.EntryAt]{}, errors.New("conversation parent is missing")
		}
	}
	if int64(stored.Entry.Id) > upper {
		return readResult[*durable.EntryAt]{}, nil
	}
	return found(stored), nil
}

func (storage *StoreStorage) latestHeadMarker(executor sqlite.SqliteExecutor, conversationID durable.ConversationId, atOrBeforeEntryID *durable.EntryId, ctx context.Context) (readResult[*durable.EntryRecord], error) {
	conversation, err := storage.conversation(executor, conversationID, ctx)
	if err != nil {
		return readResult[*durable.EntryRecord]{}, err
	}
	if conversation == nil {
		return unknownConversation[*durable.EntryRecord](conversationID), nil
	}
	var upper *int64
	if atOrBeforeEntryID != nil {
		bound := int64(*atOrBeforeEntryID)
		upper = &bound
	}
	for {
		var head sqlite.SqliteRow
		if upper == nil {
			head, err = executor.Get(storage.queries.LatestHeadAny, int64(conversation.Id))
		} else {
			head, err = executor.Get(storage.queries.LatestHeadAtBefore, int64(conversation.Id), *upper)
		}
		if err != nil {
			return readResult[*durable.EntryRecord]{}, err
		}
		if head != nil {
			headID, err := sqlite.RowInt(head, "id")
			if err != nil {
				return readResult[*durable.EntryRecord]{}, err
			}
			// Written with its entry, in the same commit.
			row, err := storage.queries.Entries.ByID(executor, headID)
			if err != nil {
				return readResult[*durable.EntryRecord]{}, err
			}
			if row == nil {
				return readResult[*durable.EntryRecord]{}, errors.New("a head marker's entry is missing")
			}
			record, err := recordPointer[durable.EntryRecord](storage.queries.Entries, *row, ctx)
			if err != nil {
				return readResult[*durable.EntryRecord]{}, err
			}
			return found(record), nil
		}
		if conversation.Parent == nil {
			return readResult[*durable.EntryRecord]{}, nil
		}
		at := int64(conversation.Parent.At)
		if upper == nil {
			upper = &at
		} else {
			bound := min(*upper, at)
			upper = &bound
		}
		if conversation, err = storage.conversation(executor, conversation.Parent.ConversationId, ctx); err != nil {
			return readResult[*durable.EntryRecord]{}, err
		}
		if conversation == nil {
			return readResult[*durable.EntryRecord]{}, errors.New("conversation parent is missing")
		}
	}
}

// entriesDescending is newest first: each conversation of the fork chain up to its fork point
// (`SqliteStorage.readEntries`).
func (storage *StoreStorage) entriesDescending(executor sqlite.SqliteExecutor, query durable.EntryQuery, limit int, start scan.Start, ctx context.Context) (readResult[durable.Page[durable.EntryRecord, durable.Cursor]], error) {
	type result = readResult[durable.Page[durable.EntryRecord, durable.Cursor]]
	conversation, err := storage.conversation(executor, query.ConversationId, ctx)
	if err != nil {
		return result{}, err
	}
	if conversation == nil {
		return unknownConversation[durable.Page[durable.EntryRecord, durable.Cursor]](query.ConversationId), nil
	}
	var upper *int64
	if query.MaxEntryId != nil {
		bound := int64(*query.MaxEntryId)
		upper = &bound
	}
	if start.HasAfter {
		bound := start.After - 1
		if upper != nil {
			bound = min(*upper, bound)
		}
		upper = &bound
	}
	var lower *int64
	if query.MinEntryId != nil {
		bound := int64(*query.MinEntryId)
		lower = &bound
	}
	values := []durable.EntryRecord{}
	for {
		rows, err := storage.queries.Entries.Select(executor, entryRange(conversation.Id, lower, upper), descending, int64(limit+1-len(values)))
		if err != nil {
			return result{}, err
		}
		if values, err = appendRecords(storage.queries.Entries, rows, values, ctx); err != nil {
			return result{}, err
		}
		if len(values) > limit || conversation.Parent == nil {
			break
		}
		at := int64(conversation.Parent.At)
		if upper != nil {
			at = min(*upper, at)
		}
		upper = &at
		if lower != nil && at < *lower {
			break
		}
		if conversation, err = storage.conversation(executor, conversation.Parent.ConversationId, ctx); err != nil {
			return result{}, err
		}
		if conversation == nil {
			return result{}, errors.New("conversation parent is missing")
		}
	}
	return found(page(values, limit, durable.ScanDescending, entryID)), nil
}

// entriesAscending is oldest first: the fork chain's segments from the root forward, each up to its fork point.
func (storage *StoreStorage) entriesAscending(executor sqlite.SqliteExecutor, query durable.EntryQuery, limit int, start scan.Start, ctx context.Context) (readResult[durable.Page[durable.EntryRecord, durable.Cursor]], error) {
	type result = readResult[durable.Page[durable.EntryRecord, durable.Cursor]]
	type segment struct {
		conversationID durable.ConversationId
		upper          *int64
	}
	conversation, err := storage.conversation(executor, query.ConversationId, ctx)
	if err != nil {
		return result{}, err
	}
	if conversation == nil {
		return unknownConversation[durable.Page[durable.EntryRecord, durable.Cursor]](query.ConversationId), nil
	}
	var upper *int64
	if query.MaxEntryId != nil {
		bound := int64(*query.MaxEntryId)
		upper = &bound
	}
	var lower *int64
	if query.MinEntryId != nil {
		bound := int64(*query.MinEntryId)
		lower = &bound
	}
	var segments []segment
	for {
		segments = append(segments, segment{conversationID: conversation.Id, upper: upper})
		if conversation.Parent == nil {
			break
		}
		at := int64(conversation.Parent.At)
		if upper != nil {
			at = min(*upper, at)
		}
		upper = &at
		if lower != nil && at < *lower {
			break
		}
		if conversation, err = storage.conversation(executor, conversation.Parent.ConversationId, ctx); err != nil {
			return result{}, err
		}
		if conversation == nil {
			return result{}, errors.New("conversation parent is missing")
		}
	}
	if start.HasAfter {
		bound := start.After + 1
		if lower != nil {
			bound = max(*lower, bound)
		}
		lower = &bound
	}
	values := []durable.EntryRecord{}
	for _, each := range slices.Backward(segments) {
		rows, err := storage.queries.Entries.Select(executor, entryRange(each.conversationID, lower, each.upper), ascending, int64(limit+1-len(values)))
		if err != nil {
			return result{}, err
		}
		if values, err = appendRecords(storage.queries.Entries, rows, values, ctx); err != nil {
			return result{}, err
		}
		if len(values) > limit {
			break
		}
	}
	return found(page(values, limit, durable.ScanAscending, entryID)), nil
}

func entryID(record durable.EntryRecord) int64 { return int64(record.Id) }

// entryRange is one conversation's entries with ids from lower to upper (inclusive, each optional).
func entryRange(conversationID durable.ConversationId, lower, upper *int64) []Condition {
	conditions := []Condition{{Column: "conversation_id", Op: chordstore.WhereEq, Value: int64(conversationID)}}
	if lower != nil {
		conditions = append(conditions, Condition{Column: "id", Op: chordstore.WhereGte, Value: *lower})
	}
	if upper != nil {
		conditions = append(conditions, Condition{Column: "id", Op: chordstore.WhereLte, Value: *upper})
	}
	return conditions
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }
