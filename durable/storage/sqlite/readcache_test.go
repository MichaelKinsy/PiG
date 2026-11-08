package sqlite_test

import (
	"bufio"
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// The read cache must answer exactly what the database answers, whoever wrote it last: the reference is a second storage
// with no cache, on its own connection to the same file.

func uncachedOptions() node.NodeSqliteStorageOptions {
	return node.NodeSqliteStorageOptions{ReadCacheBytes: new(int64(0))}
}

func openAt(t *testing.T, path string, options node.NodeSqliteStorageOptions) *sqlite.SqliteStorage {
	t.Helper()
	storage := must(node.OpenNodeSqliteStorage(path, options))
	t.Cleanup(func() { _ = storage.Close(testContext) })
	return storage
}

// requireReadCache skips a test on a system where a database has no commit token, and so no read cache.
func requireReadCache(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd":
	default:
		t.Skip("no read cache on " + runtime.GOOS)
	}
}

// cachedAndReference opens a cached storage and a reference over one new file.
func cachedAndReference(t *testing.T, options node.NodeSqliteStorageOptions) (cached, reference *sqlite.SqliteStorage, path string) {
	t.Helper()
	requireReadCache(t)
	cached, path = createSqliteStorage(t, options)
	return cached, openAt(t, path, uncachedOptions()), path
}

func TestReadCacheAnswersFromMemoryAndMatchesTheDatabase(t *testing.T) {
	cached, reference, _ := cachedAndReference(t, node.NodeSqliteStorageOptions{})
	w := newCacheWorld(1)
	w.commit(t, cached, w.root())
	for range 60 {
		w.commit(t, cached, w.next())
	}
	probes := w.probes()
	sameAnswers(t, "first read", probes, cached, reference)
	before := cached.ReadCacheStats()
	if before.Fills == 0 {
		t.Fatalf("nothing was kept: %+v", before)
	}
	sameAnswers(t, "second read", probes, cached, reference)
	after := cached.ReadCacheStats()
	if after.Hits-before.Hits < uint64(len(probes))/2 {
		t.Fatalf("the second pass hit %d of %d reads: %+v", after.Hits-before.Hits, len(probes), after)
	}
	if reference.ReadCacheStats() != (sqlite.ReadCacheStats{}) {
		t.Fatalf("the reference kept reads: %+v", reference.ReadCacheStats())
	}
}

// TestReadCacheFollowsEveryWriter drives random interleavings of this storage's commits, commits of a second storage, raw
// writes and checkpoints of other connections, and compares every kind of read after each step.
func TestReadCacheFollowsEveryWriter(t *testing.T) {
	seeds := int64(4)
	if raceDetector {
		seeds = 2
	}
	for seed := int64(1); seed <= seeds; seed++ {
		t.Run(fmt.Sprint("seed ", seed), func(t *testing.T) {
			cached, reference, path := cachedAndReference(t, node.NodeSqliteStorageOptions{})
			other := openInspector(t, path, false)
			t.Cleanup(func() { _ = other.Close() })
			w := newCacheWorld(seed)
			w.commit(t, cached, w.root())
			steps := 220
			if testing.Short() || raceDetector {
				steps = 60
			}
			for step := range steps {
				var what string
				switch choice := w.rng.IntN(12); {
				case choice < 4:
					what = "commit through the cached storage"
					w.commit(t, cached, w.next())
				case choice < 6:
					what = "commit through the reference storage"
					w.commit(t, reference, w.next())
				case choice == 6:
					what = "raw update"
					rawWrite(t, w, other)
				case choice == 7:
					mode := []string{"PASSIVE", "RESTART", "TRUNCATE"}[w.rng.IntN(3)]
					what = "checkpoint " + mode
					mustDo(t, other.Exec("PRAGMA wal_checkpoint("+mode+")"))
				default:
					what = "no write"
				}
				// Every step reads a random part of the battery, and every tenth all of it: what an earlier step kept and nothing
				// since invalidated is still answered from memory.
				probes := w.probes()
				if step%10 != 9 {
					w.rng.Shuffle(len(probes), func(i, j int) { probes[i], probes[j] = probes[j], probes[i] })
					probes = probes[:min(len(probes), 120)]
				}
				sameAnswers(t, fmt.Sprintf("step %d (%s)", step, what), probes, cached, reference)
			}
			stats := cached.ReadCacheStats()
			if stats.Hits == 0 || stats.Fills == 0 || stats.Resets == 0 || stats.Adoptions == 0 || stats.Disabled {
				t.Fatalf("the cache did not exercise every path: %+v", stats)
			}
		})
	}
}

