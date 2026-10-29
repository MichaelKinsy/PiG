package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/store/applier.test.ts
//
// Tests of the applier (slice 6a §13): recorded writes applied as recorded, checked against the store's own records:
// table names and columns by the name rules, indexes, rows against their recorded columns (NULL cells, defaults kept
// as stored, booleans, json(...) cells as JSON text) and documents with their ids kept.

const applierTable = "app.store:edits"

// applierTableWrite is the `table` write the cases below start from; change edits a copy.
func applierTableWrite(change func(write *TableDefinitionWrite)) *TableDefinitionWrite {
	write := &TableDefinitionWrite{
		Table:   applierTable,
		Version: 2,
		State:   StateReady,
		Columns: ColumnRecords{
			{Name: "id", Record: ColumnRecord{Type: ColStr}},
			{Name: "done", Record: ColumnRecord{Type: ColBool, Default: false}},
			{Name: "note", Record: ColumnRecord{Type: ColStr, Optional: true}},
			{Name: "meta", Record: ColumnRecord{Type: ColJson, Optional: true, Version: new(1), Fingerprint: "f"}},
		},
		Key:         []string{"id"},
		Fingerprint: "t",
	}
	if change != nil {
		change(write)
	}
	return write
}

// applied applies writes in one transaction of a fresh database and returns the rows of the table.
func applierRows(t *testing.T, writes ...StoreWrite) ([]sqlite.SqliteRow, error) {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	noError(t, err)
	defer func() { _ = db.Close() }()
	var rows []sqlite.SqliteRow
	err = db.Transaction(func(executor sqlite.SqliteExecutor) error {
		step := NewStep(executor, 1, NewIdAllocator(2), map[string]*TableRecord{})
		for _, write := range writes {
			if err := applyWrite(step, write, "test"); err != nil {
				return err
			}
		}
		var err error
		rows, err = executor.All(`SELECT * FROM "` + applierTable + `"`)
		return err
	})
	return rows, err
}

func TestApplier(t *testing.T) {
	t.Run("applied writes", func(t *testing.T) {
		t.Run("a table and its rows as recorded: NULL for absent opt cells, booleans as 0 and 1, json(...) as text", func(t *testing.T) {
			rows, err := applierRows(t, applierTableWrite(nil), &PutWrite{
				Table:   applierTable,
				Row:     delta.JsonObjectOf("id", "a", "done", true, "meta", delta.JsonObjectOf("x", []any{float64(1)})),
				Version: 1,
				Columns: delta.JsonObjectOf("meta", float64(1)),
			})
			noError(t, err)
			want := []sqlite.SqliteRow{{"id": "a", "done": int64(1), "note": nil, "meta": `{"x":[1]}`, "meta$v": int64(1), "$v": int64(1)}}
			if !reflect.DeepEqual(rows, want) {
				t.Fatalf("rows = %v, want %v", rows, want)
			}
		})

		for _, c := range []struct {
			name    string
			writes  func() []StoreWrite
			message string
		}{
			{
				"a put for a table not recorded",
				func() []StoreWrite {
					return []StoreWrite{&PutWrite{Table: applierTable, Row: delta.JsonObjectOf(), Version: 1, Columns: delta.JsonObjectOf()}}
				},
				"is not recorded",
			},
			{
				"a key over an opt column",
				func() []StoreWrite {
					return []StoreWrite{applierTableWrite(func(write *TableDefinitionWrite) { write.Key = []string{"note"} })}
				},
				`"note" is not a non-opt str or int column`,
			},
			{
				"a json column without a fingerprint",
				func() []StoreWrite {
					return []StoreWrite{applierTableWrite(func(write *TableDefinitionWrite) {
						write.Columns = ColumnRecords{
							{Name: "id", Record: ColumnRecord{Type: ColStr}},
							{Name: "meta", Record: ColumnRecord{Type: ColJson, Version: new(1)}},
						}
					})}
				},
				"a json(...) column has a version and a fingerprint",
			},
			{
				"a default of another type",
				func() []StoreWrite {
					return []StoreWrite{applierTableWrite(func(write *TableDefinitionWrite) {
						write.Columns = ColumnRecords{
							{Name: "id", Record: ColumnRecord{Type: ColStr}},
							{Name: "n", Record: ColumnRecord{Type: ColInt, Default: "x"}},
						}
					})}
				},
				"is not a int value",
			},
			{
				"an index over an unknown column",
				func() []StoreWrite {
					return []StoreWrite{applierTableWrite(nil), &IndexWrite{Table: applierTable, Name: "by_x", Columns: []string{"x"}}}
				},
				`"x" is not a non-opt str or int column`,
			},
			{
				"an index twice",
				func() []StoreWrite {
					return []StoreWrite{
						applierTableWrite(nil),
						&IndexWrite{Table: applierTable, Name: "by_id", Columns: []string{"id"}},
						&IndexWrite{Table: applierTable, Name: "by_id", Columns: []string{"id"}},
					}
				},
				"not a new index name",
			},
			{
				"a version for a cell the row lacks",
				func() []StoreWrite {
					return []StoreWrite{applierTableWrite(nil), &PutWrite{
						Table: applierTable, Row: delta.JsonObjectOf("id", "a", "done", false), Version: 1, Columns: delta.JsonObjectOf("meta", float64(1)),
					}}
				},
				"no json(...) cell of the row",
			},
			{
				"a json cell that is no JSON",
				func() []StoreWrite {
					return []StoreWrite{applierTableWrite(nil), &PutWrite{
						Table:   applierTable,
						Row:     delta.JsonObjectOf("id", "a", "done", false, "meta", make(chan int)),
						Version: 1,
						Columns: delta.JsonObjectOf("meta", float64(1)),
					}}
				},
				"not JSON",
			},
		} {
			t.Run("refuses "+c.name, func(t *testing.T) {
				_, err := applierRows(t, c.writes()...)
				chordErr := rejected(t, err, data.CodeInvalid, data.ReasonArgs)
				if !strings.Contains(chordErr.Message, c.message) {
					t.Fatalf("message = %q, want it to contain %q", chordErr.Message, c.message)
				}
			})
		}
	})
}
