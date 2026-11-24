package store

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/test/store/read-plan.test.ts (slice 14 §3.3, §5, §6): `exists`, `existsNode`, the one-to-one
// join and conditions as data. The cases that need no ReadPlan, no bound sets and no SQL are here; the rest come in
// the commits of read-plan.ts, sql.ts and reads.ts. Go forms: a Match is an ordered list of entries; Where, Join,
// WhereNode keep their first error in the query (`Err`), where Pi throws it from the call.

// rpSets are the fixtures of read-plan.test.ts: three tables, two of them exposing a column set.
type rpSets struct {
	SessionColumns, TeamSessions *ColumnSet
	Sessions, Teams, Notes       *TableDefinition
}

func rpFixtures(t testing.TB) rpSets {
	t.Helper()
	sessionColumns, err := DefineColumns("sessions.columns", ColumnSetOptions{
		Columns: []ColumnProp{
			Col("id", Str()), Col("app", Str()), Col("private", Bool()), Col("lastTouch", Int()), Col("archivedAt", Opt(Int())),
		},
		Key: []string{"id"},
	})
	must(t, err)
	teamSessions, err := DefineColumns("club.team-sessions", ColumnSetOptions{
		Columns: []ColumnProp{Col("team", Str()), Col("session", Str())},
		Key:     []string{"team", "session"},
		Indexes: IndexRecords{{Name: "ofSession", Columns: []string{"session"}}},
	})
	must(t, err)
	return rpSets{
		SessionColumns: sessionColumns,
		TeamSessions:   teamSessions,
		Sessions: qDefine(t, "sessions", TableOptions{
			Version: 1,
			Columns: append(slices.Clone(sessionColumns.Schemas), Col("title", Str())),
			Key:     []string{"id"},
			Indexes: IndexRecords{{Name: "by_app", Columns: []string{"app", "lastTouch"}}},
			Exposes: sessionColumns,
		}),
		Teams: qDefine(t, "teams", TableOptions{
			Version: 1,
			Columns: append(slices.Clone(teamSessions.Schemas), Col("note", Opt(Str()))),
			Key:     []string{"team", "session"},
			Indexes: IndexRecords{{Name: "of_session", Columns: []string{"session"}}},
			Exposes: teamSessions,
		}),
		Notes: qDefine(t, "notes", TableOptions{
			Version: 1,
			Columns: []ColumnProp{Col("id", Int()), Col("session", Opt(Str())), Col("n", Int())},
			Key:     []string{"id"},
		}),
	}
}

// rpMessage is the message err (an invalid/args refusal) carries; a nil err fails the test.
func rpMessage(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("no error, want an invalid/args refusal")
	}
	return rejected(t, err, data.CodeInvalid, data.ReasonArgs).Message
}

func rpJSON(t *testing.T, node WhereNode) string {
	t.Helper()
	text, err := jsstring.MarshalJSON(node.JSON())
	must(t, err)
	return string(text)
}

