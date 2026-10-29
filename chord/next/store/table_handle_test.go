package store

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/next/store/table-handle.test.ts: table handles (slice 6a §7): reads outside a
// transaction, and cells the code cannot read.

func handleEdits(t *testing.T) *TableDefinition {
	t.Helper()
	definition, err := DefineTable("edits", TableOptions{
		Version: 1,
		Columns: []ColumnProp{
			Col("id", Str()),
			Col("path", Str()),
			Col("at", Int()),
			Col("done", Bool(contracts.Default(false))),
			Col("note", Opt(Str())),
			Col("meta", Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1})),
		},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "by_path", Columns: []string{"path", "at"}}},
	})
	if err != nil {
		t.Fatalf("DefineTable: %v", err)
	}
	return definition
}

// filledHandle is a table with `count` rows e000..: path `p/<i % 3>`, at `i`.
func filledHandle(t *testing.T, count int, owner ...*testOwner) (*ChordStore, *TableHandle, *TableDefinition) {
	t.Helper()
	store := memoryStore(t)
	definition := handleEdits(t)
	edits := tableIn(t, store, definition, owner...)
	var staged []StagedWrite
	changes := NewTableChanges(edits, &staged)
	for i := range count {
		noError(t, changes.Put(Row{
			"id":   fmt.Sprintf("e%03d", i),
			"path": fmt.Sprintf("p/%d", i%3),
			"at":   int64(i),
			"meta": delta.JsonObjectOf("a", float64(i)),
		}))
	}
	if _, err := store.Commit(staged, openFence, background); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return store, edits, definition
}

func TestTableHandle(t *testing.T) {
	t.Run("reads outside a transaction", func(t *testing.T) {
		t.Run("get takes a key object and reads committed rows as stored", func(t *testing.T) {
			_, edits, _ := filledHandle(t, 3)
			row, err := edits.Get(Row{"id": "e001"}, background)
			noError(t, err)
			nexttest.RequireJSONEqual(t, row, nexttest.ParseJSON(t, `{"id":"e001","path":"p/1","at":1,"done":false,"meta":{"a":1}}`))
			row, err = edits.Get(Row{"id": "nope"}, background)
			noError(t, err)
			if row != nil {
				t.Fatalf("Get(nope) = %v, want absent", row)
			}
			_, err = edits.Get(Row{"id": "e001", "path": "x"}, background)
			_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		})

		t.Run("page fetches limit + 1 for next, and the cursor continues in order with the key tie-break", func(t *testing.T) {
			_, edits, definition := filledHandle(t, 10)
			q := NewQuery(definition).
				Where(func(e ColumnRefs) Condition { return Prefix(e["path"], "p/") }).
				OrderBy(func(e ColumnRefs) *ColumnRef { return e["path"] }, "desc")
			noError(t, q.Err())
			label := func(row Row) string { return fmt.Sprintf("%s:%s", row["path"], row["id"]) }
			var seen []string
			options := PageOptions{}
			for {
				page, err := edits.Page(q, 3, background, options)
				noError(t, err)
				if len(page.Items) > 3 {
					t.Fatalf("page has %d items", len(page.Items))
				}
				for _, row := range page.Items {
					seen = append(seen, label(row))
				}
				if page.Next == nil {
					break
				}
				options = PageOptions{After: page.Next}
			}
			whole, err := edits.Page(q, 100, background, PageOptions{})
			noError(t, err)
			var all []string
			for _, row := range whole.Items {
				all = append(all, label(row))
			}
			if !equalStrings(seen, all) {
				t.Fatalf("paged = %v, whole = %v", seen, all)
			}
			if want := []string{"p/2:e008", "p/2:e005", "p/2:e002", "p/1:e007"}; !equalStrings(all[:4], want) {
				t.Fatalf("first four = %v, want %v", all[:4], want)
			}
			four, err := edits.Page(q, 4, background, PageOptions{})
			noError(t, err)
			if four.Next == nil {
				t.Fatalf("a page of 4 of 10 has no next")
			}
			everything, err := edits.Page(NewQuery(definition), 10, background, PageOptions{})
			noError(t, err)
			if everything.Next != nil {
				t.Fatalf("the last page has next %q", *everything.Next)
			}
		})

		t.Run("rows reads every page; count counts", func(t *testing.T) {
			_, edits, definition := filledHandle(t, 250)
			rows := edits.Rows(NewQuery(definition).Where(func(e ColumnRefs) Condition { return Gte(e["at"], 10) }), background)
			defer rows.Close()
			var ids []string
			for {
				row, ok, err := rows.Next(background)
				noError(t, err)
				if !ok {
					break
				}
				ids = append(ids, row["id"].(string))
			}
			if len(ids) != 240 || ids[0] != "e010" {
				t.Fatalf("rows = %d first %q, want 240 first e010", len(ids), ids[0])
			}
			count, err := edits.Count(NewQuery(definition).Where(func(e ColumnRefs) Condition { return Eq(e["path"], "p/0") }), background)
			noError(t, err)
			if count != 84 {
				t.Fatalf("count(p/0) = %d, want 84", count)
			}
			count, err = edits.Count(NewQuery(definition).Where(func(e ColumnRefs) Condition { return IsNull(e["note"]) }), background)
			noError(t, err)
			if count != 250 {
				t.Fatalf("count(note is null) = %d, want 250", count)
			}
		})

		t.Run("refuses a query of another table, a bad limit and a foreign cursor before the backend", func(t *testing.T) {
			_, edits, definition := filledHandle(t, 1)
			other, err := DefineTable("other", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Str())}, Key: []string{"id"}})
			noError(t, err)
			_, err = edits.Page(NewQuery(other), 1, background, PageOptions{})
			_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
			_, err = edits.Page(NewQuery(definition), 0, background, PageOptions{})
			_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
			// Pi's `1.5` is no integer: Go's limit is an int64, so the check is the positive-integer one (designed out).
			after := "x"
			_, err = edits.Page(NewQuery(definition), 1, background, PageOptions{After: &after})
			_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		})

		t.Run("reads go through the store's guard: refused once the store closed", func(t *testing.T) {
			store, edits, _ := filledHandle(t, 1)
			noError(t, store.Close(background))
			_, err := edits.Get(Row{"id": "e000"}, background)
			_ = rejected(t, err, data.CodeReleased, "")
		})
	})

	t.Run("unreadable cells", func(t *testing.T) {
		t.Run("a json cell of a newer version fails the owner and the read, the data intact", func(t *testing.T) {
			owner := tableOwner()
			store, edits, _ := filledHandle(t, 1, owner)
			noError(t, store.Maintain(func(executor sqlite.SqliteExecutor) error {
				return executor.Run(`UPDATE "app.store:edits" SET "meta$v" = 2`)
			}))
			_, err := edits.Get(Row{"id": "e000"}, background)
			_ = rejected(t, err, data.CodeUnavailable, "")
			if len(owner.failures) != 1 {
				t.Fatalf("owner failures = %d, want 1", len(owner.failures))
			}
			row, err := Read(store, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
				return executor.Get(`SELECT meta FROM "app.store:edits"`)
			}, background)
			noError(t, err)
			if row["meta"] != `{"a":0}` {
				t.Fatalf("meta = %v, want {\"a\":0}", row["meta"])
			}
		})
	})
}
