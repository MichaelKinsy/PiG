package store

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/test/next/store/apply-batch.test.ts
//
// Tests of batch apply (slice 6b §1.6, S5, S17): a store's outbox replayed into an empty store gives the same
// archive, manifest included; every write kind (an adapter's create-plus-retire and change-plus-retire too); the
// refusals (claims held, seqs that do not follow, writes the records refuse) and the divergence of a recorded count.

// batchCounts are the `counts` table of the cases (version 1 with an index) and its version 2 (a column, no index).
var batchCounts = func() (v1, v2 *TableDefinition) {
	v1, err := DefineTable("counts", TableOptions{
		Version: 1,
		Columns: []ColumnProp{Col("id", Str()), Col("n", Int()), Col("tag", Str())},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "by_tag", Columns: []string{"tag"}}},
	})
	if err != nil {
		panic(err)
	}
	v2, err = EvolveTable(v1, func(t *TableEditor) *TableEditor {
		return t.Add("extra", Int(contracts.Default(0))).DropIndex("by_tag")
	})
	if err != nil {
		panic(err)
	}
	return v1, v2
}

// batchEveryKind is a store with an outbox from its first commit holding every write kind: an archive store, then
// counted writes.
func batchEveryKind(t *testing.T) *ChordStore {
	t.Helper()
	countsV1, countsV2 := batchCounts()
	store := archiveStore(t, true)
	counts := tableIn(t, store, countsV1)
	var staged []StagedWrite
	changes := NewTableChanges(counts, &staged)
	for i := range 10 {
		tag := "odd"
		if i%2 == 0 {
			tag = "even"
		}
		noError(t, changes.Put(Row{"id": fmt.Sprintf("c%d", i), "n": int64(i), "tag": tag}))
	}
	_, err := store.Commit(staged, openFence, background)
	noError(t, err)
	var bulk []StagedWrite
	more := NewTableChanges(counts, &bulk)
	noError(t, more.Update(Row{"id": "c1"}, func(c ColumnRefs) SetOf { return SetOf{Assign("n", Add(c["n"], int64(10)))} }))
	noError(t, more.Delete(Row{"id": "c2"}))
	noError(t, more.UpdateWhere(func(c ColumnRefs) Condition { return Prefix(c["tag"], "ev") }, SetOf{Assign("tag", "EVEN")}))
	noError(t, more.DeleteWhere(func(c ColumnRefs) Condition { return Lt(c["n"], int64(1)) }))
	_, err = store.Commit(bulk, openFence, background)
	noError(t, err)
	noError(t, PrepareTables([]DeclaredTable{{Definition: countsV2, Table: "app.store:counts"}}, store, openFence, "app.store", background))
	var created []int64
	fence := CommitFence{Admit: func() bool { return true }, Adopt: func(_ int64, ids []int64) error {
		created = append(created, ids...)
		return nil
	}}
	address := DocumentAddress{Owner: "app.store", Namespace: "chord", Kind: "replayed", Key: new("x")}
	base := DocumentBase{Version: 1, Fingerprint: "f1", Value: delta.JsonObjectOf("n", float64(0))}
	_, err = store.Commit([]StagedWrite{CreateDocument(address, base, `{"n":0}`, nil)}, fence, background)
	noError(t, err)
	ops := []delta.Op{{"s", []any{"n"}, float64(1)}}
	content := DocumentContent{Kind: "delta", Version: 1, Ops: ops}
	_, err = store.Commit([]StagedWrite{ChangeDocument(created[0], 1, content, `[["s",["n"],1]]`)}, openFence, background)
	noError(t, err)
	_, err = store.Commit([]StagedWrite{RetireDocument(created[0], 2)}, openFence, background)
	noError(t, err)
	batchDurableShaped(t, store)
	return store
}

