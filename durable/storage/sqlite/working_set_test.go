package sqlite_test

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/entryscan"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// The working set must answer exactly what ScanEntries answers, whatever it retains, through writes, forks, eviction,
// failed commits and changes made by another connection. ScanEntries reads the table, so it is the oracle.

func rangerOf(t *testing.T, storage *sqlite.SqliteStorage) entryscan.Ranger {
	t.Helper()
	ranger, ok := any(storage).(entryscan.Ranger)
	if !ok {
		t.Fatal("SqliteStorage does not serve entry ranges")
	}
	return ranger
}

// scanned is the oracle: ScanEntries pages, newest first, reversed to oldest first.
func scanned(t *testing.T, storage durable.Storage, query durable.EntryQuery) []durable.EntryRecord {
	t.Helper()
	var all []durable.EntryRecord
	var cursor durable.Cursor
	for {
		page := must(storage.ScanEntries(testContext, query, 3, cursor))
		all = append(all, page.Items...)
		if page.Next == nil {
			break
		}
		cursor = *page.Next
	}
	slices.Reverse(all)
	return all
}

func expectRange(t *testing.T, storage *sqlite.SqliteStorage, query durable.EntryQuery) {
	t.Helper()
	got := must(rangerOf(t, storage).VisibleEntryRange(testContext, query))
	want := scanned(t, storage, query)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("range %+v: got ids %v, want ids %v", query, entryIDs(got), entryIDs(want))
	}
}

func entryIDs(records []durable.EntryRecord) []int64 {
	ids := make([]int64, len(records))
	for index, record := range records {
		ids[index] = int64(record.Id)
	}
	return ids
}

func message(text string) []ai.Message {
	return []ai.Message{ai.UserMessage{Content: ai.UserText(text), Timestamp: 1}}
}

func entryOf(id durable.EntryId, conversation durable.ConversationId, text string) durable.EntryRecord {
	return durable.EntryRecord{
		Id: id, ConversationId: conversation, Kind: "message", Model: message(text),
		Data: map[string]any{"text": text, "n": float64(id)},
	}
}

func commitEntries(t *testing.T, storage *sqlite.SqliteStorage, records ...durable.EntryRecord) {
	t.Helper()
	writes := make([]durable.StorageWrite, len(records))
	for index, record := range records {
		writes[index] = durable.EntryWrite{Value: record}
	}
	must(storage.Commit(testContext, writes))
}

func workingSetStorage(t *testing.T, bytes int64) (*sqlite.SqliteStorage, string) {
	t.Helper()
	return createSqliteStorage(t, node.NodeSqliteStorageOptions{WorkingSetBytes: &bytes})
}

// forkedStore holds a root with entries 10..60 step 10, a child forked at 30 and a grandchild forked from the child.
func forkedStore(t *testing.T, bytes int64) (*sqlite.SqliteStorage, string) {
	storage, path := workingSetStorage(t, bytes)
	must(storage.Commit(testContext, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}},
	}))
	for id := durable.EntryId(10); id <= 60; id += 10 {
		commitEntries(t, storage, entryOf(id, 1, fmt.Sprint("root ", id)))
	}
	must(storage.Commit(testContext, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 2, Parent: &durable.ConversationParent{ConversationId: 1, At: 30}}},
	}))
	commitEntries(t, storage, entryOf(35, 2, "child 35"), entryOf(45, 2, "child 45"), entryOf(55, 2, "child 55"))
	must(storage.Commit(testContext, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: 3, Parent: &durable.ConversationParent{ConversationId: 2, At: 45}}},
	}))
	commitEntries(t, storage, entryOf(47, 3, "grand 47"), entryOf(48, 3, "grand 48"))
	return storage, path
}

func TestWorkingSetRangeMatchesScanEntries(t *testing.T) {
	for _, bytes := range []int64{sqlite.DefaultWorkingSetBytes, 1500, 1} {
		t.Run(fmt.Sprint("budget ", bytes), func(t *testing.T) {
			storage, _ := forkedStore(t, bytes)
			ids := []durable.EntryId{0, 10, 20, 25, 30, 35, 40, 45, 47, 48, 50, 60, 99}
			for _, conversation := range []durable.ConversationId{1, 2, 3} {
				for _, min := range append([]*durable.EntryId{nil}, pointers(ids)...) {
					for _, max := range append([]*durable.EntryId{nil}, pointers(ids)...) {
						expectRange(t, storage, durable.EntryQuery{ConversationId: conversation, MinEntryId: min, MaxEntryId: max})
					}
				}
			}
		})
	}
}

func pointers(ids []durable.EntryId) []*durable.EntryId {
	out := make([]*durable.EntryId, len(ids))
	for index := range ids {
		out[index] = &ids[index]
	}
	return out
}