// rawWrite changes the database the way no storage does, as another connection could.
func rawWrite(t *testing.T, w *cacheWorld, db *node.DatabaseSync) {
	t.Helper()
	switch w.rng.IntN(5) {
	case 0:
		if len(w.tasks) > 0 {
			mustDo(t, must(db.Prepare("UPDATE tasks SET record = json_set(record, '$.version', 9) WHERE id = ?")).Run(int64(w.tasks[w.rng.IntN(len(w.tasks))])))
		}
	case 1:
		mustDo(t, must(db.Prepare("UPDATE entries SET commit_seq = commit_seq + 7 WHERE id % 3 = ?")).Run(int64(w.rng.IntN(3))))
	case 2:
		mustDo(t, must(db.Prepare("DELETE FROM entries WHERE id = (SELECT max(id) FROM entries)")).Run())
	case 3:
		if len(w.submissions) > 0 {
			mustDo(t, must(db.Prepare("DELETE FROM submissions WHERE id = ?")).Run(int64(w.submissions[w.rng.IntN(len(w.submissions))])))
		}
	default:
		mustDo(t, db.Exec("PRAGMA user_version = "+itoa(w.rng.IntN(1000))))
	}
}

// readGroup is the tables a probe's read depends on, by its name.
func readGroup(name string) string {
	switch {
	case strings.HasPrefix(name, "Entry "), strings.HasPrefix(name, "VisibleEntry "), strings.HasPrefix(name, "FindLatestHeadMarker "), strings.HasPrefix(name, "ScanEntries "):
		return "entries"
	case strings.HasPrefix(name, "Conversation "), strings.HasPrefix(name, "ScanConversations "):
		return "conversations"
	case strings.HasPrefix(name, "Task "), strings.HasPrefix(name, "ScanTasks "):
		return "tasks"
	case strings.HasPrefix(name, "Submission"), strings.HasPrefix(name, "ScanSubmissions"):
		return "submissions"
	default:
		return "documents"
	}
}

// groupHits reads the probes of each group once from the cache and counts its hits.
func groupHits(storage *sqlite.SqliteStorage, probes []probe) map[string]uint64 {
	hits := map[string]uint64{}
	for _, probe := range probes {
		before := storage.ReadCacheStats().Hits
		probe.read(storage)
		hits[readGroup(probe.name)] += storage.ReadCacheStats().Hits - before
	}
	return hits
}

func TestReadCacheKeepsWhatAnOwnCommitDidNotWrite(t *testing.T) {
	cached, reference, _ := cachedAndReference(t, node.NodeSqliteStorageOptions{})
	w := newCacheWorld(7)
	w.commit(t, cached, w.root())
	for range 12 {
		w.commit(t, cached, w.entryWrites())
	}
	for range 4 {
		w.commit(t, cached, w.taskWrites())
		w.commit(t, cached, w.submissionWrites())
		w.commit(t, cached, w.documentWrites())
	}
	probes := w.probes()
	sameAnswers(t, "warm", probes, cached, reference)
	baseline := groupHits(cached, probes)
	for group, hits := range baseline {
		if hits == 0 {
			t.Fatalf("no read of %s was a hit when nothing had changed: %v", group, baseline)
		}
	}
	for _, kind := range []struct {
		written string
		batch   func() []durable.StorageWrite
	}{{"tasks", w.taskWrites}, {"documents", w.documentWrites}, {"submissions", w.submissionWrites}, {"entries", w.entryWrites}} {
		adoptions := cached.ReadCacheStats().Adoptions
		w.commit(t, cached, kind.batch())
		if got := cached.ReadCacheStats().Adoptions; got != adoptions+1 {
			t.Fatalf("a commit of %s was not adopted: %d adoptions before, %d after", kind.written, adoptions, got)
		}
		hits := groupHits(cached, probes)
		for group, want := range baseline {
			if group == kind.written {
				// A read the probes repeat is a hit the second time, once the first one kept it again.
				if hits[group] >= want {
					t.Fatalf("after a commit of %s, %d of %d reads of it were hits, want fewer", group, hits[group], want)
				}
				continue
			}
			if hits[group] != want {
				t.Fatalf("after a commit of %s, reads of %s hit %d times, want %d (the commit wrote nothing they read)", kind.written, group, hits[group], want)
			}
		}
		sameAnswers(t, "after a commit of "+kind.written, probes, cached, reference)
		// What the commit invalidated is kept again by the first read.
		baseline = groupHits(cached, probes)
	}
	if stats := cached.ReadCacheStats(); stats.Resets != 0 {
		t.Fatalf("an own commit dropped everything: %+v", stats)
	}
}