// batchDurableShaped is what only an adapter writes (Durable's commits): a rewindable creation retired in its own
// commit (an empty lifetime), and a change with the retirement of the same record in one commit.
func batchDurableShaped(t *testing.T, store *ChordStore) {
	t.Helper()
	base := DocumentBase{Version: 1, Fingerprint: "", Value: delta.JsonObjectOf("n", float64(0))}
	// made is a record created at seq, retired then too when retired.
	made := func(id int64, key string, seq int64, retired bool) DocumentRecord {
		record := DocumentRecord{
			ID: id, Owner: "app.durable", Namespace: "session", Kind: "notes", Key: &key, History: HistoryRewindable,
			CreatedAt: seq, Meta: new("{}"), Revision: 1, Version: 1, Fingerprint: "",
		}
		if retired {
			record.RetiredAt = &seq
		}
		return record
	}
	create := func(key string, retired bool) StagedWrite {
		return stagedWrite{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
			id, err := step.IDs.Mint()
			if err != nil {
				return nil, err
			}
			record := made(id, key, step.Seq, retired)
			if err := InsertDocument(step, record, new(`{"n":0}`)); err != nil {
				return nil, err
			}
			return []StoreWrite{&DocumentCreate{Record: record, Base: &base}}, nil
		}}
	}
	_, err := store.Commit([]StagedWrite{create("empty", true), create("kept", false)}, openFence, background)
	noError(t, err)
	row, err := Read(store, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
		return executor.Get("SELECT id FROM chord_documents WHERE key = 'kept'")
	}, background)
	noError(t, err)
	kept := row["id"].(int64)
	ops := []delta.Op{{"s", []any{"n"}, float64(1)}}
	changeAndRetire := stagedWrite{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
		record, err := ReadDocument(step, kept)
		if err != nil {
			return nil, err
		}
		content := DocumentContent{Kind: "delta", Version: 1, Ops: ops}
		if err := WriteContent(step, kept, HistoryRewindable, 1, content, `[["s",["n"],1]]`); err != nil {
			return nil, err
		}
		if err := RetireRecord(step, *record); err != nil {
			return nil, err
		}
		return []StoreWrite{&DocumentChange{ID: kept, Revision: 1, Content: content}, &DocumentRetire{ID: kept, Revision: 2}}, nil
	}}
	_, err = store.Commit([]StagedWrite{changeAndRetire}, openFence, background)
	noError(t, err)
}

// batchCommits is every commit of store's outbox.
func batchCommits(t *testing.T, store *ChordStore) []OutboxCommit {
	t.Helper()
	commits, err := store.Outbox().Read(store.Outbox().Floor(), 10_000, background)
	noError(t, err)
	return commits
}

// batchTexts is commits as text: seq, next id and the writes' JSON.
func batchTexts(t *testing.T, commits []OutboxCommit) []string {
	t.Helper()
	texts := make([]string, len(commits))
	for index, commit := range commits {
		writes, err := jsstring.MarshalJSON(commit.Writes)
		noError(t, err)
		texts[index] = fmt.Sprintf("%d %d %s", commit.Seq, commit.NextID, writes)
	}
	return texts
}

// batchChanged is commits with each write passed through change (Pi's `structuredClone` and a mutation): the writes
// it returns replace them, the commits and the original writes are left as they are.
func batchChanged(commits []OutboxCommit, change func(commit int, write StoreWrite) StoreWrite) []OutboxCommit {
	changed := make([]OutboxCommit, len(commits))
	for index, commit := range commits {
		writes := make([]StoreWrite, len(commit.Writes))
		for position, write := range commit.Writes {
			writes[position] = change(index, write)
		}
		changed[index] = OutboxCommit{Seq: commit.Seq, NextID: commit.NextID, Writes: writes}
	}
	return changed
}

// batchDiverging is commits whose `updateWhere` writes record one count too many.
func batchDiverging(commits []OutboxCommit) []OutboxCommit {
	return batchChanged(commits, func(_ int, write StoreWrite) StoreWrite {
		if update, ok := write.(*UpdateWhereWrite); ok {
			counted := *update
			counted.Count++
			return &counted
		}
		return write
	})
}

// batchRejected is the error the settled batch failed with; it fails the test when it succeeded.
func batchRejected(t *testing.T, settled <-chan chordctx.Settled[struct{}]) error {
	t.Helper()
	_, err := settledValue(t, settled)
	if err == nil {
		t.Fatal("the batch applied; expected a rejection")
	}
	return err
}

