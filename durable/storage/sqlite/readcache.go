package sqlite

import (
	"reflect"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/detach"
)

// DefaultReadCacheBytes is the default bound on the estimated size of the results a SqliteStorage keeps for its reads.
// pig additive (D104): the read cache has no Pi counterpart.
const DefaultReadCacheBytes = 8 << 20

// ReadCacheStats counts what a storage's read cache did since it opened.
// pig additive (D104): the read cache has no Pi counterpart.
type ReadCacheStats struct {
	// Hits are reads answered from memory; Fills are reads kept; Rejected are reads not kept because the database
	// changed while they ran or they were larger than the cache allows.
	Hits, Fills, Rejected uint64
	// Resets are the times a commit of another connection dropped everything; Adoptions are commits of this storage that
	// kept what they did not write.
	Resets, Adoptions uint64
	// Items and Bytes are what the cache holds now; Bytes is the estimated size.
	Items int
	Bytes int64
	// Disabled is true once the cache found its commit token not following the storage's own commits and stopped.
	Disabled bool
}

// tableSet is a set of the tables a read depends on or a commit writes. Documents stand for the document table and its
// revisions.
type tableSet uint8

const (
	dependsOnConversations tableSet = 1 << iota
	dependsOnEntries
	dependsOnTasks
	dependsOnSubmissions
	dependsOnDocuments

	dependsOnTables = 5
	dependsOnAll    = tableSet(1<<dependsOnTables - 1)
)

// readKind is the read a cache key names.
type readKind uint8

const (
	cacheEntry readKind = iota + 1
	cacheVisibleEntry
	cacheHeadMarker
	cacheScanEntries
	cacheConversation
	cacheScanConversations
	cacheTask
	cacheScanTasks
	cacheSubmission
	cacheScanSubmissions
	cacheSubmissionByRequest
	cacheFindDocument
	cacheDocument
	cacheScanDocuments
)

// Bits of cacheKey.flags: which optional arguments a read was given, and the values of its small enumerations.
const (
	flagHasA uint32 = 1 << iota
	flagHasB
	flagHasC
	flagHasText
	flagHasOther
	flagAscending
	flagHasAfter
	flagCurrent
	flagKeyed
	flagScopeConversation
	flagScopeTask
	flagAbortRequested
	flagAbortKnown
	flagBackground
	flagBackgroundKnown
)

// cacheKey is a read and its arguments. The fields are the arguments in the order the read declares them; flags says
// which were given.
type cacheKey struct {
	kind  readKind
	flags uint32
	limit int
	a, b  int64
	c, d  int64
	s, t  string
}

// cacheItem is one kept result. Results are never written once kept; every hit returns a copy.
type cacheItem struct {
	key        cacheKey
	value      any
	size       int64
	deps       tableSet
	stamps     [dependsOnTables]uint32
	prev, next *cacheItem
}

// pig additive (D104): Pi runs a statement for every read; PiG keeps the answers of recent reads.
//
// readCache holds the results of the reads of one SqliteStorage, each under the arguments it was asked with, in memory
// bounded by a byte budget (least recently used first out). A result is a pure function of the committed state of the
// database, so it is valid for exactly as long as no commit is made to the database file, by this storage, by another
// connection or by another process:
//
//   - The committed state has a token (chordsqlite.CommitTokener): the wal-index header SQLite keeps in shared memory and
//     rewrites in every commit, read with one pread. Every read takes the token first. A token that is not the one the
//     cache holds means a commit the cache did not make: everything is dropped and the cache holds the new token.
//   - A read takes its token before it reads, so what it read is the state of its token or a later one. The result is kept
//     only if the cache still holds that token when the read ends; a later state makes the next token differ, and the
//     result is then dropped before it can be returned.
//   - A Commit of this storage knows what it wrote. Inside its write transaction no other connection can commit, so the
//     token read there, pre, names the state it starts from; when the token read after its COMMIT is exactly one commit
//     later (CommitToken.Follows), the commit was the only one, and the cache keeps the results of the tables it did not
//     write and holds the new token. Results of the tables it wrote are stale by generation.
//
// A hit returns a detached copy, as every read of a Storage returns one.
type readCache struct {
	source chordsqlite.CommitTokener
	budget int64

	mu          sync.Mutex
	token       chordsqlite.CommitToken
	known       bool
	off         bool
	generations [dependsOnTables]uint32
	items       map[cacheKey]*cacheItem
	front, back *cacheItem
	bytes       int64
	stats       ReadCacheStats
}

