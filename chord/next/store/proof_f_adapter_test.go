package store

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Proof tests of packages/chord/src/next/store/adapter.ts (`StoreOwnerTables`, `OwnerTable`), query.ts (`Query`),
// expression.ts (`Expression`) and outbox.ts (`NewOutbox`).

func TestProofFStoreOwnerTablesDeclareClaimAndPrepareEachTable(t *testing.T) {
	store := memoryStore(t, true)
	claimant := &NamedClaimant{Label: "dur adapter"}
	var owned *StoreOwnerTables
	owned, err := NewStoreOwnerTables(store, "dur", claimant)
	must(t, err)
	if owned.Store != store || owned.Owner != "dur" {
		t.Fatalf("owned = {store %p, owner %q}, want {%p, dur}", owned.Store, owned.Owner, store)
	}
	for _, bad := range []string{"Bad Owner", "UPPER", "a:b", ""} {
		if _, err := NewStoreOwnerTables(store, bad, claimant); err == nil {
			t.Fatalf("owner name %q was accepted", bad)
		}
	}
	entries := storeBEntries(t)
	notes := qDefine(t, "notes", TableOptions{
		Version: 3,
		Columns: []ColumnProp{Col("id", Str()), Col("body", Opt(Str()))},
		Key:     []string{"id"},
	})
	first, err := owned.Declare(entries)
	must(t, err)
	second, err := owned.Declare(notes)
	must(t, err)
	if first.Sql.Table != "dur:entries" || second.Sql.Table != "dur:notes" || first.Definition != entries || second.Definition != notes {
		t.Fatalf("declared = %q/%q", first.Sql.Table, second.Sql.Table)
	}
	// The SQL name is claimed for the claimant: another claimant is refused, the same one is not.
	intruder, err := NewStoreOwnerTables(store, "dur", &NamedClaimant{Label: "intruder"})
	must(t, err)
	_, err = intruder.Declare(notes)
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	if err := store.Claims.AssertTable("dur:notes", claimant); err != nil {
		t.Fatalf("the claimant does not hold dur:notes: %v", err)
	}
	if store.Table("dur:entries") != nil || store.Table("dur:notes") != nil {
		t.Fatal("tables exist before Prepare")
	}
	must(t, owned.Prepare(background))
	// Every declared table is created at its definition's version, not only the first.
	for _, c := range []struct {
		name        string
		version     int
		fingerprint string
	}{{"dur:entries", 1, entries.Fingerprint}, {"dur:notes", 3, notes.Fingerprint}} {
		record := store.Table(c.name)
		if record == nil || record.Version != c.version || record.State != StateReady || record.Fingerprint != c.fingerprint {
			t.Fatalf("record of %s = %+v, want version %d, ready, fingerprint %s", c.name, record, c.version, c.fingerprint)
		}
	}
}

