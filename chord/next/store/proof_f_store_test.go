package store

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Proof tests of packages/chord/src/next/store/store.ts and step.ts members: the constructor `NewChordStore` (state
// from the metadata row and catalog, the outbox only when the row says so), `TableVersion` and `AnnounceTable` (the
// version a table is at or is being migrated to), `Replay` (a batch apply's turn) and `Step.RunCounted`.

func proofFCatalog() map[string]*TableRecord {
	return map[string]*TableRecord{"app:t": {Table: "app:t", Version: 2, State: StateReady}}
}

func TestProofFNewChordStoreTakesItsStateFromTheMetaRowAndCatalog(t *testing.T) {
	catalog := proofFCatalog()
	store := NewChordStore(nil, MetaRow{LastSeq: 3, NextID: 7, Outbox: 1, OutboxFloor: 2}, catalog)
	if store.LastSeq() != 3 {
		t.Fatalf("LastSeq = %d, want 3", store.LastSeq())
	}
	if store.NextID() != 7 {
		t.Fatalf("NextID = %d, want 7 (the allocator's value as persisted)", store.NextID())
	}
	id, err := store.Ids().Mint()
	if err != nil || id != 7 {
		t.Fatalf("first minted id = %d, %v; want 7", id, err)
	}
	outbox := store.Outbox()
	if outbox == nil || !store.RecordsOutbox() {
		t.Fatal("meta.Outbox = 1 gave no outbox")
	}
	if outbox.Floor() != 2 {
		t.Fatalf("outbox floor = %d, want 2", outbox.Floor())
	}
	if store.Claims == nil || store.Claims.Held() {
		t.Fatalf("a new store has fresh, empty claims (Claims = %#v)", store.Claims)
	}
	if got := store.Table("app:t"); got != catalog["app:t"] {
		t.Fatalf("Table(app:t) = %#v, want the catalog's record", got)
	}
	if store.Table("app:missing") != nil {
		t.Fatal("a table not in the catalog was found")
	}

	without := NewChordStore(nil, MetaRow{LastSeq: 0, NextID: 1, Outbox: 0, OutboxFloor: 5}, nil)
	if without.Outbox() != nil || without.RecordsOutbox() {
		t.Fatal("meta.Outbox = 0 built an outbox")
	}
	if without.LastSeq() != 0 || without.NextID() != 1 {
		t.Fatalf("[LastSeq, NextID] = [%d, %d], want [0, 1]", without.LastSeq(), without.NextID())
	}
}

func TestProofFTableVersionIsTheRecordedOrAnnouncedVersion(t *testing.T) {
	store := NewChordStore(nil, MetaRow{NextID: 1}, proofFCatalog())
	version := func(name string) (int, bool) { return store.TableVersion(name) }
	check := func(name string, wantVersion int, wantOK bool) {
		t.Helper()
		if got, ok := version(name); got != wantVersion || ok != wantOK {
			t.Fatalf("TableVersion(%s) = %d, %v; want %d, %v", name, got, ok, wantVersion, wantOK)
		}
	}
	check("app:t", 2, true)
	check("app:none", 0, false)

	// A migration announced to a higher version is the version until its withdrawal.
	withdraw := store.AnnounceTable("app:t", 3)
	check("app:t", 3, true)
	withdraw()
	check("app:t", 2, true)

	// An announcement not above the recorded version changes nothing.
	same := store.AnnounceTable("app:t", 2)
	check("app:t", 2, true)
	same()
	lower := store.AnnounceTable("app:t", 1)
	check("app:t", 2, true)
	lower()

	// A table the store does not have yet is announced at its first version.
	created := store.AnnounceTable("app:new", 1)
	check("app:new", 1, true)
	created()
	check("app:new", 0, false)
}

func TestProofFAnnounceTableWithdrawsOnlyItsOwnAnnouncement(t *testing.T) {
	store := NewChordStore(nil, MetaRow{NextID: 1}, proofFCatalog())
	first := store.AnnounceTable("app:t", 3)
	second := store.AnnounceTable("app:t", 4)
	if got, _ := store.TableVersion("app:t"); got != 4 {
		t.Fatalf("version after two announcements = %d, want 4 (the later one)", got)
	}
	// The older withdrawal must not remove the newer announcement.
	first()
	if got, _ := store.TableVersion("app:t"); got != 4 {
		t.Fatalf("version after the older withdrawal = %d, want 4", got)
	}
	second()
	if got, _ := store.TableVersion("app:t"); got != 2 {
		t.Fatalf("version after both withdrawals = %d, want the recorded 2", got)
	}
	// Announcements are per table.
	other := store.AnnounceTable("app:other", 9)
	if got, _ := store.TableVersion("app:t"); got != 2 {
		t.Fatalf("announcing app:other changed app:t to %d", got)
	}
	other()
}

func TestProofFReplayAppliesInATransactionAndTakesWhatItReturns(t *testing.T) {
	store := memoryStore(t)
	existing := &TableRecord{Table: "app:old", Version: 1, State: StateReady}
	store.catalog = map[string]*TableRecord{"app:old": existing}
	added := &TableRecord{Table: "app:new", Version: 4, State: StateReady}
	var sawCatalog map[string]*TableRecord
	err := store.Replay(func(executor sqlite.SqliteExecutor, catalog map[string]*TableRecord) (Replayed, error) {
		sawCatalog = catalog
		return Replayed{LastSeq: 5, NextID: 9, Tables: map[string]*TableRecord{"app:new": added}}, nil
	}, func(error) bool { return false })
	must(t, err)
	if sawCatalog["app:old"] != existing {
		t.Fatalf("apply saw catalog %#v, want the store's tables", sawCatalog)
	}
	if store.LastSeq() != 5 || store.NextID() != 9 {
		t.Fatalf("[LastSeq, NextID] = [%d, %d], want [5, 9]", store.LastSeq(), store.NextID())
	}
	if store.Table("app:new") != added || store.Table("app:old") != existing {
		t.Fatalf("tables after replay = %#v, want the changed one added and the rest kept", store.Tables())
	}
	if id, err := store.MintId(); err != nil || id != 9 {
		t.Fatalf("id minted after replay = %d, %v; want 9", id, err)
	}
	// A replay that changed no table leaves the catalog as it is.
	before := store.Tables()
	must(t, store.Replay(func(sqlite.SqliteExecutor, map[string]*TableRecord) (Replayed, error) {
		return Replayed{LastSeq: 6, NextID: 10}, nil
	}, func(error) bool { return false }))
	if len(store.Tables()) != len(before) || store.LastSeq() != 6 {
		t.Fatalf("tables = %#v, lastSeq = %d", store.Tables(), store.LastSeq())
	}
}

func TestProofFReplayRefusalRollsBackAndMarksDivergence(t *testing.T) {
	store := memoryStore(t)
	refusal := errors.New("batch does not apply")
	calls := 0
	apply := func(executor sqlite.SqliteExecutor, _ map[string]*TableRecord) (Replayed, error) {
		calls++
		if err := executor.Run(`CREATE TABLE proof_f_replayed (a INTEGER)`); err != nil {
			return Replayed{}, err
		}
		return Replayed{}, refusal
	}
	// An apply error that is no divergence rolls back and is returned as it is; the store stays usable.
	err := store.Replay(apply, func(error) bool { return false })
	if !errors.Is(err, refusal) {
		t.Fatalf("Replay = %v, want the apply error", err)
	}
	tables, readErr := Read(store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All(`SELECT name FROM sqlite_master WHERE name = 'proof_f_replayed'`)
	}, background)
	must(t, readErr)
	if len(tables) != 0 {
		t.Fatalf("the refused batch left its table: %v", tables)
	}
	must(t, store.AssertUsable())
	if store.LastSeq() != 0 {
		t.Fatalf("LastSeq after a refused batch = %d, want 0", store.LastSeq())
	}

	// A divergence marks the store: no batch applies any more (`invalid/args`), and apply is not called again.
	err = store.Replay(apply, func(got error) bool { return got == refusal })
	if !errors.Is(err, refusal) {
		t.Fatalf("diverging Replay = %v, want the apply error", err)
	}
	callsBefore := calls
	err = store.Replay(apply, func(error) bool { return false })
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	if calls != callsBefore {
		t.Fatal("apply ran on a diverged store")
	}
	must(t, store.AssertUsable())
}

