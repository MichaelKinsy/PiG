package store

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/test/next/store/evaluate.test.ts: the evaluator of the table vocabulary, and that it agrees
// with SQLite on the same nodes.
//
// Go forms: an absent column (JavaScript `undefined`) is an absent map key; cells are str string, int int64, num
// float64, bool bool. The Node SQLite facade is blocking, so the Pi `async` bodies are plain.

func TestEvaluator(t *testing.T) {
	T := exprTable(t)
	refs := T.Refs
	row := EvaluatedRow{"id": "r", "path": "a/x", "n": int64(2), "x": float64(1), "b": true, "count": int64(3)}

	t.Run("follows SQL's NULL rules: a comparison with NULL never matches, not and ne included", func(t *testing.T) {
		matches := func(condition Condition) bool {
			node, err := CompileWhere(T, condition, "where")
			must(t, err)
			return Matches(node, row)
		}
		for name, c := range map[string]struct {
			condition Condition
			want      bool
		}{
			"eq":     {Eq(refs["note"], "a"), false},
			"ne":     {Ne(refs["note"], "a"), false},
			"not eq": {Not(Eq(refs["note"], "a")), false},
			"or":     {Or(IsNull(refs["note"]), Ne(refs["note"], "a")), true},
			"and()":  {And(), true},
			"or()":   {Or(), false},
			"in []":  {InArray(refs["n"], []any{}), false},
		} {
			if got := matches(c.condition); got != c.want {
				t.Fatalf("%s: matches = %v, want %v", name, got, c.want)
			}
		}
	})

	t.Run("evaluates every right side against the row before (a swap)", func(t *testing.T) {
		swap := qDefine(t, "s", TableOptions{
			Version: 1, Columns: []ColumnProp{Col("id", Str()), Col("a", Str()), Col("b", Str())}, Key: []string{"id"},
		})
		set, err := CompileSet(swap, SetOf{Assign("a", swap.Refs["b"]), Assign("b", swap.Refs["a"])}, "update")
		must(t, err)
		got, err := ApplySet(set, EvaluatedRow{"id": "1", "a": "x", "b": "y"}, "s")
		must(t, err)
		qEqual(t, got, EvaluatedRow{"id": "1", "a": "y", "b": "x"})
	})

	t.Run("leaves a value without the prefix unchanged and refuses integers outside the safe range (rule 10)", func(t *testing.T) {
		apply := func(set SetOf, row EvaluatedRow) (EvaluatedRow, error) {
			compiled, err := CompileSet(T, set, "u")
			must(t, err)
			return ApplySet(compiled, row, "t")
		}
		got, err := apply(SetOf{Assign("path", ReplacePrefix(refs["path"], "b/", "c/"))}, row)
		must(t, err)
		qEqual(t, got["path"], "a/x")
		got, err = apply(SetOf{Assign("path", ReplacePrefix(refs["path"], "a/", ""))}, row)
		must(t, err)
		qEqual(t, got["path"], "x")
		big := EvaluatedRow{}
		for column, value := range row {
			big[column] = value
		}
		big["n"] = MaxSafeInteger
		_, err = apply(SetOf{Assign("n", Add(refs["n"], 1))}, big)
		rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	})
}

var evaluatorRows = []EvaluatedRow{
	{"id": "1", "path": "a/x", "n": int64(1), "x": 0.5, "b": true, "note": "z", "count": int64(1)},
	{"id": "2", "path": "a/\U0001F600", "n": int64(2), "x": float64(2), "b": false},
	{"id": "3", "path": "a\uFFFF", "n": int64(-3), "x": float64(-1), "b": true, "note": ""},
	{"id": "4", "path": "b", "n": int64(0), "x": float64(0), "b": false, "count": int64(0)},
	{"id": "5", "path": "", "n": int64(9), "x": 9.5, "b": true, "note": "a/"},
	{"id": "6", "path": "\U0010FFFF", "n": int64(5), "x": float64(1), "b": false, "count": int64(-2)},
}

// evaluatorDatabase is a `:memory:` database with the table t holding evaluatorRows, and its statements.
func evaluatorDatabase(t *testing.T, T *TableDefinition) (sqlite.SqliteDatabase, *TableSql) {
	t.Helper()
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	must(t, db.Run(CreateTableSql("t", T.Record, T.Key)))
	sql := NewTableSql("t", T.Named(), T.Rows.Physical, T.Key)
	for _, each := range evaluatorRows {
		encoded, err := T.Rows.Put(Row(each), 1)
		must(t, err)
		must(t, db.Run(sql.Put, encoded.Params...))
	}
	return db, sql
}

func idsOf(t *testing.T, rows []sqlite.SqliteRow) []string {
	t.Helper()
	ids := []string{}
	for _, row := range rows {
		ids = append(ids, row["id"].(string))
	}
	return ids
}

// sameRows fails unless SQLite selects exactly the rows the evaluator matches for condition.
func sameRows(t *testing.T, T *TableDefinition, db sqlite.SqliteDatabase, condition Condition) {
	t.Helper()
	compiled, err := CompileWhere(T, condition, "where")
	must(t, err)
	var params []sqlite.SqliteValue
	text := WhereSql(compiled, &params)
	rows, err := db.All(fmt.Sprintf("SELECT id FROM t WHERE %s ORDER BY id", text), params...)
	must(t, err)
	expected := []string{}
	for _, each := range evaluatorRows {
		if Matches(compiled, each) {
			expected = append(expected, each["id"].(string))
		}
	}
	if got := idsOf(t, rows); !reflect.DeepEqual(got, expected) {
		t.Fatalf("%s: SQLite selects %v, the evaluator matches %v", qJSON(t, compiled), got, expected)
	}
}