// scribble overwrites everything it can reach in value, as a caller that owns a result may.
func scribble(value reflect.Value) {
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return
		}
		if object, ok := value.Interface().(*chorddelta.JsonObject); ok {
			for key := range object.All() {
				object.Set(key, "scribbled")
			}
			return
		}
		scribble(value.Elem())
	case reflect.Interface:
		if value.IsNil() {
			return
		}
		inner := value.Elem()
		if inner.Kind() == reflect.Pointer || inner.Kind() == reflect.Map || inner.Kind() == reflect.Slice {
			scribble(inner)
		}
	case reflect.Struct:
		for index := range value.NumField() {
			if field := value.Field(index); field.CanSet() {
				scribble(field)
			}
		}
	case reflect.Slice:
		for index := range value.Len() {
			scribble(value.Index(index))
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			element := iterator.Value()
			switch element.Kind() {
			case reflect.String:
				value.SetMapIndex(iterator.Key(), reflect.ValueOf("scribbled").Convert(element.Type()))
			case reflect.Interface:
				if inner := element.Elem(); inner.Kind() == reflect.Pointer || inner.Kind() == reflect.Map || inner.Kind() == reflect.Slice {
					scribble(inner)
				} else if inner.Kind() == reflect.String {
					value.SetMapIndex(iterator.Key(), reflect.ValueOf("scribbled"))
				}
			default:
				scribble(element)
			}
		}
	case reflect.String:
		if value.CanSet() {
			value.SetString("scribbled")
		}
	}
}

func TestReadCacheResultsBelongToTheCaller(t *testing.T) {
	cached, reference, _ := cachedAndReference(t, node.NodeSqliteStorageOptions{})
	w := newCacheWorld(3)
	w.commit(t, cached, w.root())
	for range 80 {
		w.commit(t, cached, w.next())
	}
	probes := w.probes()
	for round := range 3 {
		// Every read returns something the caller may write: after it did, the next read is still what the database holds.
		for _, probe := range probes {
			first := probe.read(cached)
			scribble(reflect.ValueOf(&first))
			scribble(reflect.ValueOf(first.Value))
			if got, want := probe.read(cached), probe.read(reference); !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d: %s after the caller wrote a result: got %s, want %s", round, probe.name, durabletest.Describe(got), durabletest.Describe(want))
			}
		}
	}
	if cached.ReadCacheStats().Hits == 0 {
		t.Fatal("no read was a hit")
	}
}

func TestReadCacheIsBounded(t *testing.T) {
	const budget = 48 << 10
	cached, reference, _ := cachedAndReference(t, node.NodeSqliteStorageOptions{ReadCacheBytes: new(int64(budget))})
	w := newCacheWorld(5)
	w.commit(t, cached, w.root())
	for range 150 {
		w.commit(t, cached, w.entryWrites())
	}
	var kept int
	for pass := range 3 {
		for _, probe := range w.probes() {
			got, want := probe.read(cached), probe.read(reference)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("pass %d: %s: got %s, want %s", pass, probe.name, durabletest.Describe(got), durabletest.Describe(want))
			}
			stats := cached.ReadCacheStats()
			if stats.Bytes > budget {
				t.Fatalf("the cache holds %d bytes of a %d byte budget: %+v", stats.Bytes, budget, stats)
			}
			kept = max(kept, stats.Items)
		}
	}
	stats := cached.ReadCacheStats()
	if stats.Items == 0 || stats.Rejected+stats.Fills == 0 || kept == 0 {
		t.Fatalf("nothing was kept: %+v", stats)
	}
}

