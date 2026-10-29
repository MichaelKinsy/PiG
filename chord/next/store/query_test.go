package store

import (
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/test/next/store/query.test.ts: queries (slice 6a §7): `where`, `orderBy` and limits over a
// table, and cursors paging through them.
//
// Go forms: `NewQuery` is Pi's `query`; Where and OrderBy keep their first error in the query (`Err`), where Pi
// throws it from the call.

func queryEdits(t *testing.T) *TableDefinition {
	t.Helper()
	return qDefine(t, "edits", TableOptions{
		Version: 1,
		Columns: []ColumnProp{Col("id", Str()), Col("entry", Int()), Col("at", Int()), Col("note", Opt(Str()))},
		Key:     []string{"id", "entry"},
	})
}

func byAt(e ColumnRefs) *ColumnRef { return e["at"] }

func TestQueries(t *testing.T) {
	T := queryEdits(t)

	t.Run("are immutable; several wheres are and-ed", func(t *testing.T) {
		base := NewQuery(T)
		one := base.Where(func(e ColumnRefs) Condition { return Eq(e["id"], "a") })
		two := one.Where(func(e ColumnRefs) Condition { return Gt(e["at"], 1) })
		must(t, base.Err())
		must(t, one.Err())
		must(t, two.Err())
		if base.Condition != nil {
			t.Fatalf("base condition = %#v, want none", base.Condition)
		}
		qEqual(t, *one.Condition, whereCmp(WhereEq, "id", "a"))
		qEqual(t, *two.Condition, WhereNode{Op: WhereAnd, Nodes: []WhereNode{whereCmp(WhereEq, "id", "a"), whereCmp(WhereGt, "at", int64(1))}})
	})

	t.Run("order by the key ascending by default and append the key as the tie-break, not repeated", func(t *testing.T) {
		qEqual(t, NewQuery(T).Order(), Order{Columns: []string{"id", "entry"}, Direction: "asc"})
		qEqual(t, NewQuery(T).OrderBy(byAt, "desc").Order(), Order{Columns: []string{"at", "id", "entry"}, Direction: "desc"})
		qEqual(t, NewQuery(T).OrderBy(func(e ColumnRefs) *ColumnRef { return e["entry"] }, "").Order(),
			Order{Columns: []string{"entry", "id"}, Direction: "asc"})
	})

	t.Run("refuse duplicate orderBy columns, two directions and opt columns (rule 17)", func(t *testing.T) {
		qFails(t, NewQuery(T).OrderBy(byAt, "").OrderBy(byAt, "").Err(), "twice")
		qFails(t, NewQuery(T).OrderBy(byAt, "").OrderBy(func(e ColumnRefs) *ColumnRef { return e["id"] }, "desc").Err(), "one direction")
		rejected(t, NewQuery(T).OrderBy(func(e ColumnRefs) *ColumnRef { return e["note"] }, "").Err(), data.CodeInvalid, data.ReasonArgs)
	})

	t.Run("a failed where is kept: the chain returns its first error", func(t *testing.T) {
		failed := NewQuery(T).
			Where(func(e ColumnRefs) Condition { return Eq(e["at"], "x") }).
			OrderBy(byAt, "")
		rejected(t, failed.Err(), data.CodeInvalid, data.ReasonArgs)
	})
}

func TestCursors(t *testing.T) {
	T := queryEdits(t)
	order := NewQuery(T).OrderBy(byAt, "desc").Order()
	cursorPattern := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

	t.Run("round-trip the order values, also across conditions with the same order", func(t *testing.T) {
		cursor := EncodeCursor("app:edits", order, []sqlite.SqliteValue{int64(5), "a\U0001F600", int64(2)})
		if !cursorPattern.MatchString(cursor) {
			t.Fatalf("cursor %q is not base64url", cursor)
		}
		want := []sqlite.SqliteValue{int64(5), "a\U0001F600", int64(2)}
		got, err := DecodeCursor(T, "app:edits", order, cursor)
		must(t, err)
		qEqual(t, got, want)
		other := NewQuery(T).Where(func(e ColumnRefs) Condition { return Eq(e["id"], "x") }).OrderBy(byAt, "desc")
		must(t, other.Err())
		got, err = DecodeCursor(T, "app:edits", other.Order(), cursor)
		must(t, err)
		qEqual(t, got, want)
	})

	t.Run("are a trust boundary: another table, order or direction, garbage and bad values are invalid/args", func(t *testing.T) {
		cursor := EncodeCursor("app:edits", order, []sqlite.SqliteValue{int64(5), "a", int64(2)})
		refuse := func(definition *TableDefinition, sqlName string, order Order, cursor any) {
			t.Helper()
			_, err := DecodeCursor(definition, sqlName, order, cursor)
			rejected(t, err, data.CodeInvalid, data.ReasonArgs)
		}
		refuse(T, "app:edits", NewQuery(T).Order(), cursor)
		refuse(T, "app:edits", NewQuery(T).OrderBy(byAt, "").Order(), cursor)
		other := qDefine(t, "other", TableOptions{
			Version: 1, Columns: []ColumnProp{Col("id", Str()), Col("entry", Int()), Col("at", Int())}, Key: []string{"id", "entry"},
		})
		refuse(other, "app:other", order, cursor)
		// The same definition in another object's table.
		refuse(T, "other:edits", order, cursor)
		refuse(T, "app:edits", order, "%%%")
		refuse(T, "app:edits", order, 42)
		refuse(T, "app:edits", order, EncodeCursor("app:edits", order, []sqlite.SqliteValue{"x", "a", int64(2)}))
		refuse(T, "app:edits", order, EncodeCursor("app:edits", order, []sqlite.SqliteValue{int64(5), "a"}))
	})
}