func newReadCache(source chordsqlite.CommitTokener, budget int64) *readCache {
	return &readCache{source: source, budget: budget, items: map[cacheKey]*cacheItem{}}
}

// fresh reports whether no commit of this storage wrote a table item depends on since item was kept. The caller holds mu.
func (cache *readCache) fresh(item *cacheItem) bool { return cache.stampsFresh(item.deps, item.stamps) }

// stampsFresh reports whether no commit of this storage wrote a table in deps since stamps were taken. The caller holds mu.
func (cache *readCache) stampsFresh(deps tableSet, stamps [dependsOnTables]uint32) bool {
	for table := range dependsOnTables {
		if deps&(1<<table) != 0 && stamps[table] != cache.generations[table] {
			return false
		}
	}
	return true
}

// unlink removes item from the recency list. The caller holds mu.
func (cache *readCache) unlink(item *cacheItem) {
	if item.prev != nil {
		item.prev.next = item.next
	} else {
		cache.front = item.next
	}
	if item.next != nil {
		item.next.prev = item.prev
	} else {
		cache.back = item.prev
	}
	item.prev, item.next = nil, nil
}

// pushFront makes item the most recently used. The caller holds mu.
func (cache *readCache) pushFront(item *cacheItem) {
	item.next = cache.front
	if cache.front != nil {
		cache.front.prev = item
	}
	cache.front = item
	if cache.back == nil {
		cache.back = item
	}
}

// remove forgets item. The caller holds mu.
func (cache *readCache) remove(item *cacheItem) {
	cache.unlink(item)
	delete(cache.items, item.key)
	cache.bytes -= item.size
}

// reset drops everything kept. The caller holds mu.
func (cache *readCache) reset() {
	clear(cache.items)
	cache.front, cache.back = nil, nil
	cache.bytes = 0
}

// lookup returns what was kept for key, valid under token, after dropping everything kept under another token. The
// caller holds mu.
func (cache *readCache) lookup(token chordsqlite.CommitToken, key cacheKey) *cacheItem {
	if !cache.known || cache.token != token {
		if cache.known {
			cache.stats.Resets++
		}
		cache.reset()
		cache.token, cache.known = token, true
		return nil
	}
	item := cache.items[key]
	if item == nil {
		return nil
	}
	if !cache.fresh(item) {
		cache.remove(item)
		return nil
	}
	cache.unlink(item)
	cache.pushFront(item)
	return item
}

// keep stores a result read under token, unless the database changed since or the result is too large to be worth a
// slot. The caller holds mu.
func (cache *readCache) keep(token chordsqlite.CommitToken, stamps [dependsOnTables]uint32, key cacheKey, deps tableSet, value any, size int64) {
	if !cache.known || cache.token != token || cache.off || size > cache.budget/4 || !cache.stampsFresh(deps, stamps) {
		cache.stats.Rejected++
		return
	}
	if old := cache.items[key]; old != nil {
		cache.remove(old)
	}
	item := &cacheItem{key: key, value: value, size: size, deps: deps, stamps: stamps}
	cache.items[key] = item
	cache.pushFront(item)
	cache.bytes += size
	cache.stats.Fills++
	for cache.bytes > cache.budget && cache.back != nil {
		cache.remove(cache.back)
	}
}

// readThrough is a read of storage: the copy of the result kept for key if there is one, else what read returns, kept.
// deps are the tables the read depends on. A read that fails or finds the database without a token is not kept.
func readThrough[T any](storage *SqliteStorage, key cacheKey, deps tableSet, clone func(T) T, size func(T) int64, read func() (T, error)) (T, error) {
	cache := storage.cache
	if cache == nil {
		return read()
	}
	if err := storage.assertOpen(); err != nil {
		var none T
		return none, err
	}
	token, ok := cache.source.CommitToken()
	if !ok {
		return read()
	}
	cache.mu.Lock()
	if cache.off {
		cache.mu.Unlock()
		return read()
	}
	if item := cache.lookup(token, key); item != nil {
		cache.stats.Hits++
		kept := item.value.(T)
		cache.mu.Unlock()
		return clone(kept), nil
	}
	stamps := cache.generations
	cache.mu.Unlock()

	value, err := read()
	if err != nil {
		return value, err
	}
	kept := clone(value)
	bytes := size(kept) + int64(len(key.s)+len(key.t))
	cache.mu.Lock()
	cache.keep(token, stamps, key, deps, kept, bytes)
	cache.mu.Unlock()
	return value, nil
}

