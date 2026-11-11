package sqlite_test

// pi: packages/durable/src/storage/sqlite/commit.ts

// Ports packages/durable/test/sqlite-commit.test.ts: SqliteStorage.Commit over its batch path, random batch calls compared
// against the memory reference storage, and the statement count of a call, which must not grow with its batches.

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// openCommitDatabase is a SQLite database in a fresh directory.
func openCommitDatabase(t *testing.T) sqlite.SqliteDatabase {
	t.Helper()
	return must(node.OpenNodeSqliteDatabase(filepath.Join(t.TempDir(), "storage.sqlite"), node.NodeSqliteDatabaseOptions{}))
}

func openCommitStorage(t *testing.T, db sqlite.SqliteDatabase) *sqlite.SqliteStorage {
	t.Helper()
	opened := must(sqlite.Open(db))
	t.Cleanup(func() { _ = opened.Close(testContext) })
	return opened
}

// rng is deterministic pseudo-random numbers (mulberry32), so a failing seed reproduces.
type rng struct{ state uint32 }

func (r *rng) next() float64 {
	r.state += 0x6d2b79f5
	t := r.state
	t = imul(t^(t>>15), t|1)
	t ^= t + imul(t^(t>>7), t|61)
	return float64((t^(t>>14))>>0) / 4294967296
}

func imul(a, b uint32) uint32 { return a * b }

func (r *rng) int(max int) int       { return int(math.Floor(r.next() * float64(max))) }
func (r *rng) chance(p float64) bool { return r.next() < p }

func pick[T any](r *rng, values []T) T { return values[r.int(len(values))] }

// callSet is the documents the current call created or changed with the version it gave them last, in insertion order (a
// JavaScript Map).
type callSet struct {
	ids      []durable.DocumentId
	versions map[durable.DocumentId]int
}

func (set *callSet) put(id durable.DocumentId, version int) {
	if _, ok := set.versions[id]; !ok {
		set.ids = append(set.ids, id)
	}
	set.versions[id] = version
}

func (set *callSet) remove(id durable.DocumentId) {
	if _, ok := set.versions[id]; ok {
		delete(set.versions, id)
		set.ids = slices.DeleteFunc(set.ids, func(other durable.DocumentId) bool { return other == id })
	}
}

func (set *callSet) clear() { set.ids, set.versions = nil, map[durable.DocumentId]int{} }

type worldDocument struct {
	id     durable.DocumentId
	record durable.DocumentCreate
}

// world is what the generator has proposed so far: the pools later writes reuse, valid or not.
type world struct {
	nextId        int64
	conversations []durable.ConversationId
	entries       []durable.EntryId
	tasks         []durable.TaskId
	submissions   []durable.SubmissionId
	documents     []worldDocument
	seqs          []durable.Seq
	// call holds the documents the current call created or changed, and retired the addresses it retired: later batches of
	// the call target them, so batches depend on the ones before them in the same call.
	call    callSet
	retired []durable.DocumentCreate
}

func (w *world) fresh() int64 {
	id := w.nextId
	w.nextId++
	return id
}

// anyId is any ID the world knows, of any table: a write reusing it is often refused.
func (w *world) anyId(r *rng) int64 {
	var pools [][]int64
	add := func(pool []int64) {
		if len(pool) > 0 {
			pools = append(pools, pool)
		}
	}
	add(mapIds(w.conversations))
	add(mapIds(w.entries))
	add(mapIds(w.tasks))
	add(mapIds(w.submissions))
	add(mapIds(mapSlice(w.documents, func(d worldDocument) durable.DocumentId { return d.id })))
	if len(pools) == 0 {
		return w.fresh()
	}
	return pick(r, pick(r, pools))
}

func mapIds[I ~int64](ids []I) []int64 {
	out := make([]int64, len(ids))
	for index, id := range ids {
		out[index] = int64(id)
	}
	return out
}

func mapSlice[T, U any](values []T, f func(T) U) []U {
	out := make([]U, len(values))
	for index, value := range values {
		out[index] = f(value)
	}
	return out
}

func randomAddress(w *world, r *rng, id durable.DocumentId) durable.DocumentCreate {
	kind := pick(r, []string{"a", "b"})
	var key *string
	if !r.chance(0.5) {
		key = new(pick(r, []string{"x", "y"}))
	}
	base := durable.DocumentCreate{Id: id, Kind: kind, Key: key}
	scope := r.int(3)
	if scope == 0 {
		base.Scope = durable.DocumentRecordScope{Kind: durable.ScopeSession}
		return base
	}
	if scope == 1 && len(w.tasks) > 0 {
		base.Scope = durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: pick(r, w.tasks)}
		return base
	}
	conversationId := pick(r, w.conversations)
	base.Scope = durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationId}
	if r.chance(0.5) {
		base.History, base.Fork = durable.HistoryLatest, durable.ForkCurrent
	} else {
		base.History, base.Fork = durable.HistoryRewindable, durable.ForkAsOf
	}
	return base
}

