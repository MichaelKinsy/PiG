package store

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/next/store/table-changes.test.ts: a transaction's table writes (slice 6a §6, §7):
// staging, and the `tx.get` overlay over committed rows.

func changesEdits(t *testing.T) *TableDefinition {
	t.Helper()
	definition, err := DefineTable("edits", TableOptions{
		Version: 1,
		Columns: []ColumnProp{Col("id", Str()), Col("path", Str()), Col("hits", Int(contracts.Default(0))), Col("note", Opt(Str()))},
		Key:     []string{"id"},
	})
	if err != nil {
		t.Fatalf("DefineTable: %v", err)
	}
	return definition
}

// seededChanges is changes to a table holding `e1` (committed), and the staged list they append to.
func seededChanges(t *testing.T) (*ChordStore, *TableChanges, *[]StagedWrite) {
	t.Helper()
	store := memoryStore(t)
	edits := tableIn(t, store, changesEdits(t))
	var seed []StagedWrite
	noError(t, NewTableChanges(edits, &seed).Put(Row{"id": "e1", "path": "a/1", "hits": int64(1)}))
	if _, err := store.Commit(seed, openFence, background); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	staged := []StagedWrite{}
	return store, NewTableChanges(edits, &staged), &staged
}

func TestTableChanges(t *testing.T) {
	t.Run("staging", func(t *testing.T) {
		t.Run("checks at the call, appends in order, and touches no backend before the commit", func(t *testing.T) {
			store, changes, staged := seededChanges(t)
			noError(t, changes.Put(Row{"id": "e2", "path": "a/2"}))
			if err := changes.Put(Row{"id": "e3"}); err == nil || !strings.Contains(err.Error(), `column "path" is missing`) {
				t.Fatalf("Put without path = %v", err)
			}
			if err := changes.Update(Row{"id": "e1"}, Row{"id": "x"}); err == nil || !strings.Contains(err.Error(), "cannot change") {
				t.Fatalf("Update of the key = %v", err)
			}
			noError(t, changes.Update(Row{"id": "e1"}, Row{"note": "n"}))
			noError(t, changes.Delete(Row{"id": "e9"}))
			if len(*staged) != 3 {
				t.Fatalf("staged = %d, want 3", len(*staged))
			}
			row, err := Read(store, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
				return executor.Get(`SELECT count(*) AS n FROM "app.store:edits"`)
			}, background)
			noError(t, err)
			if row["n"] != int64(1) {
				t.Fatalf("rows = %v, want 1", row)
			}
		})
	})

	t.Run("the tx.get overlay", func(t *testing.T) {
		addHits := func(by int64) func(ColumnRefs) SetOf {
			return func(e ColumnRefs) SetOf { return SetOf{Assign("hits", Add(e["hits"], by))} }
		}
		equal := func(t *testing.T, got EvaluatedRow, want string) {
			t.Helper()
			if got == nil {
				t.Fatalf("row = absent, want %s", want)
			}
			nexttest.RequireJSONEqual(t, got, nexttest.ParseJSON(t, want))
		}

		t.Run("replays the transaction's own put, update and delete over the committed row", func(t *testing.T) {
			_, changes, _ := seededChanges(t)
			noError(t, changes.Update(Row{"id": "e1"}, addHits(2)))
			row, err := changes.Get(Row{"id": "e1"}, background)
			noError(t, err)
			equal(t, row, `{"id":"e1","path":"a/1","hits":3}`)
			noError(t, changes.Update(Row{"id": "e1"}, Row{"note": "n"}))
			row, err = changes.Get(Row{"id": "e1"}, background)
			noError(t, err)
			equal(t, row, `{"id":"e1","path":"a/1","hits":3,"note":"n"}`)
			noError(t, changes.Delete(Row{"id": "e1"}))
			row, err = changes.Get(Row{"id": "e1"}, background)
			noError(t, err)
			if row != nil {
				t.Fatalf("row after delete = %v, want absent", row)
			}
			noError(t, changes.Put(Row{"id": "e1", "path": "b"}))
			noError(t, changes.Update(Row{"id": "e1"}, addHits(1)))
			row, err = changes.Get(Row{"id": "e1"}, background)
			noError(t, err)
			equal(t, row, `{"id":"e1","path":"b","hits":1}`)
		})

		t.Run("an update of an absent key changes nothing", func(t *testing.T) {
			_, changes, _ := seededChanges(t)
			noError(t, changes.Update(Row{"id": "e9"}, Row{"note": "n"}))
			row, err := changes.Get(Row{"id": "e9"}, background)
			noError(t, err)
			if row != nil {
				t.Fatalf("row = %v, want absent", row)
			}
		})

		t.Run("is refused on a table after an updateWhere or deleteWhere (W10); other tables are unaffected", func(t *testing.T) {
			_, changes, _ := seededChanges(t)
			row, err := changes.Get(Row{"id": "e1"}, background)
			noError(t, err)
			if row == nil {
				t.Fatalf("e1 absent before a bulk write")
			}
			noError(t, changes.UpdateWhere(
				func(e ColumnRefs) Condition { return Prefix(e["path"], "a/") },
				Row{"note": "bulk"},
			))
			_, err = changes.Get(Row{"id": "e1"}, background)
			_ = rejected(t, err, data.CodeInvalid, data.ReasonReadAfterBulk)
			other := NewTableChanges(changes.Handle, &[]StagedWrite{})
			noError(t, other.DeleteWhere(func(e ColumnRefs) Condition { return Eq(e["id"], "x") }))
			_, err = other.Get(Row{"id": "e1"}, background)
			_ = rejected(t, err, data.CodeInvalid, data.ReasonReadAfterBulk)
			fresh, err := NewTableChanges(changes.Handle, &[]StagedWrite{}).Get(Row{"id": "e1"}, background)
			noError(t, err)
			if fresh == nil {
				t.Fatalf("a fresh TableChanges refused or lost e1")
			}
		})
	})
}
