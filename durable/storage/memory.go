// Package storage holds the in-memory reference implementation of the durable Storage contract.
package storage

// Ports packages/durable/src/storage/memory.ts

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/ops"
	"github.com/MichaelKinsy/PiG/durable/storage/internal/scan"
)

type storedTask = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

// tableName names the table that owns a record ID.
type tableName string

const (
	tableConversation tableName = "conversation"
	tableEntry        tableName = "entry"
	tableTask         tableName = "task"
	tableSubmission   tableName = "submission"
	tableDocument     tableName = "document"
)

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER, the end of the durable ID and sequence space.
const maxSafeInteger = 1<<53 - 1

func isSafeInteger(value int64) bool { return value >= -maxSafeInteger && value <= maxSafeInteger }

type documentRevision struct {
	content durable.DocumentContent
	seq     durable.Seq
}

type storedDocumentState struct {
	record    durable.DocumentRecord
	revisions []documentRevision
}

type documentAction struct {
	create  *durable.DocumentCreate
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

// scopeKey identifies one exact document scope.
type scopeKey struct {
	kind  durable.DocumentScope
	owner int64
}

// addressKey identifies one exact logical document address: a singleton or one family member.
type addressKey struct {
	kind   string
	scope  scopeKey
	family bool
	key    string
}

type documentAddressIndex struct {
	ids       []int64
	currentId *durable.DocumentId
}

func scopeKeyOf(scope durable.DocumentRecordScope) scopeKey {
	switch scope.Kind {
	case durable.ScopeConversation:
		return scopeKey{kind: scope.Kind, owner: int64(scope.ConversationId)}
	case durable.ScopeTask:
		return scopeKey{kind: scope.Kind, owner: int64(scope.TaskId)}
	default:
		return scopeKey{kind: scope.Kind}
	}
}

func addressKeyOf(address durable.DocumentAddress) addressKey {
	key := addressKey{kind: address.Kind, scope: scopeKeyOf(address.Scope)}
	if address.Key != nil {
		key.family = true
		key.key = *address.Key
	}
	return key
}

func recordAddressKey(kind string, scope durable.DocumentRecordScope, key *string) addressKey {
	return addressKeyOf(durable.DocumentAddress{Kind: kind, Scope: scope, Key: key})
}

func isAliveAt(record durable.DocumentRecord, at durable.DocumentPoint) bool {
	if at.Current {
		return record.RetiredAt == nil
	}
	return record.CreatedAt <= at.Seq && (record.RetiredAt == nil || at.Seq < *record.RetiredAt)
}

func isCurrentOnly(scope durable.DocumentRecordScope, history durable.DocumentHistory) bool {
	return scope.Kind != durable.ScopeConversation || history == durable.HistoryLatest
}

type state struct {
	recordTypes                        map[int64]tableName
	conversations                      map[durable.ConversationId]durable.ConversationRecord
	conversationIds                    []int64
	conversationIdsByOwnerConversation map[durable.ConversationId][]int64
	conversationIdsByOwnerTask         map[durable.TaskId][]int64
	entries                            map[durable.EntryId]durable.EntryRecord
	entryIds                           map[durable.ConversationId][]int64
	headEntryIds                       map[durable.ConversationId][]int64
	entryCommitSeqs                    map[durable.EntryId]durable.Seq
	tasks                              map[durable.TaskId]storedTask
	taskIds                            []int64
	taskIdsByStatus                    map[durable.TaskStatus][]int64
	submissions                        map[durable.SubmissionId]durable.SubmissionRecord
	submissionIds                      []int64
	submissionIdsByStatus              map[durable.SubmissionStatus][]int64
	submissionIdsByRequest             map[durable.ConversationId]map[string]durable.SubmissionId
	documents                          map[durable.DocumentId]*storedDocumentState
	documentAddresses                  map[addressKey]*documentAddressIndex
	documentIdsByScope                 map[scopeKey][]int64
}

// lowerBound returns the first index whose ID is not below target.
func lowerBound(ids []int64, target int64) int {
	index, _ := slices.BinarySearch(ids, target)
	return index
}

// upperBound returns the first index whose ID is above target.
func upperBound(ids []int64, target int64) int {
	low, high := 0, len(ids)
	for low < high {
		middle := int(uint(low+high) >> 1)
		if ids[middle] <= target {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low
}

func insertSorted(ids []int64, id int64) []int64 {
	if len(ids) == 0 || ids[len(ids)-1] < id {
		return append(ids, id)
	}
	return slices.Insert(ids, lowerBound(ids, id), id)
}

func removeSorted(ids []int64, id int64) []int64 {
	index := lowerBound(ids, id)
	if index < len(ids) && ids[index] == id {
		return slices.Delete(ids, index, index+1)
	}
	return ids
}

func insertMapId[K comparable](index map[K][]int64, key K, id int64) {
	index[key] = insertSorted(index[key], id)
}

// cursorAfter decodes a backend cursor; ok is false when the scan starts at the beginning.
func cursorAfter(cursor durable.Cursor) (after int64, ok bool, err error) {
	if cursor == nil {
		return 0, false, nil
	}
	value, present := cursor["after"]
	if !present {
		return 0, false, nil
	}
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
	if !isSafeInteger(after) {
		return 0, false, errInvalidCursor
	}
	return after, true, nil
}

var errInvalidCursor = errors.New("Invalid storage cursor")

// page returns at most limit values and, when more remain, a cursor after the last returned one in the scan's order.
func page[T any](
	values []T, limit int, order durable.ScanOrder, id func(T) int64, clone func(T) T,
) durable.Page[T, durable.Cursor] {
	count := min(max(limit, 0), len(values))
	items := make([]T, count)
	for index := range count {
		items[index] = clone(values[index])
	}
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

// scanIndexes visits the indexes of sorted ids in scan order, after the cursor's ID when there is one, until visit
// returns false.
func scanIndexes(ids []int64, start scan.Start, visit func(index int) bool) {
	if start.Order == durable.ScanAscending {
		first := 0
		if start.HasAfter {
			first = upperBound(ids, start.After)
		}
		for index := first; index < len(ids); index++ {
			if !visit(index) {
				return
			}
		}
		return
	}
	end := len(ids)
	if start.HasAfter {
		end = lowerBound(ids, start.After)
	}
	for index := end - 1; index >= 0; index-- {
		if !visit(index) {
			return
		}
	}
}

// PreparedMemoryCommit is a fully validated, detached state mutation whose application performs no fallible
// preparation.
type PreparedMemoryCommit struct {
	storage *MemoryStorage
	seq     durable.Seq
	writes  []durable.StorageWrite
	actions *documentActions
	applied bool
}

// Seq returns the commit sequence the mutation applies at.
func (prepared *PreparedMemoryCommit) Seq() durable.Seq { return prepared.seq }

// Writes returns a detached copy of the resolved writes, for persistence. Mutating it never changes storage state.
func (prepared *PreparedMemoryCommit) Writes() []durable.StorageWrite {
	return cloneWrites(prepared.writes)
}

// Apply applies the mutation once and returns its sequence; later calls return the same sequence.
func (prepared *PreparedMemoryCommit) Apply() durable.Seq {
	storage := prepared.storage
	storage.mu.Lock()
	defer storage.mu.Unlock()
	prepared.applyLocked()
	return prepared.seq
}

func (prepared *PreparedMemoryCommit) applyLocked() {
	if !prepared.applied {
		prepared.applied = true
		prepared.storage.applyPreparedCommit(prepared.writes, prepared.actions, prepared.seq)
	}
}

// MemoryStorage is the detached in-memory reference implementation of durable.Storage.
//
// Reads and retained writes are copied intentionally to match the ownership boundary of serialization-backed stores.
// This is backend conformance, not validation. It is safe for concurrent use; the owning Session serializes commits.
type MemoryStorage struct {
	mu      sync.RWMutex
	state   state
	nextId  int64
	nextSeq int64
	closed  bool
}

var _ durable.Storage = (*MemoryStorage)(nil)

// NewMemoryStorage returns empty storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		state: state{
			recordTypes:                        map[int64]tableName{},
			conversations:                      map[durable.ConversationId]durable.ConversationRecord{},
			conversationIdsByOwnerConversation: map[durable.ConversationId][]int64{},
			conversationIdsByOwnerTask:         map[durable.TaskId][]int64{},
			entries:                            map[durable.EntryId]durable.EntryRecord{},
			entryIds:                           map[durable.ConversationId][]int64{},
			headEntryIds:                       map[durable.ConversationId][]int64{},
			entryCommitSeqs:                    map[durable.EntryId]durable.Seq{},
			tasks:                              map[durable.TaskId]storedTask{},
			taskIdsByStatus:                    map[durable.TaskStatus][]int64{},
			submissions:                        map[durable.SubmissionId]durable.SubmissionRecord{},
			submissionIdsByStatus:              map[durable.SubmissionStatus][]int64{},
			submissionIdsByRequest:             map[durable.ConversationId]map[string]durable.SubmissionId{},
			documents:                          map[durable.DocumentId]*storedDocumentState{},
			documentAddresses:                  map[addressKey]*documentAddressIndex{},
			documentIdsByScope:                 map[scopeKey][]int64{},
		},
		nextId:  2,
		nextSeq: 1,
	}
}

// Commit atomically persists one batch and returns its sequence.
func (storage *MemoryStorage) Commit(_ context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	prepared, err := storage.prepareCommitLocked(writes, durable.Seq(storage.nextSeq))
	if err != nil {
		return 0, err
	}
	prepared.applyLocked()
	return prepared.seq, nil
}

// PrepareCommit validates and detaches one commit at the next sequence without changing observable state.
func (storage *MemoryStorage) PrepareCommit(writes []durable.StorageWrite) (*PreparedMemoryCommit, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	return storage.prepareCommitLocked(writes, durable.Seq(storage.nextSeq))
}

// PrepareCommitAt validates and detaches one commit at seq, which must not be below the next sequence.
func (storage *MemoryStorage) PrepareCommitAt(
	writes []durable.StorageWrite, seq durable.Seq,
) (*PreparedMemoryCommit, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	return storage.prepareCommitLocked(writes, seq)
}

func (storage *MemoryStorage) prepareCommitLocked(
	writes []durable.StorageWrite, seq durable.Seq,
) (*PreparedMemoryCommit, error) {
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	if !isSafeInteger(int64(seq)) || int64(seq) < storage.nextSeq {
		return nil, fmt.Errorf("Commit sequence %d does not strictly increase", seq)
	}
	detached, err := storage.resolveDocumentCopies(cloneWrites(writes))
	if err != nil {
		return nil, err
	}
	if err := storage.checkGlobalIds(detached); err != nil {
		return nil, err
	}
	actions, err := prepareDocumentActions(detached)
	if err != nil {
		return nil, err
	}
	if err := storage.checkDocumentActions(actions); err != nil {
		return nil, err
	}
	return &PreparedMemoryCommit{storage: storage, seq: seq, writes: detached, actions: actions}, nil
}

func (storage *MemoryStorage) resolveDocumentCopies(writes []durable.StorageWrite) ([]durable.StorageWrite, error) {
	if !slices.ContainsFunc(writes, func(write durable.StorageWrite) bool {
		_, copied := write.(durable.DocumentCopyWrite)
		return copied
	}) {
		return writes, nil
	}
	changed := map[durable.DocumentId]bool{}
	for _, write := range writes {
		switch typed := write.(type) {
		case durable.DocumentCreateWrite:
			changed[typed.Record.Id] = true
		case durable.DocumentCopyWrite:
			changed[typed.Record.Id] = true
		case durable.DocumentChangeWrite:
			changed[typed.Id] = true
		case durable.DocumentRetireWrite:
			changed[typed.Id] = true
		}
	}
	resolved := make([]durable.StorageWrite, len(writes))
	for index, write := range writes {
		copied, ok := write.(durable.DocumentCopyWrite)
		if !ok {
			resolved[index] = write
			continue
		}
		create, err := storage.resolveDocumentCopy(copied, changed)
		if err != nil {
			if _, ok := errors.AsType[*durable.StorageRejected](err); ok {
				return nil, err
			}
			return nil, durable.NewStorageRejected(fmt.Sprintf("Document copy %d was rejected", copied.Record.Id), err)
		}
		resolved[index] = create
	}
	return resolved, nil
}

func (storage *MemoryStorage) resolveDocumentCopy(
	write durable.DocumentCopyWrite, changed map[durable.DocumentId]bool,
) (durable.DocumentCreateWrite, error) {
	if changed[write.Source.Id] {
		return durable.DocumentCreateWrite{}, fmt.Errorf(
			"Fork source document %d is changed in the copy batch", write.Source.Id,
		)
	}
	stored, err := storage.materializeDocument(write.Source.Id, write.Source.At)
	if err != nil {
		return durable.DocumentCreateWrite{}, err
	}
	if stored == nil {
		return durable.DocumentCreateWrite{}, fmt.Errorf("Fork source document %d cannot be read", write.Source.Id)
	}
	if !copyMatches(stored.Record, write.Record) {
		return durable.DocumentCreateWrite{}, fmt.Errorf(
			"Fork source document %d does not match the copied record", write.Source.Id,
		)
	}
	return durable.DocumentCreateWrite{
		Record:  write.Record,
		Content: durable.DocumentContent{Kind: durable.ContentBase, Version: stored.Version, Value: stored.Value},
	}, nil
}

// copyMatches reports whether a definition-free copy keeps the source's conversation-document identity and semantics.
func copyMatches(source durable.DocumentRecord, create durable.DocumentCreate) bool {
	return source.Scope.Kind == durable.ScopeConversation &&
		create.Scope.Kind == durable.ScopeConversation &&
		source.Kind == create.Kind &&
		equalKey(source.Key, create.Key) &&
		source.History == create.History &&
		source.Fork == create.Fork
}

func equalKey(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (storage *MemoryStorage) applyPreparedCommit(
	prepared []durable.StorageWrite, actions *documentActions, seq durable.Seq,
) {
	state := &storage.state
	for _, write := range prepared {
		switch typed := write.(type) {
		case durable.ConversationWrite:
			value := typed.Value
			id := int64(value.Id)
			state.recordTypes[id] = tableConversation
			state.conversations[value.Id] = value
			state.conversationIds = insertSorted(state.conversationIds, id)
			if value.Owner != nil {
				insertMapId(state.conversationIdsByOwnerConversation, value.Owner.ConversationId, id)
				insertMapId(state.conversationIdsByOwnerTask, value.Owner.TaskId, id)
			}
			storage.adoptId(id)
		case durable.EntryWrite:
			value := typed.Value
			id := int64(value.Id)
			state.recordTypes[id] = tableEntry
			state.entries[value.Id] = value
			state.entryCommitSeqs[value.Id] = seq
			insertMapId(state.entryIds, value.ConversationId, id)
			if value.Head != nil {
				insertMapId(state.headEntryIds, value.ConversationId, id)
			}
			storage.adoptId(id)
		case durable.TaskWrite:
			value := typed.Value
			id := int64(value.Id)
			state.recordTypes[id] = tableTask
			previous, existed := state.tasks[value.Id]
			if !existed {
				state.taskIds = insertSorted(state.taskIds, id)
				insertMapId(state.taskIdsByStatus, value.State.Status, id)
			} else if previous.State.Status != value.State.Status {
				state.taskIdsByStatus[previous.State.Status] = removeSorted(state.taskIdsByStatus[previous.State.Status], id)
				insertMapId(state.taskIdsByStatus, value.State.Status, id)
			}
			state.tasks[value.Id] = value
			storage.adoptId(id)
		case durable.SubmissionWrite:
			storage.applySubmission(typed.Value)
		case durable.DocumentCreateWrite, durable.DocumentChangeWrite, durable.DocumentRetireWrite:
		default:
			panic(fmt.Sprintf("durable storage: unresolved %s write", durable.StorageWriteType(write)))
		}
	}
	storage.applyDocumentActions(actions, seq)
	storage.nextSeq = int64(seq) + 1
}

func (storage *MemoryStorage) applySubmission(value durable.SubmissionRecord) {
	state := &storage.state
	id := int64(value.Id)
	state.recordTypes[id] = tableSubmission
	previous, existed := state.submissions[value.Id]
	if !existed {
		state.submissionIds = insertSorted(state.submissionIds, id)
		insertMapId(state.submissionIdsByStatus, value.Status, id)
	} else if previous.Status != value.Status {
		state.submissionIdsByStatus[previous.Status] = removeSorted(state.submissionIdsByStatus[previous.Status], id)
		insertMapId(state.submissionIdsByStatus, value.Status, id)
	}
	if existed && previous.RequestId != nil {
		requests := state.submissionIdsByRequest[previous.ConversationId]
		if requests[*previous.RequestId] == value.Id {
			delete(requests, *previous.RequestId)
			if len(requests) == 0 {
				delete(state.submissionIdsByRequest, previous.ConversationId)
			}
		}
	}
	state.submissions[value.Id] = value
	if value.RequestId != nil {
		requests := state.submissionIdsByRequest[value.ConversationId]
		if requests == nil {
			requests = map[string]durable.SubmissionId{}
			state.submissionIdsByRequest[value.ConversationId] = requests
		}
		requests[*value.RequestId] = value.Id
	}
	storage.adoptId(id)
}

func (storage *MemoryStorage) adoptId(id int64) {
	if id >= storage.nextId {
		storage.nextId = id + 1
	}
}

// MintId returns a fresh candidate from the Session-global numeric ID namespace.
func (storage *MemoryStorage) MintId() (int64, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if err := storage.assertOpen(); err != nil {
		return 0, err
	}
	if !isSafeInteger(storage.nextId) {
		return 0, errors.New("ID space is exhausted")
	}
	id := storage.nextId
	storage.nextId++
	return id, nil
}

// Conversation looks up one conversation by exact ID.
func (storage *MemoryStorage) Conversation(
	_ context.Context, id durable.ConversationId,
) (*durable.ConversationRecord, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	value, ok := storage.state.conversations[id]
	if !ok {
		return nil, nil
	}
	copied := cloneConversation(value)
	return &copied, nil
}

// ScanConversations scans conversations by ID in query.Order, ascending by default.
func (storage *MemoryStorage) ScanConversations(
	_ context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	var ids []int64
	switch {
	case query.OwnerTaskId != nil:
		ids = storage.state.conversationIdsByOwnerTask[*query.OwnerTaskId]
	case query.OwnerConversationId != nil:
		ids = storage.state.conversationIdsByOwnerConversation[*query.OwnerConversationId]
	default:
		ids = storage.state.conversationIds
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	values := []durable.ConversationRecord{}
	scanIndexes(ids, start, func(index int) bool {
		if len(values) > limit {
			return false
		}
		value := storage.state.conversations[durable.ConversationId(ids[index])]
		if query.OwnerConversationId != nil &&
			(value.Owner == nil || value.Owner.ConversationId != *query.OwnerConversationId) {
			return true
		}
		values = append(values, value)
		return true
	})
	return page(values, limit, start.Order, conversationIdOf, cloneConversation), nil
}

func startAfter(ids []int64, cursor durable.Cursor) (int, error) {
	after, ok, err := cursorAfter(cursor)
	if err != nil || !ok {
		return 0, err
	}
	return upperBound(ids, after), nil
}

func conversationIdOf(record durable.ConversationRecord) int64 { return int64(record.Id) }
func entryIdOf(record durable.EntryRecord) int64               { return int64(record.Id) }
func taskIdOf(record storedTask) int64                         { return int64(record.Id) }
func submissionIdOf(record durable.SubmissionRecord) int64     { return int64(record.Id) }
func documentIdOf(record durable.DocumentRecord) int64         { return int64(record.Id) }

// Entry looks up one global entry and the sequence of the commit that persisted it.
func (storage *MemoryStorage) Entry(_ context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	entry, ok := storage.state.entries[id]
	if !ok {
		return nil, nil
	}
	return &durable.EntryAt{Entry: cloneEntry(entry), CommitSeq: storage.state.entryCommitSeqs[id]}, nil
}

// VisibleEntry looks up one entry only when it is visible through the requested conversation's ancestry.
func (storage *MemoryStorage) VisibleEntry(
	_ context.Context, conversationId durable.ConversationId, id durable.EntryId,
) (*durable.EntryAt, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	var found *durable.EntryRecord
	err := storage.visibleEntries(conversationId, int64(id), int64(id), func(entry durable.EntryRecord) bool {
		found = &entry
		return false
	})
	if err != nil || found == nil {
		return nil, err
	}
	return &durable.EntryAt{Entry: cloneEntry(*found), CommitSeq: storage.state.entryCommitSeqs[id]}, nil
}

// FindLatestHeadMarker returns the newest visible entry with a head at or below the optional inclusive cutoff.
func (storage *MemoryStorage) FindLatestHeadMarker(
	_ context.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId,
) (*durable.EntryRecord, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	if _, ok := storage.state.conversations[conversationId]; !ok {
		return nil, fmt.Errorf("Unknown conversation: %d", conversationId)
	}
	currentId := conversationId
	upperEntryId := int64(math.MaxInt64)
	if atOrBeforeEntryId != nil {
		upperEntryId = int64(*atOrBeforeEntryId)
	}
	for {
		ids := storage.state.headEntryIds[currentId]
		if index := upperBound(ids, upperEntryId) - 1; index >= 0 {
			entry := cloneEntry(storage.state.entries[durable.EntryId(ids[index])])
			return &entry, nil
		}
		conversation := storage.state.conversations[currentId]
		if conversation.Parent == nil {
			return nil, nil
		}
		upperEntryId = min(upperEntryId, int64(conversation.Parent.At))
		currentId = conversation.Parent.ConversationId
	}
}

// ScanEntries scans the inclusive visible range in query.Order (descending by default), returning at most limit
// entries.
func (storage *MemoryStorage) ScanEntries(
	_ context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanDescending)
	if err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	minEntryId := int64(math.MinInt64)
	if query.MinEntryId != nil {
		minEntryId = int64(*query.MinEntryId)
	}
	maxEntryId := int64(math.MaxInt64)
	if query.MaxEntryId != nil {
		maxEntryId = int64(*query.MaxEntryId)
	}
	// The cursor narrows the bound on the side the scan moves away from.
	if start.HasAfter && start.Order == durable.ScanDescending {
		maxEntryId = min(maxEntryId, start.After-1)
	}
	if start.HasAfter && start.Order == durable.ScanAscending {
		minEntryId = max(minEntryId, start.After+1)
	}
	visible := []durable.EntryRecord{}
	visit := func(entry durable.EntryRecord) bool {
		visible = append(visible, entry)
		return len(visible) <= limit
	}
	if start.Order == durable.ScanDescending {
		err = storage.visibleEntries(query.ConversationId, minEntryId, maxEntryId, visit)
	} else {
		err = storage.visibleEntriesAscending(query.ConversationId, minEntryId, maxEntryId, visit)
	}
	if err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	return page(visible, limit, start.Order, entryIdOf, cloneEntry), nil
}

// Task looks up the latest complete record for one task.
func (storage *MemoryStorage) Task(_ context.Context, id durable.TaskId) (*storedTask, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	value, ok := storage.state.tasks[id]
	if !ok {
		return nil, nil
	}
	copied := cloneTask(value)
	return &copied, nil
}

// ScanTasks scans task records matching every supplied filter by ID in query.Order, ascending by default.
func (storage *MemoryStorage) ScanTasks(
	_ context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor,
) (durable.Page[storedTask, durable.Cursor], error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return durable.Page[storedTask, durable.Cursor]{}, err
	}
	ids := storage.state.taskIds
	if query.Status != nil {
		ids = storage.state.taskIdsByStatus[*query.Status]
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return durable.Page[storedTask, durable.Cursor]{}, err
	}
	values := []storedTask{}
	scanIndexes(ids, start, func(index int) bool {
		if len(values) > limit {
			return false
		}
		value := storage.state.tasks[durable.TaskId(ids[index])]
		if query.ConversationId != nil && value.ConversationId != *query.ConversationId {
			return true
		}
		if query.Kind != nil && value.Kind != *query.Kind {
			return true
		}
		if query.AbortRequested != nil && value.AbortRequested != *query.AbortRequested {
			return true
		}
		if query.Background != nil && value.Background != *query.Background {
			return true
		}
		values = append(values, value)
		return true
	})
	return page(values, limit, start.Order, taskIdOf, cloneTask), nil
}

// Submission looks up the latest complete record for one admitted submission.
func (storage *MemoryStorage) Submission(
	_ context.Context, id durable.SubmissionId,
) (*durable.SubmissionRecord, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	value, ok := storage.state.submissions[id]
	if !ok {
		return nil, nil
	}
	copied := cloneSubmission(value)
	return &copied, nil
}

// ScanSubmissions scans submissions matching every supplied filter by ID in query.Order, ascending by default.
func (storage *MemoryStorage) ScanSubmissions(
	_ context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	ids := storage.state.submissionIds
	if query.Status != nil {
		ids = storage.state.submissionIdsByStatus[*query.Status]
	}
	start, err := scan.StartOf(query.Order, cursor, durable.ScanAscending)
	if err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	values := []durable.SubmissionRecord{}
	scanIndexes(ids, start, func(index int) bool {
		if len(values) > limit {
			return false
		}
		value := storage.state.submissions[durable.SubmissionId(ids[index])]
		if query.ConversationId != nil && value.ConversationId != *query.ConversationId {
			return true
		}
		values = append(values, value)
		return true
	})
	return page(values, limit, start.Order, submissionIdOf, cloneSubmission), nil
}

// SubmissionByRequest finds a submission by its conversation-scoped host deduplication key.
func (storage *MemoryStorage) SubmissionByRequest(
	_ context.Context, conversationId durable.ConversationId, requestId string,
) (*durable.SubmissionRecord, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	id, ok := storage.state.submissionIdsByRequest[conversationId][requestId]
	if !ok {
		return nil, nil
	}
	copied := cloneSubmission(storage.state.submissions[id])
	return &copied, nil
}

// FindDocument resolves the incarnation occupying one exact logical address at the selected point.
func (storage *MemoryStorage) FindDocument(
	_ context.Context, address durable.DocumentAddress, at durable.DocumentPoint,
) (*durable.DocumentRecord, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	index := storage.state.documentAddresses[addressKeyOf(address)]
	if index == nil {
		return nil, nil
	}
	if at.Current {
		if index.currentId == nil {
			return nil, nil
		}
		record := cloneDocumentRecord(storage.state.documents[*index.currentId].record)
		return &record, nil
	}
	for _, id := range index.ids {
		record := storage.state.documents[durable.DocumentId(id)].record
		if isAliveAt(record, at) {
			copied := cloneDocumentRecord(record)
			return &copied, nil
		}
	}
	return nil, nil
}

// Document materializes one specific incarnation by ID at the selected point without following a replacement at its
// address.
func (storage *MemoryStorage) Document(
	_ context.Context, id durable.DocumentId, at durable.DocumentPoint,
) (*durable.StoredDocument, error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return nil, err
	}
	stored, err := storage.materializeDocument(id, at)
	if err != nil || stored == nil {
		return nil, err
	}
	return &durable.StoredDocument{
		Record:          cloneDocumentRecord(stored.Record),
		Version:         stored.Version,
		Value:           cloneObject(stored.Value),
		DeltasSinceBase: stored.DeltasSinceBase,
	}, nil
}

