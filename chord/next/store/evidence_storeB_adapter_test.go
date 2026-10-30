package store

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Tests of packages/chord/src/next/store/adapter.ts (the store for adapters, slice 6b §2.1). Pi has no test file of
// its own for it (Durable's StoreStorage drives it); these cases follow the source: the owner name is checked, a
// declared table claims its SQL name `<owner>:<table>`, `Prepare` brings the declared tables to their definitions,
// `Put` writes the encoded row in the step (`stamp` takes the step's seq, in the row and in the record), reads decode
// by key and by query, the prepared texts, and the `json(...)` version and reader.

// storeBPut is an adapter's staged `put`: OwnerTable.Put applied inside the commit step.
type storeBPut struct {
	table   *OwnerTable
	encoded *EncodedRow
	stamp   string
}

func (put *storeBPut) StageOrder() WriteOrder { return OrderWrite }

func (put *storeBPut) Apply(step *Step) ([]StoreWrite, error) {
	write, err := put.table.Put(step, put.encoded, put.stamp)
	if err != nil {
		return nil, err
	}
	return []StoreWrite{write}, nil
}

func storeBEntries(t *testing.T) *TableDefinition {
	return qDefine(t, "entries", TableOptions{
		Version: 1,
		Columns: []ColumnProp{
			Col("id", Str()),
			Col("seq", Int()),
			Col("text", Opt(Str())),
			Col("meta", Opt(Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1}))),
		},
		Key: []string{"id"},
	})
}

func storeBPrepared(t *testing.T) (*ChordStore, *OwnerTable) {
	t.Helper()
	store := memoryStore(t, true)
	owned, err := NewStoreOwnerTables(store, "dur", &NamedClaimant{Label: "dur adapter"})
	must(t, err)
	table, err := owned.Declare(storeBEntries(t))
	must(t, err)
	must(t, owned.Prepare(background))
	return store, table
}

func storeBCommit(t *testing.T, store *ChordStore, table *OwnerTable, stamp string, rows ...Row) int64 {
	t.Helper()
	var staged []StagedWrite
	for _, row := range rows {
		encoded, err := table.Encode(row)
		must(t, err)
		staged = append(staged, &storeBPut{table: table, encoded: encoded, stamp: stamp})
	}
	seq, err := store.Commit(staged, openFence, background)
	must(t, err)
	return seq
}

func TestStoreOwnerTablesDeclareAndPrepare(t *testing.T) {
	store := memoryStore(t, true)
	if _, err := NewStoreOwnerTables(store, "Bad Owner", &NamedClaimant{Label: "x"}); err == nil {
		t.Fatal("an owner name outside the restricted alphabet was accepted")
	}
	owned, err := NewStoreOwnerTables(store, "dur", &NamedClaimant{Label: "dur adapter"})
	must(t, err)
	table, err := owned.Declare(storeBEntries(t))
	must(t, err)
	qEqual(t, table.Sql.Table, "dur:entries")
	// The SQL name is claimed: another claimant declaring it is refused (`invalid/args`).
	other, err := NewStoreOwnerTables(store, "dur", &NamedClaimant{Label: "intruder"})
	must(t, err)
	_, err = other.Declare(storeBEntries(t))
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	if store.Table("dur:entries") != nil {
		t.Fatal("the table exists before Prepare")
	}
	must(t, owned.Prepare(background))
	record := store.Table("dur:entries")
	if record == nil {
		t.Fatal("Prepare did not create the table")
	}
	qEqual(t, []any{record.Version, record.State, record.Fingerprint}, []any{1, StateReady, storeBEntries(t).Fingerprint})
}