func randomContent(r *rng, version int) durable.DocumentContent {
	if r.chance(0.4) {
		contentVersion := version
		if !r.chance(0.8) {
			contentVersion = version + 1
		}
		n, at := r.int(100), r.int(5)
		return durable.DocumentContent{Kind: durable.ContentBase, Version: contentVersion, Value: chorddelta.JsonObjectOf("n", float64(n), "at", float64(at))}
	}
	contentVersion := version
	if !r.chance(0.85) {
		contentVersion = version + 1
	}
	return durable.DocumentContent{Kind: durable.ContentDelta, Version: contentVersion, Ops: []durable.Op{{"s", []any{"n"}, float64(r.int(100))}}}
}

var commitTaskStates = []func() durable.TaskState[durable.JsonValue, durable.JsonValue]{
	func() durable.TaskState[durable.JsonValue, durable.JsonValue] {
		var phase durable.JsonValue = map[string]any{"phase": "ready"}
		return durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskPending, Checkpoint: &phase}
	},
	func() durable.TaskState[durable.JsonValue, durable.JsonValue] {
		var phase durable.JsonValue = map[string]any{"phase": "ready"}
		return durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskRunning, Checkpoint: &phase}
	},
	func() durable.TaskState[durable.JsonValue, durable.JsonValue] {
		var result durable.JsonValue = float64(1)
		return durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}}
	},
}

func documentOf(reference *storage.MemoryStorage, id durable.DocumentId) *durable.StoredDocument {
	return must(reference.Document(testContext, id, durable.CurrentPoint))
}

// pickDocument is one of documents, mostly a live one (as reference stores it).
func pickDocument[T any](r *rng, reference *storage.MemoryStorage, documents []T, idOf func(T) durable.DocumentId) T {
	if r.chance(0.15) {
		return pick(r, documents)
	}
	var live []T
	for _, document := range documents {
		if documentOf(reference, idOf(document)) != nil {
			live = append(live, document)
		}
	}
	if len(live) > 0 {
		return pick(r, live)
	}
	return pick(r, documents)
}

func worldDocumentId(d worldDocument) durable.DocumentId { return d.id }

