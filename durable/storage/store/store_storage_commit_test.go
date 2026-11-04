package store

// pi: packages/durable/src/storage/store/tables.ts
// The refusals below run through EncodeIndexed, which no public entry point of Pi exports; its mutant in portmap-mutants.json fails them.

// Ports packages/durable/test/store-storage-commit.test.ts
//
// StoreStorage's commit path (`Storage.Commit` over `ChordStore.CommitSeries`), p3's primary storage: per call one
// backend transaction with one seq per batch; Durable's checks of a batch refuse it in its turn, the batches before it
// stored; a failed statement rolls the call back and rejects, the store going on; a failed `COMMIT` stops the store;
// receipts after `COMMIT`; each commit published in a host batch of its own; other users of the store wait their turn.
//
// "publishes each commit of a call in a host batch of its own, after the call's one transaction" needs a Session over this
// storage and runs in durable/session/store_storage_publication_test.go. "refuses a batch whose rows staging cannot encode"
// sends the first ID beyond the safe integers where Pi sends 1.5 (an EntryId is an int64). The GC case's WeakRef is a weak.Pointer.

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/durable"
	dstorage "github.com/MichaelKinsy/PiG/durable/storage"
)

// outboxSeqs is the store's outbox commits, by seq.
func outboxSeqs(t *testing.T, store *chordstore.ChordStore) []int64 {
	t.Helper()
	rows := value(chordstore.Read(store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All("SELECT seq FROM chord_outbox")
	}, background))
	seqs := []int64{}
	for _, row := range rows {
		seqs = append(seqs, value(sqlite.RowInt(row, "seq")))
	}
	return seqs
}

func storageFailed(t *testing.T, err error) {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != data.CodeUnavailable || chordErr.Reason != data.ReasonStorageFailed {
		t.Fatalf("error = %v, want unavailable/storage_failed", err)
	}
}

func batchesOf(ids []durable.EntryId) [][]durable.StorageWrite {
	batches := make([][]durable.StorageWrite, len(ids))
	for index, id := range ids {
		batches[index] = []durable.StorageWrite{entryWrite(id)}
	}
	return batches
}

type receipt struct {
	index int
	seq   durable.Seq
}

func TestStoreStorageOneTransactionPerCall(t *testing.T) {
	t.Run("stores the batches in one backend transaction, one seq and one outbox commit each, receipts in order", func(t *testing.T) {
		made := counted(t, ":memory:")
		ids := []durable.EntryId{mintID[durable.EntryId](t, made.storage), mintID[durable.EntryId](t, made.storage), mintID[durable.EntryId](t, made.storage)}
		before, _ := made.counts.snapshot()
		last := made.store.LastSeq()
		var receipts []receipt
		result, err := made.storage.Commit(background, batchesOf(ids), func(index int, seq durable.Seq) { receipts = append(receipts, receipt{index, seq}) })
		must(t, err)
		if result.Stored != 3 || result.Refused != nil {
			t.Fatalf("result = %+v, want stored 3", result)
		}
		if after, _ := made.counts.snapshot(); after-before != 1 {
			t.Fatalf("transactions = %d, want 1", after-before)
		}
		want := []receipt{{0, durable.Seq(last + 1)}, {1, durable.Seq(last + 2)}, {2, durable.Seq(last + 3)}}
		if !reflect.DeepEqual(receipts, want) {
			t.Fatalf("receipts = %v, want %v", receipts, want)
		}
		for index, id := range ids {
			if found := value(made.storage.Entry(background, id)); found == nil || int64(found.CommitSeq) != last+1+int64(index) {
				t.Fatalf("entry %d at %+v", id, found)
			}
		}
		rows := value(chordstore.Read(made.store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
			return executor.All("SELECT seq FROM chord_outbox WHERE seq > ?", last)
		}, background))
		if len(rows) != 3 {
			t.Fatalf("outbox rows = %v, want 3", rows)
		}
		must(t, made.store.Close(background))
	})

	t.Run("writes the metadata once per call: three statements per entry append, plus one (cost guard)", func(t *testing.T) {
		made := counted(t, ":memory:")
		meter := newMeter(t)
		// Alone, an append is its id check, its row and claim, its outbox commit, and the metadata.
		_, err := durableCommit(made.storage, meter, entryWrite(mintID[durable.EntryId](t, made.storage)))
		must(t, err)
		if got := meter.statements(); got != 5 {
			t.Fatalf("statements of one append = %v, want 5", got)
		}
		ids := []durable.EntryId{mintID[durable.EntryId](t, made.storage), mintID[durable.EntryId](t, made.storage), mintID[durable.EntryId](t, made.storage)}
		before, _ := made.counts.snapshot()
		_, err = made.storage.Commit(meter.ctx, batchesOf(ids), func(int, durable.Seq) {})
		must(t, err)
		after, _ := made.counts.snapshot()
		if got := [2]float64{float64(after - before), meter.statements() - 5}; got != [2]float64{1, 3*4 + 1} {
			t.Fatalf("[transactions, statements] = %v, want [1 13]", got)
		}
		must(t, made.store.Close(background))
	})
}