func TestReadPlanExists(t *testing.T) {
	f := rpFixtures(t)

	t.Run("matches by the key, a prefix of it, or a declared index; anything else is refused, naming the columns", func(t *testing.T) {
		both := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"team", s["app"]}, {"session", s["id"]}}, nil)
		})
		must(t, both.Err())
		prefix := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"team", s["app"]}}, nil)
		})
		must(t, prefix.Err())
		loose, err := DefineColumns("loose", ColumnSetOptions{Columns: []ColumnProp{Col("a", Str()), Col("b", Str())}, Key: []string{"a"}})
		must(t, err)
		refused := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(loose, Match{{"b", s["id"]}}, nil)
		})
		if got := rpMessage(t, refused.Err()); !strings.Contains(got, "loose is matched by (b), no leading prefix of its key (a)") {
			t.Errorf("refused match: %q", got)
		}
		counted, err := DefineColumns("counted", ColumnSetOptions{Columns: []ColumnProp{Col("n", Int())}, Key: []string{"n"}})
		must(t, err)
		retyped := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(counted, Match{{"n", s["id"]}}, nil)
		})
		if got := rpMessage(t, retyped.Err()); !strings.Contains(got, "differ in type") {
			t.Errorf("a pair of another type: %q", got)
		}
		// A match longer than every path it could lead is no prefix of any.
		longer := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(loose, Match{{"a", s["id"]}, {"b", s["app"]}}, nil)
		})
		if got := rpMessage(t, longer.Err()); !strings.Contains(got, "loose is matched by (a, b), no leading prefix of its key (a)") {
			t.Errorf("a match longer than the key: %q", got)
		}
	})

	t.Run("a pair of host columns is of one kind; a json column is no pair", func(t *testing.T) {
		kinds := map[string]*contracts.HostKind{}
		for _, name := range []string{"project", "session"} {
			kind, err := contracts.NewHostKind(name, contracts.Prop("id", contracts.Str()))
			must(t, err)
			kinds[name] = kind
		}
		owned, err := DefineColumns("owned", ColumnSetOptions{
			Columns: []ColumnProp{Col("id", Int()), Col("project", Host(kinds["project"]))}, Key: []string{"id"},
			Indexes: IndexRecords{{Name: "by_project", Columns: []string{"project"}}},
		})
		must(t, err)
		tableOf := func(kind string) *TableDefinition {
			return qDefine(t, "refs", TableOptions{
				Version: 1, Columns: []ColumnProp{Col("id", Int()), Col("ref", Host(kinds[kind]))}, Key: []string{"id"},
			})
		}
		same := NewQuery(tableOf("project")).Where(func(r, _ ColumnRefs) Condition {
			return Exists(owned, Match{{"project", r["ref"]}}, nil)
		})
		must(t, same.Err())
		other := NewQuery(tableOf("session")).Where(func(r, _ ColumnRefs) Condition {
			return Exists(owned, Match{{"project", r["ref"]}}, nil)
		})
		if got := rpMessage(t, other.Err()); !strings.Contains(got, "differ in type") {
			t.Errorf("host columns of two kinds: %q", got)
		}
		jsonColumn := Column{Name: "j", Type: ColJson, Json: &JsonColumn{Version: 1, Fingerprint: "f"}}
		if got := rpMessage(t, CheckPair("s", jsonColumn, jsonColumn, "w")); !strings.Contains(got, `s column "j" and the column it is matched to differ in type`) {
			t.Errorf("json pair: %q", got)
		}
	})

	t.Run("is refused in writes", func(t *testing.T) {
		store := memoryStore(t)
		changes := NewTableChanges(tableIn(t, store, f.Sessions), &[]StagedWrite{})
		where := func(s ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"session", s["id"]}}, nil)
		}
		if got := rpMessage(t, changes.UpdateWhere(where, SetOf{Assign("title", "x")})); !strings.Contains(got, "exists is for queries, not for writes") {
			t.Errorf("updateWhere: %q", got)
		}
		if got := rpMessage(t, changes.DeleteWhere(where)); !strings.Contains(got, "exists is for queries, not for writes") {
			t.Errorf("deleteWhere: %q", got)
		}
	})

	t.Run("a match names columns of the set and of the enclosing side", func(t *testing.T) {
		for name, match := range map[string]Match{
			"a column the set lacks":   {{"nope", f.Sessions.Refs["id"]}},
			"no column reference":      {{"session", nil}},
			"a column of no side":      {{"session", f.Teams.Refs["note"]}},
			"a ref of another set":     {{"session", f.TeamSessions.Refs["session"]}},
			"an expression, not a ref": {{"session", Add(1, 2)}},
		} {
			q := NewQuery(f.Sessions).Where(func(_, _ ColumnRefs) Condition { return Exists(f.TeamSessions, match, nil) })
			if got := rpMessage(t, q.Err()); !strings.Contains(got, "is matched to no column") {
				t.Errorf("%s: %q", name, got)
			}
		}
	})
}