// heapInUse is the live heap after a collection.
func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// TestReadCacheSizeEstimateBoundsTheHeap holds the cache to its word: the estimate it budgets with is not smaller than the
// memory it keeps.
func TestReadCacheSizeEstimateBoundsTheHeap(t *testing.T) {
	cached, _, _ := cachedAndReference(t, node.NodeSqliteStorageOptions{ReadCacheBytes: new(int64(256 << 20))})
	w := newCacheWorld(11)
	w.commit(t, cached, w.root())
	for range 400 {
		w.commit(t, cached, w.next())
	}
	probes := w.probes()
	for _, probe := range probes {
		probe.read(cached)
	}
	empty := heapInUse()
	for _, limit := range []int{1, 2, 3, 5, 8, 13, 21, 34, 55} {
		for _, order := range []durable.ScanOrder{durable.ScanAscending, durable.ScanDescending} {
			for _, conversation := range w.conversations {
				_, _ = cached.ScanEntries(testContext, durable.EntryQuery{ConversationId: conversation.id, Order: &order}, limit, nil)
			}
		}
	}
	filled := heapInUse()
	stats := cached.ReadCacheStats()
	grown := int64(filled) - int64(empty)
	t.Logf("estimated %d bytes, heap grew %d bytes, %d items", stats.Bytes, grown, stats.Items)
	if grown <= 0 {
		t.Skip("the heap did not grow measurably")
	}
	if stats.Bytes < grown/2 || stats.Bytes > grown*8 {
		t.Fatalf("estimate %d is not near the %d bytes the heap grew", stats.Bytes, grown)
	}
	runtime.KeepAlive(cached)
}

// A database that cannot name its committed state keeps nothing, and answers from the database.
func TestReadCacheNeedsACommitToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.sqlite")
	database := must(node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{}))
	storage := must(sqlite.Open(struct{ sqlite.SqliteDatabase }{database}))
	t.Cleanup(func() { _ = storage.Close(testContext) })
	w := newCacheWorld(2)
	w.commit(t, storage, w.root())
	for range 20 {
		w.commit(t, storage, w.next())
	}
	for _, probe := range w.probes() {
		probe.read(storage)
	}
	if stats := storage.ReadCacheStats(); stats != (sqlite.ReadCacheStats{}) {
		t.Fatalf("a database without a commit token kept reads: %+v", stats)
	}
}

// frozenToken is a database whose commit token never changes, as a wal-index that is not the one SQLite writes would.
type frozenToken struct {
	*node.NodeSqliteDatabase
	token chordsqlite.CommitToken
}

func (frozen *frozenToken) CommitToken() (chordsqlite.CommitToken, bool) { return frozen.token, true }

func TestReadCacheStopsWhenItsTokenIgnoresItsOwnCommits(t *testing.T) {
	requireReadCache(t)
	path := filepath.Join(t.TempDir(), "storage.sqlite")
	database := must(node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{}))
	token, ok := database.CommitToken()
	if !ok {
		token = chordsqlite.CommitToken{1}
	}
	storage := must(sqlite.Open(&frozenToken{NodeSqliteDatabase: database, token: token}))
	t.Cleanup(func() { _ = storage.Close(testContext) })
	reference := openAt(t, path, uncachedOptions())
	w := newCacheWorld(9)
	w.commit(t, storage, w.root())
	for range 10 {
		w.commit(t, storage, w.entryWrites())
	}
	probes := w.probes()
	sameAnswers(t, "warm", probes, storage, reference)
	if storage.ReadCacheStats().Items == 0 {
		t.Fatal("nothing was kept")
	}
	// The next commit leaves the token as it was: the cache sees it does not follow the database and stops.
	w.commit(t, storage, w.entryWrites())
	if !storage.ReadCacheStats().Disabled {
		t.Fatalf("the cache kept going on a token that ignores commits: %+v", storage.ReadCacheStats())
	}
	sameAnswers(t, "after the commit", w.probes(), storage, reference)
}

// TestReadCacheReadsDuringCommitsOfOtherConnections reads from several goroutines while two other connections commit; run
// with -race. After the writers stop, every read is what the database holds.
func TestReadCacheReadsDuringCommitsOfOtherConnections(t *testing.T) {
	cached, reference, _ := cachedAndReference(t, node.NodeSqliteStorageOptions{})
	w := newCacheWorld(21)
	w.commit(t, cached, w.root())
	for range 30 {
		w.commit(t, cached, w.next())
	}
	var stop atomic.Bool
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				_, _ = cached.Entry(testContext, 101)
				_, _ = cached.ScanEntries(testContext, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 5, nil)
				_, _ = cached.ScanTasks(testContext, durable.TaskQuery{}, 3, nil)
				_, _ = cached.FindDocument(testContext, sessionAddress("a"), durable.CurrentPoint)
			}
		}()
	}
	for step := range 60 {
		target := durable.Storage(cached)
		if step%2 == 1 {
			target = reference
		}
		w.commit(t, target, w.next())
	}
	stop.Store(true)
	readers.Wait()
	sameAnswers(t, "after the writers", w.probes(), cached, reference)
}