func TestWorkingSetReadsUnknownConversationsAsScanEntriesDoes(t *testing.T) {
	storage, _ := forkedStore(t, sqlite.DefaultWorkingSetBytes)
	_, err := rangerOf(t, storage).VisibleEntryRange(testContext, durable.EntryQuery{ConversationId: 99})
	expectError(t, err, "Unknown conversation: 99")
}

// TestWorkingSetFollowsCommits interleaves reads with writes, including entries committed out of ID order and below a
// window's floor, against a random plan.
func TestWorkingSetFollowsCommits(t *testing.T) {
	for _, bytes := range []int64{sqlite.DefaultWorkingSetBytes, 4000} {
		t.Run(fmt.Sprint("budget ", bytes), func(t *testing.T) {
			random := rand.New(rand.NewSource(7))
			storage, _ := forkedStore(t, bytes)
			next := durable.EntryId(100)
			for step := range 200 {
				switch random.Intn(3) {
				case 0:
					conversation := durable.ConversationId(1 + random.Intn(3))
					id := next
					if random.Intn(4) == 0 {
						// Out of order: an ID below the newest of the conversation, unused by anyone.
						id = durable.EntryId(1000 + random.Intn(900))
					}
					next++
					if id < 1000 {
						id = next + 5000
					}
					commitEntries(t, storage, entryOf(id, conversation, fmt.Sprint("step ", step)))
				default:
					query := durable.EntryQuery{ConversationId: durable.ConversationId(1 + random.Intn(3))}
					if random.Intn(2) == 0 {
						query.MinEntryId = new(durable.EntryId(random.Intn(120)))
					}
					if random.Intn(2) == 0 {
						query.MaxEntryId = new(durable.EntryId(random.Intn(6200)))
					}
					expectRange(t, storage, query)
				}
			}
			for conversation := durable.ConversationId(1); conversation <= 3; conversation++ {
				expectRange(t, storage, durable.EntryQuery{ConversationId: conversation})
			}
		})
	}
}

func TestWorkingSetKeepsCommittedEntriesOutOfOrderAcrossReads(t *testing.T) {
	storage, _ := workingSetStorage(t, sqlite.DefaultWorkingSetBytes)
	must(storage.Commit(testContext, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}}}))
	commitEntries(t, storage, entryOf(30, 1, "thirty"), entryOf(10, 1, "ten"))
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
	commitEntries(t, storage, entryOf(20, 1, "twenty"), entryOf(40, 1, "forty"))
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1, MinEntryId: new(durable.EntryId(15)), MaxEntryId: new(durable.EntryId(35))})
}

func TestWorkingSetIgnoresAFailedCommit(t *testing.T) {
	storage, _ := forkedStore(t, sqlite.DefaultWorkingSetBytes)
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
	for _, failure := range []struct {
		name    string
		failing durable.StorageWrite
	}{
		// Entry 20 exists: the ID check rejects the batch before any write is applied.
		{"rejected before writing", durable.EntryWrite{Value: entryOf(20, 1, "duplicate")}},
		// A task without a status fails the tasks table's CHECK constraint after entry 500 was inserted, so the
		// transaction rolls back an applied entry write.
		{"rolled back after writing", durable.TaskWrite{Value: durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{Id: 501, ConversationId: 1}}},
	} {
		name, failing := failure.name, failure.failing
		_, err := storage.Commit(testContext, []durable.StorageWrite{durable.EntryWrite{Value: entryOf(500, 1, "new")}, failing})
		if err == nil {
			t.Fatalf("%s: the batch committed", name)
		}
		got := must(rangerOf(t, storage).VisibleEntryRange(testContext, durable.EntryQuery{ConversationId: 1}))
		if slices.Contains(entryIDs(got), 500) {
			t.Fatalf("%s: a rolled-back entry is in the working set: %v", name, entryIDs(got))
		}
		expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
	}
}

// TestWorkingSetSeesAnotherConnectionsWrites: a second storage on the same file commits; the first must not serve its
// retained entries.
func TestWorkingSetSeesAnotherConnectionsWrites(t *testing.T) {
	first, path := forkedStore(t, sqlite.DefaultWorkingSetBytes)
	expectRange(t, first, durable.EntryQuery{ConversationId: 1})
	expectRange(t, first, durable.EntryQuery{ConversationId: 3})
	second := reopen(t, path)
	commitEntries(t, second, entryOf(70, 1, "from another connection"), entryOf(49, 3, "grand 49"))
	for _, conversation := range []durable.ConversationId{1, 2, 3} {
		expectRange(t, first, durable.EntryQuery{ConversationId: conversation})
	}
	if got := entryIDs(must(rangerOf(t, first).VisibleEntryRange(testContext, durable.EntryQuery{ConversationId: 1}))); !slices.Contains(got, 70) {
		t.Fatalf("the first storage missed the other connection's entry: %v", got)
	}
}