func TestApplyBatch(t *testing.T) {
	t.Run("replay", func(t *testing.T) {
		t.Run("a store's outbox replayed into an empty store gives the same archive, manifest included", func(t *testing.T) {
			source := batchEveryKind(t)
			all := batchCommits(t, source)
			var kinds []string
			for _, commit := range all {
				for _, write := range commit.Writes {
					if !slices.Contains(kinds, write.StoreWriteKind()) {
						kinds = append(kinds, write.StoreWriteKind())
					}
				}
			}
			slices.Sort(kinds)
			want := []string{
				"alter", "document.change", "document.create", "document.retire", "dropIndex", "index", "put", "table", "update",
				"updateWhere", "delete", "deleteWhere",
			}
			slices.Sort(want)
			if !reflect.DeepEqual(kinds, want) {
				t.Fatalf("kinds = %v, want %v", kinds, want)
			}
			replica := memoryStore(t)
			noError(t, ApplyBatch(replica, all[:3], background))
			noError(t, ApplyBatch(replica, all[3:], background))
			if got, want := partTexts(t, exported(t, replica, background)), partTexts(t, exported(t, source, background)); !reflect.DeepEqual(got, want) {
				t.Fatalf("the replica's archive differs from the source's:\n%v\nwant\n%v", got, want)
			}
			if replica.LastSeq() != source.LastSeq() || replica.NextID() != source.NextID() {
				t.Fatalf("[lastSeq, nextId] = [%d, %d], want [%d, %d]", replica.LastSeq(), replica.NextID(), source.LastSeq(), source.NextID())
			}
		})

		t.Run("restores the allocator: a replica mints above every id it holds", func(t *testing.T) {
			source := batchEveryKind(t)
			replica := memoryStore(t)
			noError(t, ApplyBatch(replica, batchCommits(t, source), background))
			minted, err := replica.MintId()
			noError(t, err)
			if minted != source.NextID() {
				t.Fatalf("mint = %d, want %d", minted, source.NextID())
			}
		})

		t.Run("writes the applied commits to the replica's own outbox when it has one", func(t *testing.T) {
			source := batchEveryKind(t)
			all := batchCommits(t, source)
			replica := memoryStore(t, true)
			noError(t, ApplyBatch(replica, all, background))
			if got, want := batchTexts(t, batchCommits(t, replica)), batchTexts(t, all); !reflect.DeepEqual(got, want) {
				t.Fatalf("the replica's outbox differs from the source's:\n%v\nwant\n%v", got, want)
			}
		})
	})

	t.Run("refusals", func(t *testing.T) {
		t.Run("while an owner holds a claim on the store (its memory would go stale)", func(t *testing.T) {
			replica := memoryStore(t)
			noError(t, replica.Claims.Documents("app.store", "chord", &NamedClaimant{Label: "an object"}))
			rejected(t, ApplyBatch(replica, nil, background), data.CodeInvalid, data.ReasonArgs)
		})

		t.Run("a claim taken between the call and its turn in the queue refuses the batch; a claim is refused while it applies", func(t *testing.T) {
			replica, arm := gatedStore(t)
			held := <-replica.Take(func() {})
			replaying := ApplyBatchAsync(replica, nil, background)
			owner := &NamedClaimant{Label: "an object"}
			noError(t, replica.Claims.Documents("app.store", "chord", owner))
			held()
			rejected(t, batchRejected(t, replaying), data.CodeInvalid, data.ReasonArgs)
			if replica.LastSeq() != 0 {
				t.Fatalf("lastSeq = %d, want 0", replica.LastSeq())
			}
			replica.Claims.Release(owner)
			all := batchCommits(t, batchEveryKind(t))
			gate := arm()
			applying := ApplyBatchAsync(replica, all, background)
			// Once the batch applies (its transaction is held open), a claim is refused.
			<-gate.Entered()
			rejected(t, replica.Claims.Documents("app.store", "chord", owner), data.CodeInvalid, data.ReasonArgs)
			gate.Release()
			_, err := settledValue(t, applying)
			noError(t, err)
			if replica.LastSeq() != all[len(all)-1].Seq {
				t.Fatalf("lastSeq = %d, want %d", replica.LastSeq(), all[len(all)-1].Seq)
			}
			noError(t, replica.Claims.Documents("app.store", "chord", owner))
		})

		t.Run("a batch queued behind one that diverges is refused at its turn", func(t *testing.T) {
			all := batchCommits(t, batchEveryKind(t))
			replica := memoryStore(t)
			first := ApplyBatchAsync(replica, batchDiverging(all), background)
			second := ApplyBatchAsync(replica, all, background)
			if err := batchRejected(t, first); !strings.Contains(err.Error(), "recorded") {
				t.Fatalf("diverging batch = %v, want recorded", err)
			}
			rejected(t, batchRejected(t, second), data.CodeInvalid, data.ReasonArgs)
			if replica.LastSeq() != 0 {
				t.Fatalf("lastSeq = %d, want 0", replica.LastSeq())
			}
		})

		t.Run("a commit whose next id is not above a document it creates (a saturated id aside)", func(t *testing.T) {
			replica := memoryStore(t)
			record := DocumentRecord{
				ID: 100, Owner: "app.store", Namespace: "chord", Kind: "x", History: HistoryRewindable, CreatedAt: 1, Revision: 1,
				Version: 1, Fingerprint: "",
			}
			base := DocumentBase{Version: 1, Fingerprint: "", Value: delta.NewJsonObject(0)}
			create := &DocumentCreate{Record: record, Base: &base}
			rejected(t, ApplyBatch(replica, []OutboxCommit{{Seq: 1, NextID: 2, Writes: []StoreWrite{create}}}, background), data.CodeInvalid, data.ReasonArgs)
			rejected(t, ApplyBatch(replica, []OutboxCommit{{Seq: 1, NextID: 100, Writes: []StoreWrite{create}}}, background), data.CodeInvalid, data.ReasonArgs)
			noError(t, ApplyBatch(replica, []OutboxCommit{{Seq: 1, NextID: 101, Writes: []StoreWrite{create}}}, background))
			saturated := &DocumentCreate{Record: record, Base: &base}
			saturated.Record.ID = MaxSafeInteger
			saturated.Record.Key = new("max")
			noError(t, ApplyBatch(replica, []OutboxCommit{{Seq: 2, NextID: MaxSafeInteger, Writes: []StoreWrite{saturated}}}, background))
			if replica.LastSeq() != 2 {
				t.Fatalf("lastSeq = %d, want 2", replica.LastSeq())
			}
		})

		t.Run("a first commit that does not follow lastSeq, or one that does not follow the one before; nothing applied", func(t *testing.T) {
			all := batchCommits(t, batchEveryKind(t))
			replica := memoryStore(t)
			rejected(t, ApplyBatch(replica, all[1:], background), data.CodeInvalid, data.ReasonArgs)
			rejected(t, ApplyBatch(replica, []OutboxCommit{all[0], all[2]}, background), data.CodeInvalid, data.ReasonArgs)
			if replica.LastSeq() != 0 {
				t.Fatalf("lastSeq = %d, want 0", replica.LastSeq())
			}
			noError(t, ApplyBatch(replica, all, background))
			if replica.LastSeq() != all[len(all)-1].Seq {
				t.Fatalf("lastSeq = %d, want %d", replica.LastSeq(), all[len(all)-1].Seq)
			}
		})

		t.Run("a write the records refuse rolls the batch back, and the store goes on", func(t *testing.T) {
			all := batchCommits(t, batchEveryKind(t))
			replica := memoryStore(t)
			broken := batchChanged(all, func(commit int, write StoreWrite) StoreWrite {
				if table, ok := write.(*TableDefinitionWrite); ok && commit == 0 {
					renamed := *table
					renamed.Table = "no:such:table"
					return &renamed
				}
				return write
			})
			if _, ok := all[0].Writes[0].(*TableDefinitionWrite); !ok {
				t.Fatalf("the first write of the first commit is %T, want a table write", all[0].Writes[0])
			}
			rejected(t, ApplyBatch(replica, broken, background), data.CodeInvalid, data.ReasonArgs)
			forged := batchChanged(all, func(_ int, write StoreWrite) StoreWrite { return write })
			forged[0].Writes = append(forged[0].Writes, &PutWrite{Table: "x", Row: delta.NewJsonObject(0), Version: 0, Columns: delta.NewJsonObject(0)})
			rejected(t, ApplyBatch(replica, forged, background), data.CodeInvalid, data.ReasonArgs)
			noError(t, ApplyBatch(replica, all, background))
		})

		t.Run("a recorded count the store does not reproduce stops applying and marks the store diverged (S17)", func(t *testing.T) {
			all := batchCommits(t, batchEveryKind(t))
			if !slices.ContainsFunc(all, func(commit OutboxCommit) bool {
				return slices.ContainsFunc(commit.Writes, func(write StoreWrite) bool { return write.StoreWriteKind() == "updateWhere" })
			}) {
				t.Fatal("no commit has an updateWhere")
			}
			replica := memoryStore(t)
			if err := ApplyBatch(replica, batchDiverging(all), background); err == nil || !strings.Contains(err.Error(), "recorded") {
				t.Fatalf("diverging batch = %v, want recorded", err)
			}
			rejected(t, ApplyBatch(replica, all, background), data.CodeInvalid, data.ReasonArgs)
			if replica.LastSeq() != 0 {
				t.Fatalf("lastSeq = %d, want 0", replica.LastSeq())
			}
		})
	})
}
