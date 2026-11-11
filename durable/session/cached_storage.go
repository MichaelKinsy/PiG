// Write-through cache of hot Storage record reads for the Session that owns the Storage.
//
// Ports packages/durable/src/session/cached-storage.ts
//
// Go mapping decisions:
//   - The cache is read from several goroutines (the line and the off-line readers), so one mutex guards its state.
//     It is never held across a read of the backend.
//   - Cached records are shared: Published keeps a copy of the written record's struct, and every read of it returns
//     that pointer, as upstream shares the frozen object. Object.freeze has no Go counterpart; callers must not
//     modify a record Storage returned, as with every Storage.
//   - `Storage.publishing` is the PublishingStorage capability. A Go method cannot be absent, so Publishing runs
//     publish directly when the backend has no scope, which is what the Session does for a Storage without one.
//     ScopeOf reports the backend's own scope or nil.
package session

import (
	"container/list"
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
)

type anyTaskRecord = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]

const (
	conversationCapacity = 4096
	// Ended task records carry inputs and outcomes, such as a tool call's arguments, so fewer are kept.
	endedTaskCapacity  = 256
	submissionCapacity = 256
)

// awaitedTask is a task some task waits on: how many such waits name it, and its ended record once it ended.
type awaitedTask struct {
	count  int
	record *anyTaskRecord
}

// taskWait is, per task that waits or left its wait and has not changed since: what it waited on, and whether it left.
type taskWait struct {
	on   []durable.TaskId
	left bool
}

// recordReads is, per record with a read in flight: the reads, and how many published commits wrote it since the
// first began.
type recordReads struct {
	count   int
	version int
}

var (
	_ durable.Storage           = (*CachedStorage)(nil)
	_ durable.PublishingStorage = (*CachedStorage)(nil)
)

// CachedStorage serves hot record reads from memory: conversations, tasks, and written submissions, each table
// bounded by a count, the least recently used record leaving first, except live tasks, which are kept until they end,
// and the ended tasks a task waits on, kept until it moved on from its wait: a task's record is read again at each of
// its state changes, a waiter reads the outcomes of what it waited on once it resumes (`runtime.outcomes()`), and
// both are bounded by live work, which the scheduler keeps in memory too. Without that, a burst of more tasks than the
// count would make each of those reads a Storage read on the line. Every commit goes through it, and one process owns
// a Storage, so it applies each batch as it is published and never goes stale: Commit's batches when the Session
// publishes them (Published), not at their durability receipts. A read result is kept only when no commit published
// since the read started wrote its record, as the read may not observe that commit; versions are kept only for
// records with a read in flight. Entries, scans, and documents pass through: the Session keeps documents and the
// newest entry per conversation itself, and context ranges hold the entries a run reads.
//
// Not for a read-only Session: another process writes its Storage.
type CachedStorage struct {
	storage durable.Storage

	mu            sync.Mutex
	conversations *lru[durable.ConversationId, *durable.ConversationRecord]
	// liveTasks are the live (non-terminal) task records, until their task ends.
	liveTasks map[durable.TaskId]*anyTaskRecord
	// awaited are the tasks some task waits on, or waited on until its wait ended and it has not moved on since.
	awaited map[durable.TaskId]*awaitedTask
	// waits are, per task that waits, or left its wait and has not changed since, what it waited on.
	waits       map[durable.TaskId]*taskWait
	tasks       *lru[durable.TaskId, *anyTaskRecord]
	submissions *lru[durable.SubmissionId, *durable.SubmissionRecord]
	// reads are, per record with a read in flight, its reads. Every record kind shares one numeric ID namespace.
	reads map[int64]*recordReads
}

