package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/store/store.test.ts
//
// The store (slice 6a §2, §3, slice 6b §1): opening, the commit step, refusals and unknown outcomes (D4), the id
// allocator (D3), one host per store (rule 7), close.

var (
	storeAddress = DocumentAddress{Namespace: "chord", Owner: "app.store", Kind: "notes", Key: new("")}
	storeBase    = DocumentBase{Version: 1, Fingerprint: "0000000000000000", Value: delta.JsonObjectOf("n", float64(0))}
)

const storeBaseText = `{"n":0}`

// adoption is what a fence adopted: a seq and the ids of the documents the commit created.
type adoption struct {
	seq int64
	ids []int64
}

func (each adoption) String() string { return fmt.Sprintf("%d:%v", each.seq, each.ids) }

// recording is a fence that admits and records what it adopted.
func recording() (*[]adoption, CommitFence) {
	adopted := &[]adoption{}
	return adopted, CommitFence{
		Admit: func() bool { return true },
		Adopt: func(seq int64, ids []int64) error {
			*adopted = append(*adopted, adoption{seq, ids})
			return nil
		},
	}
}

// sameAdoptions fails unless got is want (an absent id list is an empty one).
func sameAdoptions(t *testing.T, got []adoption, want ...adoption) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("adopted %v, want %v", got, want)
	}
}

// storeCreated creates the singleton at storeAddress and returns its id.
func storeCreated(t *testing.T, store *ChordStore) int64 {
	t.Helper()
	adopted, fence := recording()
	_, err := store.Commit([]StagedWrite{CreateDocument(storeAddress, storeBase, storeBaseText, nil)}, fence, background)
	noError(t, err)
	return (*adopted)[0].ids[0]
}

// brokenWrite is a staged write whose step fails in the backend: its own code fails, a refusal.
var brokenWrite StagedWrite = stagedWrite{
	order: OrderWrite,
	apply: func(step *Step) ([]StoreWrite, error) {
		if err := step.Run("INSERT INTO no_such_table VALUES (1)"); err != nil {
			return nil, err
		}
		return []StoreWrite{&DocumentRetire{ID: 0, Revision: 0}}, nil
	},
}

// openStoreAt opens the store of the SQLite file at path.
func openStoreAt(t *testing.T, path string) *ChordStore {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	noError(t, err)
	opened, err := OpenStore(SqliteBackend(db), background, StoreOptions{})
	noError(t, err)
	return opened.(*ChordStore)
}

// newBatch is `new Batch(() => {}, undefined)`.
func newBatch() *state.Batch { return state.NewBatch(func(state.BatchError) {}, nil) }

// settledValue is the one value a store call settled on its channel with.
func settledValue[T any](t *testing.T, settled <-chan chordctx.Settled[T]) (T, error) {
	t.Helper()
	select {
	case outcome := <-settled:
		return outcome.Value, outcome.Err
	case <-time.After(10 * time.Second):
		t.Fatal("a store call did not settle")
		panic("unreachable")
	}
}