func TestReadPlanExistsNode(t *testing.T) {
	f := rpFixtures(t)
	clubOnly, err := Compile(f.SessionColumns, Eq(f.SessionColumns.Refs["app"], "club"))
	must(t, err)

	t.Run("compile is the condition as data", func(t *testing.T) {
		if got := rpJSON(t, clubOnly); got != `{"eq":["app","club"]}` {
			t.Errorf("compile: %s", got)
		}
		// A column of the table, not of the set, is no column of the set's scope.
		_, err := Compile(f.SessionColumns, Eq(f.Sessions.Refs["app"], "club"))
		if got := rpMessage(t, err); !strings.Contains(got, `no column "app"`) {
			t.Errorf("a ref of no set: %q", got)
		}
	})

	t.Run("take a condition as data: an optional pointer keeps its row, a pointer to a hidden row drops it", func(t *testing.T) {
		feed := NewQuery(f.Notes).Where(func(n, _ ColumnRefs) Condition {
			return Or(IsNull(n["session"]), ExistsNodeOf(f.SessionColumns, Match{{"id", n["session"]}}, clubOnly))
		})
		must(t, feed.Err())
		want := `{"or":[{"isNull":"session"},{"exists":["sessions.columns",[["id","session"]],{"eq":["app","club"]}]}]}`
		if got := rpJSON(t, *feed.Condition); got != want {
			t.Errorf("feed condition = %s, want %s", got, want)
		}
		own := NewQuery(f.Sessions).WhereNode(f.SessionColumns, clubOnly)
		must(t, own.Err())
		qEqual(t, *own.Condition, clubOnly)
	})

	t.Run("apply to a joined set, with an exists matched on the partner", func(t *testing.T) {
		inU14, err := Compile(f.SessionColumns, Exists(f.TeamSessions, Match{{"session", f.SessionColumns.Refs["id"]}},
			func(t ColumnRefs) Condition { return Eq(t["team"], "u14") }))
		must(t, err)
		joined := NewQuery(f.Teams).
			Where(func(t, _ ColumnRefs) Condition { return Eq(t["team"], "u12") }).
			Join(f.SessionColumns, func(t ColumnRefs) Match { return Match{{"id", t["session"]}} }).
			WhereNode(f.SessionColumns, inU14)
		must(t, joined.Err())
		want := `{"and":[{"eq":["team","u12"]},{"exists":["club.team-sessions",[["session","sessions.columns:id"]],{"eq":["team","u14"]}]}]}`
		if got := rpJSON(t, *joined.Condition); got != want {
			t.Errorf("joined condition = %s, want %s", got, want)
		}
	})

	t.Run("rename every column of the partner's side, nested conditions included", func(t *testing.T) {
		node, err := Compile(f.SessionColumns, And(
			Not(Eq(f.SessionColumns.Refs["app"], "x")),
			Or(IsNull(f.SessionColumns.Refs["archivedAt"]), Gt(f.SessionColumns.Refs["lastTouch"], 5)),
			InArray(f.SessionColumns.Refs["id"], []string{"s1"}),
			Prefix(f.SessionColumns.Refs["id"], "s"),
		))
		must(t, err)
		joined := NewQuery(f.Teams).
			Join(f.SessionColumns, func(t ColumnRefs) Match { return Match{{"id", t["session"]}} }).
			WhereNode(f.SessionColumns, node)
		must(t, joined.Err())
		want := `{"and":[{"not":{"eq":["sessions.columns:app","x"]}},` +
			`{"or":[{"isNull":"sessions.columns:archivedAt"},{"gt":["sessions.columns:lastTouch",5]}]},` +
			`{"inArray":["sessions.columns:id",["s1"]]},{"prefix":["sessions.columns:id","s"]}]}`
		if got := rpJSON(t, *joined.Condition); got != want {
			t.Errorf("joined condition = %s, want %s", got, want)
		}
	})

	t.Run("a node of another set is refused, whole, before it is added", func(t *testing.T) {
		q := NewQuery(f.Sessions).WhereNode(f.TeamSessions, clubOnly)
		if got := rpMessage(t, q.Err()); !strings.Contains(got, `no column "app"`) {
			t.Errorf("whereNode(TeamSessions, clubOnly): %q", got)
		}
		// A node over a set the query has no side of.
		other, err := Compile(f.TeamSessions, Eq(f.TeamSessions.Refs["team"], "u12"))
		must(t, err)
		if got := rpMessage(t, NewQuery(f.Sessions).WhereNode(f.TeamSessions, other).Err()); !strings.Contains(got, "the query has no side of that set") {
			t.Errorf("whereNode of a set with no side: %q", got)
		}
	})

	t.Run("record the columns of the sets an exists was written for", func(t *testing.T) {
		q := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"session", s["id"]}}, func(t ColumnRefs) Condition {
				return And(Eq(t["team"], "u14"), Ne(t["session"], "s9"))
			})
		})
		must(t, q.Err())
		// A column used twice is recorded once, where it was first used.
		want := Expected{{Set: "club.team-sessions", Columns: []Column{f.TeamSessions.ByName["session"], f.TeamSessions.ByName["team"]}}}
		qEqual(t, q.Expected, want)
		// A where copies what is expected: the query it came from keeps its own.
		more := q.Where(func(s, _ ColumnRefs) Condition {
			return Exists(f.SessionColumns, Match{{"id", s["id"]}}, func(c ColumnRefs) Condition { return Eq(c["app"], "club") })
		})
		must(t, more.Err())
		qEqual(t, q.Expected, want)
		if len(more.Expected) != 2 || more.Expected[1].Set != "sessions.columns" {
			t.Errorf("the second where expects %v", more.Expected)
		}
		// A second where over a set already expected adds to a copy of its columns.
		narrow := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"session", s["id"]}}, nil)
		})
		wider := narrow.Where(func(s, _ ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"team", s["app"]}}, nil)
		})
		must(t, wider.Err())
		qEqual(t, narrow.Expected, Expected{{Set: "club.team-sessions", Columns: []Column{f.TeamSessions.ByName["session"]}}})
		qEqual(t, wider.Expected, want)
		if base := NewQuery(f.Sessions); len(base.Expected) != 0 {
			t.Errorf("the base query expects %v", base.Expected)
		}
	})
}