// NewCachedStorage caches the record reads of storage.
func NewCachedStorage(storage durable.Storage) *CachedStorage {
	return &CachedStorage{
		storage:       storage,
		conversations: newLru[durable.ConversationId, *durable.ConversationRecord](conversationCapacity),
		liveTasks:     map[durable.TaskId]*anyTaskRecord{},
		awaited:       map[durable.TaskId]*awaitedTask{},
		waits:         map[durable.TaskId]*taskWait{},
		tasks:         newLru[durable.TaskId, *anyTaskRecord](endedTaskCapacity),
		submissions:   newLru[durable.SubmissionId, *durable.SubmissionRecord](submissionCapacity),
		reads:         map[int64]*recordReads{},
	}
}

// Publishing is the backend's own scope for a publication when it has one; otherwise it runs publish directly.
func (cached *CachedStorage) Publishing(ctx context.Context, publish func()) {
	if scope := ScopeOf(cached.storage); scope != nil {
		scope(ctx, publish)
		return
	}
	publish()
}

// Commit passes the batches through; the Session applies each one with Published when it publishes it.
func (cached *CachedStorage) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	return cached.storage.Commit(ctx, batches, receive)
}

// Published applies a batch Commit stored, when the Session publishes it. Its records are kept as they are, no deep
// copies: the cache keeps a copy of each record's struct (a write carries it by value), and what the record references, such
// as its state and memos, is the write's own, shared and not duplicated.
func (cached *CachedStorage) Published(writes []durable.StorageWrite) {
	cached.mu.Lock()
	defer cached.mu.Unlock()
	for _, write := range writes {
		switch write := write.(type) {
		case durable.ConversationWrite:
			cached.wrote(int64(write.Value.Id))
			record := write.Value
			cached.conversations.set(record.Id, &record)
		case durable.TaskWrite:
			cached.wrote(int64(write.Value.Id))
			record := write.Value
			cached.followWait(&record)
			cached.keepTask(&record)
		case durable.SubmissionWrite:
			cached.wrote(int64(write.Value.Id))
			record := write.Value
			cached.submissions.set(record.Id, &record)
		}
	}
}

// MintId passes through.
func (cached *CachedStorage) MintId() (int64, error) {
	return cached.storage.MintId()
}

// Conversation serves a kept record without a read.
func (cached *CachedStorage) Conversation(ctx context.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	cached.mu.Lock()
	record, ok := cached.conversations.get(id)
	cached.mu.Unlock()
	if ok {
		return record, nil
	}
	return keepRead(cached, int64(id),
		func() (*durable.ConversationRecord, error) { return cached.storage.Conversation(ctx, id) },
		func(record *durable.ConversationRecord) { cached.conversations.set(id, record) })
}

// ScanConversations passes through.
func (cached *CachedStorage) ScanConversations(ctx context.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	return cached.storage.ScanConversations(ctx, query, limit, cursor)
}

// Entry passes through.
func (cached *CachedStorage) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryAt, error) {
	return cached.storage.Entry(ctx, id)
}

// VisibleEntry passes through.
func (cached *CachedStorage) VisibleEntry(ctx context.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.EntryAt, error) {
	return cached.storage.VisibleEntry(ctx, conversationId, id)
}

// FindLatestHeadMarker passes through.
func (cached *CachedStorage) FindLatestHeadMarker(ctx context.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId) (*durable.EntryRecord, error) {
	return cached.storage.FindLatestHeadMarker(ctx, conversationId, atOrBeforeEntryId)
}

// ScanEntries passes through.
func (cached *CachedStorage) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	return cached.storage.ScanEntries(ctx, query, limit, cursor)
}

// Task serves a kept record without a read.
func (cached *CachedStorage) Task(ctx context.Context, id durable.TaskId) (*anyTaskRecord, error) {
	cached.mu.Lock()
	record := cached.cachedTask(id)
	cached.mu.Unlock()
	if record != nil {
		return record, nil
	}
	return keepRead(cached, int64(id),
		func() (*anyTaskRecord, error) { return cached.storage.Task(ctx, id) },
		func(record *anyTaskRecord) { cached.keepTask(record) })
}