// ScanDocuments scans incarnations alive in one exact scope at the selected point.
func (storage *MemoryStorage) ScanDocuments(
	_ context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor,
) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	if err := storage.assertOpen(); err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	ids := storage.state.documentIdsByScope[scopeKeyOf(query.Scope)]
	start, err := startAfter(ids, cursor)
	if err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	values := []durable.DocumentRecord{}
	for index := start; index < len(ids) && len(values) <= limit; index++ {
		record := storage.state.documents[durable.DocumentId(ids[index])].record
		if query.Kind != nil && record.Kind != *query.Kind {
			continue
		}
		if isAliveAt(record, query.At) {
			values = append(values, record)
		}
	}
	return page(values, limit, durable.ScanAscending, documentIdOf, cloneDocumentRecord), nil
}

// Close releases the storage; all later operations fail.
func (storage *MemoryStorage) Close(_ context.Context) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.closed = true
	return nil
}

// materializeDocument returns a value that shares structure with retained state; callers copy before exposing it.
func (storage *MemoryStorage) materializeDocument(
	id durable.DocumentId, at durable.DocumentPoint,
) (*durable.StoredDocument, error) {
	stored := storage.state.documents[id]
	if stored == nil {
		return nil, nil
	}
	if !at.Current && isCurrentOnly(stored.record.Scope, stored.record.History) {
		return nil, fmt.Errorf("Document %d does not retain historical content", id)
	}
	if !isAliveAt(stored.record, at) {
		return nil, nil
	}
	revisions := stored.revisions
	if !at.Current {
		end := len(revisions)
		for end > 0 && revisions[end-1].seq > at.Seq {
			end--
		}
		revisions = revisions[:end]
	}
	baseIndex := len(revisions) - 1
	for baseIndex >= 0 && revisions[baseIndex].content.Kind != durable.ContentBase {
		baseIndex--
	}
	if baseIndex < 0 {
		return nil, fmt.Errorf("Document %d is missing a required base", id)
	}
	base := revisions[baseIndex].content
	batches := make([][]durable.Op, 0, len(revisions)-baseIndex-1)
	for _, revision := range revisions[baseIndex+1:] {
		if revision.content.Kind != durable.ContentDelta || revision.content.Version != base.Version {
			return nil, fmt.Errorf("Document %d crosses a stored version boundary without a base", id)
		}
		batches = append(batches, revision.content.Ops)
	}
	value, err := ops.ApplyBatches(base.Value, batches)
	if err != nil {
		return nil, err
	}
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return nil, fmt.Errorf("Document %d does not materialize to an object", id)
	}
	return &durable.StoredDocument{
		Record:          stored.record,
		Version:         base.Version,
		Value:           object,
		DeltasSinceBase: len(revisions) - baseIndex - 1,
	}, nil
}