// cacheCommit is a Commit of this storage as the cache follows it. A nil cacheCommit does nothing.
type cacheCommit struct {
	cache   *readCache
	batches [][]durable.StorageWrite
	pre     chordsqlite.CommitToken
	havePre bool
}

// beginCommit starts following a commit of batches, or returns nil when the cache keeps nothing a commit could spare.
func (cache *readCache) beginCommit(batches [][]durable.StorageWrite) *cacheCommit {
	if cache == nil {
		return nil
	}
	cache.mu.Lock()
	keeping := cache.known && !cache.off && len(cache.items) > 0
	cache.mu.Unlock()
	if !keeping {
		return nil
	}
	return &cacheCommit{cache: cache, batches: batches}
}

// start is called inside the write transaction of the commit, which no other connection can commit through: it takes the
// state the commit starts from.
func (commit *cacheCommit) start() {
	if commit == nil {
		return
	}
	commit.pre, commit.havePre = commit.cache.source.CommitToken()
}

// done is called after the commit returned; wrote is whether it stored a batch. It keeps what the commit did not write
// when the commit was the only one since start; otherwise the next read finds a token it does not hold and drops
// everything. A commit that wrote and left the token unchanged shows the token is not following the database, and ends the
// cache.
func (commit *cacheCommit) done(wrote bool) {
	if commit == nil || !commit.havePre {
		return
	}
	cache := commit.cache
	post, ok := cache.source.CommitToken()
	if !ok {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	switch {
	case post == commit.pre && wrote:
		cache.off = true
		cache.stats.Disabled = true
		cache.reset()
	case post.Follows(commit.pre) && cache.known && cache.token == commit.pre:
		touched := tablesWritten(commit.batches)
		for table := range dependsOnTables {
			if touched&(1<<table) != 0 {
				cache.generations[table]++
			}
		}
		cache.token = post
		cache.stats.Adoptions++
	}
}

// openReadCache is the read cache of a storage over db, or nil when db cannot name its committed state or budget keeps
// nothing.
func openReadCache(db SqliteDatabase, budget int64) *readCache {
	source, ok := db.(chordsqlite.CommitTokener)
	if !ok || budget <= 0 || source.AttachCommitToken() != nil {
		return nil
	}
	return newReadCache(source, budget)
}

// release drops everything and stops keeping.
func (cache *readCache) release() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.off = true
	cache.reset()
}

