package store

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/next/store/expression.test.ts: the table expression language: the builders and their
// compilation into the data vocabulary.
//
// Go forms: a JavaScript `undefined` operand or value is `Undefined`, `null` is nil; a set is a SetOf; a compiled
// node is compared with its Go struct form, and a compiled set with the JSON Pi's `JSON.parse(JSON.stringify(set))`
// gives (the exact text, key order included). The case `eq({ column: "nope" } as never, 1)` builds the Condition
// with a plain map as its column, since a Go builder takes a *ColumnRef.

// exprTable is the table of the expression and evaluator tests.
func exprTable(t testing.TB) *TableDefinition {
	t.Helper()
	return qDefine(t, "t", TableOptions{
		Version: 1,
		Columns: []ColumnProp{
			Col("id", Str()),
			Col("path", Str()),
			Col("n", Int()),
			Col("x", Num()),
			Col("b", Bool()),
			Col("note", Opt(Str())),
			Col("count", Opt(Int())),
			Col("meta", Opt(Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1}))),
		},
		Key: []string{"id"},
	})
}

func whereCmp(op WhereOp, column string, value Scalar) WhereNode {
	return WhereNode{Op: op, Column: column, Value: value}
}

func colNode(name string) ExprNode { return ExprNode{Op: ExprCol, Column: name} }

func litNode(value Scalar) ExprNode { return ExprNode{Op: ExprLiteral, Value: value} }

func TestCompilingConditions(t *testing.T) {
	T := exprTable(t)
	refs := T.Refs

	t.Run("compiles every operator into data", func(t *testing.T) {
		where := And(
			Eq(refs["path"], "a"),
			Ne(refs["n"], 1),
			Lt(refs["n"], 2),
			Lte(refs["x"], 2.5),
			Gt(refs["n"], 0),
			Gte(refs["path"], "a"),
			InArray(refs["n"], []any{1, 2}),
			IsNull(refs["note"]),
			Prefix(refs["path"], "a/"),
			Or(Not(Eq(refs["b"], true))),
		)
		got, err := CompileWhere(T, where, "where")
		must(t, err)
		qEqual(t, got, WhereNode{Op: WhereAnd, Nodes: []WhereNode{
			whereCmp(WhereEq, "path", "a"),
			whereCmp(WhereNe, "n", int64(1)),
			whereCmp(WhereLt, "n", int64(2)),
			whereCmp(WhereLte, "x", 2.5),
			whereCmp(WhereGt, "n", int64(0)),
			whereCmp(WhereGte, "path", "a"),
			{Op: WhereInArray, Column: "n", Values: []Scalar{int64(1), int64(2)}},
			{Op: WhereIsNull, Column: "note"},
			whereCmp(WherePrefix, "path", "a/"),
			{Op: WhereOr, Nodes: []WhereNode{{Op: WhereNot, Nodes: []WhereNode{whereCmp(WhereEq, "b", true)}}}},
		}})
		qEqual(t, qJSON(t, got), `{"and":[{"eq":["path","a"]},{"ne":["n",1]},{"lt":["n",2]},{"lte":["x",2.5]},{"gt":["n",0]},`+
			`{"gte":["path","a"]},{"inArray":["n",[1,2]]},{"isNull":"note"},{"prefix":["path","a/"]},{"or":[{"not":{"eq":["b",true]}}]}]}`)
	})

	t.Run("refuses undefined and null operands (rule 16), wrong types, json and unknown columns", func(t *testing.T) {
		_, err := CompileWhere(T, Eq(refs["note"], Undefined), "where")
		qFails(t, err, "ask with isNull")
		_, err = CompileWhere(T, Eq(refs["note"], nil), "where")
		rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		_, err = CompileWhere(T, Eq(refs["n"], "1"), "where")
		rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		_, err = CompileWhere(T, Eq(refs["n"], 1.5), "where")
		rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		_, err = CompileWhere(T, Eq(refs["meta"], 1), "where")
		qFails(t, err, "not queryable")
		_, err = CompileWhere(T, Condition{Op: WhereEq, Args: []any{map[string]any{"column": "nope"}, 1}}, "where")
		qFails(t, err, "expected a column reference")
		_, err = CompileWhere(T, "x", "where")
		qFails(t, err, "expected a condition")
		_, err = CompileWhere(T, Prefix(refs["n"], "a"), "where")
		qFails(t, err, "str() column")
	})

	t.Run("makes text operands storable", func(t *testing.T) {
		got, err := CompileWhere(T, Eq(refs["path"], "a\xed\xa0\x80"), "where")
		must(t, err)
		qEqual(t, got, whereCmp(WhereEq, "path", "a\uFFFD"))
		_, err = CompileWhere(T, Prefix(refs["path"], "a\x00"), "where")
		qFails(t, err, "NUL")
	})
}