// visibleEntries visits the conversation's fork-aware history newest-first within the inclusive ID bounds until visit
// returns false.
func (storage *MemoryStorage) visibleEntries(
	conversationId durable.ConversationId, minEntryId, maxEntryId int64, visit func(durable.EntryRecord) bool,
) error {
	if _, ok := storage.state.conversations[conversationId]; !ok {
		return fmt.Errorf("Unknown conversation: %d", conversationId)
	}
	currentId := conversationId
	upperEntryId := maxEntryId
	for {
		ids := storage.state.entryIds[currentId]
		for index := upperBound(ids, upperEntryId) - 1; index >= 0; index-- {
			if ids[index] < minEntryId {
				break
			}
			if !visit(storage.state.entries[durable.EntryId(ids[index])]) {
				return nil
			}
		}
		conversation := storage.state.conversations[currentId]
		if conversation.Parent == nil {
			return nil
		}
		upperEntryId = min(upperEntryId, int64(conversation.Parent.At))
		if upperEntryId < minEntryId {
			return nil
		}
		currentId = conversation.Parent.ConversationId
	}
}

// visibleEntriesAscending visits the conversation's fork-aware history oldest first within the inclusive ID bounds
// until visit returns false: the fork chain's segments from the root conversation forward, each capped at its fork
// point.
func (storage *MemoryStorage) visibleEntriesAscending(
	conversationId durable.ConversationId, minEntryId, maxEntryId int64, visit func(durable.EntryRecord) bool,
) error {
	if _, ok := storage.state.conversations[conversationId]; !ok {
		return fmt.Errorf("Unknown conversation: %d", conversationId)
	}
	type segment struct {
		conversationId durable.ConversationId
		upper          int64
	}
	segments := []segment{}
	currentId := conversationId
	upperEntryId := maxEntryId
	for {
		segments = append(segments, segment{conversationId: currentId, upper: upperEntryId})
		conversation := storage.state.conversations[currentId]
		if conversation.Parent == nil {
			break
		}
		upperEntryId = min(upperEntryId, int64(conversation.Parent.At))
		if upperEntryId < minEntryId {
			break
		}
		currentId = conversation.Parent.ConversationId
	}
	for _, current := range slices.Backward(segments) {
		ids := storage.state.entryIds[current.conversationId]
		for index := lowerBound(ids, minEntryId); index < len(ids); index++ {
			if ids[index] > current.upper {
				break
			}
			if !visit(storage.state.entries[durable.EntryId(ids[index])]) {
				return nil
			}
		}
	}
	return nil
}