// TestReadCacheLeavesTheFileAsItWas writes the same history with and without the cache, reads in between, and compares
// the files byte for byte once both are closed.
func TestReadCacheLeavesTheFileAsItWas(t *testing.T) {
	write := func(options node.NodeSqliteStorageOptions) []byte {
		storage, path := createSqliteStorage(t, options)
		w := newCacheWorld(33)
		w.commit(t, storage, w.root())
		for range 120 {
			w.commit(t, storage, w.next())
			for _, probe := range w.probes()[:30] {
				probe.read(storage)
			}
		}
		mustDo(t, storage.Close(testContext))
		return must(os.ReadFile(path))
	}
	with, without := write(node.NodeSqliteStorageOptions{}), write(uncachedOptions())
	if !bytes.Equal(with, without) {
		t.Fatalf("the database file differs: %d bytes with the cache, %d without", len(with), len(without))
	}
}

// --- A writer that is killed ---

const readCacheKillChild = "PIG_READCACHE_KILL_CHILD"

// A killed writer leaves a cached reader reading what the database holds. The child commits entries through its own
// storage and prints the number of each commit once it returned; the reader reads while it runs, must see every
// acknowledged commit, and agrees with an uncached reader once the child is gone.
func TestReadCacheSurvivesAKilledWriter(t *testing.T) {
	if path := os.Getenv(readCacheKillChild); path != "" {
		runReadCacheKillChild(path)
		return
	}
	cached, reference, path := cachedAndReference(t, node.NodeSqliteStorageOptions{})
	w := newCacheWorld(44)
	w.commit(t, cached, w.root())
	for _, killAfter := range []int{1, 3, 9, 40, 120, 5} {
		command := exec.Command(os.Args[0], "-test.run=^TestReadCacheSurvivesAKilledWriter$")
		command.Env = append(os.Environ(), readCacheKillChild+"="+path, "PIG_READCACHE_KILL_BASE="+strconv.FormatInt(w.nextId, 10))
		output, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		var acknowledged atomic.Int64
		var finished sync.WaitGroup
		finished.Add(1)
		go func() {
			defer finished.Done()
			for lines := bufio.NewScanner(output); lines.Scan(); {
				if text, ok := strings.CutPrefix(lines.Text(), "committed "); ok {
					count, _ := strconv.ParseInt(text, 10, 64)
					acknowledged.Store(count)
				}
			}
		}()
		base := w.nextId
		for acknowledged.Load() < int64(killAfter) {
			runtime.Gosched()
			// Reads that keep the cache warm, and the check that matters: an acknowledged commit is visible.
			count := acknowledged.Load()
			for range 3 {
				_, _ = cached.ScanEntries(testContext, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 4, nil)
			}
			if count > 0 {
				id := durable.EntryId(base + count)
				found, err := cached.Entry(testContext, id)
				if err != nil || found == nil {
					t.Fatalf("the cache does not see entry %d of commit %d the child acknowledged: %v, %v", id, count, found, err)
				}
			}
		}
		_ = command.Process.Kill()
		_ = command.Wait()
		finished.Wait()
		w.nextId = base + 100000
		// Quiet again: the cache and the database agree on everything.
		sameAnswers(t, fmt.Sprint("after a kill at ", killAfter), w.probes(), cached, reference)
		last := durable.EntryId(base + acknowledged.Load())
		for _, storage := range []durable.Storage{cached, reference} {
			if found, err := storage.Entry(testContext, last); err != nil || found == nil {
				t.Fatalf("the acknowledged entry %d is missing after the kill: %v, %v", last, found, err)
			}
		}
	}
	if stats := cached.ReadCacheStats(); stats.Hits == 0 || stats.Disabled {
		t.Fatalf("unexpected cache state: %+v", stats)
	}
}

