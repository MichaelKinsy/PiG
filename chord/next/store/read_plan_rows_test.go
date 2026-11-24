package store

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports the rest of packages/chord/test/store/read-plan.test.ts: the cases that run rows on SQLite, through the store's
// own bindings (`store.reads`) over `seeded()`: `exists`, `existsNode` and `whereNode`, the join, the uses a plan lists
// and the probe, the checks of a nested exists when a read opens, a table handle's own reads. The cases that need no
// store are in read_plan_test.go. Test names share the prefix `ReadPlan`.

// rpWorld is a store with the three tables, their sets bound, and some rows (Pi's `seeded()`).
type rpWorld struct {
	store  *ChordStore
	f      rpSets
	tables map[*TableDefinition]string
	owner  *testOwner
}

func rpSeeded(t *testing.T, f rpSets) *rpWorld {
	t.Helper()
	store := memoryStore(t)
	w := &rpWorld{store: store, f: f, tables: map[*TableDefinition]string{}, owner: tableOwner()}
	var staged []StagedWrite
	for _, definition := range []*TableDefinition{f.Sessions, f.Teams, f.Notes} {
		handle := tableIn(t, store, definition)
		w.tables[definition] = handle.Sql.Table
		if definition.Exposes != nil {
			must(t, store.Reads.Bind(definition.Exposes.Name, Bound{Table: handle.Sql.Table, Definition: definition}, w.owner))
		}
		changes := NewTableChanges(handle, &staged)
		switch definition {
		case f.Sessions:
			for index, app := range []string{"club", "club", "family", "club"} {
				id := "s" + string(rune('0'+index))
				must(t, changes.Put(Row{"id": id, "app": app, "private": index == 2, "lastTouch": int64(10 * index), "title": "t" + string(rune('0'+index))}))
			}
		case f.Teams:
			for _, pair := range [][2]string{{"u12", "s0"}, {"u12", "s1"}, {"u14", "s1"}, {"u12", "gone"}} {
				must(t, changes.Put(Row{"team": pair[0], "session": pair[1]}))
			}
		default:
			must(t, changes.Put(Row{"id": int64(1), "session": "s0", "n": int64(1)}))
			must(t, changes.Put(Row{"id": int64(2), "n": int64(2)}))
			must(t, changes.Put(Row{"id": int64(3), "session": "s2", "n": int64(3)}))
		}
	}
	fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return nil }}
	if _, err := store.Commit(staged, fence, background); err != nil {
		t.Fatal(err)
	}
	return w
}

// plan is the plan of q over the world, for rows.
func (w *rpWorld) plan(t *testing.T, q *Query) *ReadPlan {
	t.Helper()
	must(t, q.Err())
	plan, err := NewReadPlan(q, w.tables[q.Table], Projection{}, ReadRows, w.store.Reads)
	must(t, err)
	return plan
}

// rows are the rows of plan for rng.
func (w *rpWorld) rows(t *testing.T, plan *ReadPlan, rng Range) []StoredRead {
	t.Helper()
	statement := plan.Select(rng)
	rows, err := Read(w.store, func(executor sqlite.SqliteExecutor) ([]StoredRead, error) {
		return executor.All(statement.SQL, statement.Params...)
	}, background)
	must(t, err)
	return rows
}

// planned is Pi's `planned(q, range)`: the plan of q over a fresh seeded store, and its rows for rng (limit 10 by default).
func rpPlanned(t *testing.T, f rpSets, q *Query, rng ...Range) (*rpWorld, *ReadPlan, []StoredRead) {
	t.Helper()
	w := rpSeeded(t, f)
	plan := w.plan(t, q)
	rangeOf := Range{Limit: 10}
	if len(rng) > 0 {
		rangeOf = rng[0]
	}
	return w, plan, w.rows(t, plan, rangeOf)
}

// column is the value of column in each row.
func rpColumn(rows []StoredRead, column string) []any {
	values := make([]any, 0, len(rows))
	for _, row := range rows {
		values = append(values, row[column])
	}
	return values
}

// explained is the query plan SQLite chose for a statement, one line per step.
func (w *rpWorld) explained(t *testing.T, statement Statement) string {
	t.Helper()
	steps, err := Read(w.store, func(executor sqlite.SqliteExecutor) ([]StoredRead, error) {
		return executor.All("EXPLAIN QUERY PLAN "+statement.SQL, statement.Params...)
	}, background)
	must(t, err)
	details := make([]string, len(steps))
	for index, step := range steps {
		details[index] = step["detail"].(string)
	}
	return strings.Join(details, "; ")
}

