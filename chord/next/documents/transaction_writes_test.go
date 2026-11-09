package documents

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Tests of a transaction's table writes through the Tx interface, what `self.tx` hands its callback: `tx.put`,
// `tx.update`, `tx.delete`, `tx.updateWhere`, `tx.deleteWhere` and the `tx.get` overlay of them.
//
// Pi tests these writes where they are staged, on TableChanges (store/table-changes.test.ts, store/table-writes.test.ts:
// `tx.update` is `this.#changes(table).update(key, set)`, packages/chord/src/documents/transaction.ts:280-287),
// and uses `tx.update` and `tx.updateWhere` through `self.tx` in the example test/next/examples/20-tables.ts; it has no
// test that calls `tx.delete` or `tx.deleteWhere` itself. Each test below runs Pi's case, with its inputs and expected
// results, on a transaction of the harness object (transaction_test.go) instead of a bare TableChanges.

// changesEdits is the table of Pi's table-changes.test.ts (`Edits`: id, path, hits with a default, an optional note).
func changesEdits(t *testing.T) *store.TableDefinition {
	t.Helper()
	definition, err := store.DefineTable("edits", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Str()), store.Col("path", store.Str()),
			store.Col("hits", store.Int(contracts.Default(0))), store.Col("note", store.Opt(store.Str())),
		},
		Key: []string{"id"},
	})
	requireNoError(t, err)
	return definition
}

// changeTables is count handles of the Pi table, each over its own physical table of s's object and store.
func changeTables(t *testing.T, s *storedObject, count int) []*store.TableHandle {
	t.Helper()
	definition := changesEdits(t)
	declared := make([]store.DeclaredTable, count)
	handles := make([]*store.TableHandle, count)
	for i := range handles {
		name := fmt.Sprintf("app.owner:changes%d", i)
		declared[i] = store.DeclaredTable{Definition: definition, Table: name}
		handles[i] = store.NewTableHandle(definition, name, s.store, &tableOwner{s.o})
	}
	requireNoError(t, store.PrepareTables(declared, s.store, openFence, "app.owner", background))
	return handles
}