// randomWrite is one random write, biased toward valid ones, with reference telling which documents are live.
func randomWrite(w *world, r *rng, reference *storage.MemoryStorage) durable.StorageWrite {
	switch r.int(9) {
	case 0:
		var id durable.ConversationId
		if r.chance(0.85) {
			id = durable.IdFromNumber[durable.ConversationId](w.fresh())
		} else {
			id = durable.IdFromNumber[durable.ConversationId](w.anyId(r))
		}
		w.conversations = append(w.conversations, id)
		return durable.ConversationWrite{Value: durable.ConversationRecord{Id: id}}
	case 1, 2:
		var id durable.EntryId
		if r.chance(0.9) {
			id = durable.IdFromNumber[durable.EntryId](w.fresh())
		} else {
			id = durable.IdFromNumber[durable.EntryId](w.anyId(r))
		}
		w.entries = append(w.entries, id)
		var head *durable.EntryId
		if r.chance(0.2) && len(w.entries) > 0 {
			head = new(pick(r, w.entries))
		}
		conversationId := pick(r, w.conversations)
		return durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: conversationId, Kind: "note", Data: float64(r.int(1000)), Head: head}}
	case 3:
		var id durable.TaskId
		switch {
		case len(w.tasks) > 0 && r.chance(0.6):
			id = pick(r, w.tasks)
		case r.chance(0.9):
			id = durable.IdFromNumber[durable.TaskId](w.fresh())
		default:
			id = durable.IdFromNumber[durable.TaskId](w.anyId(r))
		}
		w.tasks = append(w.tasks, id)
		task := storedTask{Id: id, ConversationId: pick(r, w.conversations), Kind: pick(r, []string{"t.one", "t.two"}), Version: 1}
		task.Input = map[string]any{"value": float64(r.int(10))}
		task.State = pick(r, commitTaskStates)()
		task.Background = r.chance(0.5)
		task.AbortRequested = r.chance(0.2)
		return durable.TaskWrite{Value: task}
	case 4:
		var id durable.SubmissionId
		switch {
		case len(w.submissions) > 0 && r.chance(0.5):
			id = pick(r, w.submissions)
		case r.chance(0.9):
			id = durable.IdFromNumber[durable.SubmissionId](w.fresh())
		default:
			id = durable.IdFromNumber[durable.SubmissionId](w.anyId(r))
		}
		w.submissions = append(w.submissions, id)
		// One request ID per submission: storages differ on duplicates, which the Session never writes.
		var requestId *string
		if r.chance(0.5) {
			requestId = new("r" + strconv.FormatInt(int64(id), 10))
		}
		return durable.SubmissionWrite{Value: durable.SubmissionRecord{Id: id, ConversationId: pick(r, w.conversations), Type: durable.SubmissionTypeInput, Status: durable.SubmissionQueued, RequestId: requestId}}
	case 5:
		var id durable.DocumentId
		if r.chance(0.9) {
			id = durable.IdFromNumber[durable.DocumentId](w.fresh())
		} else {
			id = durable.IdFromNumber[durable.DocumentId](w.anyId(r))
		}
		// Often at an address the call retired or another document has: the address's live count decides.
		var record durable.DocumentCreate
		switch {
		case len(w.retired) > 0 && r.chance(0.5):
			record = pick(r, w.retired)
			record.Id = id
		case len(w.documents) > 0 && r.chance(0.3):
			record = pick(r, w.documents).record
			record.Id = id
		default:
			record = randomAddress(w, r, id)
		}
		w.documents = append(w.documents, worldDocument{id, record})
		w.call.put(id, 1)
		return durable.DocumentCreateWrite{Record: record, Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("n", float64(0), "at", float64(0))}}
	case 6, 7:
		if len(w.documents) == 0 {
			return randomWrite(w, r, reference)
		}
		var id durable.DocumentId
		if len(w.call.ids) > 0 && r.chance(0.4) {
			id = pick(r, w.call.ids)
		} else {
			id = pickDocument(r, reference, w.documents, worldDocumentId).id
		}
		if r.chance(0.25) {
			for _, document := range w.documents {
				if document.id == id {
					w.retired = append(w.retired, document.record)
					break
				}
			}
			w.call.remove(id)
			return durable.DocumentRetireWrite{Id: id}
		}
		version, ok := w.call.versions[id]
		if !ok {
			version = 1
			if stored := documentOf(reference, id); stored != nil {
				version = stored.Version
			}
		}
		content := randomContent(r, version)
		w.call.put(id, content.Version)
		return durable.DocumentChangeWrite{Id: id, Content: content}
	default:
		var sources []worldDocument
		for _, document := range w.documents {
			if document.record.Scope.Kind == durable.ScopeConversation {
				sources = append(sources, document)
			}
		}
		if len(sources) == 0 {
			return randomWrite(w, r, reference)
		}
		source := pickDocument(r, reference, sources, worldDocumentId)
		id := durable.IdFromNumber[durable.DocumentId](w.fresh())
		conversationId := pick(r, w.conversations)
		var record durable.DocumentCreate
		if r.chance(0.85) {
			record = source.record
			record.Id = id
			record.Scope = durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationId}
		} else {
			record = randomAddress(w, r, id)
		}
		w.documents = append(w.documents, worldDocument{id, record})
		historical := 0.05
		if source.record.History == durable.HistoryRewindable {
			historical = 0.4
		}
		at := durable.CurrentPoint
		if len(w.seqs) > 0 && r.chance(historical) {
			at = durable.DocumentPoint{Seq: pick(r, w.seqs)}
		}
		return durable.DocumentCopyWrite{Record: record, Source: durable.DocumentCopySource{Id: source.id, At: at}}
	}
}

// replacement is a document's replacement in one batch: a new incarnation at its address and its retirement, in either
// order, so the address's current incarnation changes within the batch.
func replacement(w *world, r *rng, reference *storage.MemoryStorage) []durable.StorageWrite {
	if len(w.documents) == 0 {
		return nil
	}
	target := pickDocument(r, reference, w.documents, worldDocumentId)
	id := durable.IdFromNumber[durable.DocumentId](w.fresh())
	record := target.record
	record.Id = id
	w.documents = append(w.documents, worldDocument{id, record})
	w.call.put(id, 1)
	w.call.remove(target.id)
	w.retired = append(w.retired, target.record)
	pair := []durable.StorageWrite{
		durable.DocumentCreateWrite{Record: record, Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: chorddelta.JsonObjectOf("n", float64(0), "at", float64(0))}},
		durable.DocumentRetireWrite{Id: target.id},
	}
	if r.chance(0.5) {
		return pair
	}
	slices.Reverse(pair)
	return pair
}