func TestReadPlanJoin(t *testing.T) {
	f := rpFixtures(t)
	page := NewQuery(f.Teams).
		Where(func(t, _ ColumnRefs) Condition { return Eq(t["team"], "u12") }).
		Join(f.SessionColumns, func(t ColumnRefs) Match { return Match{{"id", t["session"]}} }).
		Where(func(_, session ColumnRefs) Condition { return Eq(session["app"], "club") }).
		OrderBy(func(_, session ColumnRefs) *ColumnRef { return session["lastTouch"] }, "desc")

	t.Run("a joined query orders by a partner column, named <set>:<column>", func(t *testing.T) {
		must(t, page.Err())
		qEqual(t, page.Order(), Order{Columns: []string{"sessions.columns:lastTouch", "team", "session"}, Direction: "desc"})
		qEqual(t, page.Joined.Pairs, []MatchPair{{SetColumn: "id", Column: "session"}})
		if column, found := page.Column("sessions.columns:lastTouch"); !found || column.Type != ColInt {
			t.Errorf("Column of a partner name = %v, %v", column, found)
		}
		if _, found := page.Column("sessions.columns:nope"); found {
			t.Error("Column found a partner column the set lacks")
		}
		if column, found := page.Column("note"); !found || column.Name != "note" {
			t.Errorf("Column of an own name = %v, %v", column, found)
		}
		want := `{"and":[{"eq":["team","u12"]},{"eq":["sessions.columns:app","club"]}]}`
		if got := rpJSON(t, *page.Condition); got != want {
			t.Errorf("joined condition = %s, want %s", got, want)
		}
	})

	t.Run("refuses a match short of the whole key, a second join, the table's own set and a column of another type", func(t *testing.T) {
		short := NewQuery(f.Sessions).Join(f.TeamSessions, func(s ColumnRefs) Match { return Match{{"session", s["id"]}} })
		if got := rpMessage(t, short.Err()); !strings.Contains(got, "the match covers the whole key") {
			t.Errorf("short match: %q", got)
		}
		both := func(t ColumnRefs) Match { return Match{{"team", t["team"]}, {"session", t["session"]}} }
		if got := rpMessage(t, page.Join(f.TeamSessions, both).Err()); !strings.Contains(got, "joins one set") {
			t.Errorf("second join: %q", got)
		}
		if got := rpMessage(t, NewQuery(f.Teams).Join(f.TeamSessions, both).Err()); !strings.Contains(got, "the set is the table's own") {
			t.Errorf("own set: %q", got)
		}
		retyped := NewQuery(f.Notes).Join(f.SessionColumns, func(n ColumnRefs) Match { return Match{{"id", n["n"]}} })
		if got := rpMessage(t, retyped.Err()); !strings.Contains(got, "differ in type") {
			t.Errorf("another type: %q", got)
		}
	})

	t.Run("names the columns a match refers to", func(t *testing.T) {
		for name, match := range map[string]Match{
			"a column the set lacks": {{"nope", f.Teams.Refs["session"]}},
			"no reference":           {{"id", nil}},
			"another table's column": {{"id", f.Sessions.Refs["id"]}},
		} {
			q := NewQuery(f.Teams).Join(f.SessionColumns, func(ColumnRefs) Match { return match })
			if got := rpMessage(t, q.Err()); !strings.Contains(got, "is matched to no column") {
				t.Errorf("%s: %q", name, got)
			}
		}
	})

	t.Run("orders a joined query only by a non-opt str or int column of a side it has", func(t *testing.T) {
		for name, order := range map[string]func(row, partner ColumnRefs) *ColumnRef{
			"an opt partner column": func(_, p ColumnRefs) *ColumnRef { return p["archivedAt"] },
			"a bool partner column": func(_, p ColumnRefs) *ColumnRef { return p["private"] },
			"no column":             func(_, _ ColumnRefs) *ColumnRef { return nil },
			"a column of no side":   func(_, _ ColumnRefs) *ColumnRef { return f.Sessions.Refs["title"] },
		} {
			q := NewQuery(f.Teams).Join(f.SessionColumns, func(t ColumnRefs) Match { return Match{{"id", t["session"]}} }).OrderBy(order, "")
			if got := rpMessage(t, q.Err()); !strings.Contains(got, "orderBy: expected a non-opt str() or int() column") {
				t.Errorf("%s: %q", name, got)
			}
		}
		twice := page.OrderBy(func(_, p ColumnRefs) *ColumnRef { return p["lastTouch"] }, "desc")
		if got := rpMessage(t, twice.Err()); !strings.Contains(got, `"sessions.columns:lastTouch" twice`) {
			t.Errorf("a partner column twice: %q", got)
		}
	})

	t.Run("keeps the match in the order of the set's key, whatever order it was written in", func(t *testing.T) {
		pairs := qDefine(t, "pairs", TableOptions{
			Version: 1, Columns: []ColumnProp{Col("a", Str()), Col("b", Str())}, Key: []string{"a", "b"},
		})
		joined := NewQuery(pairs).Join(f.TeamSessions, func(p ColumnRefs) Match {
			return Match{{"session", p["b"]}, {"team", p["a"]}}
		})
		must(t, joined.Err())
		qEqual(t, joined.Joined.Pairs, []MatchPair{{SetColumn: "team", Column: "a"}, {SetColumn: "session", Column: "b"}})
	})
}