// ReadCacheStats reports the read cache of the storage; the zero value when it has none.
// pig additive (D104): the read cache has no Pi counterpart.
func (storage *SqliteStorage) ReadCacheStats() ReadCacheStats {
	cache := storage.cache
	if cache == nil {
		return ReadCacheStats{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	stats := cache.stats
	stats.Items, stats.Bytes = len(cache.items), cache.bytes
	return stats
}

// tablesWritten is the tables a batch of writes writes: every table when it holds a write this does not know.
func tablesWritten(batches [][]durable.StorageWrite) (touched tableSet) {
	for _, batch := range batches {
		for _, write := range batch {
			switch durable.StorageWriteType(write) {
			case "conversation":
				touched |= dependsOnConversations
			case "entry":
				touched |= dependsOnEntries
			case "task":
				touched |= dependsOnTasks
			case "submission":
				touched |= dependsOnSubmissions
			case "document.create", "document.copy", "document.change", "document.retire":
				touched |= dependsOnDocuments
			default:
				return dependsOnAll
			}
		}
	}
	return touched
}

// Clones: what the cache keeps and what a hit returns share nothing mutable with the result of a read, as a Storage
// returns detached values.

func cloneEntryAt(found *durable.EntryAt) *durable.EntryAt {
	if found == nil {
		return nil
	}
	return &durable.EntryAt{Entry: detach.Entry(found.Entry), CommitSeq: found.CommitSeq}
}

func cloneEntryRecord(found *durable.EntryRecord) *durable.EntryRecord {
	if found == nil {
		return nil
	}
	record := detach.Entry(*found)
	return &record
}

func cloneConversationRecord(found *durable.ConversationRecord) *durable.ConversationRecord {
	if found == nil {
		return nil
	}
	record := detach.Conversation(*found)
	return &record
}

func cloneTaskRecord(found *storedTask) *storedTask {
	if found == nil {
		return nil
	}
	record := detach.Task(*found)
	return &record
}

func cloneSubmissionRecord(found *durable.SubmissionRecord) *durable.SubmissionRecord {
	if found == nil {
		return nil
	}
	record := detach.Submission(*found)
	return &record
}

func cloneDocumentRecord(found *durable.DocumentRecord) *durable.DocumentRecord {
	if found == nil {
		return nil
	}
	record := detach.DocumentRecord(*found)
	return &record
}

func cloneStoredDocument(found *durable.StoredDocument) *durable.StoredDocument {
	if found == nil {
		return nil
	}
	return &durable.StoredDocument{
		Record: detach.DocumentRecord(found.Record), Version: found.Version,
		Value: detach.Object(found.Value), DeltasSinceBase: found.DeltasSinceBase,
	}
}

func cloneCursor(cursor *durable.Cursor) *durable.Cursor {
	if cursor == nil {
		return nil
	}
	copied := make(durable.Cursor, len(*cursor))
	for key, value := range *cursor {
		copied[key] = detach.JSON(value)
	}
	return &copied
}

func clonePage[T any](found durable.Page[T, durable.Cursor], one func(T) T) durable.Page[T, durable.Cursor] {
	copied := durable.Page[T, durable.Cursor]{Next: cloneCursor(found.Next)}
	if found.Items != nil {
		copied.Items = make([]T, len(found.Items))
		for index, item := range found.Items {
			copied.Items[index] = one(item)
		}
	}
	return copied
}

func cloneEntryPage(found durable.Page[durable.EntryRecord, durable.Cursor]) durable.Page[durable.EntryRecord, durable.Cursor] {
	return clonePage(found, detach.Entry)
}

func cloneConversationPage(found durable.Page[durable.ConversationRecord, durable.Cursor]) durable.Page[durable.ConversationRecord, durable.Cursor] {
	return clonePage(found, detach.Conversation)
}

func cloneTaskPage(found durable.Page[storedTask, durable.Cursor]) durable.Page[storedTask, durable.Cursor] {
	return clonePage(found, detach.Task)
}

func cloneSubmissionPage(found durable.Page[durable.SubmissionRecord, durable.Cursor]) durable.Page[durable.SubmissionRecord, durable.Cursor] {
	return clonePage(found, detach.Submission)
}

func cloneDocumentPage(found durable.Page[durable.DocumentRecord, durable.Cursor]) durable.Page[durable.DocumentRecord, durable.Cursor] {
	return clonePage(found, detach.DocumentRecord)
}

// Sizes: an estimate of the memory a kept result holds, a little over what it is, so the budget bounds the heap.

const (
	itemOverhead   = 256
	recordOverhead = 128
)

func sizeEntryAt(found *durable.EntryAt) int64 {
	if found == nil {
		return itemOverhead
	}
	return itemOverhead + sizeEntry(&found.Entry)
}

func sizeEntryRecord(found *durable.EntryRecord) int64 {
	if found == nil {
		return itemOverhead
	}
	return itemOverhead + sizeEntry(found)
}

func sizeEntry(record *durable.EntryRecord) int64 {
	size := int64(recordOverhead+len(record.Kind)) + sizeJSON(record.Data)
	if len(record.Model) > 0 {
		size += sizeReflect(reflect.ValueOf(record.Model), 0)
	}
	if len(record.Edits) > 0 {
		size += sizeReflect(reflect.ValueOf(record.Edits), 0)
	}
	return size
}

func sizeConversationRecord(*durable.ConversationRecord) int64 { return itemOverhead + recordOverhead }

func sizeTaskRecord(found *storedTask) int64 {
	if found == nil {
		return itemOverhead
	}
	return itemOverhead + sizeTask(found)
}

func sizeTask(record *storedTask) int64 {
	return 3*recordOverhead + int64(len(record.Kind)) + sizeJSON(record.Input) + sizeReflect(reflect.ValueOf(&record.State), 0) +
		sizeReflect(reflect.ValueOf(record.Memos), 0) + sizeReflect(reflect.ValueOf(record.Trace), 0) + sizeReflect(reflect.ValueOf(record.Links), 0)
}

func sizeSubmissionRecord(found *durable.SubmissionRecord) int64 {
	if found == nil {
		return itemOverhead
	}
	return itemOverhead + sizeSubmission(found)
}

func sizeSubmission(record *durable.SubmissionRecord) int64 {
	size := int64(2*recordOverhead) + sizeJSON(record.Detail) + sizeReflect(reflect.ValueOf(record.Trace), 0)
	if record.RequestId != nil {
		size += int64(len(*record.RequestId))
	}
	if record.Reason != nil {
		size += int64(len(*record.Reason))
	}
	return size
}

func sizeDocumentRecord(found *durable.DocumentRecord) int64 {
	if found == nil {
		return itemOverhead
	}
	return itemOverhead + sizeDocument(found)
}

func sizeDocument(record *durable.DocumentRecord) int64 {
	size := int64(recordOverhead + len(record.Kind))
	if record.Key != nil {
		size += int64(len(*record.Key))
	}
	return size
}

func sizeStoredDocument(found *durable.StoredDocument) int64 {
	if found == nil {
		return itemOverhead
	}
	return itemOverhead + sizeDocument(&found.Record) + sizeJSON(found.Value)
}

func sizePage[T any](found durable.Page[T, durable.Cursor], one func(T) int64) int64 {
	size := int64(itemOverhead)
	for _, item := range found.Items {
		size += one(item)
	}
	if found.Next != nil {
		size += 64 + int64(len(*found.Next))*96
	}
	return size
}

func sizeEntryPage(found durable.Page[durable.EntryRecord, durable.Cursor]) int64 {
	return sizePage(found, func(record durable.EntryRecord) int64 { return sizeEntry(&record) })
}

func sizeConversationPage(found durable.Page[durable.ConversationRecord, durable.Cursor]) int64 {
	return sizePage(found, func(durable.ConversationRecord) int64 { return recordOverhead })
}

func sizeTaskPage(found durable.Page[storedTask, durable.Cursor]) int64 {
	return sizePage(found, func(record storedTask) int64 { return sizeTask(&record) })
}

func sizeSubmissionPage(found durable.Page[durable.SubmissionRecord, durable.Cursor]) int64 {
	return sizePage(found, func(record durable.SubmissionRecord) int64 { return sizeSubmission(&record) })
}

func sizeDocumentPage(found durable.Page[durable.DocumentRecord, durable.Cursor]) int64 {
	return sizePage(found, func(record durable.DocumentRecord) int64 { return sizeDocument(&record) })
}

// sizeJSON is the estimated heap of a JSON value.
func sizeJSON(value durable.JsonValue) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case bool, float64, int, int64:
		return 16
	case string:
		return 16 + int64(len(typed))
	case []any:
		size := int64(24)
		for _, item := range typed {
			size += 16 + sizeJSON(item)
		}
		return size
	case map[string]any:
		size := int64(48)
		for key, item := range typed {
			size += 80 + int64(len(key)) + sizeJSON(item)
		}
		return size
	case *delta.JsonObject:
		if typed == nil {
			return 8
		}
		size := int64(64)
		for key, item := range typed.All() {
			size += 96 + int64(len(key)) + sizeJSON(item)
		}
		return size
	default:
		return sizeReflect(reflect.ValueOf(value), 0)
	}
}