// settle is a read's result, or its error's message: both storages must agree on either.
func settle[T any](read func() (T, error)) any {
	value, err := read()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"value": value}
}

// snapshot is everything a storage exposes about the world's records, at the current point and at seqs.
func snapshot(s durable.Storage, w *world, seqs []durable.Seq) any {
	const all = 10_000
	unique := func(ids []int64) []int64 {
		seen := map[int64]bool{}
		var out []int64
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		return out
	}
	documentIds := unique(mapIds(mapSlice(w.documents, worldDocumentId)))
	scopes := []durable.DocumentRecordScope{{Kind: durable.ScopeSession}}
	for _, conversationId := range w.conversations {
		scopes = append(scopes, durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationId})
	}
	for _, taskId := range w.tasks {
		scopes = append(scopes, durable.DocumentRecordScope{Kind: durable.ScopeTask, TaskId: taskId})
	}
	out := map[string]any{}
	out["conversations"] = must(s.ScanConversations(testContext, durable.ConversationQuery{}, all, nil)).Items
	var entries []any
	for _, id := range unique(mapIds(w.entries)) {
		entries = append(entries, settle(func() (*durable.EntryAt, error) { return s.Entry(testContext, durable.EntryId(id)) }))
	}
	out["entries"] = entries
	var transcripts []any
	ascending := durable.ScanAscending
	for _, id := range unique(mapIds(w.conversations)) {
		transcripts = append(transcripts, settle(func() (durable.Page[durable.EntryRecord, durable.Cursor], error) {
			return s.ScanEntries(testContext, durable.EntryQuery{ConversationId: durable.ConversationId(id), Order: &ascending}, all, nil)
		}))
	}
	out["transcripts"] = transcripts
	out["tasks"] = must(s.ScanTasks(testContext, durable.TaskQuery{}, all, nil)).Items
	out["submissions"] = must(s.ScanSubmissions(testContext, durable.SubmissionQuery{}, all, nil)).Items
	var requests []any
	for _, conversationId := range unique(mapIds(w.conversations)) {
		for _, id := range unique(mapIds(w.submissions)) {
			requests = append(requests, settle(func() (*durable.SubmissionRecord, error) {
				return s.SubmissionByRequest(testContext, durable.ConversationId(conversationId), "r"+strconv.FormatInt(id, 10))
			}))
		}
	}
	out["requests"] = requests
	var documents []any
	for _, id := range documentIds {
		for _, at := range append([]durable.DocumentPoint{durable.CurrentPoint}, mapSlice(seqs, func(seq durable.Seq) durable.DocumentPoint { return durable.DocumentPoint{Seq: seq} })...) {
			documents = append(documents, settle(func() (*durable.StoredDocument, error) { return s.Document(testContext, durable.DocumentId(id), at) }))
		}
	}
	out["documents"] = documents
	var scans []any
	for _, scope := range scopes {
		scans = append(scans, settle(func() (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
			return s.ScanDocuments(testContext, durable.DocumentQuery{Scope: scope, At: durable.CurrentPoint}, all, nil)
		}))
	}
	out["scopes"] = scans
	var addresses []any
	for _, document := range w.documents {
		address := durable.DocumentAddress{Kind: document.record.Kind, Scope: document.record.Scope, Key: document.record.Key}
		addresses = append(addresses, settle(func() (*durable.DocumentRecord, error) {
			return s.FindDocument(testContext, address, durable.CurrentPoint)
		}))
	}
	out["addresses"] = addresses
	return out
}

// dumpTables is every row of a SQLite storage's tables, in key order: also what reads do not show, such as the revisions
// a current-only document dropped.
func dumpTables(t *testing.T, db sqlite.SqliteDatabase) map[string]any {
	t.Helper()
	dump := map[string]any{}
	for _, table := range []string{"durable_metadata", "record_ids", "conversations", "entries", "tasks", "submissions", "documents", "document_revisions"} {
		dump[table] = must(db.All("SELECT * FROM " + table + " ORDER BY 1, 2"))
	}
	return dump
}