func TestEvaluatorAgreesWithSQLite(t *testing.T) {
	T := exprTable(t)
	refs := T.Refs

	t.Run("selects the rows the evaluator matches", func(t *testing.T) {
		db, _ := evaluatorDatabase(t, T)
		for _, condition := range []Condition{
			Eq(refs["note"], "z"),
			Ne(refs["note"], "z"),
			Not(Ne(refs["count"], 0)),
			Lt(refs["path"], "a\uFFFF"),
			Gte(refs["path"], "a/\U0001F600"),
			Prefix(refs["path"], "a"),
			Prefix(refs["path"], ""),
			Prefix(refs["path"], "\U0010FFFF"),
			InArray(refs["path"], []any{"b", "", "x"}),
			InArray(refs["b"], []any{true}),
			Or(IsNull(refs["count"]), Gt(refs["x"], 1)),
			And(Not(IsNull(refs["note"])), Lte(refs["n"], 1)),
			Not(Or(Eq(refs["b"], true), Lt(refs["count"], 0))),
		} {
			sameRows(t, T, db, condition)
		}
	})

	t.Run("agrees on every operator's NULL truth under not, and, or and when", func(t *testing.T) {
		predicates := []Condition{
			Eq(refs["note"], "z"),
			Ne(refs["note"], "z"),
			Lt(refs["count"], 1),
			Lte(refs["count"], 0),
			Gt(refs["note"], "a"),
			Gte(refs["count"], 0),
			InArray(refs["note"], []any{"z", ""}),
			InArray(refs["note"], []any{}),
			Prefix(refs["note"], "a"),
			Prefix(refs["note"], ""),
			IsNull(refs["count"]),
		}
		var wrapped []Condition
		for _, p := range predicates {
			wrapped = append(wrapped,
				p,
				Not(p),
				And(p, Eq(refs["b"], true)),
				Or(p, Eq(refs["b"], true)),
				Not(And(p, Eq(refs["b"], true))),
				Not(Or(p, Eq(refs["b"], false))),
			)
		}
		db, _ := evaluatorDatabase(t, T)
		for _, condition := range wrapped {
			sameRows(t, T, db, condition)
		}
		for _, condition := range wrapped {
			db, sql := evaluatorDatabase(t, T)
			set, err := CompileSet(T, SetOf{Assign("n", When(condition, 1, 2))}, "updateWhere")
			must(t, err)
			statement := sql.UpdateWhere(WhereNode{Op: WhereAnd}, set, map[string]int{})
			must(t, db.Run(statement.SQL, statement.Params...))
			stored, err := db.All(`SELECT * FROM t ORDER BY id`)
			must(t, err)
			for index, each := range stored {
				read, err := T.Rows.Decode(each, background)
				must(t, err)
				want, err := ApplySet(set, evaluatorRows[index], "t")
				must(t, err)
				if !reflect.DeepEqual(EvaluatedRow(read), want) {
					t.Fatalf("%s: SQLite gives %#v, the evaluator %#v", qJSON(t, set), read, want)
				}
			}
		}
	})

	t.Run("binds a long inArray list as one parameter (beyond SQLite's 32,766 variables)", func(t *testing.T) {
		db, _ := evaluatorDatabase(t, T)
		values := make([]any, 0, 40001)
		for i := range 40000 {
			values = append(values, "v"+fmt.Sprint(i))
		}
		values = append(values, "b")
		compiled, err := CompileWhere(T, InArray(refs["path"], values), "where")
		must(t, err)
		var params []sqlite.SqliteValue
		text := WhereSql(compiled, &params)
		if len(params) != 1 {
			t.Fatalf("params = %d, want 1", len(params))
		}
		rows, err := db.All("SELECT id FROM t WHERE "+text, params...)
		must(t, err)
		qEqual(t, idsOf(t, rows), []string{"4"})
	})

	t.Run("updates rows as the evaluator does", func(t *testing.T) {
		sets := []SetOf{
			{Assign("n", Add(refs["n"], 10)), Assign("count", Sub(Coalesce(refs["count"], 5), 1))},
			{Assign("path", ReplacePrefix(refs["path"], "a/", "\U0001F600/")), Assign("note", Concat(refs["path"], "!"))},
			{Assign("x", Min(refs["x"], 1)), Assign("n", Max(refs["n"], 1)), Assign("note", When(Prefix(refs["path"], "a"), refs["path"], refs["note"]))},
			{Assign("note", refs["path"]), Assign("path", Coalesce(refs["note"], "none")), Assign("count", Undefined)},
		}
		for _, each := range sets {
			db, sql := evaluatorDatabase(t, T)
			set, err := CompileSet(T, each, "updateWhere")
			must(t, err)
			statement := sql.UpdateWhere(WhereNode{Op: WhereAnd}, set, map[string]int{})
			must(t, db.Run(statement.SQL, statement.Params...))
			stored, err := db.All(`SELECT * FROM t ORDER BY id`)
			must(t, err)
			for index, row := range stored {
				read, err := T.Rows.Decode(row, background)
				must(t, err)
				want, err := ApplySet(set, evaluatorRows[index], "t")
				must(t, err)
				if !reflect.DeepEqual(EvaluatedRow(read), want) {
					t.Fatalf("%s: row %d: SQLite gives %#v, the evaluator %#v", strings.TrimSpace(qJSON(t, set)), index, read, want)
				}
			}
		}
	})
}