// maxSizeDepth ends the walk of a value that is not a tree.
const maxSizeDepth = 64

// sizeReflect is the estimated heap a value holds, beyond the memory of the value itself when it is a field of a struct.
func sizeReflect(value reflect.Value, depth int) int64 {
	if !value.IsValid() || depth > maxSizeDepth {
		return 0
	}
	switch value.Kind() {
	case reflect.String:
		return int64(value.Len())
	case reflect.Pointer:
		if value.IsNil() {
			return 0
		}
		return int64(value.Type().Elem().Size()) + sizeReflect(value.Elem(), depth+1)
	case reflect.Interface:
		if value.IsNil() {
			return 0
		}
		return 16 + sizeReflect(value.Elem(), depth+1)
	case reflect.Slice:
		size := int64(value.Len()) * int64(value.Type().Elem().Size())
		if value.Type().Elem().Kind() < reflect.Array {
			return size
		}
		for index := range value.Len() {
			size += sizeReflect(value.Index(index), depth+1)
		}
		return size
	case reflect.Array:
		var size int64
		for index := range value.Len() {
			size += sizeReflect(value.Index(index), depth+1)
		}
		return size
	case reflect.Map:
		size := int64(48)
		iterator := value.MapRange()
		for iterator.Next() {
			size += 64 + int64(iterator.Key().Type().Size()) + int64(iterator.Value().Type().Size()) +
				sizeReflect(iterator.Key(), depth+1) + sizeReflect(iterator.Value(), depth+1)
		}
		return size
	case reflect.Struct:
		var size int64
		for index := range value.NumField() {
			size += sizeReflect(value.Field(index), depth+1)
		}
		return size
	default:
		return 0
	}
}
