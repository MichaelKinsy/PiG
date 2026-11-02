package store

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/store/sql.test.ts: SQL text (slice 6a §8): quoted names, the DDL of tables and
// indexes, and the statements of a table.

func sqlEdits(t *testing.T) *TableDefinition {
	t.Helper()
	definition, err := DefineTable("edits", TableOptions{
		Version: 1,
		Columns: []ColumnProp{
			Col("id", Str()),
			Col("at", Int(contracts.Default(0))),
			Col("x", Num()),
			Col("b", Bool()),
			Col("note", Opt(Str())),
			Col("meta", Json(contracts.Obj(), JsonColumnOptions{Version: 2})),
		},
		Key: []string{"id"},
	})
	if err != nil {
		t.Fatalf("DefineTable: %v", err)
	}
	return definition
}

func sqlEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestSqlNames(t *testing.T) {
	t.Run("renders <host kind>:<object type>:<table>, quoted", func(t *testing.T) {
		sqlEqual(t, TableSqlName("@pi/file-history.store", "edits"), "@pi/file-history.store:edits")
		sqlEqual(t, Quote("session:a:b"), `"session:a:b"`)
	})
}

func TestSqlDDL(t *testing.T) {
	edits := sqlEdits(t)

	t.Run("renders a STRICT WITHOUT ROWID table with checks, defaults, hidden version columns and the key", func(t *testing.T) {
		sqlEqual(t, CreateTableSql("s:o:edits", edits.Record, edits.Key),
			`CREATE TABLE "s:o:edits" ("id" TEXT NOT NULL, "at" INTEGER NOT NULL DEFAULT 0 CHECK ("at" BETWEEN -9007199254740991 AND 9007199254740991), `+
				`"x" REAL NOT NULL, "b" INTEGER NOT NULL CHECK ("b" IN (0, 1)), "note" TEXT, "meta" TEXT NOT NULL, "meta$v" INTEGER NOT NULL, `+
				`"$v" INTEGER NOT NULL, PRIMARY KEY ("id")) STRICT, WITHOUT ROWID`)
		sqlEqual(t, CreateIndexSql("s:o:edits", "by_at", []string{"at"}, []string{"id"}),
			`CREATE INDEX "s:o:edits:by_at" ON "s:o:edits" ("at", "id")`)
		sqlEqual(t, CreateIndexSql("s:o:edits", "by_id", []string{"id", "at"}, []string{"id"}),
			`CREATE INDEX "s:o:edits:by_id" ON "s:o:edits" ("id", "at")`)
		sqlEqual(t, DropIndexSql("s:o:edits", "by_at"), `DROP INDEX "s:o:edits:by_at"`)
	})

	t.Run("keeps int columns within ±(2^53 - 1) and bool columns within 0 and 1 (rule 10)", func(t *testing.T) {
		db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() {
			if err := db.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		}()
		if err := db.Run(CreateTableSql("t", edits.Record, edits.Key)); err != nil {
			t.Fatalf("create: %v", err)
		}
		sql := NewTableSql("t", edits.Named(), edits.Rows.Physical, edits.Key)
		encoded, err := edits.Rows.Put(Row{"id": "a", "x": float64(1), "b": true, "meta": delta.NewJsonObject(0)}, 1)
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := db.Run(sql.Put, encoded.Params...); err != nil {
			t.Fatalf("put: %v", err)
		}
		if err := db.Run(`UPDATE t SET at = ?`, int64(1)<<53); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Fatalf("update at = 2^53: %v, want CHECK", err)
		}
		if err := db.Run(`UPDATE t SET b = 2`); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Fatalf("update b = 2: %v, want CHECK", err)
		}
		if err := db.Run(`UPDATE t SET at = 'x'`); err == nil {
			t.Fatal("update at = 'x' succeeded, want an error")
		}
		if err := db.Run(`INSERT INTO t (id, x, b, meta, meta$v, "$v") VALUES ('d', 1, 0, '{}', 2, 1)`); err != nil {
			t.Fatalf("insert: %v", err)
		}
		row, err := db.Get(`SELECT at FROM t WHERE id = 'd'`)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		sqlEqual(t, row, sqlite.SqliteRow{"at": int64(0)})
	})
}

func TestSqlStatements(t *testing.T) {
	edits := sqlEdits(t)
	sql := NewTableSql("t", edits.Named(), edits.Rows.Physical, edits.Key)

	t.Run("renders the fixed statements once", func(t *testing.T) {
		sqlEqual(t, sql.Put, `INSERT OR REPLACE INTO "t" ("id", "at", "x", "b", "note", "meta", "meta$v", "$v") VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
		sqlEqual(t, sql.Get, `SELECT "id", "at", "x", "b", "note", "meta", "meta$v" FROM "t" WHERE "id" = ?`)
		sqlEqual(t, sql.Delete, `DELETE FROM "t" WHERE "id" = ?`)
	})

	t.Run("stamps a json column's version with its value, NULL with a clear", func(t *testing.T) {
		versions := map[string]int{"meta": 2}
		set := SetNode{{Column: "meta", Node: ExprNode{Op: ExprJSON, Json: delta.JsonObjectOf("a", float64(1))}}}
		sqlEqual(t, sql.Update(set, []sqlite.SqliteValue{"k"}, versions), Statement{
			SQL:    `UPDATE "t" SET "meta" = ?, "meta$v" = ? WHERE "id" = ?`,
			Params: []sqlite.SqliteValue{`{"a":1}`, int64(2), "k"},
		})
		clear := SetNode{{Column: "meta", Node: ExprNode{Op: ExprNull}}}
		sqlEqual(t, sql.Update(clear, []sqlite.SqliteValue{"k"}, versions).Params, []sqlite.SqliteValue{nil, "k"})
	})

	t.Run("binds every value: one text per query shape, whatever the values (rule 18)", func(t *testing.T) {
		order := Order{Columns: []string{"at", "id"}, Direction: "desc"}
		small := &WhereNode{Op: WhereInArray, Column: "id", Values: []Scalar{"a", "b"}}
		many := make([]Scalar, 40_000)
		for i := range many {
			many[i] = fmt.Sprintf("k%d", i)
		}
		large := &WhereNode{Op: WhereInArray, Column: "id", Values: many}
		a := sql.Select(small, order, []sqlite.SqliteValue{int64(1), "x"}, 10, nil)
		b := sql.Select(large, order, []sqlite.SqliteValue{int64(2), "y"}, 99, nil)
		sqlEqual(t, a.SQL, b.SQL)
		sqlEqual(t, a.SQL, `SELECT "id", "at", "x", "b", "note", "meta", "meta$v" FROM "t" WHERE "id" IN (SELECT value FROM json_each(?)) AND ("at", "id") < (?, ?) ORDER BY "at" DESC, "id" DESC LIMIT ?`)
		sqlEqual(t, a.Params, []sqlite.SqliteValue{`["a","b"]`, int64(1), "x", int64(10)})
		sqlEqual(t, sql.Count(&WhereNode{Op: WherePrefix, Column: "id", Value: "\U0010FFFF"}), Statement{
			SQL:    `SELECT count(*) AS count FROM "t" WHERE "id" >= ?`,
			Params: []sqlite.SqliteValue{"\U0010FFFF"},
		})
		sqlEqual(t, sql.DeleteWhere(WhereNode{Op: WherePrefix, Column: "id", Value: "a"}), Statement{
			SQL:    `DELETE FROM "t" WHERE ("id" >= ? AND "id" < ?)`,
			Params: []sqlite.SqliteValue{"a", "b"},
		})
	})
}