func TestProofFReplayIsRefusedWhileAnOwnerHoldsAClaim(t *testing.T) {
	store := memoryStore(t)
	claimant := &NamedClaimant{Label: "owner"}
	must(t, store.Claims.Table("app:t", claimant))
	called := false
	apply := func(sqlite.SqliteExecutor, map[string]*TableRecord) (Replayed, error) {
		called = true
		return Replayed{LastSeq: 1, NextID: 2}, nil
	}
	err := store.Replay(apply, func(error) bool { return false })
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	if called || store.LastSeq() != 0 {
		t.Fatalf("a refused replay applied (called %v, lastSeq %d)", called, store.LastSeq())
	}
	store.Claims.Release(claimant)
	must(t, store.Replay(apply, func(error) bool { return false }))
	if !called || store.LastSeq() != 1 {
		t.Fatalf("replay after the release: called %v, lastSeq %d", called, store.LastSeq())
	}
}

func TestProofFReplayStopsTheStoreWhenTheBackendFails(t *testing.T) {
	store, fail := faultyStore(t)
	fail(failCommit)
	err := store.Replay(func(sqlite.SqliteExecutor, map[string]*TableRecord) (Replayed, error) {
		return Replayed{LastSeq: 8, NextID: 8}, nil
	}, func(error) bool { return false })
	_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
	if store.LastSeq() != 0 {
		t.Fatalf("LastSeq after a failed COMMIT = %d, want 0", store.LastSeq())
	}
	err = store.AssertUsable()
	_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
}

func TestProofFStepRunCountedReturnsTheRowsChangedAndCountsTheStatement(t *testing.T) {
	kit := newEngine(t)
	text := "{}"
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		for id, key := range map[int64]string{1: "a", 2: "b", 3: "c"} {
			if err := InsertDocument(step, adapterRecord(id, key, "none", step.Seq), &text); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	var counts []int64
	var statements []int
	kit.mustCommit(stagedApply(func(step *Step) ([]StoreWrite, error) {
		run := func(sql string, params ...sqlite.SqliteValue) error {
			before := step.Statements
			changed, err := step.RunCounted(sql, params...)
			counts = append(counts, changed)
			statements = append(statements, step.Statements-before)
			return err
		}
		if err := run(`UPDATE `+TableDocuments+` SET meta = ? WHERE id <= ?`, "m", int64(2)); err != nil {
			return nil, err
		}
		if err := run(`UPDATE `+TableDocuments+` SET meta = ? WHERE id = ?`, "z", int64(99)); err != nil {
			return nil, err
		}
		return nil, run(`DELETE FROM `+TableDocuments+` WHERE id = ?`, int64(3))
	}))
	want := []int64{2, 0, 1}
	for index, got := range counts {
		if got != want[index] {
			t.Fatalf("rows changed by statement %d = %d, want %d (all %v)", index, got, want[index], counts)
		}
		if statements[index] != 1 {
			t.Fatalf("statement %d counted %d statements, want 1", index, statements[index])
		}
	}
	if len(counts) != 3 {
		t.Fatalf("ran %d statements, want 3", len(counts))
	}
}