func TestProofFOwnerTableWritesStampedRowsAndReadsThemBack(t *testing.T) {
	store, table := storeBPrepared(t)
	var owner *OwnerTable = table
	if owner.Definition.Name != "entries" || owner.Sql.Table != "dur:entries" {
		t.Fatalf("table = %q/%q", owner.Definition.Name, owner.Sql.Table)
	}
	encoded, err := owner.Encode(Row{"id": "k", "seq": int64(0), "text": "v"})
	must(t, err)
	before := encoded.Row.Value("seq")
	paramsBefore := append([]sqlite.SqliteValue(nil), encoded.Params...)
	var recorded StoreWrite
	seq, err := store.Commit([]StagedWrite{stagedApply(func(step *Step) ([]StoreWrite, error) {
		write, err := owner.Put(step, encoded, "seq")
		recorded = write
		return []StoreWrite{write}, err
	})}, openFence, background)
	must(t, err)
	put := recorded.(*PutWrite)
	if put.Table != "dur:entries" || put.Version != 1 || put.Row.Value("seq") != float64(seq) {
		t.Fatalf("recorded put = %+v, want table dur:entries, version 1, seq %d", put, seq)
	}
	qEqual(t, encoded.Row.Value("seq"), before)
	qEqual(t, encoded.Params, paramsBefore)

	row, err := Read(store, func(executor sqlite.SqliteExecutor) (DecodedRow, error) {
		return owner.Get(background, executor, []sqlite.SqliteValue{"k"})
	}, background)
	must(t, err)
	qEqual(t, row, DecodedRow{"id": "k", "seq": seq, "text": "v"})
	where := WhereNode{Op: WhereEq, Column: "text", Value: "v"}
	rows, err := Read(store, func(executor sqlite.SqliteExecutor) ([]DecodedRow, error) {
		return owner.Select(background, executor, &where, Order{Columns: []string{"id"}, Direction: "asc"}, 5)
	}, background)
	must(t, err)
	qEqual(t, rows, []DecodedRow{{"id": "k", "seq": seq, "text": "v"}})
	none := WhereNode{Op: WhereEq, Column: "text", Value: "other"}
	rows, err = Read(store, func(executor sqlite.SqliteExecutor) ([]DecodedRow, error) {
		return owner.Select(background, executor, &none, Order{Columns: []string{"id"}, Direction: "asc"}, 5)
	}, background)
	must(t, err)
	if len(rows) != 0 {
		t.Fatalf("select of no match = %v", rows)
	}

	// Without a stamp the row's own value is written, and the same key is an upsert.
	plain, err := owner.Encode(Row{"id": "k", "seq": int64(7), "text": "w"})
	must(t, err)
	_, err = store.Commit([]StagedWrite{stagedApply(func(step *Step) ([]StoreWrite, error) {
		write, err := owner.Put(step, plain, "")
		return []StoreWrite{write}, err
	})}, openFence, background)
	must(t, err)
	row, err = Read(store, func(executor sqlite.SqliteExecutor) (DecodedRow, error) {
		return owner.Get(background, executor, []sqlite.SqliteValue{"k"})
	}, background)
	must(t, err)
	qEqual(t, row, DecodedRow{"id": "k", "seq": int64(7), "text": "w"})
	if text := owner.PrepareFirst(nil, nil, Order{Columns: []string{"id"}, Direction: "desc"}); !strings.HasSuffix(text, `ORDER BY "id" DESC LIMIT 1`) {
		t.Fatalf("PrepareFirst = %q, want a literal LIMIT 1", text)
	}
	if text := owner.Prepare([]string{"id"}, nil, Order{Columns: []string{"id"}, Direction: "asc"}); !strings.HasSuffix(text, `ORDER BY "id" ASC LIMIT ?`) {
		t.Fatalf("Prepare = %q, want a bound LIMIT", text)
	}
}

func TestProofFNewOwnerTableBuildsItsSqlFromTheDefinition(t *testing.T) {
	definition := storeBEntries(t)
	var table *OwnerTable = NewOwnerTable(definition, "x:entries")
	if table.Definition != definition || table.Sql.Table != "x:entries" {
		t.Fatalf("table = %+v", table)
	}
	qEqual(t, table.PrepareGet([]string{"text"}), `SELECT "text" FROM "x:entries" WHERE "id" = ?`)
	qEqual(t, table.Sql.Put != "", true)
	if !strings.Contains(table.Sql.Put, `"x:entries"`) {
		t.Fatalf("Put statement %q does not name the table", table.Sql.Put)
	}
	version, err := table.JsonVersion("meta")
	must(t, err)
	qEqual(t, version, 1)
}

