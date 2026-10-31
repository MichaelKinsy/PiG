package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/next/store/store.test.ts, "a commit series" (Pi e5c6182b): CommitSeries stores several
// commits in one backend transaction, one seq each.

// unchecked is a series commit with no check of its own.
func unchecked(writes ...StagedWrite) SeriesCommit {
	return SeriesCommit{Check: func(*Step) error { return nil }, Writes: writes}
}

// keyedCreate is a creation of the singleton at storeAddress under key.
func keyedCreate(key string) StagedWrite {
	address := storeAddress
	address.Key = new(key)
	return CreateDocument(address, storeBase, "{}", nil)
}

// receipt is what a series gave its receive: the commit's index, its seq and its created ids.
type receipt struct {
	index int
	seq   int64
	ids   []int64
}

func noReceipt(int, int64, []int64) error { return nil }

func TestCommitSeries(t *testing.T) {
	t.Run("stores its commits in one transaction, one seq each, each checked against the ones before it", func(t *testing.T) {
		store := memoryStore(t, true)
		var seen []int64
		var receipts []receipt
		meter := metered(t)
		result, err := store.CommitSeries([]SeriesCommit{
			unchecked(CreateDocument(storeAddress, storeBase, "{}", nil)),
			// No writes: no seq, it stands at the commit before it.
			unchecked(),
			{
				// The check reads through its step: it sees the first commit's creation.
				Check: func(step *Step) error {
					found, err := FindDocument(step, storeAddress, CurrentPoint)
					if err == nil && found != nil {
						seen = append(seen, found.ID)
					}
					return err
				},
				Writes: []StagedWrite{keyedCreate("b")},
			},
		}, func(index int, seq int64, ids []int64) error {
			receipts = append(receipts, receipt{index, seq, ids})
			return nil
		}, meter.Context)
		noError(t, err)
		if result.Stored != 3 || result.Refused != nil {
			t.Fatalf("result = %+v, want stored 3", result)
		}
		if !reflect.DeepEqual(seen, []int64{2}) {
			t.Fatalf("seen = %v, want [2]", seen)
		}
		want := []receipt{{0, 1, []int64{2}}, {1, 1, []int64{}}, {2, 2, []int64{3}}}
		if !reflect.DeepEqual(receipts, want) {
			t.Fatalf("receipts = %v, want %v", receipts, want)
		}
		if store.LastSeq() != 2 {
			t.Fatalf("lastSeq = %d, want 2", store.LastSeq())
		}
		if commits := meter.Counts()["chord.store.commits"]; commits != 2 {
			t.Fatalf("chord.store.commits = %v, want 2", commits)
		}
		rows := engineRows(t, store, "SELECT seq FROM "+TableOutbox+" ORDER BY seq")
		if len(rows) != 2 || rows[0]["seq"] != int64(1) || rows[1]["seq"] != int64(2) {
			t.Fatalf("outbox = %v, want seqs 1, 2", rows)
		}
	})

	t.Run("stops at a check that refuses: the commits before it are stored, it and the ones after are not", func(t *testing.T) {
		store := memoryStore(t)
		refusal := errors.New("refused")
		var receipts []int
		result, err := store.CommitSeries([]SeriesCommit{
			unchecked(CreateDocument(storeAddress, storeBase, "{}", nil)),
			{Check: func(*Step) error { return refusal }, Writes: []StagedWrite{keyedCreate("b")}},
			unchecked(keyedCreate("c")),
		}, func(index int, _ int64, _ []int64) error {
			receipts = append(receipts, index)
			return nil
		}, background)
		noError(t, err)
		if result.Stored != 1 || result.Refused != refusal {
			t.Fatalf("result = %+v, want stored 1 refused %v", result, refusal)
		}
		if !reflect.DeepEqual(receipts, []int{0}) || store.LastSeq() != 1 {
			t.Fatalf("[receipts, lastSeq] = [%v, %d], want [[0], 1]", receipts, store.LastSeq())
		}
		rows := engineRows(t, store, "SELECT key FROM "+TableDocuments+" ORDER BY id")
		if len(rows) != 1 || rows[0]["key"] != "" {
			t.Fatalf("documents = %v, want the one at key \"\"", rows)
		}
		// The store goes on at the next seq.
		adopted, fence := recording()
		_, err = store.Commit([]StagedWrite{keyedCreate("b")}, fence, background)
		noError(t, err)
		sameAdoptions(t, *adopted, adoption{2, []int64{3}})
	})

	t.Run("rolls the whole series back when a check's statement fails, even if the check returns its own error", func(t *testing.T) {
		store := memoryStore(t)
		own := errors.New("own")
		_, err := store.CommitSeries([]SeriesCommit{
			unchecked(CreateDocument(storeAddress, storeBase, "{}", nil)),
			{
				Check: func(step *Step) error {
					_, _ = step.Get("SELECT * FROM no_such_table")
					return own
				},
			},
		}, noReceipt, background)
		if err != own {
			t.Fatalf("CommitSeries error = %v, want the check's own", err)
		}
		if store.LastSeq() != 0 {
			t.Fatalf("lastSeq = %d, want 0", store.LastSeq())
		}
		// A rollback, not a stop: the store goes on; the id the rolled back creation took is not given back (D3).
		if id := storeCreated(t, store); id != 3 {
			t.Fatalf("created id = %d, want 3", id)
		}
	})

	t.Run("rolls the whole series back when a commit's writes fail, the store going on", func(t *testing.T) {
		store := memoryStore(t)
		var receipts []int
		_, err := store.CommitSeries([]SeriesCommit{
			unchecked(CreateDocument(storeAddress, storeBase, "{}", nil)),
			unchecked(brokenWrite),
		}, func(index int, _ int64, _ []int64) error {
			receipts = append(receipts, index)
			return nil
		}, background)
		if err == nil || !strings.Contains(err.Error(), "no such table") {
			t.Fatalf("CommitSeries error = %v, want no such table", err)
		}
		if len(receipts) != 0 || store.LastSeq() != 0 {
			t.Fatalf("[receipts, lastSeq] = [%v, %d], want [[], 0]", receipts, store.LastSeq())
		}
		if id := storeCreated(t, store); id != 3 {
			t.Fatalf("created id = %d, want 3", id)
		}
	})

	t.Run("writes the metadata once per series: three statements per delta with its outbox commit, plus one (cost guard)", func(t *testing.T) {
		store := memoryStore(t, true)
		id := storeCreated(t, store)
		meter := metered(t)
		delta := func(n int64) SeriesCommit {
			ops := `[["s",["n"],` + string(rune('0'+n)) + `]]`
			return unchecked(ChangeDocument(id, n, DocumentContent{Kind: "delta", Version: 1, Ops: opsOf(t, ops)}, ops))
		}
		_, err := store.CommitSeries([]SeriesCommit{delta(1), delta(2), delta(3)}, noReceipt, meter.Context)
		noError(t, err)
		// A delta committed alone is four statements with an outbox, the metadata among them; a series writes it once.
		want := map[string]float64{"chord.store.commits": 3, "chord.store.statements": 3*3 + 1}
		if got := meter.Counts(); !reflect.DeepEqual(got, want) {
			t.Fatalf("counts = %v, want %v", got, want)
		}
		rows := engineRows(t, store, "SELECT last_seq FROM "+TableMeta)
		if len(rows) != 1 || rows[0]["last_seq"] != int64(4) {
			t.Fatalf("meta = %v, want last_seq 4", rows)
		}
	})

	for _, at := range []struct {
		statement string
		broken    bool
	}{{failBegin, false}, {failCommit, false}, {failRollback, true}} {
		t.Run("a failed "+at.statement+" is an unknown outcome: nothing is received, the store stops storage_failed", func(t *testing.T) {
			store, fail := faultyStore(t)
			var told []error
			noError(t, store.Attach(Attachment{Failed: func(err error) { told = append(told, err) }, Batch: newBatch()}))
			fail(at.statement)
			var receipts []int
			second := keyedCreate("b")
			if at.broken {
				second = brokenWrite
			}
			_, err := store.CommitSeries([]SeriesCommit{
				unchecked(CreateDocument(storeAddress, storeBase, "{}", nil)),
				unchecked(second),
			}, func(index int, _ int64, _ []int64) error {
				receipts = append(receipts, index)
				return nil
			}, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
			if len(receipts) != 0 || store.LastSeq() != 0 || len(told) != 1 {
				t.Fatalf("[receipts, lastSeq, told] = [%v, %d, %d], want [[], 0, 1]", receipts, store.LastSeq(), len(told))
			}
			_, err = store.CommitSeries([]SeriesCommit{unchecked(CreateDocument(storeAddress, storeBase, "{}", nil))}, noReceipt, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
		})
	}

	t.Run("lets a later commit see the table records an earlier one changed; a rollback leaves the catalog as it was", func(t *testing.T) {
		store := memoryStore(t)
		edits := catalogEdits(t)
		table := StageTable(&TableDefinitionWrite{
			Table: catalogTable, Version: edits.Version, State: StateReady, Columns: edits.Record, Key: edits.Key, Fingerprint: edits.Fingerprint,
		})
		_, err := store.CommitSeries([]SeriesCommit{unchecked(table), unchecked(brokenWrite)}, noReceipt, background)
		if err == nil {
			t.Fatal("CommitSeries succeeded, want the broken write's error")
		}
		if record := store.Table(catalogTable); record != nil {
			t.Fatalf("Table = %+v, want none", record)
		}
		var seen []int
		version := func(step *Step) {
			if record := step.Table(catalogTable); record != nil {
				seen = append(seen, record.Version)
			} else {
				seen = append(seen, -1)
			}
		}
		sees := stagedWrite{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
			version(step)
			return nil, nil
		}}
		later := SeriesCommit{Check: func(step *Step) error { version(step); return nil }, Writes: []StagedWrite{sees}}
		result, err := store.CommitSeries([]SeriesCommit{unchecked(table), later}, noReceipt, background)
		noError(t, err)
		if result.Stored != 2 || result.Refused != nil {
			t.Fatalf("result = %+v, want stored 2", result)
		}
		if !reflect.DeepEqual(seen, []int{1, 1}) {
			t.Fatalf("seen = %v, want [1 1]", seen)
		}
		if record := store.Table(catalogTable); record == nil || record.Version != 1 {
			t.Fatalf("Table = %+v, want version 1", record)
		}
	})

	t.Run("runs a commit queued behind a series after the series' receipts", func(t *testing.T) {
		store := memoryStore(t)
		var events []string
		series := startQueued(t, store, func() (SeriesResult, error) {
			return store.CommitSeries([]SeriesCommit{
				unchecked(CreateDocument(storeAddress, storeBase, "{}", nil)),
				unchecked(keyedCreate("b")),
			}, func(index int, seq int64, _ []int64) error {
				events = append(events, "receive "+string(rune('0'+index))+" "+string(rune('0'+seq)))
				return nil
			}, background)
		})
		fence := CommitFence{
			Admit: func() bool { events = append(events, "admit"); return true },
			Adopt: func(seq int64, _ []int64) error { events = append(events, "adopt "+string(rune('0'+seq))); return nil },
		}
		queued := startQueued(t, store, func() (int64, error) { return store.Commit([]StagedWrite{keyedCreate("c")}, fence, background) })
		_, err := settledValue(t, series)
		noError(t, err)
		_, err = settledValue(t, queued)
		noError(t, err)
		want := []string{"receive 0 1", "receive 1 2", "admit", "adopt 3"}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("events = %v, want %v", events, want)
		}
	})

	t.Run("counts only commits with writes against the seq bound: empty ones at Number.MAX_SAFE_INTEGER still stand", func(t *testing.T) {
		store := reimported(t, memoryStore(t), func(parts []ArchivePart) []ArchivePart {
			edited := append([]ArchivePart{}, parts...)
			manifest := *edited[0].(*ManifestPart)
			manifest.LastSeq = MaxSafeInteger - 1
			edited[0] = &manifest
			return edited
		})
		storeCreated(t, store)
		var receipts []receipt
		empty, err := store.CommitSeries([]SeriesCommit{unchecked(), unchecked()}, func(index int, seq int64, ids []int64) error {
			receipts = append(receipts, receipt{index, seq, ids})
			return nil
		}, background)
		noError(t, err)
		if empty.Stored != 2 || empty.Refused != nil {
			t.Fatalf("empty = %+v, want stored 2", empty)
		}
		want := []receipt{{0, MaxSafeInteger, []int64{}}, {1, MaxSafeInteger, []int64{}}}
		if !reflect.DeepEqual(receipts, want) {
			t.Fatalf("receipts = %v, want %v", receipts, want)
		}
		_, err = store.CommitSeries([]SeriesCommit{unchecked(keyedCreate("b"))}, noReceipt, background)
		_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
	})

	t.Run("stops the store when a receipt fails", func(t *testing.T) {
		store := memoryStore(t)
		_, err := store.CommitSeries([]SeriesCommit{unchecked(CreateDocument(storeAddress, storeBase, "{}", nil))},
			func(int, int64, []int64) error { return errors.New("receipt threw") }, background)
		_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
		_, err = Read(store, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
		_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
	})
}