// staleRevisions are the revisions a current-only document should have dropped, which no read shows: one older than its
// latest base, or any once it is retired.
func staleRevisions(t *testing.T, db sqlite.SqliteDatabase) []sqlite.SqliteRow {
	t.Helper()
	return must(db.All(`SELECT revision.document_id, revision.seq FROM document_revisions AS revision
			JOIN documents AS document ON document.id = revision.document_id
			WHERE (document.scope_kind != 'conversation' OR json_extract(document.record, '$.history') = 'latest')
			AND (document.retired_at IS NOT NULL OR revision.seq < (
				SELECT MAX(seq) FROM document_revisions WHERE document_id = revision.document_id AND kind = 'base'
			))`))
}

// commitEach commits batches one call each until one is refused: what one call of all of them must store.
func commitEach(t *testing.T, s durable.Storage, batches [][]durable.StorageWrite) {
	t.Helper()
	for _, batch := range batches {
		if must(s.Commit(testContext, [][]durable.StorageWrite{batch}, func(int, durable.Seq) {})).Stored == 0 {
			return
		}
	}
}

type callOutcome struct {
	Stored   int
	Refused  bool
	Receipts [][2]int64
}

// commitCall commits batches and returns what the call reported: stored count, refused or not, and the receipts.
func commitCall(t *testing.T, s durable.Storage, batches [][]durable.StorageWrite) callOutcome {
	t.Helper()
	var receipts [][2]int64
	result := must(s.Commit(testContext, batches, func(index int, seq durable.Seq) { receipts = append(receipts, [2]int64{int64(index), int64(seq)}) }))
	return callOutcome{result.Stored, result.Refused != nil, receipts}
}

func TestSqliteStorageBatchCommitsAgainstMemoryReference(t *testing.T) {
	for _, seed := range []uint32{1, 2, 3, 4, 5, 6} {
		t.Run(fmt.Sprintf("agrees with MemoryStorage on random batch calls (seed %d)", seed), func(t *testing.T) {
			r := &rng{state: seed}
			reference := storage.NewMemoryStorage()
			t.Cleanup(func() { _ = reference.Close(testContext) })
			groupedDb := openCommitDatabase(t)
			sqliteStorage := openCommitStorage(t, groupedDb)
			// The same batches, one call each: a group must leave exactly the rows its batches leave alone.
			singleDb := openCommitDatabase(t)
			single := openCommitStorage(t, singleDb)
			w := &world{nextId: 2, conversations: []durable.ConversationId{durable.ROOT_CONVERSATION_ID}, call: callSet{versions: map[durable.DocumentId]int{}}}
			root := [][]durable.StorageWrite{{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}}
			expectEqual(t, commitCall(t, sqliteStorage, root), commitCall(t, reference, root))
			commitEach(t, single, root)
			refusals, prefixes, groups := 0, 0, 0
			for round := range 120 {
				var batches [][]durable.StorageWrite
				w.call.clear()
				w.retired = nil
				count := 1 + r.int(6)
				for range count {
					var writes []durable.StorageWrite
					size := 1 + r.int(4)
					for range size {
						writes = append(writes, randomWrite(w, r, reference))
					}
					if r.chance(0.2) {
						writes = append(writes, replacement(w, r, reference)...)
					}
					batches = append(batches, writes)
				}
				expected := commitCall(t, reference, batches)
				actual := commitCall(t, sqliteStorage, batches)
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("round %d: %+v, want %+v for %s", round, actual, expected, durabletest.Describe(batches))
				}
				commitEach(t, single, batches)
				expectEqual(t, dumpTables(t, groupedDb), dumpTables(t, singleDb))
				if stale := staleRevisions(t, groupedDb); len(stale) > 0 {
					t.Fatalf("round %d revisions: %v", round, stale)
				}
				if expected.Refused {
					refusals++
					if expected.Stored > 0 {
						prefixes++
					}
				} else if len(batches) > 1 {
					groups++
				}
				for _, receipt := range expected.Receipts {
					w.seqs = append(w.seqs, durable.Seq(receipt[1]))
				}
				var seqs []durable.Seq
				if len(w.seqs) > 0 {
					seqs = []durable.Seq{w.seqs[r.int(len(w.seqs))]}
				}
				expectEqual(t, snapshot(sqliteStorage, w, seqs), snapshot(reference, w, seqs))
			}
			expectEqual(t, snapshot(sqliteStorage, w, w.seqs), snapshot(reference, w, w.seqs))
			// The generator must exercise every path: refusals, refused prefixes, accepted groups.
			if refusals <= 10 || prefixes <= 10 || groups <= 10 {
				t.Fatalf("refusals = %d, prefixes = %d, groups = %d, want each above 10", refusals, prefixes, groups)
			}
		})
	}
}

// ran is a statement a transaction ran, with its parameters.
type ran struct {
	sql    string
	params []sqlite.SqliteValue
}