// writeTable returns the table a creating write claims its ID in; ok is false for document change and retirement.
func writeTable(write durable.StorageWrite) (id int64, table tableName, ok bool) {
	switch typed := write.(type) {
	case durable.ConversationWrite:
		return int64(typed.Value.Id), tableConversation, true
	case durable.EntryWrite:
		return int64(typed.Value.Id), tableEntry, true
	case durable.TaskWrite:
		return int64(typed.Value.Id), tableTask, true
	case durable.SubmissionWrite:
		return int64(typed.Value.Id), tableSubmission, true
	case durable.DocumentCreateWrite:
		return int64(typed.Record.Id), tableDocument, true
	case durable.DocumentCopyWrite:
		return int64(typed.Record.Id), tableDocument, true
	default:
		return 0, "", false
	}
}

// checkClaim applies the global ID ownership rule to one creating write: conversations, entries, and documents are
// created once; tasks and submissions are replaced in place.
func checkClaim(id int64, table, existing, earlier tableName) error {
	if table == tableConversation || table == tableEntry || table == tableDocument {
		if existing != "" {
			return fmt.Errorf("ID %d already belongs to %s", id, existing)
		}
		if earlier != "" {
			return fmt.Errorf("ID %d is written more than once", id)
		}
		return nil
	}
	if existing != "" && existing != table {
		return fmt.Errorf("ID %d already belongs to %s", id, existing)
	}
	if earlier != "" && earlier != table {
		return fmt.Errorf("ID %d is written as two record types", id)
	}
	return nil
}