// durableCommit is commitWrites under meter's context.
func durableCommit(storage *StoreStorage, meter *meter, writes ...durable.StorageWrite) (durable.Seq, error) {
	var seq durable.Seq
	result, err := storage.Commit(meter.ctx, [][]durable.StorageWrite{writes}, func(_ int, received durable.Seq) { seq = received })
	if err == nil && result.Refused != nil {
		err = result.Refused
	}
	return seq, err
}

func TestStoreStorageRefusals(t *testing.T) {
	t.Run("stores the batches before a refused one with Durable's message, none after it, and goes on", func(t *testing.T) {
		made := counted(t, ":memory:")
		first := mintID[durable.EntryId](t, made.storage)
		after := mintID[durable.EntryId](t, made.storage)
		last := made.store.LastSeq()
		var receipts []int
		result, err := made.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(first)}, {entryWrite(first, "again")}, {entryWrite(after)}},
			func(index int, _ durable.Seq) { receipts = append(receipts, index) })
		must(t, err)
		if result.Stored != 1 || result.Refused == nil || result.Refused.Error() != fmt.Sprintf("ID %d already belongs to entry", first) {
			t.Fatalf("result = %+v", result)
		}
		if !reflect.DeepEqual(receipts, []int{0}) {
			t.Fatalf("receipts = %v, want [0]", receipts)
		}
		if found := value(made.storage.Entry(background, first)); found == nil || found.Entry.Kind != "m" {
			t.Fatalf("first = %+v", found)
		}
		if found := value(made.storage.Entry(background, after)); found != nil {
			t.Fatalf("after = %+v, want none", found)
		}
		if made.store.LastSeq() != last+1 {
			t.Fatalf("lastSeq = %d, want %d", made.store.LastSeq(), last+1)
		}
		if seq := value(commitWrites(made.storage, entryWrite(after))); int64(seq) != last+2 {
			t.Fatalf("next seq = %d, want %d", seq, last+2)
		}
		must(t, made.store.Close(background))
	})

	t.Run("refuses a batch its staging fails in that batch's turn, keeping the batches before it (review)", func(t *testing.T) {
		made := counted(t, ":memory:")
		entry := mintID[durable.EntryId](t, made.storage)
		document := mintID[durable.DocumentId](t, made.storage)
		var receipts []int
		result, err := made.storage.Commit(background, [][]durable.StorageWrite{
			{entryWrite(entry)},
			// Staging refuses it: one document retired twice.
			{durable.DocumentRetireWrite{Id: document}, durable.DocumentRetireWrite{Id: document}},
		}, func(index int, _ durable.Seq) { receipts = append(receipts, index) })
		must(t, err)
		if result.Stored != 1 || result.Refused == nil || result.Refused.Error() != fmt.Sprintf("Document %d is retired more than once", document) {
			t.Fatalf("result = %+v", result)
		}
		if !reflect.DeepEqual(receipts, []int{0}) {
			t.Fatalf("receipts = %v, want [0]", receipts)
		}
		if value(made.storage.Entry(background, entry)) == nil {
			t.Fatal("the entry of the first batch is not stored")
		}
		must(t, made.store.Close(background))
	})

	for _, malformed := range []struct {
		name   string
		writes []durable.StorageWrite
	}{
		{"a duplicate retirement", []durable.StorageWrite{durable.DocumentRetireWrite{Id: 101}, durable.DocumentRetireWrite{Id: 101}}},
		{"a duplicate content command", []durable.StorageWrite{
			durable.DocumentChangeWrite{Id: 101, Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf()}},
			durable.DocumentChangeWrite{Id: 101, Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf()}},
		}},
	} {
		t.Run("refuses a later batch with "+malformed.name+" as Memory does, keeping the prefix (review)", func(t *testing.T) {
			batches := [][]durable.StorageWrite{
				{durable.ConversationWrite{Value: durable.ConversationRecord{Id: 100}}},
				malformed.writes,
			}
			type outcome struct {
				Stored       int
				Refused      string
				Receipts     []int
				Conversation *durable.ConversationRecord
			}
			var outcomes []outcome
			for _, storage := range []durable.Storage{dstorage.NewMemoryStorage(), openStoreStorage(t, ":memory:")} {
				receipts := []int{}
				result, err := storage.Commit(background, batches, func(index int, _ durable.Seq) { receipts = append(receipts, index) })
				must(t, err)
				found := value(storage.Conversation(background, 100))
				outcomes = append(outcomes, outcome{result.Stored, fmt.Sprint(result.Refused), receipts, found})
				must(t, storage.Close(background))
			}
			if outcomes[0].Stored != 1 || !reflect.DeepEqual(outcomes[0].Receipts, []int{0}) || outcomes[0].Conversation == nil || outcomes[0].Conversation.Id != 100 {
				t.Fatalf("Memory's outcome = %+v", outcomes[0])
			}
			if jsonOf(t, outcomes[1]) != jsonOf(t, outcomes[0]) {
				t.Fatalf("StoreStorage's outcome = %s, want Memory's %s", jsonOf(t, outcomes[1]), jsonOf(t, outcomes[0]))
			}
		})
	}

	for _, refusal := range []struct {
		name    string
		calls   func(id durable.DocumentId) [][]durable.StorageWrite
		message string
	}{
		{"a change of a document an earlier batch retired", func(id durable.DocumentId) [][]durable.StorageWrite {
			return [][]durable.StorageWrite{
				{durable.DocumentRetireWrite{Id: id}},
				{durable.DocumentChangeWrite{Id: id, Content: durable.DocumentContent{Kind: durable.ContentBase, Version: 1, Value: delta.JsonObjectOf("n", float64(2))}}},
			}
		}, "is retired"},
		{"a creation at the address an earlier batch took", func(id durable.DocumentId) [][]durable.StorageWrite {
			key := "taken"
			create := func(id durable.DocumentId, n float64) durable.StorageWrite {
				return durable.DocumentCreateWrite{
					Record:  durable.DocumentCreate{Id: id, Kind: "n", Key: &key, Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
					Content: base(n),
				}
			}
			return [][]durable.StorageWrite{{create(id+1, 2)}, {create(id+2, 3)}}
		}, "already has a current incarnation"},
	} {
		t.Run("refuses "+refusal.name+" against the state the batches before it left", func(t *testing.T) {
			made := counted(t, ":memory:")
			document := durable.DocumentId(200)
			value(commitWrites(made.storage, durable.DocumentCreateWrite{
				Record:  durable.DocumentCreate{Id: document, Kind: "n", Scope: durable.DocumentRecordScope{Kind: durable.ScopeSession}},
				Content: base(1),
			}))
			last := made.store.LastSeq()
			var receipts []int
			result, err := made.storage.Commit(background, refusal.calls(document), func(index int, _ durable.Seq) { receipts = append(receipts, index) })
			must(t, err)
			if result.Stored != 1 || result.Refused == nil || !strings.Contains(result.Refused.Error(), refusal.message) {
				t.Fatalf("result = %+v, want stored 1 refused %q", result, refusal.message)
			}
			if !reflect.DeepEqual(receipts, []int{0}) || made.store.LastSeq() != last+1 {
				t.Fatalf("[receipts, lastSeq] = [%v, %d], want [[0], %d]", receipts, made.store.LastSeq(), last+1)
			}
			seqs := outboxSeqs(t, made.store)
			if seqs[len(seqs)-1] != last+1 {
				t.Fatalf("outbox seqs = %v, want to end at %d", seqs, last+1)
			}
			must(t, made.store.Close(background))
		})
	}

	t.Run("refuses a batch whose rows staging cannot encode, keeping the batches before it", func(t *testing.T) {
		made := counted(t, ":memory:")
		entry := mintID[durable.EntryId](t, made.storage)
		var receipts []int
		// An EntryId is an int64, so Pi's 1.5 cannot be sent: an ID beyond the safe integers (2^53 - 1) is the integer the
		// store's int columns refuse at staging, as they refuse 1.5. The Session never sends one (Storage trusts it).
		unsafe := durable.EntryId(1 << 53)
		result, err := made.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(entry)}, {entryWrite(unsafe)}},
			func(index int, _ durable.Seq) { receipts = append(receipts, index) })
		must(t, err)
		if result.Stored != 1 || result.Refused == nil || !strings.Contains(result.Refused.Error(), `column "id": 9007199254740992 is not a int value`) {
			t.Fatalf("result = %+v, want stored 1 refused with the int column's refusal", result)
		}
		if !reflect.DeepEqual(receipts, []int{0}) {
			t.Fatalf("receipts = %v, want [0]", receipts)
		}
		if value(made.storage.Entry(background, entry)) == nil {
			t.Fatal("the entry of the first batch is not stored")
		}
		must(t, made.store.Close(background))
	})

	t.Run("checks a batch against the batches before it in the call: a copy reads their state", func(t *testing.T) {
		made := counted(t, ":memory:")
		source := mintID[durable.DocumentId](t, made.storage)
		child := mintID[durable.ConversationId](t, made.storage)
		copied := mintID[durable.DocumentId](t, made.storage)
		record := func(id durable.DocumentId, conversationID durable.ConversationId) durable.DocumentCreate {
			return durable.DocumentCreate{
				Id: id, Kind: "c", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationID},
				History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
			}
		}
		result, err := made.storage.Commit(background, [][]durable.StorageWrite{
			{durable.DocumentCreateWrite{Record: record(source, durable.ROOT_CONVERSATION_ID), Content: base(1)}},
			{durable.DocumentChangeWrite{Id: source, Content: deltaTo(2)}},
			{
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: child}},
				durable.DocumentCopyWrite{Record: record(copied, child), Source: durable.DocumentCopySource{Id: source, At: durable.CurrentPoint}},
			},
		}, func(int, durable.Seq) {})
		must(t, err)
		if result.Stored != 3 || result.Refused != nil {
			t.Fatalf("result = %+v, want stored 3", result)
		}
		if found := value(made.storage.Document(background, copied, durable.CurrentPoint)); found == nil || jsonOf(t, found.Value) != `{"n":2}` {
			t.Fatalf("copy = %+v", found)
		}
		must(t, made.store.Close(background))
	})
}