// pi: packages/chord/src/store/read.ts
func TestReadPlanRowsExists(t *testing.T) {
	f := rpFixtures(t)

	t.Run("is true for a row with a matching row in the set's table, with and without an inner condition", func(t *testing.T) {
		_, _, all := rpPlanned(t, f, NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"session", s["id"]}}, nil)
		}))
		qEqual(t, rpColumn(all, "id"), []any{"s0", "s1"})
		_, _, u14 := rpPlanned(t, f, NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"session", s["id"]}}, func(team ColumnRefs) Condition { return Eq(team["team"], "u14") })
		}))
		qEqual(t, rpColumn(u14, "id"), []any{"s1"})
	})

	t.Run("negates and nests", func(t *testing.T) {
		_, _, none := rpPlanned(t, f, NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return And(Eq(s["app"], "club"), Not(Exists(f.TeamSessions, Match{{"session", s["id"]}}, nil)))
		}))
		qEqual(t, rpColumn(none, "id"), []any{"s3"})
		// Teams whose session is a club session that is itself in u14.
		_, _, nested := rpPlanned(t, f, NewQuery(f.Teams).Where(func(team, _ ColumnRefs) Condition {
			return Exists(f.SessionColumns, Match{{"id", team["session"]}}, func(s ColumnRefs) Condition {
				return And(
					Eq(s["app"], "club"),
					Exists(f.TeamSessions, Match{{"session", s["id"]}}, func(x ColumnRefs) Condition { return Eq(x["team"], "u14") }),
				)
			})
		}))
		pairs := make([][2]any, len(nested))
		for index, row := range nested {
			pairs[index] = [2]any{row["team"], row["session"]}
		}
		qEqual(t, pairs, [][2]any{{"u12", "s1"}, {"u14", "s1"}})
	})

	t.Run("matches nothing for a NULL in a matched column: the exists is false", func(t *testing.T) {
		_, _, noted := rpPlanned(t, f, NewQuery(f.Notes).Where(func(n, _ ColumnRefs) Condition {
			return Exists(f.SessionColumns, Match{{"id", n["session"]}}, nil)
		}))
		qEqual(t, rpColumn(noted, "id"), []any{int64(1), int64(3)})
	})
}

func TestReadPlanRowsExistsNode(t *testing.T) {
	f := rpFixtures(t)
	clubOnly, err := Compile(f.SessionColumns, Eq(f.SessionColumns.Refs["app"], "club"))
	must(t, err)

	t.Run("take a condition as data: an optional pointer keeps its row, a pointer to a hidden row drops it", func(t *testing.T) {
		_, _, feed := rpPlanned(t, f, NewQuery(f.Notes).Where(func(n, _ ColumnRefs) Condition {
			return Or(IsNull(n["session"]), ExistsNodeOf(f.SessionColumns, Match{{"id", n["session"]}}, clubOnly))
		}))
		qEqual(t, rpColumn(feed, "id"), []any{int64(1), int64(2)})
		_, _, own := rpPlanned(t, f, NewQuery(f.Sessions).WhereNode(f.SessionColumns, clubOnly))
		qEqual(t, rpColumn(own, "id"), []any{"s0", "s1", "s3"})
	})

	t.Run("apply to a joined set, with an exists matched on the partner", func(t *testing.T) {
		inU14, err := Compile(f.SessionColumns, Exists(f.TeamSessions, Match{{"session", f.SessionColumns.Refs["id"]}},
			func(team ColumnRefs) Condition { return Eq(team["team"], "u14") }))
		must(t, err)
		_, _, joined := rpPlanned(t, f, NewQuery(f.Teams).
			Where(func(team, _ ColumnRefs) Condition { return Eq(team["team"], "u12") }).
			Join(f.SessionColumns, func(team ColumnRefs) Match { return Match{{"id", team["session"]}} }).
			WhereNode(f.SessionColumns, inU14))
		qEqual(t, rpColumn(joined, "session"), []any{"s1"})
	})

	t.Run("check a nested exists against the table bound to its set when the read opens", func(t *testing.T) {
		w := rpSeeded(t, f)
		object := delta.JsonObjectOf
		plan := func(node any) error {
			q := NewQuery(f.Sessions).WhereNode(f.SessionColumns, node)
			if err := q.Err(); err != nil {
				return err
			}
			_, err := NewReadPlan(q, w.tables[f.Sessions], Projection{}, ReadRows, w.store.Reads)
			return err
		}
		for _, c := range []struct {
			node any
			want string
		}{
			{object("exists", []any{"club.team-sessions", []any{[]any{"team", "id"}}, object("eq", []any{"nope", "x"})}), `no column "nope"`},
			{object("exists", []any{"club.team-sessions", []any{[]any{"note", "id"}}, nil}), "no leading prefix"},
		} {
			if got := rpMessage(t, plan(c.node)); !strings.Contains(got, c.want) {
				t.Errorf("%v: %q, want %q", c.node, got, c.want)
			}
		}
		// A set nobody serves is `unavailable` (read-plan.ts `served`), not a malformed node.
		unserved := object("exists", []any{"nobody.serves", []any{[]any{"x", "id"}}, nil})
		if got := rejected(t, plan(unserved), data.CodeUnavailable, "").Message; !strings.Contains(got, `column set "nobody.serves" is not served`) {
			t.Errorf("unserved: %q", got)
		}
	})
}