func TestProofFQueryChainsWhereAndOrderIntoWhatARunReturns(t *testing.T) {
	store, table := storeBPrepared(t)
	storeBCommit(t, store, table, "", Row{"id": "a", "seq": int64(1)}, Row{"id": "b", "seq": int64(2)}, Row{"id": "c", "seq": int64(9)})
	run := func(query *Query) []string {
		t.Helper()
		must(t, query.Err())
		rows, err := Read(store, func(executor sqlite.SqliteExecutor) ([]DecodedRow, error) {
			return table.Select(background, executor, query.Condition, query.Order(), 10)
		}, background)
		must(t, err)
		ids := make([]string, len(rows))
		for index, row := range rows {
			ids[index], _ = row["id"].(string)
		}
		return ids
	}
	var base *Query = NewQuery(table.Definition)
	if got := strings.Join(run(base), ","); got != "a,b,c" {
		t.Fatalf("unfiltered query = %s, want the rows in key order", got)
	}
	atLeastB := base.Where(func(row ColumnRefs) Condition { return Gte(row["id"], "b") })
	below5 := atLeastB.Where(func(row ColumnRefs) Condition { return Lt(row["seq"], 5) })
	if got := strings.Join(run(atLeastB), ","); got != "b,c" {
		t.Fatalf("where id >= b = %s", got)
	}
	// Several wheres are and-ed; the earlier query is unchanged.
	if got := strings.Join(run(below5), ","); got != "b" {
		t.Fatalf("where id >= b and seq < 5 = %s, want b", got)
	}
	if got := strings.Join(run(atLeastB), ","); got != "b,c" {
		t.Fatalf("the earlier query changed to %s", got)
	}
	if base.Condition != nil {
		t.Fatalf("the base query got a condition %#v", base.Condition)
	}
	descending := atLeastB.OrderBy(func(row ColumnRefs) *ColumnRef { return row["seq"] }, "desc")
	if got := strings.Join(run(descending), ","); got != "c,b" {
		t.Fatalf("order by seq desc = %s, want c,b", got)
	}
	qEqual(t, descending.Order(), Order{Columns: []string{"seq", "id"}, Direction: "desc"})
	qEqual(t, atLeastB.Order(), Order{Columns: []string{"id"}, Direction: "asc"})
	// Ordering by a key column does not repeat it as the tie-break.
	qEqual(t, base.OrderBy(func(row ColumnRefs) *ColumnRef { return row["id"] }, "").Order(), Order{Columns: []string{"id"}, Direction: "asc"})
	// A refused call is kept and later calls return the same query.
	refused := atLeastB.OrderBy(func(row ColumnRefs) *ColumnRef { return row["text"] }, "")
	_ = rejected(t, refused.Err(), data.CodeInvalid, data.ReasonArgs)
	again := refused.Where(func(row ColumnRefs) Condition { return Eq(row["id"], "a") })
	if again.Err() == nil || again.Err() != refused.Err() {
		t.Fatalf("a call on a failed query = %v, want the first error %v", again.Err(), refused.Err())
	}
}