// chain is every error in err's cause chain, as text.
func chain(err error) []string {
	var messages []string
	for ; err != nil; err = errors.Unwrap(err) {
		messages = append(messages, err.Error())
	}
	return messages
}

func TestStoreStorageFailures(t *testing.T) {
	t.Run("rejects with nothing stored when a batch's check fails in the backend, and the store goes on", func(t *testing.T) {
		made := counted(t, ":memory:")
		first, second := mintID[durable.EntryId](t, made.storage), mintID[durable.EntryId](t, made.storage)
		last := made.store.LastSeq()
		// The second batch's id check: its first read of the record ids.
		claimReads := 0
		made.db.setFaults(faults{fail: func(sql string, _ int) bool {
			if !strings.Contains(sql, "record_ids") {
				return false
			}
			claimReads++
			return claimReads == 2
		}})
		var receipts []int
		_, err := made.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(first)}, {entryWrite(second)}}, func(index int, _ durable.Seq) { receipts = append(receipts, index) })
		if err == nil || !strings.Contains(err.Error(), "injected") {
			t.Fatalf("Commit error = %v, want injected", err)
		}
		if len(receipts) != 0 || made.store.LastSeq() != last {
			t.Fatalf("[receipts, lastSeq] = [%v, %d], want [[], %d]", receipts, made.store.LastSeq(), last)
		}
		if found := value(made.storage.Entry(background, first)); found != nil {
			t.Fatalf("entry = %+v, want none", found)
		}
		if seq := value(commitWrites(made.storage, entryWrite(first))); int64(seq) != last+1 {
			t.Fatalf("next seq = %d, want %d", seq, last+1)
		}
		must(t, made.store.Close(background))
	})

	t.Run("a failed COMMIT is an unknown outcome: the call rejects storage_failed, nothing is received or stored", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "store.sqlite")
		made := counted(t, path)
		ids := []durable.EntryId{mintID[durable.EntryId](t, made.storage), mintID[durable.EntryId](t, made.storage)}
		last := made.store.LastSeq()
		made.db.setFaults(faults{commit: true})
		var receipts []int
		_, err := made.storage.Commit(background, batchesOf(ids), func(index int, _ durable.Seq) { receipts = append(receipts, index) })
		storageFailed(t, err)
		if len(receipts) != 0 {
			t.Fatalf("receipts = %v, want none", receipts)
		}
		_, err = made.storage.Entry(background, ids[0])
		storageFailed(t, err)
		must(t, made.store.Close(background))
		store, storage := reopen(t, path)
		if found := value(storage.Entry(background, ids[0])); found != nil {
			t.Fatalf("entry after reopen = %+v, want none", found)
		}
		if seq := value(commitWrites(storage, entryWrite(ids[1]))); int64(seq) != last+1 {
			t.Fatalf("next seq = %d, want %d", seq, last+1)
		}
		must(t, store.Close(background))
	})

	// The record the audit's source document and its copy share.
	auditRecord := func(id durable.DocumentId, conversationID durable.ConversationId) durable.DocumentCreate {
		return durable.DocumentCreate{
			Id: id, Kind: "audit", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: conversationID},
			History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
		}
	}
	// The source document, stored before the call.
	seed := durable.DocumentCreateWrite{Record: auditRecord(100, durable.ROOT_CONVERSATION_ID), Content: base(0)}
	setN := func(n float64) durable.StorageWrite {
		return durable.DocumentChangeWrite{Id: 100, Content: deltaTo(n)}
	}
	// Three batches: an entry and a delta; a conversation, a copy of the source as the first batch left it and an
	// entry; then an entry and a delta, or, refused, an entry whose id the first batch took.
	batches := func(third string) [][]durable.StorageWrite {
		last := []durable.StorageWrite{entryWrite(104, "b3"), setN(2)}
		if third == "refused" {
			last = []durable.StorageWrite{entryWrite(101, "again")}
		}
		return [][]durable.StorageWrite{
			{entryWrite(101, "b1"), setN(1)},
			{
				durable.ConversationWrite{Value: durable.ConversationRecord{Id: 105}},
				durable.DocumentCopyWrite{Record: auditRecord(102, 105), Source: durable.DocumentCopySource{Id: 100, At: durable.CurrentPoint}},
				entryWrite(103, "b2"),
			},
			last,
		}
	}
	// stateOf is what a storage holds of the audit's records, as JSON.
	stateOf := func(storage durable.Storage) string {
		var entries []any
		for _, id := range []durable.EntryId{101, 103, 104} {
			if found := value(storage.Entry(background, id)); found != nil {
				entries = append(entries, found.Entry.Kind)
			} else {
				entries = append(entries, nil)
			}
		}
		documentValue := func(id durable.DocumentId) any {
			if found := value(storage.Document(background, id, durable.CurrentPoint)); found != nil {
				return found.Value
			}
			return nil
		}
		var child any
		if found := value(storage.Conversation(background, 105)); found != nil {
			child = found
		}
		return jsonOf(t, map[string]any{"entries": entries, "source": documentValue(100), "copy": documentValue(102), "child": child})
	}
	// outcomeOf is what a call returned and what it left: its result, its receipts' seqs after the store's last one,
	// the state.
	outcomeOf := func(storage durable.Storage, third string, last int64) (stored int, refused string, seqs []int64, state string) {
		seqs = []int64{}
		result, err := storage.Commit(background, batches(third), func(_ int, seq durable.Seq) { seqs = append(seqs, int64(seq)-last) })
		must(t, err)
		return result.Stored, fmt.Sprint(result.Refused), seqs, stateOf(storage)
	}

	for _, third := range []string{"valid", "refused"} {
		t.Run("rolls a 3-batch call back at a failure of any of its statements, as at a failed COMMIT, and reopens before it (third "+third+")", func(t *testing.T) {
			// What MemoryStorage makes of the call: the outcome every retry must reach.
			memory := dstorage.NewMemoryStorage()
			value(commitWrites(memory, rootWrite()))
			value(commitWrites(memory, seed))
			expectedStored, expectedRefused, expectedSeqs, expectedState := outcomeOf(memory, third, 2)
			wantStored, wantSeqs := 3, []int64{1, 2, 3}
			if third == "refused" {
				wantStored, wantSeqs = 2, []int64{1, 2}
			}
			if expectedStored != wantStored || !reflect.DeepEqual(expectedSeqs, wantSeqs) {
				t.Fatalf("Memory's outcome = %d %v, want %d %v", expectedStored, expectedSeqs, wantStored, wantSeqs)
			}
			// The statements of the call's transaction (a read transaction after it would count its own).
			probe := counted(t, ":memory:")
			value(commitWrites(probe.storage, seed))
			result, err := probe.storage.Commit(background, batches(third), func(int, durable.Seq) {})
			must(t, err)
			_, total := probe.counts.snapshot()
			// Its cost: no read beyond a batch's checks, the metadata once (statement counts per batch in the cost guard).
			wantTotal := 29
			if third == "refused" {
				wantTotal = 23
			}
			if total != wantTotal {
				t.Fatalf("statements of the call = %d, want %d", total, wantTotal)
			}
			if result.Stored != expectedStored {
				t.Fatalf("stored = %d, want %d", result.Stored, expectedStored)
			}
			if got := stateOf(probe.storage); got != expectedState {
				t.Fatalf("state = %s, want %s", got, expectedState)
			}
			must(t, probe.store.Close(background))
			// Each statement in turn, then the COMMIT.
			for at := 1; at <= total+1; at++ {
				path := filepath.Join(t.TempDir(), fmt.Sprintf("store-%d.sqlite", at))
				opened := counted(t, path)
				value(commitWrites(opened.storage, seed))
				last := opened.store.LastSeq()
				outbox := outboxSeqs(t, opened.store)
				before := stateOf(opened.storage)
				if at <= total {
					opened.db.setFaults(faults{fail: func(_ string, index int) bool { return index == at }})
				} else {
					opened.db.setFaults(faults{commit: true})
				}
				var receipts []int
				_, err := opened.storage.Commit(background, batches(third), func(index int, _ durable.Seq) { receipts = append(receipts, index) })
				if len(receipts) != 0 || opened.store.LastSeq() != last {
					t.Fatalf("at %d: [receipts, lastSeq] = [%v, %d], want [[], %d]", at, receipts, opened.store.LastSeq(), last)
				}
				if at <= total {
					// A rollback: the call rejects with its failure, the store goes on.
					if !contains(chain(err), fmt.Sprintf("injected: statement %d failed", at)) {
						t.Fatalf("at %d: error chain = %v", at, chain(err))
					}
					if got := stateOf(opened.storage); got != before {
						t.Fatalf("at %d: state = %s, want %s", at, got, before)
					}
				} else {
					storageFailed(t, err)
				}
				must(t, opened.store.Close(background))
				// On disk: as before the call, the seq, the outbox, the records and the allocator.
				again, againStorage := reopen(t, path)
				if again.LastSeq() != last || !reflect.DeepEqual(outboxSeqs(t, again), outbox) || stateOf(againStorage) != before {
					t.Fatalf("at %d: reopened [lastSeq, outbox, state] = [%d, %v, %s]", at, again.LastSeq(), outboxSeqs(t, again), stateOf(againStorage))
				}
				if minted := value(againStorage.MintId()); minted <= 100 {
					t.Fatalf("at %d: MintId = %d, want above 100", at, minted)
				}
				// A retry reaches MemoryStorage's outcome.
				stored, refused, seqs, state := outcomeOf(againStorage, third, last)
				if stored != expectedStored || refused != expectedRefused || !reflect.DeepEqual(seqs, expectedSeqs) || state != expectedState {
					t.Fatalf("at %d: retry = %d %q %v %s, want %d %q %v %s", at, stored, refused, seqs, state, expectedStored, expectedRefused, expectedSeqs, expectedState)
				}
				wantOutbox := append([]int64{}, outbox...)
				for _, seq := range expectedSeqs {
					wantOutbox = append(wantOutbox, last+seq)
				}
				if got := outboxSeqs(t, again); !reflect.DeepEqual(got, wantOutbox) {
					t.Fatalf("at %d: outbox = %v, want %v", at, got, wantOutbox)
				}
				must(t, again.Close(background))
			}
		})
	}

	t.Run("a call on a closed storage, or on a stopped store, rejects and stores nothing", func(t *testing.T) {
		made := counted(t, ":memory:")
		entry := mintID[durable.EntryId](t, made.storage)
		made.db.setFaults(faults{commit: true})
		_, err := commitWrites(made.storage, entryWrite(entry))
		storageFailed(t, err)
		_, err = made.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(entry)}}, func(int, durable.Seq) {})
		storageFailed(t, err)
		must(t, made.store.Close(background))
		other := counted(t, ":memory:")
		must(t, other.storage.Close(background))
		if _, err := other.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(entry)}}, func(int, durable.Seq) {}); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("Commit on a closed storage = %v", err)
		}
		must(t, other.store.Close(background))
	})

	t.Run("a call queued when the store closes rejects released, and stores nothing; the running one is stored", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "store.sqlite")
		made := counted(t, path)
		first, second := mintID[durable.EntryId](t, made.storage), mintID[durable.EntryId](t, made.storage)
		entered, release := make(chan struct{}), make(chan struct{})
		made.db.setFaults(faults{hold: func(_ string, index int) {
			if index == 1 {
				close(entered)
				<-release
			}
		}})
		type settled struct {
			result durable.CommitResult
			err    error
		}
		running, queued := make(chan settled, 1), make(chan settled, 1)
		go func() {
			result, err := made.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(first)}}, func(int, durable.Seq) {})
			running <- settled{result, err}
		}()
		<-entered
		go func() {
			result, err := made.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(second)}}, func(int, durable.Seq) {})
			queued <- settled{result, err}
		}()
		waitUnderway(t, made.storage, 2)
		closing := make(chan error, 1)
		go func() { closing <- made.store.Close(background) }()
		for made.store.AssertUsable() == nil {
			time.Sleep(time.Millisecond)
		}
		close(release)
		if got := <-running; got.err != nil || got.result.Stored != 1 {
			t.Fatalf("running = %+v", got)
		}
		got := <-queued
		chordErr, ok := data.IsChordError(got.err)
		if !ok || chordErr.Code != data.CodeReleased {
			t.Fatalf("queued = %+v, want released", got)
		}
		must(t, <-closing)
		store, storage := reopen(t, path)
		if value(storage.Entry(background, first)) == nil || value(storage.Entry(background, second)) != nil {
			t.Fatal("after reopen the running call is not stored alone")
		}
		must(t, store.Close(background))
	})
}