// recordingDatabase is a database recording the statements its transactions run.
type recordingDatabase struct {
	sqlite.SqliteDatabase
	ran *[]ran
}

func (database recordingDatabase) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	return database.SqliteDatabase.Transaction(func(transaction sqlite.SqliteExecutor) error {
		return callback(recordingExecutor{SqliteExecutor: transaction, ran: database.ran})
	})
}

type recordingExecutor struct {
	sqlite.SqliteExecutor
	ran *[]ran
}

func (executor recordingExecutor) record(sql string, params []sqlite.SqliteValue) {
	*executor.ran = append(*executor.ran, ran{sql, params})
}

func (executor recordingExecutor) Exec(sql string) error {
	executor.record(sql, nil)
	return executor.SqliteExecutor.Exec(sql)
}

func (executor recordingExecutor) Run(sql string, params ...sqlite.SqliteValue) error {
	executor.record(sql, params)
	return executor.SqliteExecutor.Run(sql, params...)
}

func (executor recordingExecutor) RunCounted(sql string, params ...sqlite.SqliteValue) (int64, error) {
	executor.record(sql, params)
	return executor.SqliteExecutor.RunCounted(sql, params...)
}

func (executor recordingExecutor) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	executor.record(sql, params)
	return executor.SqliteExecutor.Get(sql, params...)
}

func (executor recordingExecutor) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	executor.record(sql, params)
	return executor.SqliteExecutor.All(sql, params...)
}

// groupOf is one call of count batches, each a document delta and an entry (as a Session turn writes), after a call that
// creates the documents: the statements of the second call, and the database.
func groupOf(t *testing.T, count int, data durable.JsonValue) ([]ran, sqlite.SqliteDatabase) {
	t.Helper()
	database := openCommitDatabase(t)
	var statements []ran
	s := openCommitStorage(t, recordingDatabase{SqliteDatabase: database, ran: &statements})
	setup := []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}
	var documents []durable.DocumentId
	for range count {
		id := mint[durable.DocumentId](t, s)
		documents = append(documents, id)
		setup = append(setup, durable.DocumentCreateWrite{
			Record:  durable.DocumentCreate{Id: id, Kind: "counter", Scope: sessionScope, Key: new(strconv.FormatInt(int64(id), 10))},
			Content: base(chorddelta.JsonObjectOf("n", float64(0))),
		})
	}
	commit(t, s, setup...)
	var batches [][]durable.StorageWrite
	for _, id := range documents {
		batches = append(batches, []durable.StorageWrite{
			durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"s", []any{"n"}, float64(1)})},
			durable.EntryWrite{Value: durable.EntryRecord{Id: mint[durable.EntryId](t, s), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "note", Data: data}},
		})
	}
	before := len(statements)
	if stored := must(s.Commit(testContext, batches, func(int, durable.Seq) {})).Stored; stored != count {
		t.Fatalf("stored = %d, want %d", stored, count)
	}
	return slices.Clone(statements[before:]), database
}

func entryInserts(statements []ran) []ran {
	return slices.DeleteFunc(slices.Clone(statements), func(statement ran) bool { return !strings.HasPrefix(statement.sql, "INSERT INTO entries") })
}

// valuesChunks is the parameters of each statement that inserts rows of width cells: VALUES of 1, 2, 4 ... rows within 100
// parameters, the largest first.
func valuesChunks(rows, width int) []int {
	size := 1
	for size*2*width <= 100 {
		size *= 2
	}
	var chunks []int
	for rows > 0 {
		for size > rows {
			size /= 2
		}
		chunks = append(chunks, size*width)
		rows -= size
	}
	return chunks
}

// insertsInto is the parameters of each statement of statements that inserts into table, in order.
func insertsInto(statements []ran, table string) []int {
	var counts []int
	for _, statement := range statements {
		if strings.HasPrefix(statement.sql, "INSERT INTO "+table+" ") {
			counts = append(counts, len(statement.params))
		}
	}
	return counts
}