// rpBindings are the bindings of a test that has no store: Pi's plain `Map` of set name to bound table.
type rpBindings map[string]Bound

func (bindings rpBindings) Get(set string) (Bound, bool) {
	bound, found := bindings[set]
	return bound, found
}

func rpDefineColumns(t *testing.T, name string, options ColumnSetOptions) *ColumnSet {
	t.Helper()
	set, err := DefineColumns(name, options)
	must(t, err)
	return set
}

// pi: packages/chord/src/store/read-plan.ts
func TestReadPlanWrittenFor(t *testing.T) {
	own := qDefine(t, "own", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Int()), Col("partner", Str())}, Key: []string{"id"}})

	t.Run("refuses a joined set whose key the bound table has differently: the join would pair a row with several", func(t *testing.T) {
		// Sol 1, Astra S2.
		old := rpDefineColumns(t, "partners", ColumnSetOptions{Columns: []ColumnProp{Col("id", Str()), Col("tenant", Str())}, Key: []string{"id"}})
		next := rpDefineColumns(t, "partners", ColumnSetOptions{Columns: []ColumnProp{Col("id", Str()), Col("tenant", Str())}, Key: []string{"id", "tenant"}})
		partner := qDefine(t, "partner", TableOptions{Version: 1, Columns: next.Schemas, Key: []string{"id", "tenant"}, Exposes: next})
		q := NewQuery(own).Join(old, func(row ColumnRefs) Match { return Match{{"id", row["partner"]}} })
		must(t, q.Err())
		_, err := NewReadPlan(q, "own", Projection{}, ReadRows, rpBindings{old.Name: {Table: "partner", Definition: partner}})
		if got, want := rpMessage(t, err), `column set "partners" is joined by (id), its key is (id, tenant): not what this read was written for`; got != want {
			t.Fatalf("message = %q", got)
		}
	})

	t.Run("refuses a column an exists compares that the bound table has with another type; an unused one may change", func(t *testing.T) {
		// Sol 1: the reader compares `score` as an int; the publisher made it a num.
		old := rpDefineColumns(t, "scores", ColumnSetOptions{Columns: []ColumnProp{Col("id", Int()), Col("score", Int()), Col("note", Str())}, Key: []string{"id"}})
		retyped := rpDefineColumns(t, "scores", ColumnSetOptions{Columns: []ColumnProp{Col("id", Int()), Col("score", Num()), Col("note", Str())}, Key: []string{"id"}})
		unused := rpDefineColumns(t, "scores", ColumnSetOptions{Columns: []ColumnProp{Col("id", Int()), Col("score", Int()), Col("note", Int())}, Key: []string{"id"}})
		bound := func(set *ColumnSet) rpBindings {
			definition := qDefine(t, "other", TableOptions{Version: 1, Columns: set.Schemas, Key: []string{"id"}, Exposes: set})
			return rpBindings{set.Name: {Table: "other", Definition: definition}}
		}
		q := NewQuery(own).Where(func(row, _ ColumnRefs) Condition {
			return Exists(old, Match{{"id", row["id"]}}, func(c ColumnRefs) Condition { return Eq(c["score"], 1) })
		})
		must(t, q.Err())
		_, err := NewReadPlan(q, "own", Projection{}, ReadRows, bound(retyped))
		if got, want := rpMessage(t, err), `column "score" of column set "scores" is not what this read was written for`; got != want {
			t.Fatalf("message = %q", got)
		}
		if _, err := NewReadPlan(q, "own", Projection{}, ReadRows, bound(unused)); err != nil {
			t.Fatalf("an unused column that changed: %v", err)
		}
	})
}