func (storage *MemoryStorage) checkGlobalIds(writes []durable.StorageWrite) error {
	claimed := map[int64]tableName{}
	for _, write := range writes {
		id, table, ok := writeTable(write)
		if !ok {
			continue
		}
		if err := checkClaim(id, table, storage.state.recordTypes[id], claimed[id]); err != nil {
			return err
		}
		claimed[id] = table
	}
	return nil
}

func prepareDocumentActions(writes []durable.StorageWrite) (*documentActions, error) {
	actions := &documentActions{actions: map[durable.DocumentId]*documentAction{}}
	for _, write := range writes {
		switch typed := write.(type) {
		case durable.DocumentCreateWrite:
			action := actions.get(typed.Record.Id)
			if action.create != nil || action.content != nil {
				return nil, fmt.Errorf("Document %d has more than one content command", typed.Record.Id)
			}
			action.create = &typed.Record
			action.content = &typed.Content
		case durable.DocumentChangeWrite:
			action := actions.get(typed.Id)
			if action.content != nil {
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

func (storage *MemoryStorage) checkDocumentActions(actions *documentActions) error {
	liveCounts := map[addressKey]int{}
	var liveOrder []addressKey
	for _, id := range actions.order {
		action := actions.actions[id]
		existing := storage.state.documents[id]
		if action.create == nil && existing == nil {
			return fmt.Errorf("Unknown document: %d", id)
		}
		if action.create != nil && existing != nil {
			return fmt.Errorf("Document %d already exists", id)
		}
		if existing != nil && existing.record.RetiredAt != nil {
			return fmt.Errorf("Document %d is retired", id)
		}
		if action.content != nil && action.content.Kind == durable.ContentDelta {
			if existing == nil || len(existing.revisions) == 0 {
				return fmt.Errorf("Document %d delta has no base", id)
			}
			if existing.revisions[len(existing.revisions)-1].content.Version != action.content.Version {
				return fmt.Errorf("Document %d version transition requires a base", id)
			}
		}

		var key addressKey
		if action.create == nil {
			key = recordAddressKey(existing.record.Kind, existing.record.Scope, existing.record.Key)
		} else {
			key = recordAddressKey(action.create.Kind, action.create.Scope, action.create.Key)
		}
		var currentId *durable.DocumentId
		if index := storage.state.documentAddresses[key]; index != nil {
			currentId = index.currentId
		}
		live, counted := liveCounts[key]
		if !counted {
			liveOrder = append(liveOrder, key)
			if currentId != nil {
				live = 1
			}
		}
		if action.retire && currentId != nil && *currentId == id {
			live--
		}
		if action.create != nil && !action.retire {
			live++
		}
		liveCounts[key] = live
	}
	for _, key := range liveOrder {
		if liveCounts[key] > 1 {
			return errors.New("Document address already has a current incarnation")
		}
	}
	return nil
}

func (storage *MemoryStorage) applyDocumentActions(actions *documentActions, seq durable.Seq) {
	state := &storage.state
	for _, id := range actions.order {
		action := actions.actions[id]
		stored := state.documents[id]
		if action.create != nil {
			record := durable.DocumentRecord{
				Id:        action.create.Id,
				Kind:      action.create.Kind,
				Key:       action.create.Key,
				CreatedAt: seq,
				Scope:     action.create.Scope,
				History:   action.create.History,
				Fork:      action.create.Fork,
			}
			if action.retire {
				retiredAt := seq
				record.RetiredAt = &retiredAt
			}
			stored = &storedDocumentState{
				record:    record,
				revisions: []documentRevision{{content: *action.content, seq: seq}},
			}
			state.recordTypes[int64(id)] = tableDocument
			state.documents[id] = stored

			key := recordAddressKey(record.Kind, record.Scope, record.Key)
			address := state.documentAddresses[key]
			if address == nil {
				address = &documentAddressIndex{}
				state.documentAddresses[key] = address
			}
			address.ids = insertSorted(address.ids, int64(id))
			insertMapId(state.documentIdsByScope, scopeKeyOf(record.Scope), int64(id))
			storage.adoptId(int64(id))
		} else if action.content != nil {
			revision := documentRevision{content: *action.content, seq: seq}
			if revision.content.Kind == durable.ContentBase &&
				isCurrentOnly(stored.record.Scope, stored.record.History) {
				stored.revisions = []documentRevision{revision}
			} else {
				stored.revisions = append(stored.revisions, revision)
			}
		}

		if action.retire && action.create == nil {
			retiredAt := seq
			stored.record.RetiredAt = &retiredAt
		}
		if action.retire && isCurrentOnly(stored.record.Scope, stored.record.History) {
			stored.revisions = nil
		}
		if action.create != nil || action.retire {
			address := state.documentAddresses[recordAddressKey(stored.record.Kind, stored.record.Scope, stored.record.Key)]
			if action.retire && address.currentId != nil && *address.currentId == id {
				address.currentId = nil
			}
			if action.create != nil && !action.retire {
				currentId := id
				address.currentId = &currentId
			}
		}
	}
}

func (storage *MemoryStorage) assertOpen() error {
	if storage.closed {
		return errors.New("MemoryStorage is closed")
	}
	return nil
}