func TestProofFExpressionCompilesIntoDataForEveryOperation(t *testing.T) {
	table := exprTable(t)
	refs := table.Refs
	var ref *Expression = refs["n"]
	if column, ok := ref.isRef(); !ok || column != "n" || ref.Op != ExprCol {
		t.Fatalf("column ref = %+v", ref)
	}
	qEqual(t, NewColumnRef("n"), &Expression{Op: ExprCol, Args: []any{"n"}})
	for _, notRef := range []*Expression{
		{Op: ExprCol, Args: []any{"n", "m"}},
		{Op: ExprCol, Args: []any{int64(1)}},
		{Op: ExprAdd, Args: []any{"n"}},
		nil,
	} {
		if _, ok := notRef.isRef(); ok {
			t.Fatalf("%+v is no column reference", notRef)
		}
	}
	compile := func(column string, value *Expression) (SetNode, error) {
		return CompileSet(table, SetOf{Assign(column, value)}, "set")
	}
	cases := []struct {
		name   string
		column string
		expr   *Expression
		want   ExprNode
	}{
		{"add", "n", &Expression{Op: ExprAdd, Args: []any{refs["n"], 2}}, ExprNode{Op: ExprAdd, Args: []ExprNode{colNode("n"), litNode(int64(2))}}},
		{"sub", "n", &Expression{Op: ExprSub, Args: []any{refs["n"], 2}}, ExprNode{Op: ExprSub, Args: []ExprNode{colNode("n"), litNode(int64(2))}}},
		{"max", "x", &Expression{Op: ExprMax, Args: []any{refs["x"], 1.5}}, ExprNode{Op: ExprMax, Args: []ExprNode{colNode("x"), litNode(1.5)}}},
		{"min", "n", &Expression{Op: ExprMin, Args: []any{3, refs["n"]}}, ExprNode{Op: ExprMin, Args: []ExprNode{litNode(int64(3)), colNode("n")}}},
		{"concat", "path", &Expression{Op: ExprConcat, Args: []any{refs["path"], "/x"}}, ExprNode{Op: ExprConcat, Args: []ExprNode{colNode("path"), litNode("/x")}}},
		{"coalesce", "path", &Expression{Op: ExprCoalesce, Args: []any{refs["note"], "dflt"}}, ExprNode{Op: ExprCoalesce, Args: []ExprNode{colNode("note"), litNode("dflt")}}},
		{"replacePrefix", "path", &Expression{Op: ExprReplacePrefix, Args: []any{refs["path"], "a/", "b/"}}, ExprNode{Op: ExprReplacePrefix, Args: []ExprNode{colNode("path")}, From: "a/", To: "b/"}},
		{"when", "n", &Expression{Op: ExprWhen, Args: []any{Gt(refs["n"], 1), 10, refs["n"]}}, ExprNode{Op: ExprWhen, Cond: &WhereNode{Op: WhereGt, Column: "n", Value: int64(1)}, Args: []ExprNode{litNode(int64(10)), colNode("n")}}},
	}
	for _, c := range cases {
		node, err := compile(c.column, c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		qEqual(t, node, SetNode{{Column: c.column, Node: c.want}})
	}
	// The builders make the same Expressions.
	qEqual(t, Add(refs["n"], 2), cases[0].expr)
	qEqual(t, Coalesce(refs["note"], "dflt"), cases[5].expr)
	// An expression whose result may be NULL does not fit a column that is not opt(...); coalescing with a value does.
	_, err := compile("path", &Expression{Op: ExprConcat, Args: []any{refs["note"], "x"}})
	qFails(t, err, "the expression may be NULL")
	_, err = compile("count", &Expression{Op: ExprAdd, Args: []any{refs["count"], 1}})
	must(t, err)
	// A type that the operation does not give, and an operation nobody knows, are refused.
	_, err = compile("path", &Expression{Op: ExprAdd, Args: []any{refs["n"], 1}})
	qFails(t, err, "add gives int")
	_, err = compile("n", &Expression{Op: ExprOp("pow"), Args: []any{refs["n"], 2}})
	qFails(t, err, `unknown operation "pow"`)
	_, err = compile("n", &Expression{Op: ExprAdd, Args: []any{&Expression{Op: ExprCol, Args: []any{"nope"}}, 1}})
	qFails(t, err, `no column "nope"`)
	// An Expression is a reference only as a condition's column: a computed one is refused there.
	_, err = CompileWhere(table, Eq(&Expression{Op: ExprAdd, Args: []any{refs["n"], 1}}, 2), "where")
	qFails(t, err, "expected a column reference")
}

func TestProofFNewOutboxStartsAfterItsFloor(t *testing.T) {
	store := memoryStore(t, true)
	for _, key := range []string{"a", "b", "c"} {
		outboxCreated(t, store, key)
	}
	outbox := NewOutbox(store, 1)
	if outbox.Floor() != 1 {
		t.Fatalf("Floor = %d, want 1", outbox.Floor())
	}
	// Reads start at the floor and not before it.
	commits, err := outbox.Read(1, 10, background)
	must(t, err)
	if len(commits) != 2 || commits[0].Seq != 2 || commits[1].Seq != 3 {
		t.Fatalf("commits after the floor = %+v, want seqs 2 and 3", commits)
	}
	_, err = outbox.Read(0, 10, background)
	_ = rejected(t, err, data.CodeInvalid, data.ReasonArgs)
	// A new outbox is not behind another operation: it trims and reads at once, and Trim moves its floor.
	must(t, outbox.Trim(2, background))
	if outbox.Floor() != 2 {
		t.Fatalf("Floor after Trim(2) = %d, want 2", outbox.Floor())
	}
	if outbox.store != store {
		t.Fatal("the outbox is of another store")
	}
	later, err := NewOutbox(store, 0).Read(0, 10, background)
	must(t, err)
	if len(later) != 1 || later[0].Seq != 3 {
		t.Fatalf("commits left = %+v, want only seq 3", later)
	}
}