func TestReadPlanRowsJoin(t *testing.T) {
	f := rpFixtures(t)
	page := NewQuery(f.Teams).
		Where(func(team, _ ColumnRefs) Condition { return Eq(team["team"], "u12") }).
		Join(f.SessionColumns, func(team ColumnRefs) Match { return Match{{"id", team["session"]}} }).
		Where(func(_, session ColumnRefs) Condition { return Eq(session["app"], "club") }).
		OrderBy(func(_, session ColumnRefs) *ColumnRef { return session["lastTouch"] }, "desc")

	t.Run("pairs each row with the one row of that key, drops a row without a partner, orders by a partner column", func(t *testing.T) {
		_, _, rows := rpPlanned(t, f, page)
		pairs := make([][2]any, len(rows))
		for index, row := range rows {
			pairs[index] = [2]any{row["session"], row["sessions.columns:lastTouch"]}
		}
		qEqual(t, pairs, [][2]any{{"s1", int64(10)}, {"s0", int64(0)}})
	})

	t.Run("continues after a position of a joined order, and ends through one", func(t *testing.T) {
		w := rpSeeded(t, f)
		plan := w.plan(t, page)
		after := w.rows(t, plan, Range{After: Position{int64(10), "u12", "s1"}, Limit: 10})
		qEqual(t, rpColumn(after, "session"), []any{"s0"})
		through := w.rows(t, plan, Range{Through: Position{int64(10), "u12", "s1"}, Limit: 10})
		qEqual(t, rpColumn(through, "session"), []any{"s1"})
		// A position of the leading order columns alone bounds every row equal in them.
		prefix := w.rows(t, plan, Range{Through: Position{int64(10)}, Limit: 10})
		qEqual(t, rpColumn(prefix, "session"), []any{"s1"})
	})

	t.Run("is driven from the query's own table, whatever the planner would prefer", func(t *testing.T) {
		w, plan, _ := rpPlanned(t, f, page)
		got := w.explained(t, plan.Select(Range{Limit: 51}))
		if !regexp.MustCompile(`^SEARCH t .*; SEARCH p USING PRIMARY KEY`).MatchString(got) {
			t.Fatalf("query plan = %q", got)
		}
	})

	t.Run("fails a read whose partner column is not what it was written for", func(t *testing.T) {
		w := rpSeeded(t, f)
		other, err := DefineColumns("sessions.columns", ColumnSetOptions{Columns: []ColumnProp{Col("id", Str()), Col("app", Int())}, Key: []string{"id"}})
		must(t, err)
		q := NewQuery(f.Teams).
			Join(other, func(team ColumnRefs) Match { return Match{{"id", team["session"]}} }).
			Where(func(_, session ColumnRefs) Condition { return Eq(session["app"], 1) })
		must(t, q.Err())
		_, err = NewReadPlan(q, w.tables[f.Teams], Projection{}, ReadRows, w.store.Reads)
		if got := rpMessage(t, err); !strings.Contains(got, `column "app" of column set "sessions.columns" is not what this read was written for`) {
			t.Fatalf("message = %q", got)
		}
	})
}