func TestSqliteStorageBatchCommitStatements(t *testing.T) {
	t.Run("runs eight statements for one batch and binds the rows of a group in VALUES of 1, 2, 4 ... rows", func(t *testing.T) {
		// Metadata read, three check reads (IDs, documents, latest versions), three inserts, metadata write.
		one, _ := groupOf(t, 1, float64(0))
		if len(one) != 8 {
			t.Fatalf("one batch ran %d statements, want 8", len(one))
		}
		// Pi inserts the rows of little text from one JSON array parameter, eight statements for any group; the stored rows
		// are the same, written here by statements of 1, 2, 4 ... rows of bound cells (insert_values_differential_test.go).
		group, database := groupOf(t, 150, float64(0))
		for table, width := range map[string]int{"record_ids": 2, "entries": 5, "document_revisions": 5} {
			if got, want := insertsInto(group, table), valuesChunks(150, width); !slices.Equal(got, want) {
				t.Fatalf("parameters per %s insert = %v, want %v", table, got, want)
			}
		}
		for _, statement := range group {
			if strings.HasPrefix(statement.sql, "INSERT INTO") && strings.Contains(statement.sql, "json_each") {
				t.Fatalf("an insert reads a JSON array: %s", statement.sql)
			}
		}
		if got := must(database.Get("SELECT count(*) AS n FROM entries")); got["n"] != int64(150) {
			t.Fatalf("%v entries stored, want 150", got["n"])
		}
		// One SQL text per table and size, a handful per table whatever the group: 150 rows are 9 statements of 16, one of 4, one of 2.
		texts := map[string]bool{}
		for _, statement := range entryInserts(group) {
			texts[statement.sql] = true
		}
		if len(texts) != 3 {
			t.Fatalf("%d SQL texts for the entry inserts of a group of 150, want 3", len(texts))
		}
	})

	t.Run("binds the rows of little text column by column, whatever the characters they hold", func(t *testing.T) {
		// 100 entries of 7,000 characters: 700 K characters together, which Pi's insert sends as two JSON arrays.
		statements, database := groupOf(t, 100, strings.Repeat("x", 7_000))
		if got, want := insertsInto(statements, "entries"), valuesChunks(100, 5); !slices.Equal(got, want) {
			t.Fatalf("parameters per entry insert = %v, want %v", got, want)
		}
		for _, row := range must(database.All("SELECT record FROM entries")) {
			var record struct{ Data string }
			if err := json.Unmarshal([]byte(row["record"].(string)), &record); err != nil || len(record.Data) != 7_000 {
				t.Fatalf("stored entry data is %d characters, want 7000: %v", len(record.Data), err)
			}
		}
	})

	t.Run("binds rows with much text column by column, 1, 2, 4 ... rows per statement within 100 parameters", func(t *testing.T) {
		// Records full of quotes, which a JSON array would escape a second time: 2 of 400,000 quotes, then 19 of 10,000.
		quotes := strings.Repeat(`"`, 400_000)
		statements, database := groupOf(t, 2, quotes)
		counts := func(statements []ran) []int {
			return mapSlice(entryInserts(statements), func(statement ran) int { return len(statement.params) })
		}
		if got := counts(statements); !slices.Equal(got, []int{10}) {
			t.Fatalf("parameters per entry insert = %v, want [10]", got)
		}
		for _, row := range must(database.All("SELECT record FROM entries")) {
			var record struct{ Data string }
			if err := json.Unmarshal([]byte(row["record"].(string)), &record); err != nil || record.Data != quotes {
				t.Fatalf("stored entry data differs from the quotes: %v", err)
			}
		}
		// 19 rows of 5 cells: 16 + 2 + 1, the largest power of two within 100 parameters first.
		many, _ := groupOf(t, 19, strings.Repeat(`"`, 10_000))
		if got := counts(many); !slices.Equal(got, []int{80, 10, 5}) {
			t.Fatalf("parameters per entry insert = %v, want [80 10 5]", got)
		}
	})

	t.Run("reads and writes through indexes: no statement of a call scans a stored table", func(t *testing.T) {
		database := openCommitDatabase(t)
		var statements []ran
		s := openCommitStorage(t, recordingDatabase{SqliteDatabase: database, ran: &statements})
		conversationId := durable.ROOT_CONVERSATION_ID
		record := func(id durable.DocumentId) durable.DocumentCreate {
			return durable.DocumentCreate{Id: id, Kind: "plan", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationId}, History: durable.HistoryLatest, Fork: durable.ForkCurrent}
		}
		first, second := mint[durable.DocumentId](t, s), mint[durable.DocumentId](t, s)
		must(s.Commit(testContext, [][]durable.StorageWrite{
			{durable.ConversationWrite{Value: durable.ConversationRecord{Id: conversationId}}},
			{durable.DocumentCreateWrite{Record: record(first), Content: base(chorddelta.JsonObjectOf("n", float64(0)))}},
		}, func(int, durable.Seq) {}))
		before := len(statements)
		// Every read and write the batch path has: IDs, documents, versions, addresses, the upsert, revision drops.
		must(s.Commit(testContext, [][]durable.StorageWrite{
			{durable.DocumentChangeWrite{Id: first, Content: delta(durable.Op{"s", []any{"n"}, float64(1)})}},
			{
				durable.DocumentChangeWrite{Id: first, Content: base(chorddelta.JsonObjectOf("n", float64(2)))},
				durable.EntryWrite{Value: durable.EntryRecord{Id: mint[durable.EntryId](t, s), ConversationId: conversationId, Kind: "note"}},
			},
			{
				durable.DocumentRetireWrite{Id: first},
				durable.DocumentCreateWrite{Record: record(second), Content: base(chorddelta.JsonObjectOf())},
			},
		}, func(int, durable.Seq) {}))
		batched := slices.DeleteFunc(slices.Clone(statements[before:]), func(statement ran) bool {
			return !strings.Contains(statement.sql, "json_each") && !strings.HasPrefix(statement.sql, "INSERT INTO")
		})
		if len(batched) <= 5 {
			t.Fatalf("%d json_each and insert statements, want more than 5", len(batched))
		}
		for _, statement := range batched {
			for _, row := range must(database.All("EXPLAIN QUERY PLAN "+statement.sql, statement.params...)) {
				detail, _ := row["detail"].(string)
				if strings.HasPrefix(detail, "SCAN") && !strings.Contains(detail, "VIRTUAL TABLE") && !strings.Contains(detail, "VALUES CLAUSE") {
					t.Fatalf("%s\nscans: %s", statement.sql, detail)
				}
			}
		}
	})
}