// seedE1 commits `e1` to each table, as Pi's `seeded()` does.
func seedE1(t *testing.T, s *storedObject, tables ...*store.TableHandle) {
	t.Helper()
	_, err := s.self().Tx(func(tx Tx) (any, error) {
		for _, table := range tables {
			if err := tx.Put(table, store.Row{"id": "e1", "path": "a/1", "hits": int64(1)}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}, background)
	requireNoError(t, err)
}

func requireRowJSON(t *testing.T, got store.Row, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("row = absent, want %s", want)
	}
	nexttest.RequireJSONEqual(t, got, nexttest.ParseJSON(t, want))
}

// requireInvalid is `toMatchObject({ code: "invalid", reason })`.
func requireInvalid(t *testing.T, err error, reason data.ChordErrorReason) {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != data.CodeInvalid || chordErr.Reason != reason {
		t.Fatalf("error = %v, want invalid/%s", err, reason)
	}
}

func requireAbsent(t *testing.T, row store.Row, err error, what string) {
	t.Helper()
	requireNoError(t, err)
	if row != nil {
		t.Fatalf("%s = %v, want absent", what, row)
	}
}

// TestTablesInATransactionReplayOwnPutUpdateAndDeleteOverTheCommittedRowWithGet runs `it("replays the
// transaction's own put, update and delete over the committed row")` of packages/chord/test/store/table-changes.test.ts:52
// on `tx.put`, `tx.update`, `tx.delete` and `tx.get` of a transaction; and `it("an update of an absent key changes
// nothing")` of table-changes.test.ts:65. The rows the transaction leaves are then the committed ones.
func TestTablesInATransactionReplayOwnPutUpdateAndDeleteOverTheCommittedRowWithGet(t *testing.T) {
	s := storedOn(t, nil, false, nil)
	edits := changeTables(t, s, 1)[0]
	seedE1(t, s, edits)
	addHits := func(by int64) func(store.ColumnRefs) store.SetOf {
		return func(e store.ColumnRefs) store.SetOf {
			return store.SetOf{store.Assign("hits", store.Add(e["hits"], by))}
		}
	}
	_, err := s.self().Tx(func(tx Tx) (any, error) {
		key := store.Row{"id": "e1"}
		requireNoError(t, tx.Update(edits, key, addHits(2)))
		row, err := tx.Get(edits, key)
		requireNoError(t, err)
		requireRowJSON(t, row, `{"id":"e1","path":"a/1","hits":3}`)
		requireNoError(t, tx.Update(edits, key, store.Row{"note": "n"}))
		row, err = tx.Get(edits, key)
		requireNoError(t, err)
		requireRowJSON(t, row, `{"id":"e1","path":"a/1","hits":3,"note":"n"}`)
		requireNoError(t, tx.Delete(edits, key))
		row, err = tx.Get(edits, key)
		requireAbsent(t, row, err, "row after delete")
		requireNoError(t, tx.Put(edits, store.Row{"id": "e1", "path": "b"}))
		requireNoError(t, tx.Update(edits, key, addHits(1)))
		row, err = tx.Get(edits, key)
		requireNoError(t, err)
		requireRowJSON(t, row, `{"id":"e1","path":"b","hits":1}`)
		return nil, nil
	}, background)
	requireNoError(t, err)
	requireRowJSON(t, s.tableRow(t, edits, "e1"), `{"id":"e1","path":"b","hits":1}`)

	_, err = s.self().Tx(func(tx Tx) (any, error) {
		requireNoError(t, tx.Update(edits, store.Row{"id": "e9"}, store.Row{"note": "n"}))
		row, err := tx.Get(edits, store.Row{"id": "e9"})
		requireAbsent(t, row, err, "row of an absent key")
		return nil, nil
	}, background)
	requireNoError(t, err)
	requireAbsent(t, s.tableRow(t, edits, "e9"), nil, "committed row of an absent key")
}

// TestTablesInATransactionDeleteTheRowWhenTheTransactionCommits runs the delete of `it("replays the transaction's own
// put, update and delete over the committed row")` (table-changes.test.ts:52, `changes.delete({ id: "e1" })` then
// `get` is undefined), which `tx.delete` runs (transaction.ts:63 "Deletes the row with `key` when the transaction
// commits"), on a commit: the row stays until it, and is gone after; a discarded transaction deletes nothing.
func TestTablesInATransactionDeleteTheRowWhenTheTransactionCommits(t *testing.T) {
	s := storedOn(t, nil, false, nil)
	edits := changeTables(t, s, 1)[0]
	seedE1(t, s, edits)
	_, err := s.self().Tx(func(tx Tx) (any, error) {
		requireNoError(t, tx.Delete(edits, store.Row{"id": "e1"}))
		requireRowJSON(t, s.tableRow(t, edits, "e1"), `{"id":"e1","path":"a/1","hits":1}`)
		return nil, errors.New("no")
	}, background)
	requireErrorText(t, err, "no")
	requireRowJSON(t, s.tableRow(t, edits, "e1"), `{"id":"e1","path":"a/1","hits":1}`)
	_, err = s.self().Tx(func(tx Tx) (any, error) {
		requireNoError(t, tx.Delete(edits, store.Row{"id": "e1"}))
		return nil, nil
	}, background)
	requireNoError(t, err)
	requireAbsent(t, s.tableRow(t, edits, "e1"), nil, "row after the commit")
}

// TestTablesInATransactionRefuseGetOnATableAfterAnUpdateWhereOrDeleteWhereOtherTablesAreUnaffected runs `it("is
// refused on a table after an updateWhere or deleteWhere (W10); other tables are unaffected")` of
// packages/chord/test/store/table-changes.test.ts:71 on `tx.updateWhere`, `tx.deleteWhere` and `tx.get`: Pi's three
// TableChanges (updated, deleted-from, untouched) are three tables of the transaction.
func TestTablesInATransactionRefuseGetOnATableAfterAnUpdateWhereOrDeleteWhereOtherTablesAreUnaffected(t *testing.T) {
	s := storedOn(t, nil, false, nil)
	tables := changeTables(t, s, 3)
	seedE1(t, s, tables...)
	key := store.Row{"id": "e1"}
	_, err := s.self().Tx(func(tx Tx) (any, error) {
		row, err := tx.Get(tables[0], key)
		requireNoError(t, err)
		if row == nil {
			t.Fatal("e1 absent before a bulk write")
		}
		requireNoError(t, tx.UpdateWhere(tables[0],
			func(e store.ColumnRefs) store.Condition { return store.Prefix(e["path"], "a/") }, store.Row{"note": "bulk"}))
		_, err = tx.Get(tables[0], key)
		requireInvalid(t, err, data.ReasonReadAfterBulk)
		requireNoError(t, tx.DeleteWhere(tables[1], func(e store.ColumnRefs) store.Condition { return store.Eq(e["id"], "x") }))
		_, err = tx.Get(tables[1], key)
		requireInvalid(t, err, data.ReasonReadAfterBulk)
		row, err = tx.Get(tables[2], key)
		requireNoError(t, err)
		if row == nil {
			t.Fatal("an untouched table refused or lost e1")
		}
		return nil, nil
	}, background)
	requireNoError(t, err)
	requireRowJSON(t, s.tableRow(t, tables[0], "e1"), `{"id":"e1","path":"a/1","hits":1,"note":"bulk"}`)
	requireRowJSON(t, s.tableRow(t, tables[1], "e1"), `{"id":"e1","path":"a/1","hits":1}`)
}

// TestTablesInATransactionUpdateWhereAndDeleteWhereAreOneStatementEachWithTheirSets runs `it("updateWhere and
// deleteWhere are one statement each, recorded with where, set and count")` of
// packages/chord/test/store/table-writes.test.ts:124 on `tx.put`, `tx.updateWhere` and `tx.deleteWhere`: five
// rows, the `a/` ones moved to `c/` by an expression set, `b/4` deleted; the two bulk writes, the outbox commit and
// lastSeq are the four statements.
func TestTablesInATransactionUpdateWhereAndDeleteWhereAreOneStatementEachWithTheirSets(t *testing.T) {
	meter := nexttest.Metered(t)
	s := storedOn(t, nil, false, nil)
	edits := changeTables(t, s, 1)[0]
	_, err := s.self().Tx(func(tx Tx) (any, error) {
		for i := range 5 {
			path := fmt.Sprintf("b/%d", i)
			if i < 3 {
				path = fmt.Sprintf("a/%d", i)
			}
			if err := tx.Put(edits, store.Row{"id": fmt.Sprintf("e%d", i), "path": path}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}, background)
	requireNoError(t, err)
	before := meter.Counts()
	_, err = s.self().Tx(func(tx Tx) (any, error) {
		requireNoError(t, tx.UpdateWhere(edits,
			func(e store.ColumnRefs) store.Condition { return store.Prefix(e["path"], "a/") },
			func(e store.ColumnRefs) store.SetOf {
				return store.SetOf{store.Assign("path", store.ReplacePrefix(e["path"], "a/", "c/"))}
			}))
		return nil, tx.DeleteWhere(edits, func(e store.ColumnRefs) store.Condition { return store.Eq(e["path"], "b/4") })
	}, meter.Context)
	requireNoError(t, err)
	if got := meter.Counts()["chord.store.statements"] - before["chord.store.statements"]; got != 4 {
		t.Fatalf("statements = %v, want 4", got)
	}
	var paths []string
	_, err = s.self().Tx(func(tx Tx) (any, error) {
		page, err := tx.Page(edits, store.NewQuery(edits.Definition), 10)
		requireNoError(t, err)
		for _, row := range page.Items {
			paths = append(paths, row["path"].(string))
		}
		return nil, nil
	}, background)
	requireNoError(t, err)
	if want := []string{"c/0", "c/1", "c/2", "b/3"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

// TestTablesInATransactionUpdateAndUpdateWhereRunTheTablesExample runs the `history` object of the example
// packages/chord/test/examples/20-tables.ts: `record` is `tx.put` (20-tables.ts:53), `hit` is `tx.update` with an
// expression set (packages/chord/test/examples/20-tables.ts:58), `finish` is `tx.updateWhere` with a prefix
// condition (packages/chord/test/examples/20-tables.ts:63); five rows, e1 hit twice and `src/a` finished give two
// hits of e1 and three finished rows, and the open ones page by `at` descending (e3, then e1).
func TestTablesInATransactionUpdateAndUpdateWhereRunTheTablesExample(t *testing.T) {
	s := storedOn(t, nil, false, nil)
	definition, err := store.DefineTable("edits", store.TableOptions{
		Version: 1,
		Columns: []store.ColumnProp{
			store.Col("id", store.Str()), store.Col("path", store.Str()), store.Col("at", store.Int()),
			store.Col("hits", store.Int(contracts.Default(0))), store.Col("done", store.Bool(contracts.Default(false))),
		},
		Key:     []string{"id"},
		Indexes: store.IndexRecords{{Name: "by_path", Columns: []string{"path", "at"}}},
	})
	requireNoError(t, err)
	requireNoError(t, store.PrepareTables([]store.DeclaredTable{{Definition: definition, Table: "app.owner:history"}}, s.store, openFence, "app.owner", background))
	table := store.NewTableHandle(definition, "app.owner:history", s.store, &tableOwner{s.o})
	tx := func(fn func(tx Tx) error) {
		t.Helper()
		_, err := s.self().Tx(func(tx Tx) (any, error) { return nil, fn(tx) }, background)
		requireNoError(t, err)
	}
	for i := range 5 {
		path := "src/b.ts"
		if i%2 == 0 {
			path = "src/a.ts"
		}
		tx(func(tx Tx) error {
			return tx.Put(table, store.Row{"id": fmt.Sprintf("e%d", i), "path": path, "at": int64(i)})
		})
	}
	for range 2 {
		tx(func(tx Tx) error {
			return tx.Update(table, store.Row{"id": "e1"}, store.Row{"hits": store.Add(store.NewColumnRef("hits"), 1)})
		})
	}
	tx(func(tx Tx) error {
		return tx.UpdateWhere(table, store.Prefix(store.NewColumnRef("path"), "src/a"), store.Row{"done": true})
	})
	row, err := table.Get(background, store.Row{"id": "e1"})
	requireNoError(t, err)
	if row["hits"] != int64(2) {
		t.Fatalf("hits of e1 = %v, want 2", row["hits"])
	}
	open := store.NewQuery(definition).
		Where(func(row store.ColumnRefs) store.Condition { return store.Eq(row["done"], false) }).
		OrderBy(func(row store.ColumnRefs) *store.ColumnRef { return row["at"] }, "desc")
	first, err := table.Page(background, open, 1, store.PageOptions{})
	requireNoError(t, err)
	next, err := table.Page(background, open, 1, store.PageOptions{After: first.Next})
	requireNoError(t, err)
	if first.Items[0]["id"] != "e3" || next.Items[0]["id"] != "e1" {
		t.Fatalf("open pages = %v, %v, want e3 then e1", first.Items, next.Items)
	}
	finished, err := table.Count(background, store.NewQuery(definition).Where(func(row store.ColumnRefs) store.Condition { return store.Eq(row["done"], true) }))
	requireNoError(t, err)
	if finished != 3 {
		t.Fatalf("finished = %d, want 3", finished)
	}
}