func TestStore(t *testing.T) {
	t.Run("opening", func(t *testing.T) {
		t.Run("creates a new store at seq 0 without an outbox, and reopens a file at its last seq and ids", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.sqlite")
			store := openStoreAt(t, path)
			if store.LastSeq() != 0 || store.Outbox() != nil {
				t.Fatalf("[lastSeq, outbox] = [%d, %v]", store.LastSeq(), store.Outbox())
			}
			if id := storeCreated(t, store); id != 2 {
				t.Fatalf("first id = %d, want 2", id)
			}
			noError(t, store.Close(background))
			reopened := openStoreAt(t, path)
			if reopened.LastSeq() != 1 {
				t.Fatalf("reopened lastSeq = %d, want 1", reopened.LastSeq())
			}
			adopted, fence := recording()
			keyed := storeAddress
			keyed.Key = new("b")
			_, err := reopened.Commit([]StagedWrite{CreateDocument(keyed, storeBase, "{}", nil)}, fence, background)
			noError(t, err)
			sameAdoptions(t, *adopted, adoption{2, []int64{3}})
			noError(t, reopened.Close(background))
		})

		t.Run("refuses a database that is not UTF-8, closing the backend (rule 12)", func(t *testing.T) {
			db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
			noError(t, err)
			noError(t, db.Exec(`PRAGMA encoding = "UTF-16le"`))
			if _, err := OpenStore(SqliteBackend(db), background, StoreOptions{}); err == nil || !strings.Contains(err.Error(), "not UTF-8") {
				t.Fatalf("OpenStore = %v, want not UTF-8", err)
			}
			if _, err := db.Get("SELECT 1"); err == nil {
				t.Fatal("the backend is still open after a refused open")
			}
		})
	})

	t.Run("the commit step", func(t *testing.T) {
		t.Run("assigns one seq per commit, applies retirements before creations, and adopts before the next step", func(t *testing.T) {
			store := memoryStore(t)
			id := storeCreated(t, store)
			var events []string
			fence := func(name string) CommitFence {
				return CommitFence{
					Admit: func() bool { return true },
					Adopt: func(seq int64, ids []int64) error {
						joined := make([]string, len(ids))
						for index, each := range ids {
							joined[index] = fmt.Sprint(each)
						}
						events = append(events, fmt.Sprintf("%s %d %s", name, seq, strings.Join(joined, ",")))
						return nil
					},
				}
			}
			held, entered, release := holdingWrite()
			first := startQueued(t, store, func() (int64, error) {
				writes := []StagedWrite{CreateDocument(storeAddress, storeBase, "{}", nil), RetireDocument(id, 1), held}
				return store.Commit(writes, fence("recreate"), background)
			})
			// Both commits are queued before either settles (Pi starts both before awaiting): the first is held until
			// the second took its place.
			<-entered
			content := DocumentContent{Kind: "delta", Version: 1, Ops: []delta.Op{{"s", []any{"n"}, float64(1)}}}
			second := startQueued(t, store, func() (int64, error) {
				return store.Commit([]StagedWrite{ChangeDocument(3, 1, content, `[["s",["n"],1]]`)}, fence("change"), background)
			})
			release()
			seqs := make([]int64, 0, 2)
			for _, settled := range []<-chan chordctx.Settled[int64]{first, second} {
				seq, err := settledValue(t, settled)
				noError(t, err)
				seqs = append(seqs, seq)
			}
			if !reflect.DeepEqual(seqs, []int64{2, 3}) {
				t.Fatalf("seqs = %v, want [2 3]", seqs)
			}
			if !reflect.DeepEqual(events, []string{"recreate 2 3", "change 3 "}) {
				t.Fatalf("events = %q", events)
			}
			if store.LastSeq() != 3 {
				t.Fatalf("lastSeq = %d, want 3", store.LastSeq())
			}
		})

		t.Run("rejects a commit its fence refuses at the entry released, writing nothing; an empty commit takes no seq", func(t *testing.T) {
			store := memoryStore(t)
			refusing := CommitFence{Admit: func() bool { return false }, Adopt: func(int64, []int64) error { return nil }}
			_, err := store.Commit([]StagedWrite{CreateDocument(storeAddress, storeBase, "{}", nil)}, refusing, background)
			_ = rejected(t, err, data.CodeReleased, "")
			adopted, fence := recording()
			seq, err := store.Commit(nil, fence, background)
			noError(t, err)
			if seq != 0 {
				t.Fatalf("an empty commit's seq = %d, want 0", seq)
			}
			sameAdoptions(t, *adopted, adoption{0, []int64{}})
			if id := storeCreated(t, store); id != 2 {
				t.Fatalf("first id after a refused commit = %d, want 2", id)
			}
		})

		t.Run("numbers up to Number.MAX_SAFE_INTEGER after an import near it, then fails storage_failed: no seq repeats", func(t *testing.T) {
			parts := exported(t, memoryStore(t), background)
			manifest := parts[0].(*ManifestPart)
			values := []any{&ManifestPart{Format: manifest.Format, Schema: manifest.Schema, LastSeq: MaxSafeInteger - 1, NextID: manifest.NextID}}
			for _, part := range parts[1:] {
				values = append(values, part)
			}
			db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
			noError(t, err)
			imported, err := ImportStore(SqliteBackend(db), SlicePartSource(values), background, StoreOptions{})
			noError(t, err)
			store := imported.(*ChordStore)
			t.Cleanup(func() { _ = store.Close(background) })
			if id := storeCreated(t, store); id != 2 {
				t.Fatalf("first id = %d, want 2", id)
			}
			if store.LastSeq() != MaxSafeInteger {
				t.Fatalf("lastSeq = %d, want Number.MAX_SAFE_INTEGER", store.LastSeq())
			}
			keyed := storeAddress
			keyed.Key = new("b")
			_, fence := recording()
			_, err = store.Commit([]StagedWrite{CreateDocument(keyed, storeBase, "{}", nil)}, fence, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
			if store.LastSeq() != MaxSafeInteger {
				t.Fatalf("lastSeq after the refusal = %d, want Number.MAX_SAFE_INTEGER", store.LastSeq())
			}
			_, err = Read(store, func(sqlite.SqliteExecutor) (any, error) { return nil, nil }, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
		})

		t.Run("counts one commit and three statements for a delta, four with an outbox (cost guard)", func(t *testing.T) {
			for _, c := range []struct {
				outbox     bool
				statements float64
			}{{false, 3}, {true, 4}} {
				store := memoryStore(t, c.outbox)
				id := storeCreated(t, store)
				telemetry := metered(t)
				ops := []delta.Op{{"s", []any{"n"}, float64(1)}}
				content := DocumentContent{Kind: "delta", Version: 1, Ops: ops}
				_, fence := recording()
				_, err := store.Commit([]StagedWrite{ChangeDocument(id, 1, content, `[["s",["n"],1]]`)}, fence, telemetry.Context)
				noError(t, err)
				want := map[string]float64{"chord.store.commits": 1, "chord.store.statements": c.statements}
				if got := telemetry.Counts(); !reflect.DeepEqual(got, want) {
					t.Fatalf("outbox %v: counts = %v, want %v", c.outbox, got, want)
				}
			}
		})
	})

	t.Run("refusals and unknown outcomes (D4)", func(t *testing.T) {
		t.Run("a step whose own code throws is refused: the commit rejects with that error, nothing is adopted, the store goes on", func(t *testing.T) {
			store := memoryStore(t)
			var told []error
			noError(t, store.Attach(Attachment{Failed: func(err error) { told = append(told, err) }, Batch: newBatch()}))
			adopted, fence := recording()
			_, err := store.Commit([]StagedWrite{brokenWrite}, fence, background)
			if err == nil || !strings.Contains(err.Error(), "no such table") {
				t.Fatalf("Commit(broken) = %v, want no such table", err)
			}
			if _, isChord := data.IsChordError(err); isChord {
				t.Fatalf("the refusal is a ChordError: %v", err)
			}
			if len(*adopted) != 0 || len(told) != 0 || store.LastSeq() != 0 {
				t.Fatalf("[adopted, told, lastSeq] = [%v, %v, %d]", *adopted, told, store.LastSeq())
			}
			if id := storeCreated(t, store); id != 2 {
				t.Fatalf("first id after a refusal = %d, want 2", id)
			}
			if store.LastSeq() != 1 {
				t.Fatalf("lastSeq = %d, want 1", store.LastSeq())
			}
		})

		for _, c := range []struct {
			at    string
			write StagedWrite
		}{{failBegin, nil}, {failCommit, nil}, {failRollback, brokenWrite}} {
			t.Run(fmt.Sprintf("a failed %s is an unknown outcome: the store stops storage_failed, its host told once", c.at), func(t *testing.T) {
				store, fail := faultyStore(t)
				var told []error
				noError(t, store.Attach(Attachment{Failed: func(err error) { told = append(told, err) }, Batch: newBatch()}))
				fail(c.at)
				writes := []StagedWrite{CreateDocument(storeAddress, storeBase, "{}", nil)}
				if c.write != nil {
					writes = []StagedWrite{c.write}
				}
				_, fence := recording()
				_, err := store.Commit(writes, fence, background)
				_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
				_, err = store.Commit([]StagedWrite{CreateDocument(storeAddress, storeBase, "{}", nil)}, fence, background)
				_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
				_, err = Read(store, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
				_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
				if len(told) != 1 {
					t.Fatalf("told %d times, want once", len(told))
				}
				noError(t, store.Close(background))
				noError(t, store.Close(background))
			})
		}

		t.Run("an adopt that throws stops the store, an empty commit's too; a conditional update on another revision is refused", func(t *testing.T) {
			store := memoryStore(t)
			id := storeCreated(t, store)
			throwing := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return errors.New("adopt threw") }}
			_, err := store.Commit([]StagedWrite{RetireDocument(id, 1)}, throwing, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
			empty := memoryStore(t)
			_, err = empty.Commit(nil, throwing, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
			_, err = Read(empty, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
			other := memoryStore(t)
			otherID := storeCreated(t, other)
			stale := ChangeDocument(otherID, 7, DocumentContent{Kind: "delta", Version: 1, Ops: []delta.Op{}}, "[]")
			_, fence := recording()
			if _, err := other.Commit([]StagedWrite{stale}, fence, background); err == nil || !strings.Contains(err.Error(), "not live at revision 7") {
				t.Fatalf("stale change = %v, want not live at revision 7", err)
			}
			if other.LastSeq() != 1 {
				t.Fatalf("lastSeq = %d, want 1", other.LastSeq())
			}
			one, err := Read(other, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
			noError(t, err)
			if one != 1 {
				t.Fatalf("read = %d, want 1", one)
			}
		})

		t.Run("spare a read whose context was aborted (aborted), and reject a read that settles after the stop", func(t *testing.T) {
			store, fail := faultyStore(t)
			cancelled, cancel := chordctx.WithCancel(background)
			cancel(nil)
			_, err := Read(store, func(sqlite.SqliteExecutor) (int, error) { return 0, errors.New("backend") }, cancelled)
			_ = rejected(t, err, data.CodeAborted, "")
			gate := make(chan struct{})
			started := make(chan struct{})
			late := make(chan error, 1)
			go func() {
				_, err := Read(store, func(sqlite.SqliteExecutor) (int, error) {
					close(started)
					<-gate
					return 1, nil
				}, background)
				late <- err
			}()
			<-started
			fail(failCommit)
			_, fence := recording()
			_, err = store.Commit([]StagedWrite{CreateDocument(storeAddress, storeBase, "{}", nil)}, fence, background)
			if err == nil {
				t.Fatal("the commit with a failing COMMIT succeeded")
			}
			close(gate)
			_ = rejected(t, <-late, data.CodeUnavailable, data.ReasonStorageFailed)
		})

		t.Run("runs a read transaction's queries on one state, guarded as a read", func(t *testing.T) {
			store := memoryStore(t)
			storeCreated(t, store)
			counts, err := ReadTransaction(store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
				documents, err := executor.Get("SELECT count(*) AS n FROM chord_documents")
				if err != nil {
					return nil, err
				}
				one, err := executor.Get("SELECT 1 AS one")
				return []sqlite.SqliteRow{documents, one}, err
			}, background)
			noError(t, err)
			if want := []sqlite.SqliteRow{{"n": int64(1)}, {"one": int64(1)}}; !reflect.DeepEqual(counts, want) {
				t.Fatalf("counts = %v, want %v", counts, want)
			}
			_, err = ReadTransaction(store, func(sqlite.SqliteExecutor) (int, error) { return 0, errors.New("backend") }, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
		})
	})

	t.Run("the id allocator (D3)", func(t *testing.T) {
		// raising is a staged write raising the allocator above id, as an adapter's explicit id does.
		raising := func(id int64) StagedWrite {
			return stagedWrite{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
				step.IDs.Raise(id)
				return nil, nil
			}}
		}

		t.Run("starts at 2, takes a creation's id as it applies, and gives a mint during a running step the next one", func(t *testing.T) {
			store := memoryStore(t, true)
			held, entered, release := holdingWrite()
			_, fence := recording()
			commit := startQueued(t, store, func() (int64, error) {
				return store.Commit([]StagedWrite{CreateDocument(storeAddress, storeBase, "{}", nil), held}, fence, background)
			})
			<-entered
			minted, err := store.MintId()
			noError(t, err)
			if minted != 3 {
				t.Fatalf("a mint during the step = %d, want 3", minted)
			}
			release()
			_, err = settledValue(t, commit)
			noError(t, err)
			minted, err = store.MintId()
			noError(t, err)
			if minted != 4 {
				t.Fatalf("a mint after the step = %d, want 4", minted)
			}
			commits, err := store.Outbox().Read(0, 10, background)
			noError(t, err)
			created := commits[0].Writes[0].(*DocumentCreate)
			if commits[0].NextID != 4 || created.Record.ID != 2 {
				t.Fatalf("[nextId, id] = [%d, %d], want [4, 2]", commits[0].NextID, created.Record.ID)
			}
			if store.NextID() != 4 {
				t.Fatalf("nextId = %d, want 4", store.NextID())
			}
		})

		t.Run("leaves a refused step's ids unused and keeps the raised value", func(t *testing.T) {
			store := memoryStore(t)
			_, fence := recording()
			_, _ = store.Commit([]StagedWrite{CreateDocument(storeAddress, storeBase, "{}", nil), brokenWrite}, fence, background)
			if id := storeCreated(t, store); id != 3 {
				t.Fatalf("id after a refused step = %d, want 3", id)
			}
		})

		t.Run("saturates at Number.MAX_SAFE_INTEGER across reopen: mints and Chord creations are refused, the store goes on", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.sqlite")
			store := openStoreAt(t, path)
			_, fence := recording()
			_, err := store.Commit([]StagedWrite{raising(MaxSafeInteger)}, fence, background)
			noError(t, err)
			if store.NextID() != MaxSafeInteger {
				t.Fatalf("nextId = %d, want Number.MAX_SAFE_INTEGER", store.NextID())
			}
			noError(t, store.Close(background))
			reopened := openStoreAt(t, path)
			if reopened.NextID() != MaxSafeInteger {
				t.Fatalf("reopened nextId = %d, want Number.MAX_SAFE_INTEGER", reopened.NextID())
			}
			if _, err := reopened.MintId(); err == nil || !strings.Contains(err.Error(), "ID space is exhausted") {
				t.Fatalf("MintId = %v, want ID space is exhausted", err)
			}
			_, err = reopened.Commit([]StagedWrite{CreateDocument(storeAddress, storeBase, "{}", nil)}, fence, background)
			if err == nil || !strings.Contains(err.Error(), "ID space is exhausted") {
				t.Fatalf("Commit(create) = %v, want ID space is exhausted", err)
			}
			one, err := Read(reopened, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
			noError(t, err)
			if one != 1 {
				t.Fatalf("read = %d, want 1", one)
			}
			noError(t, reopened.Close(background))
		})
	})

	t.Run("one host per store (rule 7)", func(t *testing.T) {
		t.Run("refuses a second attachment until the first detaches", func(t *testing.T) {
			store := memoryStore(t)
			attachment := Attachment{Failed: func(error) {}, Batch: newBatch()}
			noError(t, store.Attach(attachment))
			if store.Batch() != attachment.Batch {
				t.Fatal("the store's batch is not the attachment's")
			}
			if err := store.Attach(attachment); err == nil || !strings.Contains(err.Error(), "already attached") {
				t.Fatalf("second Attach = %v, want already attached", err)
			}
			store.Detach()
			if store.Batch() != nil {
				t.Fatal("a detached store still has a batch")
			}
			noError(t, store.Attach(attachment))
		})
	})

	t.Run("close", func(t *testing.T) {
		t.Run("waits for the running step, rejects queued commits and later reads released, and closes the backend", func(t *testing.T) {
			store := memoryStore(t)
			gate := make(chan struct{})
			entered := make(chan struct{})
			create := CreateDocument(storeAddress, storeBase, "{}", nil)
			held := stagedWrite{order: OrderCreate, apply: func(step *Step) ([]StoreWrite, error) {
				close(entered)
				<-gate
				return create.Apply(step)
			}}
			_, fence := recording()
			running := startQueued(t, store, func() (int64, error) { return store.Commit([]StagedWrite{held}, fence, background) })
			<-entered
			queuedAt := storeAddress
			queuedAt.Key = new("q")
			queued := startQueued(t, store, func() (int64, error) {
				return store.Commit([]StagedWrite{CreateDocument(queuedAt, storeBase, "{}", nil)}, fence, background)
			})
			closed := make(chan error, 1)
			go func() { closed <- store.Close(background) }()
			// Close stops the store at its call: wait for it before the running step goes on.
			deadline := time.Now().Add(10 * time.Second)
			for store.AssertUsable() == nil {
				if time.Now().After(deadline) {
					t.Fatal("Close did not stop the store")
				}
				time.Sleep(time.Millisecond)
			}
			close(gate)
			seq, err := settledValue(t, running)
			noError(t, err)
			if seq != 1 {
				t.Fatalf("running commit's seq = %d, want 1", seq)
			}
			_, err = settledValue(t, queued)
			_ = rejected(t, err, data.CodeReleased, "")
			noError(t, <-closed)
			_, err = Read(store, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
			_ = rejected(t, err, data.CodeReleased, "")
		})
	})

	t.Run("a commit series", func(t *testing.T) {
		// unchecked is a series commit with no check of its own.
		unchecked := func(writes ...StagedWrite) SeriesCommit {
			return SeriesCommit{Check: func(*Step) error { return nil }, Writes: writes}
		}
		// created is the creation of the document at the key of storeAddress.
		created := func(key string) StagedWrite {
			address := storeAddress
			address.Key = &key
			return CreateDocument(address, storeBase, "{}", nil)
		}
		noReceipt := func(int, int64, []int64) error { return nil }
		// edits is the table a series creates.
		edits, err := DefineTable("edits", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Str()), Col("at", Int())}, Key: []string{"id"}})
		noError(t, err)
		const tableName = "app.store:edits"

		t.Run("stores its commits in one transaction, one seq each, each checked against the ones before it", func(t *testing.T) {
			store := memoryStore(t, true)
			var seen []int64
			var receipts []string
			telemetry := metered(t)
			result, err := store.CommitSeries([]SeriesCommit{
				unchecked(created("")),
				// No writes: no seq, it stands at the commit before it.
				unchecked(),
				{
					// The check reads through its step: it sees the first commit's creation.
					Check: func(step *Step) error {
						record, err := FindDocument(step, storeAddress, CurrentPoint)
						if err != nil {
							return err
						}
						if record == nil {
							seen = append(seen, -1)
						} else {
							seen = append(seen, record.ID)
						}
						return nil
					},
					Writes: []StagedWrite{created("b")},
				},
			}, func(index int, seq int64, ids []int64) error {
				receipts = append(receipts, fmt.Sprintf("%d %d %v", index, seq, ids))
				return nil
			}, telemetry.Context)
			noError(t, err)
			if result != (SeriesResult{Stored: 3}) {
				t.Fatalf("result = %+v, want {Stored: 3}", result)
			}
			if !reflect.DeepEqual(seen, []int64{2}) {
				t.Fatalf("the check saw %v, want [2]", seen)
			}
			if want := []string{"0 1 [2]", "1 1 []", "2 2 [3]"}; !reflect.DeepEqual(receipts, want) {
				t.Fatalf("receipts = %q, want %q", receipts, want)
			}
			if store.LastSeq() != 2 {
				t.Fatalf("lastSeq = %d, want 2", store.LastSeq())
			}
			if commits := telemetry.Counts()["chord.store.commits"]; commits != 2 {
				t.Fatalf("chord.store.commits = %v, want 2", commits)
			}
			outbox, err := Read(store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
				return executor.All("SELECT seq FROM chord_outbox")
			}, background)
			noError(t, err)
			var seqs []int64
			for _, row := range outbox {
				seq, err := sqlite.RowInt(row, "seq")
				noError(t, err)
				seqs = append(seqs, seq)
			}
			if !reflect.DeepEqual(seqs, []int64{1, 2}) {
				t.Fatalf("outbox seqs = %v, want [1 2]", seqs)
			}
		})

		t.Run("stops at a check that refuses: the commits before it are stored, it and the ones after are not", func(t *testing.T) {
			store := memoryStore(t)
			refusal := errors.New("refused")
			var receipts []int
			result, err := store.CommitSeries([]SeriesCommit{
				unchecked(created("")),
				{Check: func(*Step) error { return refusal }, Writes: []StagedWrite{created("b")}},
				unchecked(created("c")),
			}, func(index int, _ int64, _ []int64) error {
				receipts = append(receipts, index)
				return nil
			}, background)
			noError(t, err)
			if result.Stored != 1 || result.Refused != refusal {
				t.Fatalf("result = %+v, want {Stored: 1, Refused: refused}", result)
			}
			if !reflect.DeepEqual(receipts, []int{0}) {
				t.Fatalf("receipts = %v, want [0]", receipts)
			}
			if store.LastSeq() != 1 {
				t.Fatalf("lastSeq = %d, want 1", store.LastSeq())
			}
			rows, err := Read(store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
				return executor.All("SELECT key FROM chord_documents ORDER BY id")
			}, background)
			noError(t, err)
			var keys []any
			for _, row := range rows {
				keys = append(keys, row["key"])
			}
			if !reflect.DeepEqual(keys, []any{""}) {
				t.Fatalf("keys = %q, want [\"\"]", keys)
			}
			// The store goes on at the next seq.
			adopted, fence := recording()
			keyed := storeAddress
			keyed.Key = new("b")
			_, err = store.Commit([]StagedWrite{CreateDocument(keyed, storeBase, "{}", nil)}, fence, background)
			noError(t, err)
			sameAdoptions(t, *adopted, adoption{2, []int64{3}})
		})

		t.Run("rolls the whole series back when a check's statement fails, even if the check throws its own error", func(t *testing.T) {
			store := memoryStore(t)
			own := errors.New("own")
			_, err := store.CommitSeries([]SeriesCommit{
				unchecked(created("")),
				{Check: func(step *Step) error {
					// The check swallows the backend's failure and throws its own error.
					_, _ = step.Get("SELECT * FROM no_such_table")
					return own
				}},
			}, noReceipt, background)
			if err != own {
				t.Fatalf("CommitSeries = %v, want the check's own error", err)
			}
			if store.LastSeq() != 0 {
				t.Fatalf("lastSeq = %d, want 0", store.LastSeq())
			}
			// A rollback, not a stop: the store goes on; the id the rolled back creation took is not given back (D3).
			if id := storeCreated(t, store); id != 3 {
				t.Fatalf("id after the rollback = %d, want 3", id)
			}
		})

		t.Run("rolls the whole series back when a commit's writes throw, the store going on", func(t *testing.T) {
			store := memoryStore(t)
			var receipts []int
			_, err := store.CommitSeries([]SeriesCommit{unchecked(created("")), unchecked(brokenWrite)}, func(index int, _ int64, _ []int64) error {
				receipts = append(receipts, index)
				return nil
			}, background)
			if err == nil || !strings.Contains(err.Error(), "no such table") {
				t.Fatalf("CommitSeries = %v, want no such table", err)
			}
			if len(receipts) != 0 || store.LastSeq() != 0 {
				t.Fatalf("[receipts, lastSeq] = [%v, %d], want [[], 0]", receipts, store.LastSeq())
			}
			if id := storeCreated(t, store); id != 3 {
				t.Fatalf("id after the rollback = %d, want 3", id)
			}
		})

		t.Run("writes the metadata once per series: three statements per delta with its outbox commit, plus one (cost guard)", func(t *testing.T) {
			store := memoryStore(t, true)
			id := storeCreated(t, store)
			telemetry := metered(t)
			change := func(n int) SeriesCommit {
				ops := []delta.Op{{"s", []any{"n"}, float64(n)}}
				content := DocumentContent{Kind: "delta", Version: 1, Ops: ops}
				return unchecked(ChangeDocument(id, int64(n), content, fmt.Sprintf(`[["s",["n"],%d]]`, n)))
			}
			_, err := store.CommitSeries([]SeriesCommit{change(1), change(2), change(3)}, noReceipt, telemetry.Context)
			noError(t, err)
			// A delta committed alone is four statements with an outbox, the metadata among them; a series writes it once.
			want := map[string]float64{"chord.store.commits": 3, "chord.store.statements": 3*3 + 1}
			if got := telemetry.Counts(); !reflect.DeepEqual(got, want) {
				t.Fatalf("counts = %v, want %v", got, want)
			}
			meta, err := Read(store, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
				return executor.Get("SELECT last_seq FROM chord_meta")
			}, background)
			noError(t, err)
			if lastSeq, err := sqlite.RowInt(meta, "last_seq"); err != nil || lastSeq != 4 {
				t.Fatalf("last_seq = %d (%v), want 4", lastSeq, err)
			}
		})

		for _, c := range []struct {
			at    string
			write StagedWrite
		}{{failBegin, nil}, {failCommit, nil}, {failRollback, brokenWrite}} {
			t.Run(fmt.Sprintf("a failed %s is an unknown outcome: nothing is received, the store stops storage_failed", c.at), func(t *testing.T) {
				store, fail := faultyStore(t)
				var told []error
				noError(t, store.Attach(Attachment{Failed: func(err error) { told = append(told, err) }, Batch: newBatch()}))
				fail(c.at)
				var receipts []int
				second := c.write
				if second == nil {
					second = created("b")
				}
				_, err := store.CommitSeries([]SeriesCommit{unchecked(created("")), unchecked(second)}, func(index int, _ int64, _ []int64) error {
					receipts = append(receipts, index)
					return nil
				}, background)
				_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
				if len(receipts) != 0 || store.LastSeq() != 0 || len(told) != 1 {
					t.Fatalf("[receipts, lastSeq, told] = [%v, %d, %d], want [[], 0, 1]", receipts, store.LastSeq(), len(told))
				}
				_, err = store.CommitSeries([]SeriesCommit{unchecked(created(""))}, noReceipt, background)
				_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
			})
		}

		t.Run("lets a later commit see the table records an earlier one changed; a rollback leaves the catalog as it was", func(t *testing.T) {
			store := memoryStore(t)
			table := StageTable(&TableDefinitionWrite{
				Table: tableName, Version: edits.Version, State: StateReady, Columns: edits.Record, Key: edits.Key, Fingerprint: edits.Fingerprint,
			})
			if _, err := store.CommitSeries([]SeriesCommit{unchecked(table), unchecked(brokenWrite)}, noReceipt, background); err == nil {
				t.Fatal("a series whose second commit throws succeeded")
			}
			if store.Table(tableName) != nil {
				t.Fatal("a rolled back series left its table in the catalog")
			}
			var seen []int
			versionIn := func(step *Step) int {
				if record := step.Table(tableName); record != nil {
					return record.Version
				}
				return 0
			}
			sees := stagedWrite{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
				seen = append(seen, versionIn(step))
				return nil, nil
			}}
			later := SeriesCommit{Check: func(step *Step) error {
				seen = append(seen, versionIn(step))
				return nil
			}, Writes: []StagedWrite{sees}}
			result, err := store.CommitSeries([]SeriesCommit{unchecked(table), later}, noReceipt, background)
			noError(t, err)
			if result != (SeriesResult{Stored: 2}) {
				t.Fatalf("result = %+v, want {Stored: 2}", result)
			}
			if !reflect.DeepEqual(seen, []int{1, 1}) {
				t.Fatalf("the later commit saw table versions %v, want [1 1]", seen)
			}
			if record := store.Table(tableName); record == nil || record.Version != 1 {
				t.Fatalf("table record = %+v, want version 1", record)
			}
		})

		t.Run("runs a commit queued behind a series after the series' receipts", func(t *testing.T) {
			store := memoryStore(t)
			var events []string
			series := startQueued(t, store, func() (SeriesResult, error) {
				return store.CommitSeries([]SeriesCommit{unchecked(created("")), unchecked(created("b"))}, func(index int, seq int64, _ []int64) error {
					events = append(events, fmt.Sprintf("receive %d %d", index, seq))
					return nil
				}, background)
			})
			fence := CommitFence{
				Admit: func() bool {
					events = append(events, "admit")
					return true
				},
				Adopt: func(seq int64, _ []int64) error {
					events = append(events, fmt.Sprintf("adopt %d", seq))
					return nil
				},
			}
			queued := startQueued(t, store, func() (int64, error) {
				return store.Commit([]StagedWrite{created("c")}, fence, background)
			})
			_, err := settledValue(t, series)
			noError(t, err)
			_, err = settledValue(t, queued)
			noError(t, err)
			if want := []string{"receive 0 1", "receive 1 2", "admit", "adopt 3"}; !reflect.DeepEqual(events, want) {
				t.Fatalf("events = %q, want %q", events, want)
			}
		})

		t.Run("counts only commits with writes against the seq bound: empty ones at Number.MAX_SAFE_INTEGER still stand", func(t *testing.T) {
			parts := exported(t, memoryStore(t), background)
			manifest := parts[0].(*ManifestPart)
			values := []any{&ManifestPart{Format: manifest.Format, Schema: manifest.Schema, LastSeq: MaxSafeInteger - 1, NextID: manifest.NextID}}
			for _, part := range parts[1:] {
				values = append(values, part)
			}
			db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
			noError(t, err)
			imported, err := ImportStore(SqliteBackend(db), SlicePartSource(values), background, StoreOptions{})
			noError(t, err)
			store := imported.(*ChordStore)
			t.Cleanup(func() { _ = store.Close(background) })
			storeCreated(t, store)
			var receipts []string
			empty, err := store.CommitSeries([]SeriesCommit{unchecked(), unchecked()}, func(index int, seq int64, _ []int64) error {
				receipts = append(receipts, fmt.Sprintf("%d %d", index, seq))
				return nil
			}, background)
			noError(t, err)
			if empty != (SeriesResult{Stored: 2}) {
				t.Fatalf("empty series = %+v, want {Stored: 2}", empty)
			}
			if want := []string{fmt.Sprintf("0 %d", MaxSafeInteger), fmt.Sprintf("1 %d", MaxSafeInteger)}; !reflect.DeepEqual(receipts, want) {
				t.Fatalf("receipts = %q, want %q", receipts, want)
			}
			_, err = store.CommitSeries([]SeriesCommit{unchecked(created("b"))}, noReceipt, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
		})

		t.Run("stops the store when a receipt throws", func(t *testing.T) {
			store := memoryStore(t)
			_, err := store.CommitSeries([]SeriesCommit{unchecked(created(""))}, func(int, int64, []int64) error {
				return errors.New("receipt threw")
			}, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
			_, err = Read(store, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
			_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
		})
	})
}