func TestOwnerTablePutStampsTheStepsSeqAndReadsDecode(t *testing.T) {
	store, table := storeBPrepared(t)
	seq := storeBCommit(t, store, table, "seq",
		Row{"id": "b", "seq": int64(0), "text": "two", "meta": delta.JsonObjectOf("a", float64(2))},
		Row{"id": "a", "seq": int64(0)})
	// The stamped column takes the step's seq: in the stored rows and in the records of the outbox.
	commits, err := store.Outbox().Read(0, 10, background)
	must(t, err)
	last := commits[len(commits)-1]
	if len(last.Writes) != 2 {
		t.Fatalf("writes = %d, want 2", len(last.Writes))
	}
	put := last.Writes[0].(*PutWrite)
	qEqual(t, put.Table, "dur:entries")
	qEqual(t, put.Row.Value("seq"), float64(seq))
	qEqual(t, put.Version, 1)
	qEqual(t, put.Columns.Value("meta"), float64(1))
	// An unstamped put keeps the row's own value.
	storeBCommit(t, store, table, "", Row{"id": "c", "seq": int64(7)})
	rows, err := Read(store, func(executor sqlite.SqliteExecutor) ([]DecodedRow, error) {
		return table.Select(executor, nil, Order{Columns: []string{"id"}, Direction: "asc"}, 10, background)
	}, background)
	must(t, err)
	qEqual(t, rows, []DecodedRow{
		{"id": "a", "seq": int64(seq)},
		{"id": "b", "seq": int64(seq), "text": "two", "meta": delta.JsonObjectOf("a", float64(2))},
		{"id": "c", "seq": int64(7)},
	})
	// Get is by key params, in key order; an absent row is nil.
	row, err := Read(store, func(executor sqlite.SqliteExecutor) (DecodedRow, error) {
		return table.Get(executor, []sqlite.SqliteValue{"c"}, background)
	}, background)
	must(t, err)
	qEqual(t, row, DecodedRow{"id": "c", "seq": int64(7)})
	row, err = Read(store, func(executor sqlite.SqliteExecutor) (DecodedRow, error) {
		return table.Get(executor, []sqlite.SqliteValue{"zz"}, background)
	}, background)
	must(t, err)
	if row != nil {
		t.Fatalf("Get of an absent key = %#v, want nil", row)
	}
	// Select honours where, order and limit.
	where := WhereNode{Op: WhereGte, Column: "id", Value: "b"}
	rows, err = Read(store, func(executor sqlite.SqliteExecutor) ([]DecodedRow, error) {
		return table.Select(executor, &where, Order{Columns: []string{"id"}, Direction: "desc"}, 1, background)
	}, background)
	must(t, err)
	qEqual(t, len(rows), 1)
	qEqual(t, rows[0]["id"], "c")
}

func TestOwnerTableEncodeIsTheTrustBoundary(t *testing.T) {
	_, table := storeBPrepared(t)
	_, err := table.Encode(Row{"id": "a"})
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	_, err = table.Encode(Row{"id": "a", "seq": int64(1), "other": "x"})
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	encoded, err := table.Encode(Row{"id": "a", "seq": int64(1)})
	must(t, err)
	qEqual(t, encoded.Params[:2], []sqlite.SqliteValue{"a", int64(1)})
}

func TestOwnerTablePreparedTexts(t *testing.T) {
	_, table := storeBPrepared(t)
	order := Order{Columns: []string{"id"}, Direction: "asc"}
	where := WhereNode{Op: WhereEq, Column: "seq", Value: int64(1)}
	page := table.Prepare([]string{"id", "seq"}, &where, order)
	qEqual(t, page, `SELECT "id", "seq" FROM "dur:entries" WHERE "seq" = ? ORDER BY "id" ASC LIMIT ?`)
	first := table.PrepareFirst([]string{"id"}, nil, order)
	qEqual(t, first, `SELECT "id" FROM "dur:entries" ORDER BY "id" ASC LIMIT 1`)
	qEqual(t, table.PrepareGet([]string{"seq"}), `SELECT "seq" FROM "dur:entries" WHERE "id" = ?`)
}

func TestOwnerTableJsonVersionAndReader(t *testing.T) {
	_, table := storeBPrepared(t)
	version, err := table.JsonVersion("meta")
	must(t, err)
	qEqual(t, version, 1)
	_, err = table.JsonVersion("id")
	if err == nil || !strings.Contains(err.Error(), `column "id" is no json(...) column`) {
		t.Fatalf("JsonVersion(id) error = %v, want no json(...) column", err)
	}
	_, err = table.JsonVersion("nope")
	qFails(t, err, `no column "nope"`)
	// At version 1 a cell is the JSON it was written as: the version column is not consulted.
	read, err := table.Reader("meta")
	must(t, err)
	cell, err := read(`{"a":1}`, 1, background)
	must(t, err)
	qEqual(t, cell, delta.JsonObjectOf("a", float64(1)))
	_, err = table.Reader("id")
	if err == nil {
		t.Fatal("Reader(id) of a scalar column succeeded")
	}
	// A newer version column migrates on access.
	notes := NewOwnerTable(qDefine(t, "notes", TableOptions{
		Version: 1,
		Columns: []ColumnProp{
			Col("id", Str()),
			Col("meta", Json(contracts.Obj(contracts.Prop("a", contracts.Int()), contracts.Prop("b", contracts.Int())), JsonColumnOptions{
				Version: 2,
				Migrate: func(old delta.JsonValue, _ int) (any, error) {
					a, _ := old.(*delta.JsonObject).Value("a").(float64)
					return delta.JsonObjectOf("a", a, "b", float64(0)), nil
				},
			})),
		},
		Key: []string{"id"},
	}), "dur:notes")
	read, err = notes.Reader("meta")
	must(t, err)
	var ctx context.Context = background
	cell, err = read(`{"a":5}`, 1, ctx)
	must(t, err)
	qEqual(t, cell, delta.JsonObjectOf("a", float64(5), "b", float64(0)))
	cell, err = read(`{"a":5,"b":6}`, 2, ctx)
	must(t, err)
	qEqual(t, cell, delta.JsonObjectOf("a", float64(5), "b", float64(6)))
}