// TestWorkingSetResultsAreNeverWrittenAgain: a range already handed out keeps its value while commits append, entries
// evict and windows grow.
func TestWorkingSetResultsAreNeverWrittenAgain(t *testing.T) {
	storage, _ := workingSetStorage(t, 3000)
	must(storage.Commit(testContext, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}}}))
	for id := durable.EntryId(101); id <= 108; id++ {
		commitEntries(t, storage, entryOf(id, 1, fmt.Sprint("first ", id)))
	}
	// Reading creates the window; the commits that follow grow it with spare capacity.
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
	for id := durable.EntryId(201); id <= 203; id++ {
		commitEntries(t, storage, entryOf(id, 1, fmt.Sprint("grown ", id)))
	}
	held := must(rangerOf(t, storage).VisibleEntryRange(testContext, durable.EntryQuery{ConversationId: 1}))
	snapshot := slices.Clone(held)
	for id := durable.EntryId(109); id <= 160; id++ {
		commitEntries(t, storage, entryOf(id, 1, fmt.Sprint("later ", id)))
		if id%7 == 0 {
			expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
		}
		if !reflect.DeepEqual(held, snapshot) {
			t.Fatalf("a range handed out changed after commit %d", id)
		}
	}
	if cap(held) != len(held) {
		t.Fatalf("range has spare capacity %d > %d: an append would write into the working set", cap(held), len(held))
	}
}

// TestWorkingSetRangeCannotAppendIntoTheWindow: the window keeps spare capacity after commits append, and a range ends
// where the window ends, so an unbounded capacity would let a caller's append overwrite the next committed entry.
func TestWorkingSetRangeCannotAppendIntoTheWindow(t *testing.T) {
	storage, _ := workingSetStorage(t, sqlite.DefaultWorkingSetBytes)
	must(storage.Commit(testContext, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}}}))
	commitEntries(t, storage, entryOf(101, 1, "a"))
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
	for id := durable.EntryId(102); id <= 106; id++ {
		commitEntries(t, storage, entryOf(id, 1, "grown"))
	}
	held := must(rangerOf(t, storage).VisibleEntryRange(testContext, durable.EntryQuery{ConversationId: 1}))
	if cap(held) != len(held) {
		t.Fatalf("range capacity %d exceeds its length %d", cap(held), len(held))
	}
	_ = append(held, durable.EntryRecord{Id: 999})
	commitEntries(t, storage, entryOf(107, 1, "next"))
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
}

func TestWorkingSetOfZeroBytesRetainsNothingAndStillAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sqlite")
	zero := int64(0)
	storage := must(node.OpenNodeSqliteStorage(path, node.NodeSqliteStorageOptions{WorkingSetBytes: &zero}))
	t.Cleanup(func() { _ = storage.Close(testContext) })
	must(storage.Commit(testContext, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}}}))
	commitEntries(t, storage, entryOf(101, 1, "a"), entryOf(102, 1, "b"))
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
	commitEntries(t, storage, entryOf(103, 1, "c"))
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
}

// TestWorkingSetReadsMatchAReopenedStore: what a warm storage serves is what a fresh process decodes from the file.
func TestWorkingSetReadsMatchAReopenedStore(t *testing.T) {
	storage, path := forkedStore(t, sqlite.DefaultWorkingSetBytes)
	for conversation := durable.ConversationId(1); conversation <= 3; conversation++ {
		warm := must(rangerOf(t, storage).VisibleEntryRange(testContext, durable.EntryQuery{ConversationId: conversation}))
		cold := must(rangerOf(t, reopen(t, path)).VisibleEntryRange(testContext, durable.EntryQuery{ConversationId: conversation}))
		if !reflect.DeepEqual(warm, cold) {
			t.Fatalf("conversation %d: warm %v != cold %v", conversation, entryIDs(warm), entryIDs(cold))
		}
	}
}

// plainDatabase hides the node adapter's row reader, so the working set loads through All.
type plainDatabase struct{ sqlite.SqliteDatabase }

func TestWorkingSetLoadsThroughADatabaseWithoutARowReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.sqlite")
	database := must(node.OpenNodeSqliteDatabase(path, node.NodeSqliteStorageOptions{}))
	if _, ok := any(plainDatabase{database}).(sqlite.TextReader); ok {
		t.Fatal("the wrapper still exposes a row reader")
	}
	if _, ok := any(database).(sqlite.TextReader); !ok {
		t.Fatal("the node adapter has no row reader: the other tests no longer exercise it")
	}
	storage := must(sqlite.Open(plainDatabase{database}))
	t.Cleanup(func() { _ = storage.Close(testContext) })
	must(storage.Commit(testContext, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: 1}}}))
	commitEntries(t, storage, entryOf(10, 1, "a"), entryOf(20, 1, "b"))
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
	reopened := reopen(t, path)
	commitEntries(t, reopened, entryOf(30, 1, "c"))
	expectRange(t, storage, durable.EntryQuery{ConversationId: 1})
}