func TestSqliteStorageBatchCommitRegressions(t *testing.T) {
	for _, order := range []string{"creation first", "retirement first"} {
		t.Run("refuses a third incarnation after a replacement in an earlier batch of the call ("+order+")", func(t *testing.T) {
			// Review finding: retiring A after creating B in one batch must leave B the address's current incarnation.
			var outcomes []durable.CommitResult
			for _, s := range []durable.Storage{storage.NewMemoryStorage(), openCommitStorage(t, openCommitDatabase(t))} {
				id := func(n int64) durable.DocumentId { return durable.DocumentId(n) }
				at := func(n int64) durable.DocumentCreate {
					return durable.DocumentCreate{Id: id(n), Kind: "slot", Scope: sessionScope}
				}
				create := func(n int64) durable.StorageWrite {
					return durable.DocumentCreateWrite{Record: at(n), Content: base(chorddelta.JsonObjectOf())}
				}
				must(s.Commit(testContext, [][]durable.StorageWrite{{create(10)}}, func(int, durable.Seq) {}))
				replace := []durable.StorageWrite{create(11), durable.DocumentRetireWrite{Id: id(10)}}
				if order == "retirement first" {
					slices.Reverse(replace)
				}
				outcomes = append(outcomes, must(s.Commit(testContext, [][]durable.StorageWrite{replace, {create(12)}}, func(int, durable.Seq) {})))
			}
			for _, outcome := range outcomes {
				if outcome.Stored != 1 {
					t.Fatalf("stored = %d, want 1", outcome.Stored)
				}
				expectError(t, outcome.Refused, "Document address already has a current incarnation")
			}
		})
	}

	t.Run("rejects the whole call as a storage failure when a stored document record cannot be parsed", func(t *testing.T) {
		database := openCommitDatabase(t)
		s := openCommitStorage(t, database)
		createRoot(t, s)
		id := mint[durable.DocumentId](t, s)
		commit(t, s, durable.DocumentCreateWrite{Record: durable.DocumentCreate{Id: id, Kind: "slot", Scope: sessionScope}, Content: base(chorddelta.JsonObjectOf())})
		mustDo(t, database.Run("UPDATE documents SET record = ? WHERE id = ?", "[1]", int64(id)))
		entryId := mint[durable.EntryId](t, s)
		var receipts []int
		_, err := s.Commit(testContext, [][]durable.StorageWrite{
			{durable.EntryWrite{Value: durable.EntryRecord{Id: entryId, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "note"}}},
			{durable.DocumentChangeWrite{Id: id, Content: delta(durable.Op{"s", []any{"n"}, float64(1)})}},
		}, func(index int, _ durable.Seq) { receipts = append(receipts, index) })
		if err == nil || len(receipts) != 0 {
			t.Fatalf("err = %v, receipts = %v, want a storage failure and no receipts", err, receipts)
		}
		if must(s.Entry(testContext, entryId)) != nil {
			t.Fatal("a batch of a failed call was stored")
		}
	})
}