func runReadCacheKillChild(path string) {
	base, _ := strconv.ParseInt(os.Getenv("PIG_READCACHE_KILL_BASE"), 10, 64)
	storage, err := node.OpenNodeSqliteStorage(path, node.NodeSqliteStorageOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	rng := rand.New(rand.NewPCG(uint64(base), 1))
	for count := int64(1); ; count++ {
		entry := durable.EntryRecord{
			Id: durable.EntryId(base + count), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "m",
			Data: map[string]any{"count": float64(count), "text": strings.Repeat("x", 200+rng.IntN(2000))},
		}
		if _, err := durabletest.CommitWrites(testContext, storage, []durable.StorageWrite{durable.EntryWrite{Value: entry}}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("committed", count)
	}
}

// hookedDatabase runs a function once after a read or after a transaction of the connection it wraps, so a test can make
// another connection commit at the one moment a race needs it.
type hookedDatabase struct {
	*node.NodeSqliteDatabase
	afterRead        func()
	afterTransaction func()
}

func (database *hookedDatabase) Get(sqlText string, params ...chordsqlite.SqliteValue) (chordsqlite.SqliteRow, error) {
	row, err := database.NodeSqliteDatabase.Get(sqlText, params...)
	if hook := database.afterRead; hook != nil && strings.Contains(sqlText, "FROM entries") {
		database.afterRead = nil
		hook()
	}
	return row, err
}

func (database *hookedDatabase) Transaction(callback func(transaction chordsqlite.SqliteExecutor) error) error {
	err := database.NodeSqliteDatabase.Transaction(callback)
	if hook := database.afterTransaction; hook != nil {
		database.afterTransaction = nil
		hook()
	}
	return err
}

func openHooked(t *testing.T) (*sqlite.SqliteStorage, *hookedDatabase, *sqlite.SqliteStorage) {
	t.Helper()
	requireReadCache(t)
	path := filepath.Join(t.TempDir(), "storage.sqlite")
	hooked := &hookedDatabase{NodeSqliteDatabase: must(node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{}))}
	storage := must(sqlite.Open(hooked))
	t.Cleanup(func() { _ = storage.Close(testContext) })
	return storage, hooked, openAt(t, path, uncachedOptions())
}

// A commit of another connection that lands while a read runs must not be hidden by the result of that read: the read
// answers for the state it started from, and the next read sees the commit.
// mutation-checked: taking the token after the read instead of before it keeps the stale answer, and fails it.
func TestReadCacheDoesNotKeepAReadThatAnotherCommitOvertook(t *testing.T) {
	cached, hooked, other := openHooked(t)
	w := newCacheWorld(61)
	w.commit(t, cached, w.root())
	entry := func(id durable.EntryId) []durable.StorageWrite {
		return []durable.StorageWrite{durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "m"}}}
	}
	w.commit(t, cached, entry(500))
	// Keep something so the cache is warm.
	if found, err := cached.Entry(testContext, 500); err != nil || found == nil {
		t.Fatalf("Entry(500) = %v, %v", found, err)
	}
	hooked.afterRead = func() {
		w.commit(t, other, entry(501))
		// A read of another thread meanwhile finds the cache holding an older token and drops it for the new one.
		if _, err := cached.Conversation(testContext, durable.ROOT_CONVERSATION_ID); err != nil {
			t.Error(err)
		}
	}
	if found, err := cached.Entry(testContext, 501); err != nil || found != nil {
		t.Fatalf("the read that started before the commit answered %v, %v; want none", found, err)
	}
	for range 2 {
		if found, err := cached.Entry(testContext, 501); err != nil || found == nil {
			t.Fatalf("the read after the commit answered %v, %v; want the entry", found, err)
		}
	}
}

// An own commit that another connection's commit follows at once is not the only one since it started: the cache must
// not adopt it as if it were, and the next read must show both.
// mutation-checked: adopting any later token without requiring exactly one commit keeps the old page, and fails it.
func TestReadCacheDoesNotAdoptACommitAnotherOneFollowed(t *testing.T) {
	cached, hooked, other := openHooked(t)
	w := newCacheWorld(62)
	w.commit(t, cached, w.root())
	entry := func(id durable.EntryId) []durable.StorageWrite {
		return []durable.StorageWrite{durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "m"}}}
	}
	w.commit(t, cached, entry(600))
	query := durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}
	if page := must(cached.ScanEntries(testContext, query, 10, nil)); len(page.Items) != 1 {
		t.Fatalf("page = %v", page)
	}
	hooked.afterTransaction = func() { w.commit(t, other, entry(602)) }
	w.commit(t, cached, entry(601))
	for range 2 {
		page := must(cached.ScanEntries(testContext, query, 10, nil))
		if ids := entryIDs(page.Items); !reflect.DeepEqual(ids, []int64{602, 601, 600}) {
			t.Fatalf("the page holds %v, want [602 601 600]", ids)
		}
	}
	if stats := cached.ReadCacheStats(); stats.Adoptions != 0 {
		t.Fatalf("a commit another one followed was adopted: %+v", stats)
	}
}