func contains(items []string, want string) bool {
	return slices.Contains(items, want)
}

// waitUnderway waits until storage has count calls underway.
func waitUnderway(t *testing.T, storage *StoreStorage, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		storage.mu.Lock()
		underway := storage.underway
		storage.mu.Unlock()
		if underway == count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("calls underway = %d, want %d", underway, count)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStoreStorageDurability(t *testing.T) {
	t.Run("keeps every batch of a stored call across a crash: reopened, the seqs, rows, documents and ids are there", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "store.sqlite")
		made := counted(t, path)
		entry := mintID[durable.EntryId](t, made.storage)
		document := mintID[durable.DocumentId](t, made.storage)
		record := durable.DocumentCreate{
			Id: document, Kind: "n", Scope: durable.DocumentRecordScope{Kind: durable.ScopeConversation, ConversationId: durable.ROOT_CONVERSATION_ID},
			History: durable.HistoryRewindable, Fork: durable.ForkAsOf,
		}
		var seqs []durable.Seq
		_, err := made.storage.Commit(background, [][]durable.StorageWrite{
			{entryWrite(entry)},
			{durable.DocumentCreateWrite{Record: record, Content: base(1)}},
			{durable.DocumentChangeWrite{Id: document, Content: deltaTo(2)}},
		}, func(_ int, seq durable.Seq) { seqs = append(seqs, seq) })
		must(t, err)
		minted := mintID[durable.EntryId](t, made.storage)
		// A crash: the store is never closed; another process opens the file.
		other, reopened := reopen(t, path)
		if found := value(reopened.Entry(background, entry)); found == nil || found.CommitSeq != seqs[0] {
			t.Fatalf("entry = %+v, want seq %d", found, seqs[0])
		}
		if found := value(reopened.Document(background, document, durable.AtSeq(seqs[1]))); found == nil || jsonOf(t, found.Value) != `{"n":1}` {
			t.Fatalf("document at %d = %+v", seqs[1], found)
		}
		if found := value(reopened.Document(background, document, durable.CurrentPoint)); found == nil || jsonOf(t, found.Value) != `{"n":2}` {
			t.Fatalf("document now = %+v", found)
		}
		if id := value(reopened.MintId()); id <= int64(document) {
			t.Fatalf("MintId = %d, want above %d", id, document)
		}
		if seq := value(commitWrites(reopened, entryWrite(minted))); seq != seqs[2]+1 {
			t.Fatalf("next seq = %d, want %d", seq, seqs[2]+1)
		}
		must(t, other.Close(background))
		must(t, made.store.Close(background))
	})
}