func TestReadPlanRowsUsesAndTheProbe(t *testing.T) {
	f := rpFixtures(t)
	sorted := func(set map[string]bool) []string {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		slices.Sort(names)
		return names
	}

	t.Run("lists each table a read uses with the columns it depends on", func(t *testing.T) {
		q := NewQuery(f.Teams).
			Where(func(team, _ ColumnRefs) Condition { return InArray(team["team"], []string{"u12"}) }).
			Join(f.SessionColumns, func(team ColumnRefs) Match { return Match{{"id", team["session"]}} }).
			Where(func(_, session ColumnRefs) Condition {
				return And(Eq(session["app"], "club"), IsNull(session["archivedAt"]))
			}).
			OrderBy(func(_, session ColumnRefs) *ColumnRef { return session["lastTouch"] }, "desc")
		must(t, q.Err())
		w := rpSeeded(t, f)
		plan, err := NewReadPlan(q, w.tables[f.Teams], Projection{Reads: []string{}, PartnerReads: []string{"id"}}, ReadRows, w.store.Reads)
		must(t, err)
		own, joined := plan.Uses[0], plan.Uses[1]
		qEqual(t, []any{own.Role, sorted(own.Depends)}, []any{UseOwn, []string{"session", "team"}})
		qEqual(t, []any{joined.Role, sorted(joined.Depends), sorted(joined.Enters)},
			[]any{UseJoined, []string{"app", "archivedAt", "id", "lastTouch"}, []string{"app", "archivedAt", "lastTouch"}})
		// The match column after the column the query fixes: the key (team, session).
		qEqual(t, []any{joined.Pairs, joined.Keyed}, []any{[]string{"session"}, true})
		counted, err := NewReadPlan(q, w.tables[f.Teams], Projection{}, ReadCount, w.store.Reads)
		must(t, err)
		qEqual(t, sorted(counted.Uses[1].Depends), []string{"app", "archivedAt", "id"})
	})

	t.Run("is one keyed lookup: by the own key, by a join's match, by an exists' match", func(t *testing.T) {
		w := rpSeeded(t, f)
		team := NewQuery(f.Teams).
			Where(func(team, _ ColumnRefs) Condition { return Eq(team["team"], "u12") }).
			Join(f.SessionColumns, func(team ColumnRefs) Match { return Match{{"id", team["session"]}} }).
			OrderBy(func(_, session ColumnRefs) *ColumnRef { return session["lastTouch"] }, "desc")
		teamPlan := w.plan(t, team)
		if got := w.explained(t, teamPlan.Probe(Range{Limit: 1}, []string{"session"}, []sqlite.SqliteValue{"s1"})); !regexp.MustCompile(`^SEARCH t USING PRIMARY KEY \(team=\? AND session=\?\)`).MatchString(got) {
			t.Fatalf("own-key probe plan = %q", got)
		}
		visible := NewQuery(f.Sessions).
			Where(func(s, _ ColumnRefs) Condition {
				return And(
					Eq(s["app"], "club"),
					Exists(f.TeamSessions, Match{{"session", s["id"]}}, func(team ColumnRefs) Condition { return Eq(team["team"], "u12") }),
				)
			}).
			OrderBy(func(s, _ ColumnRefs) *ColumnRef { return s["lastTouch"] }, "desc")
		visiblePlan := w.plan(t, visible)
		if got := w.explained(t, visiblePlan.Probe(Range{Limit: 1}, []string{"id"}, []sqlite.SqliteValue{"s1"})); !regexp.MustCompile(`^SEARCH t USING PRIMARY KEY \(id=\?\)`).MatchString(got) {
			t.Fatalf("exists probe plan = %q", got)
		}
		index := slices.IndexFunc(visiblePlan.Uses, func(use Use) bool { return use.Role == UseExists })
		use := visiblePlan.Uses[index]
		qEqual(t, []any{use.PairedBy, use.Pairs, use.Keyed, use.Stable}, []any{[]string{"session"}, []string{"id"}, true, true})
	})

	t.Run("has no keyed probe where the own table has no index for the match", func(t *testing.T) {
		w := rpSeeded(t, f)
		q := NewQuery(f.Notes).Join(f.SessionColumns, func(n ColumnRefs) Match { return Match{{"id", n["session"]}} })
		if plan := w.plan(t, q); plan.Uses[1].Keyed {
			t.Fatal("the join of notes by session is keyed: the table has no index on session")
		}
	})
}

func TestReadPlanRowsTableHandle(t *testing.T) {
	f := rpFixtures(t)
	t.Run("page and count take an exists, and refuse a joined query instead of ignoring the join", func(t *testing.T) {
		// Astra S3.
		w := rpSeeded(t, f)
		sessions := tableIn(t, w.store, f.Sessions)
		inTeam := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"session", s["id"]}}, nil)
		})
		page, err := sessions.Page(background, inTeam, 10, PageOptions{})
		must(t, err)
		ids := []any{}
		for _, row := range page.Items {
			ids = append(ids, row["id"])
		}
		qEqual(t, ids, []any{"s0", "s1"})
		count, err := sessions.Count(background, inTeam)
		must(t, err)
		qEqual(t, count, int64(2))
		teams := tableIn(t, w.store, f.Teams)
		joined := NewQuery(f.Teams).Join(f.SessionColumns, func(team ColumnRefs) Match { return Match{{"id", team["session"]}} })
		_, err = teams.Page(background, joined, 10, PageOptions{})
		if got := rpMessage(t, err); !strings.Contains(got, "a joined query is read as a named query") {
			t.Errorf("page: %q", got)
		}
		_, err = teams.Count(background, joined)
		if got := rpMessage(t, err); !strings.Contains(got, "a joined query is read as a named query") {
			t.Errorf("count: %q", got)
		}
	})
}
