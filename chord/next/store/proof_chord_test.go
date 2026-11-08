package store

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Proof of the ChordStore, Step, Expression and Outbox constructors and members Pi's tests reach only through the
// store they build: each case names the Go symbol and asserts what Pi's counterpart does (store.ts, step.ts,
// expression.ts, outbox.ts).

// runCountedWrite is a staged write whose apply runs one statement through Step.RunCounted.
type runCountedWrite struct {
	sql        string
	changed    *int64
	statements *int
}

func (write *runCountedWrite) StageOrder() WriteOrder { return OrderWrite }

func (write *runCountedWrite) Apply(step *Step) ([]StoreWrite, error) {
	changed, err := step.RunCounted(write.sql)
	*write.changed = changed
	*write.statements = step.Statements
	return nil, err
}

func TestNewChordStoreCarriesItsMetaRowAndOutbox(t *testing.T) {
	database := openMemory(t, nil, nil).db
	with := NewChordStore(database, MetaRow{NextID: 7, LastSeq: 3, Outbox: 1, OutboxFloor: 2}, map[string]*TableRecord{"a": {}})
	if with.LastSeq() != 3 || with.NextID() != 7 {
		t.Fatalf("LastSeq, NextID = %d, %d; want 3, 7", with.LastSeq(), with.NextID())
	}
	if with.Outbox() == nil || with.Outbox().Floor() != 2 {
		t.Fatalf("outbox = %v; want one starting after floor 2", with.Outbox())
	}
	if !with.RecordsOutbox() || with.Table("a") == nil {
		t.Fatal("the outbox flag or the catalog the store was built with is lost")
	}
	without := NewChordStore(database, MetaRow{NextID: 1}, nil)
	if without.Outbox() != nil || without.RecordsOutbox() {
		t.Fatal("a store whose metadata has no outbox has one")
	}
}

func TestStoreOwnerTablesRefuseAnOwnerNameOutsideTheRestrictedAlphabet(t *testing.T) {
	store := memoryStore(t)
	var owned *StoreOwnerTables
	owned, err := NewStoreOwnerTables(store, "ok@owner/1.2_x~y-z", &NamedClaimant{Label: "a"})
	if err != nil || owned == nil {
		t.Fatalf("a restricted-alphabet owner name was refused: %v", err)
	}
	for _, bad := range []string{"", "Upper", "has space", "a:b"} {
		if _, err := NewStoreOwnerTables(store, bad, &NamedClaimant{Label: "b"}); err == nil {
			t.Fatalf("the owner name %q was accepted", bad)
		}
	}
}

func TestNewOutboxStartsAfterItsFloorAndRefusesAReadBelowIt(t *testing.T) {
	store := memoryStore(t)
	outbox := NewOutbox(store, 5)
	if outbox.Floor() != 5 {
		t.Fatalf("Floor = %d, want 5", outbox.Floor())
	}
	_, err := outbox.Read(4, 10, background)
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
}

func TestAnnounceTableIsWithdrawnOnlyByItsOwnVersion(t *testing.T) {
	store := memoryStore(t)
	first := store.AnnounceTable("t", 2)
	second := store.AnnounceTable("t", 3)
	first() // a withdrawal of an announcement a newer one replaced leaves the newer one
	store.mu.Lock()
	version, held := store.announced["t"]
	store.mu.Unlock()
	if !held || version != 3 {
		t.Fatalf("announced = %d, %v after the first withdrew; want version 3 kept", version, held)
	}
	second()
	store.mu.Lock()
	_, held = store.announced["t"]
	store.mu.Unlock()
	if held {
		t.Fatal("the announcement stays after its own withdrawal")
	}
}

func TestReplayRunsInOneTransactionAndTakesTheLastSeqItLeaves(t *testing.T) {
	store := memoryStore(t, true)
	var seen map[string]*TableRecord
	err := store.Replay(func(executor sqlite.SqliteExecutor, catalog map[string]*TableRecord) (Replayed, error) {
		seen = catalog
		return Replayed{LastSeq: 9, NextID: 40}, nil
	}, IsDiverged)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if seen == nil || store.LastSeq() != 9 || store.NextID() != 40 {
		t.Fatalf("after Replay LastSeq, NextID = %d, %d; want 9, 40", store.LastSeq(), store.NextID())
	}
	boom := errors.New("batch does not apply")
	if err := store.Replay(func(sqlite.SqliteExecutor, map[string]*TableRecord) (Replayed, error) {
		return Replayed{}, boom
	}, func(error) bool { return true }); !errors.Is(err, boom) {
		t.Fatalf("a failing apply = %v, want its own error", err)
	}
	// A divergence marks the store: no later batch applies.
	err = store.Replay(func(sqlite.SqliteExecutor, map[string]*TableRecord) (Replayed, error) { return Replayed{}, nil }, IsDiverged)
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	if store.LastSeq() != 9 {
		t.Fatalf("a refused batch moved LastSeq to %d", store.LastSeq())
	}
}

func TestStepRunCountedReturnsTheRowsAStatementChangedAndCountsIt(t *testing.T) {
	store, table := storeBPrepared(t)
	storeBCommit(t, store, table, "seq", Row{"id": "a", "seq": int64(0)}, Row{"id": "b", "seq": int64(0)})
	var changed int64
	var statements int
	write := &runCountedWrite{sql: `UPDATE "dur:entries" SET text = 'z'`, changed: &changed, statements: &statements}
	if _, err := store.Commit([]StagedWrite{write}, openFence, background); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if changed != 2 {
		t.Fatalf("RunCounted of an update of two rows = %d, want 2", changed)
	}
	if statements != 1 {
		t.Fatalf("the step counted %d statements after one RunCounted, want 1", statements)
	}
	step := NewStep(nil, 1, nil, nil)
	if step.Statements != 0 {
		t.Fatalf("a new step has run %d statements", step.Statements)
	}
}

func TestExpressionBuildersMakeTheColumnRefAndTheOperationTheCompilerReads(t *testing.T) {
	ref := NewColumnRef("n")
	if ref.Op != ExprCol || len(ref.Args) != 1 || ref.Args[0] != "n" {
		t.Fatalf("NewColumnRef = %+v", ref)
	}
	sum := Add(ref, int64(2))
	var expression *Expression = sum
	if expression.Op != ExprAdd || len(expression.Args) != 2 || expression.Args[0] != ref {
		t.Fatalf("Add = %+v; want an add of the ref and 2", expression)
	}
	table := exprTable(t)
	set, err := CompileSet(table, SetOf{Assign("n", sum)}, "update")
	if err != nil {
		t.Fatalf("CompileSet: %v", err)
	}
	if len(set) != 1 {
		t.Fatalf("compiled set = %v", set)
	}
}