// cachedTask is a live record, else an awaited ended one, else one of the bounded table; nil when none is kept.
func (cached *CachedStorage) cachedTask(id durable.TaskId) *anyTaskRecord {
	if record, ok := cached.liveTasks[id]; ok {
		return record
	}
	if awaited, ok := cached.awaited[id]; ok && awaited.record != nil {
		return awaited.record
	}
	record, _ := cached.tasks.get(id)
	return record
}

// ScanTasks passes through.
func (cached *CachedStorage) ScanTasks(ctx context.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[anyTaskRecord, durable.Cursor], error) {
	return cached.storage.ScanTasks(ctx, query, limit, cursor)
}

// Submission is cached as written only: this process writes every submission it settles, and a read one is usually
// settled.
func (cached *CachedStorage) Submission(ctx context.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	cached.mu.Lock()
	record, ok := cached.submissions.get(id)
	cached.mu.Unlock()
	if ok {
		return record, nil
	}
	return cached.storage.Submission(ctx, id)
}

// ScanSubmissions passes through.
func (cached *CachedStorage) ScanSubmissions(ctx context.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	return cached.storage.ScanSubmissions(ctx, query, limit, cursor)
}

// SubmissionByRequest passes through.
func (cached *CachedStorage) SubmissionByRequest(ctx context.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	return cached.storage.SubmissionByRequest(ctx, conversationId, requestId)
}

// FindDocument passes through.
func (cached *CachedStorage) FindDocument(ctx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	return cached.storage.FindDocument(ctx, address, at)
}

// Document passes through.
func (cached *CachedStorage) Document(ctx context.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	return cached.storage.Document(ctx, id, at)
}

// ScanDocuments passes through.
func (cached *CachedStorage) ScanDocuments(ctx context.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	return cached.storage.ScanDocuments(ctx, query, limit, cursor)
}

// Close passes through.
func (cached *CachedStorage) Close(ctx context.Context) error {
	return cached.storage.Close(ctx)
}

// CachedSizes is the records kept per table.
type CachedSizes struct {
	Conversations int
	Tasks         int
	LiveTasks     int
	Awaited       int
	Waits         int
	Submissions   int
}

// Sizes is the records kept per table, for tests that bound memory.
func (cached *CachedStorage) Sizes() CachedSizes {
	cached.mu.Lock()
	defer cached.mu.Unlock()
	return CachedSizes{
		Conversations: cached.conversations.size(),
		Tasks:         cached.tasks.size(),
		LiveTasks:     len(cached.liveTasks),
		Awaited:       len(cached.awaited),
		Waits:         len(cached.waits),
		Submissions:   cached.submissions.size(),
	}
}

// ReadsInFlight is the records with a read in flight, for tests that bound memory.
func (cached *CachedStorage) ReadsInFlight() int {
	cached.mu.Lock()
	defer cached.mu.Unlock()
	return len(cached.reads)
}

// keepTask keeps a task record: a live one until its task ends, an ended one in the bounded table. mu is held.
func (cached *CachedStorage) keepTask(record *anyTaskRecord) {
	if record.State.Status == durable.TaskTerminal {
		delete(cached.liveTasks, record.Id)
		if awaited, ok := cached.awaited[record.Id]; ok {
			awaited.record = record
		} else {
			cached.tasks.set(record.Id, record)
		}
		return
	}
	cached.tasks.delete(record.Id)
	cached.liveTasks[record.Id] = record
}

// followWait follows a task's waits from its written record: a wait holds the ended records of its `on` until the
// task, after leaving the wait, changes again (or ends), as the phase that resumes reads their outcomes before its
// next commit. mu is held.
func (cached *CachedStorage) followWait(record *anyTaskRecord) {
	wait := cached.waits[record.Id]
	if record.State.Status == durable.TaskWaiting {
		if wait != nil {
			cached.release(wait.on)
		}
		on := record.State.On
		cached.waits[record.Id] = &taskWait{on: on}
		for _, id := range on {
			awaited := cached.awaited[id]
			if awaited == nil {
				// One that ended already moves over from the bounded table.
				awaited = &awaitedTask{}
				if ended, ok := cached.tasks.get(id); ok {
					awaited.record = ended
				}
				cached.tasks.delete(id)
				cached.awaited[id] = awaited
			}
			awaited.count++
		}
		return
	}
	if wait == nil {
		return
	}
	if !wait.left && record.State.Status != durable.TaskTerminal {
		wait.left = true
		return
	}
	delete(cached.waits, record.Id)
	cached.release(wait.on)
}