func TestCompilingSets(t *testing.T) {
	T := exprTable(t)
	refs := T.Refs

	t.Run("compiles plain values and expressions into one vocabulary, a clear as null (rule 14)", func(t *testing.T) {
		got, err := CompileSet(T, SetOf{
			Assign("path", "p"), Assign("note", Undefined), Assign("b", true), Assign("meta", delta.JsonObjectOf("a", float64(1))),
		}, "update")
		must(t, err)
		qEqual(t, got, SetNode{
			{Column: "path", Node: litNode("p")},
			{Column: "note", Node: ExprNode{Op: ExprNull}},
			{Column: "b", Node: litNode(true)},
			{Column: "meta", Node: ExprNode{Op: ExprJSON, Json: delta.JsonObjectOf("a", float64(1))}},
		})
		set, err := CompileSet(T, SetOf{
			Assign("n", Add(refs["n"], 1)),
			Assign("count", Sub(Coalesce(refs["count"], 0), refs["n"])),
			Assign("x", Max(refs["x"], 1.5)),
			Assign("path", ReplacePrefix(Concat(refs["path"], "/"), "a/", "b/")),
			Assign("note", When(Gt(refs["n"], 1), refs["path"], refs["note"])),
		}, "update")
		must(t, err)
		qEqual(t, qJSON(t, set), `{"n":{"add":[{"col":"n"},1]},`+
			`"count":{"sub":[{"coalesce":[{"col":"count"},0]},{"col":"n"}]},`+
			`"x":{"max":[{"col":"x"},1.5]},`+
			`"path":{"replacePrefix":[{"concat":[{"col":"path"},"/"]},"a/","b/"]},`+
			`"note":{"when":[{"gt":["n",1]},{"col":"path"},{"col":"note"}]}}`)
	})

	t.Run("refuses an empty set, key columns (rule 13), unknown columns and clears of non-opt columns", func(t *testing.T) {
		_, err := CompileSet(T, SetOf{}, "update")
		qFails(t, err, "empty")
		_, err = CompileSet(T, SetOf{Assign("id", "x")}, "update")
		qFails(t, err, "cannot change")
		_, err = CompileSet(T, SetOf{Assign("nope", 1)}, "update")
		qFails(t, err, `no column "nope"`)
		_, err = CompileSet(T, SetOf{Assign("path", Undefined)}, "update")
		qFails(t, err, "cannot be cleared")
	})

	t.Run("undefined clears an opt column; null is a json(...) cell's JSON null and refused for scalar columns", func(t *testing.T) {
		got, err := CompileSet(T, SetOf{Assign("note", Undefined), Assign("meta", Undefined)}, "update")
		must(t, err)
		qEqual(t, got, SetNode{{Column: "note", Node: ExprNode{Op: ExprNull}}, {Column: "meta", Node: ExprNode{Op: ExprNull}}})
		nullable := qDefine(t, "n", TableOptions{
			Version: 1,
			Columns: []ColumnProp{Col("id", Str()), Col("data", Json(contracts.Nul(), JsonColumnOptions{Version: 1}))},
			Key:     []string{"id"},
		})
		got, err = CompileSet(nullable, SetOf{Assign("data", nil)}, "update")
		must(t, err)
		qEqual(t, got, SetNode{{Column: "data", Node: ExprNode{Op: ExprJSON, Json: nil}}})
		_, err = CompileSet(T, SetOf{Assign("note", nil)}, "update")
		qFails(t, err, "null is no str value")
	})

	t.Run("refuses integer arithmetic outside int columns (rule 10), type mismatches and nullable results into non-opt columns", func(t *testing.T) {
		_, err := CompileSet(T, SetOf{Assign("x", Add(refs["n"], 1))}, "update")
		qFails(t, err, "add gives int")
		_, err = CompileSet(T, SetOf{Assign("path", Max(refs["n"], 1))}, "update")
		qFails(t, err, "gives int or num")
		_, err = CompileSet(T, SetOf{Assign("path", refs["n"])}, "update")
		qFails(t, err, `column "n" is int, not str`)
		_, err = CompileSet(T, SetOf{Assign("n", Add(refs["count"], 1))}, "update")
		qFails(t, err, "may be NULL")
		_, err = CompileSet(T, SetOf{Assign("path", refs["note"])}, "update")
		qFails(t, err, "may be NULL")
		got, err := CompileSet(T, SetOf{Assign("path", Coalesce(refs["note"], "d"))}, "update")
		must(t, err)
		qEqual(t, got, SetNode{{Column: "path", Node: ExprNode{Op: ExprCoalesce, Args: []ExprNode{colNode("note"), litNode("d")}}}})
		_, err = CompileSet(T, SetOf{Assign("meta", refs["meta"])}, "update")
		qFails(t, err, "plain values")
	})
}