func TestStoreStorageOtherUsersOfTheStore(t *testing.T) {
	t.Run("another owner's call waits for a series in its transaction; the series' receipts come first", func(t *testing.T) {
		made := counted(t, ":memory:")
		other := value(Open(background, made.store, "other"))
		first, second := mintID[durable.EntryId](t, made.storage), mintID[durable.EntryId](t, made.storage)
		entered, release := make(chan struct{}), make(chan struct{})
		made.db.setFaults(faults{hold: func(_ string, index int) {
			if index == 2 {
				close(entered)
				<-release
			}
		}})
		var mu sync.Mutex
		var events []string
		record := func(text string) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, text)
		}
		var calls sync.WaitGroup
		calls.Go(func() {
			_, err := made.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(first)}, {entryWrite(second)}},
				func(index int, seq durable.Seq) { record(fmt.Sprintf("harness %d %d", index, seq)) })
			if err != nil {
				t.Error(err)
			}
		})
		<-entered
		calls.Go(func() {
			_, err := other.Commit(background, [][]durable.StorageWrite{{rootWrite()}},
				func(index int, seq durable.Seq) { record(fmt.Sprintf("other %d %d", index, seq)) })
			if err != nil {
				t.Error(err)
			}
		})
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		if len(events) != 0 {
			t.Fatalf("events before the release = %v", events)
		}
		mu.Unlock()
		last := made.store.LastSeq()
		close(release)
		calls.Wait()
		want := []string{fmt.Sprintf("harness 0 %d", last+1), fmt.Sprintf("harness 1 %d", last+2), fmt.Sprintf("other 0 %d", last+3)}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("events = %v, want %v", events, want)
		}
		seqs := outboxSeqs(t, made.store)
		if got := seqs[len(seqs)-3:]; !reflect.DeepEqual(got, []int64{last + 1, last + 2, last + 3}) {
			t.Fatalf("outbox tail = %v", got)
		}
		must(t, other.Close(background))
		must(t, made.store.Close(background))
	})

	t.Run("a read during a series sees all of it or none of it", func(t *testing.T) {
		made := counted(t, ":memory:")
		first, second := mintID[durable.EntryId](t, made.storage), mintID[durable.EntryId](t, made.storage)
		entered, release := make(chan struct{}), make(chan struct{})
		// Held after the first batch's writes, in the second batch's check.
		claimReads := 0
		made.db.setFaults(faults{hold: func(sql string, _ int) {
			if !strings.Contains(sql, "record_ids") {
				return
			}
			claimReads++
			if claimReads == 2 {
				close(entered)
				<-release
			}
		}})
		series := make(chan error, 1)
		go func() {
			_, err := made.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(first)}, {entryWrite(second)}}, func(int, durable.Seq) {})
			series <- err
		}()
		<-entered
		type read struct {
			found *durable.EntryAt
			err   error
		}
		reads := make(chan read, 2)
		for _, id := range []durable.EntryId{first, second} {
			go func() {
				found, err := made.storage.Entry(background, id)
				reads <- read{found, err}
			}()
		}
		time.Sleep(50 * time.Millisecond)
		close(release)
		must(t, <-series)
		for range 2 {
			got := <-reads
			must(t, got.err)
			if got.found == nil {
				t.Fatal("a read during the series saw only part of it")
			}
		}
		must(t, made.store.Close(background))
	})

	t.Run("an export holds the queue: a series waits for it and is not in its archive", func(t *testing.T) {
		made := counted(t, ":memory:")
		entry := mintID[durable.EntryId](t, made.storage)
		last := made.store.LastSeq()
		export, err := made.store.Export(background)
		must(t, err)
		part, ok, err := export.Next(background)
		must(t, err)
		manifest, isManifest := part.(*chordstore.ManifestPart)
		if !ok || !isManifest || manifest.LastSeq != last {
			t.Fatalf("first part = %+v", part)
		}
		settled := make(chan error, 1)
		go func() {
			result, err := made.storage.Commit(background, [][]durable.StorageWrite{{entryWrite(entry)}}, func(int, durable.Seq) {})
			if err == nil && result.Stored != 1 {
				err = fmt.Errorf("stored %d", result.Stored)
			}
			settled <- err
		}()
		select {
		case err := <-settled:
			t.Fatalf("the series settled while the export held the queue: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		must(t, export.Close())
		must(t, <-settled)
		if made.store.LastSeq() != last+1 {
			t.Fatalf("lastSeq = %d, want %d", made.store.LastSeq(), last+1)
		}
		must(t, made.store.Close(background))
	})
}

func TestStoreStorageGC(t *testing.T) {
	t.Run("keeps nothing of a settled call: its batches are collected", func(t *testing.T) {
		made := counted(t, ":memory:")
		// Pi's WeakRef of the batches array is a weak pointer to the array of the call's batches; the call's writes are
		// reached only from it, so a Storage that kept a batch after the call settled would keep it alive.
		sent := func() weak.Pointer[[]durable.StorageWrite] {
			batches := [][]durable.StorageWrite{{entryWrite(mintID[durable.EntryId](t, made.storage))}}
			_, err := made.storage.Commit(background, batches, func(int, durable.Seq) {})
			must(t, err)
			return weak.Make(&batches[0])
		}
		ref := sent()
		for round := 0; round < 10 && ref.Value() != nil; round++ {
			time.Sleep(time.Millisecond)
			runtime.GC()
		}
		if ref.Value() != nil {
			t.Fatal("the batches of a settled call are still reachable")
		}
		must(t, made.store.Close(background))
	})
}