// release ends a wait on `on`: what no other wait names goes back to the bounded table. mu is held.
func (cached *CachedStorage) release(on []durable.TaskId) {
	for _, id := range on {
		awaited := cached.awaited[id]
		awaited.count--
		if awaited.count > 0 {
			continue
		}
		delete(cached.awaited, id)
		if awaited.record != nil {
			cached.tasks.set(id, awaited.record)
		}
	}
}

// wrote notes that a published commit wrote record id: a read of it in flight may have missed that. mu is held.
func (cached *CachedStorage) wrote(id int64) {
	if reads, ok := cached.reads[id]; ok {
		reads.version++
	}
}

// keepRead returns a read's result; it keeps a found record unless a commit published since wrote it.
func keepRead[T any](cached *CachedStorage, id int64, read func() (*T, error), keep func(*T)) (*T, error) {
	cached.mu.Lock()
	reads := cached.reads[id]
	if reads == nil {
		reads = &recordReads{}
		cached.reads[id] = reads
	}
	reads.count++
	version := reads.version
	cached.mu.Unlock()

	value, err := read()

	cached.mu.Lock()
	defer cached.mu.Unlock()
	reads.count--
	if reads.count == 0 {
		delete(cached.reads, id)
	}
	if err != nil || value == nil {
		return value, err
	}
	if reads.version == version {
		keep(value)
	}
	return value, nil
}

// ScopeOf is storage's publication scope (the PublishingStorage capability), for a wrapper to expose as its own; nil
// when it has none.
func ScopeOf(storage durable.Storage) func(ctx context.Context, publish func()) {
	publishing, ok := storage.(durable.PublishingStorage)
	if !ok {
		return nil
	}
	return publishing.Publishing
}

// lru is a map bounded to capacity entries that drops the least recently used one. Not safe for concurrent use.
type lru[K comparable, V any] struct {
	items    map[K]*list.Element
	order    *list.List
	capacity int
}

type lruEntry[K comparable, V any] struct {
	key   K
	value V
}

func newLru[K comparable, V any](capacity int) *lru[K, V] {
	return &lru[K, V]{items: map[K]*list.Element{}, order: list.New(), capacity: capacity}
}

// get returns the value of key, made the most recently used.
func (cache *lru[K, V]) get(key K) (V, bool) {
	element, ok := cache.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	cache.order.MoveToBack(element)
	return element.Value.(*lruEntry[K, V]).value, true
}

// peek returns the value of key without changing its recency.
func (cache *lru[K, V]) peek(key K) (V, bool) {
	element, ok := cache.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	return element.Value.(*lruEntry[K, V]).value, true
}

func (cache *lru[K, V]) size() int { return len(cache.items) }

func (cache *lru[K, V]) delete(key K) {
	if element, ok := cache.items[key]; ok {
		cache.order.Remove(element)
		delete(cache.items, key)
	}
}

// set makes key the most recently used, dropping the least recently used beyond capacity.
func (cache *lru[K, V]) set(key K, value V) {
	if element, ok := cache.items[key]; ok {
		element.Value.(*lruEntry[K, V]).value = value
		cache.order.MoveToBack(element)
		return
	}
	cache.items[key] = cache.order.PushBack(&lruEntry[K, V]{key: key, value: value})
	if len(cache.items) > cache.capacity {
		oldest := cache.order.Front()
		cache.order.Remove(oldest)
		delete(cache.items, oldest.Value.(*lruEntry[K, V]).key)
	}
}
