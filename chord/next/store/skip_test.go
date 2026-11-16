package store

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Pi's skip.test.ts tests the skip rule through OpenQuery, ReadRefresh and TableChanges.changes (the oracle with its
// randomized writes and the deterministic cases of the rule's table); those cases come with open-query.ts, which they
// need. These are the contract of skip.ts alone, over a store with real rows and a read adopted before the writes: what
// skips without a probe, what a probe decides (and what it costs), what always runs, and the budget of probes.

type skipWorld struct {
	store                       *ChordStore
	f                           rpSets
	pins                        *ColumnSet
	sessions, teams, pinsHandle *TableHandle
	bindings                    rpBindings
}

func skipFixture(t *testing.T) *skipWorld {
	t.Helper()
	f := rpFixtures(t)
	pinColumns, err := DefineColumns("pins.columns", ColumnSetOptions{
		Columns: []ColumnProp{Col("id", Int()), Col("session", Str())},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "ofSession", Columns: []string{"session"}}},
	})
	must(t, err)
	pins := qDefine(t, "pins", TableOptions{
		Version: 1,
		Columns: append(slices.Clone(pinColumns.Schemas), Col("at", Int())),
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "of_session", Columns: []string{"session"}}},
		Exposes: pinColumns,
	})
	store := memoryStore(t)
	w := &skipWorld{store: store, f: f, pins: pinColumns}
	w.sessions, w.teams, w.pinsHandle = tableIn(t, store, f.Sessions), tableIn(t, store, f.Teams), tableIn(t, store, pins)
	w.bindings = rpBindings{
		f.SessionColumns.Name: {Table: w.sessions.Sql.Table, Definition: f.Sessions},
		f.TeamSessions.Name:   {Table: w.teams.Sql.Table, Definition: f.Teams},
		pinColumns.Name:       {Table: w.pinsHandle.Sql.Table, Definition: pins},
	}
	w.write(t, func(sessions, teams, pins *TableChanges) {
		for index, app := range []string{"club", "club", "family", "club"} {
			must(t, sessions.Put(Row{"id": "s" + string(rune('0'+index)), "app": app, "private": false, "lastTouch": int64(10 * index), "title": "t"}))
		}
		for _, pair := range [][2]string{{"u12", "s0"}, {"u12", "s1"}, {"u14", "s1"}} {
			must(t, teams.Put(Row{"team": pair[0], "session": pair[1]}))
		}
		must(t, pins.Put(Row{"id": int64(1), "session": "s2", "at": int64(1)}))
	})
	return w
}

// write commits what apply stages and returns the writes as the skip test takes them.
func (w *skipWorld) write(t *testing.T, apply func(sessions, teams, pins *TableChanges)) []Change {
	t.Helper()
	var staged []StagedWrite
	sessions, teams, pins := NewTableChanges(w.sessions, &staged), NewTableChanges(w.teams, &staged), NewTableChanges(w.pinsHandle, &staged)
	apply(sessions, teams, pins)
	fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return nil }}
	if _, err := w.store.Commit(staged, fence, background); err != nil {
		t.Fatal(err)
	}
	return slices.Concat(sessions.Changes(), teams.Changes(), pins.Changes())
}

// adopted is a read of query, run and adopted as the store does before a later commit's writes.
func (w *skipWorld) adopted(t *testing.T, query *Query, table string, what ReadKind, projection Projection) *RegisteredRead {
	t.Helper()
	must(t, query.Err())
	kind := ReadRows
	if what.Kind == "count" {
		kind = ReadCount
	}
	plan, err := NewReadPlan(query, table, projection, kind, w.bindings)
	must(t, err)
	read := NewRegisteredRead(plan, what, &adoptedRuns{})
	read.Adopt(fetched(t, w.store, read))
	return read
}

// skips runs the skip test of changes against read; the probes it asked are the second result.
func (w *skipWorld) skips(t *testing.T, read *RegisteredRead, changes []Change, before ...func(transaction sqlite.SqliteExecutor)) (bool, float64) {
	t.Helper()
	meter := metered(t)
	skipped, err := Read(w.store, func(executor sqlite.SqliteExecutor) (bool, error) {
		for _, each := range before {
			each(executor)
		}
		return Skips(read, changes, NewStep(executor, 0, nil, nil), meter.Context)
	}, background)
	must(t, err)
	return skipped, meter.Counts()["chord.query.probes"]
}

func club(id string, lastTouch int64) Row {
	return Row{"id": id, "app": "club", "private": false, "lastTouch": lastTouch, "title": id}
}

func TestSkipsOwnTable(t *testing.T) {
	list := func(w *skipWorld, reads ...string) *RegisteredRead {
		if reads == nil {
			reads = []string{"id"}
		}
		query := NewQuery(w.f.Sessions).
			Where(func(s, _ ColumnRefs) Condition { return Eq(s["app"], "club") }).
			OrderBy(func(s, _ ColumnRefs) *ColumnRef { return s["lastTouch"] }, "desc")
		// s3 and s1 are the page; s0 follows, past its limit.
		return w.adopted(t, query, w.sessions.Sql.Table, ReadKind{Kind: "rows", Range: Range{Limit: 2}}, Projection{Reads: reads})
	}
	for _, c := range []struct {
		name  string
		reads []string
		write func(sessions, teams, pins *TableChanges)
		skips bool
		probe float64
	}{
		{"an update of a column the read does not depend on skips without a probe", nil, func(s, _, _ *TableChanges) {
			must(t, s.Update(Row{"id": "s2"}, SetOf{Assign("title", "x")}))
		}, true, 0},
		{"the same of a row the read fetched", nil, func(s, _, _ *TableChanges) {
			must(t, s.Update(Row{"id": "s3"}, SetOf{Assign("title", "x")}))
		}, true, 0},
		{"an update of an order column of a fetched row runs", nil, func(s, _, _ *TableChanges) {
			must(t, s.Update(Row{"id": "s3"}, SetOf{Assign("lastTouch", int64(99))}))
		}, false, 0},
		{"an update of an order column of the row past the limit runs: it is fetched too", nil, func(s, _, _ *TableChanges) {
			must(t, s.Update(Row{"id": "s0"}, SetOf{Assign("lastTouch", int64(99))}))
		}, false, 0},
		{"an order column touched on a row that is out of the read: the probe says it did not enter", nil, func(s, _, _ *TableChanges) {
			must(t, s.Update(Row{"id": "s2"}, SetOf{Assign("lastTouch", int64(99))}))
		}, true, 1},
		{"a condition column touched on a row that enters: the probe finds it", nil, func(s, _, _ *TableChanges) {
			must(t, s.Update(Row{"id": "s2"}, SetOf{Assign("app", "club"), Assign("lastTouch", int64(99))}))
		}, false, 1},
		{"a put of a row inside the range: the probe finds it", nil, func(s, _, _ *TableChanges) { must(t, s.Put(club("s4", 99))) }, false, 1},
		{"a put of a row behind the row past the limit: a probe miss", nil, func(s, _, _ *TableChanges) { must(t, s.Put(club("s4", -5))) }, true, 1},
		{"a put of a fetched key runs (it may replace the row)", nil, func(s, _, _ *TableChanges) { must(t, s.Put(club("s1", -5))) }, false, 0},
		{"a delete of a row that was not fetched needs no probe", nil, func(s, _, _ *TableChanges) { must(t, s.Delete(Row{"id": "s2"})) }, true, 0},
		{"a delete of a fetched row runs", nil, func(s, _, _ *TableChanges) { must(t, s.Delete(Row{"id": "s1"})) }, false, 0},
		{"a bulk update of columns the read does not depend on skips", nil, func(s, _, _ *TableChanges) {
			must(t, s.UpdateWhere(func(e ColumnRefs) Condition { return Eq(e["app"], "club") }, SetOf{Assign("title", "bulk")}))
		}, true, 0},
		{"a bulk update of a column it depends on runs", nil, func(s, _, _ *TableChanges) {
			must(t, s.UpdateWhere(func(e ColumnRefs) Condition { return Eq(e["app"], "club") }, SetOf{Assign("lastTouch", int64(1))}))
		}, false, 0},
		{"a bulk delete runs", nil, func(s, _, _ *TableChanges) {
			must(t, s.DeleteWhere(func(e ColumnRefs) Condition { return Eq(e["id"], "s2") }))
		}, false, 0},
		{"an update of a mapped column of a row out of the read: it cannot enter, no probe", []string{"id", "title"}, func(s, _, _ *TableChanges) {
			must(t, s.Update(Row{"id": "s2"}, SetOf{Assign("title", "x")}))
		}, true, 0},
		{"a write to another table is of no use of the read", nil, func(_, teams, _ *TableChanges) {
			must(t, teams.Put(Row{"team": "u1", "session": "s3"}))
		}, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := skipFixture(t)
			read := list(w, c.reads...)
			changes := w.write(t, c.write)
			skipped, probes := w.skips(t, read, changes)
			if skipped != c.skips || probes != c.probe {
				t.Fatalf("skipped %v after %v probes, want %v after %v", skipped, probes, c.skips, c.probe)
			}
		})
	}

	t.Run("a transaction writing many rows asks a few probes, then runs the read", func(t *testing.T) {
		w := skipFixture(t)
		read := list(w)
		changes := w.write(t, func(s, _, _ *TableChanges) {
			for index := range 9 {
				must(t, s.Put(club("b"+string(rune('0'+index)), -1)))
			}
		})
		skipped, probes := w.skips(t, read, changes)
		if skipped || probes != 8 {
			t.Fatalf("skipped %v after %v probes, want a run after 8", skipped, probes)
		}
		// Eight rows is within the budget: all eight miss.
		skipped, probes = w.skips(t, read, changes[:8])
		if !skipped || probes != 8 {
			t.Fatalf("8 changes: skipped %v after %v probes, want a skip after 8", skipped, probes)
		}
	})

	t.Run("a put or a delete is judged by its row, whatever columns it lists", func(t *testing.T) {
		w := skipFixture(t)
		read := list(w)
		w.write(t, func(s, _, _ *TableChanges) { must(t, s.Put(club("s4", 99))) })
		table := w.sessions.Sql.Table
		put := Change{Table: table, Kind: ChangePut, Key: map[string]sqlite.SqliteValue{"id": "s4"}, Assigned: []string{"title"}}
		if skipped, probes := w.skips(t, read, []Change{put}); skipped || probes != 1 {
			t.Errorf("put: skipped %v after %v probes, want a run after 1", skipped, probes)
		}
		deleted := Change{Table: table, Kind: ChangeDelete, Key: map[string]sqlite.SqliteValue{"id": "s1"}, Assigned: []string{"title"}}
		if skipped, probes := w.skips(t, read, []Change{deleted}); skipped || probes != 0 {
			t.Errorf("delete: skipped %v after %v probes, want a run after 0", skipped, probes)
		}
	})

	t.Run("a read without a result is never skipped: loading, failed, forgotten", func(t *testing.T) {
		w := skipFixture(t)
		changes := w.write(t, func(s, _, _ *TableChanges) { must(t, s.Update(Row{"id": "s2"}, SetOf{Assign("title", "x")})) })
		read := list(w)
		read.Forget()
		if skipped, _ := w.skips(t, read, changes); skipped {
			t.Fatal("a forgotten read was skipped")
		}
		read.Adopt(ReadRun{Kind: "failed", Err: errFailedRun})
		if skipped, _ := w.skips(t, read, changes); skipped {
			t.Fatal("a failed read was skipped")
		}
		// A step's own failed run, kept for the later steps of its group, is no result either.
		read = list(w)
		failed := func(transaction sqlite.SqliteExecutor) {
			read.Ran(transaction, ReadRun{Kind: "failed", Err: errFailedRun})
		}
		if skipped, _ := w.skips(t, read, changes, failed); skipped {
			t.Fatal("a read whose step run failed was skipped")
		}
	})

	t.Run("a count keeps no rows: a put runs it, an update of a column it does not test skips it", func(t *testing.T) {
		w := skipFixture(t)
		query := NewQuery(w.f.Sessions).Where(func(s, _ ColumnRefs) Condition { return Eq(s["app"], "club") })
		count := w.adopted(t, query, w.sessions.Sql.Table, ReadKind{Kind: "count"}, Projection{})
		for _, c := range []struct {
			name  string
			write func(s *TableChanges)
			skips bool
		}{
			{"put", func(s *TableChanges) { must(t, s.Put(club("s9", 1))) }, false},
			{"delete", func(s *TableChanges) { must(t, s.Delete(Row{"id": "s2"})) }, false},
			{"an order column", func(s *TableChanges) { must(t, s.Update(Row{"id": "s2"}, SetOf{Assign("lastTouch", int64(1))})) }, true},
			{"the condition column", func(s *TableChanges) { must(t, s.Update(Row{"id": "s2"}, SetOf{Assign("app", "club")})) }, false},
		} {
			changes := w.write(t, func(s, _, _ *TableChanges) { c.write(s) })
			if skipped, probes := w.skips(t, count, changes); skipped != c.skips || probes != 0 {
				t.Errorf("%s: skipped %v after %v probes, want %v after 0", c.name, skipped, probes, c.skips)
			}
		}
	})
}

var errFailedRun = &runFailure{}

type runFailure struct{}

func (*runFailure) Error() string { return "the run failed" }

func TestSkipsJoinedAndExistsTables(t *testing.T) {
	joined := func(w *skipWorld) *RegisteredRead {
		query := NewQuery(w.f.Teams).
			Where(func(team, _ ColumnRefs) Condition { return Eq(team["team"], "u12") }).
			Join(w.f.SessionColumns, func(team ColumnRefs) Match { return Match{{"id", team["session"]}} }).
			OrderBy(func(_, session ColumnRefs) *ColumnRef { return session["lastTouch"] }, "desc")
		// s1 is the page; s0 follows, past its limit.
		return w.adopted(t, query, w.teams.Sql.Table, ReadKind{Kind: "rows", Range: Range{Limit: 1}}, Projection{})
	}
	existsTeam := func(w *skipWorld) *RegisteredRead {
		query := NewQuery(w.f.Sessions).
			Where(func(s, _ ColumnRefs) Condition { return Exists(w.f.TeamSessions, Match{{"session", s["id"]}}, nil) }).
			OrderBy(func(s, _ ColumnRefs) *ColumnRef { return s["lastTouch"] }, "desc")
		// s1 is the page; s0 follows, past its limit: s2 and s3 have no team.
		return w.adopted(t, query, w.sessions.Sql.Table, ReadKind{Kind: "rows", Range: Range{Limit: 1}}, Projection{Reads: []string{"id"}})
	}
	// The matched own column (title) has no index: the probe is not asked, the read runs.
	existsUnkeyed := func(w *skipWorld) *RegisteredRead {
		query := NewQuery(w.f.Sessions).Where(func(s, _ ColumnRefs) Condition { return Exists(w.f.TeamSessions, Match{{"session", s["title"]}}, nil) })
		return w.adopted(t, query, w.sessions.Sql.Table, ReadKind{Kind: "rows", Range: Range{Limit: 5}}, Projection{Reads: []string{"id"}})
	}
	existsPin := func(w *skipWorld) *RegisteredRead {
		query := NewQuery(w.f.Sessions).Where(func(s, _ ColumnRefs) Condition { return Exists(w.pins, Match{{"session", s["id"]}}, nil) })
		return w.adopted(t, query, w.sessions.Sql.Table, ReadKind{Kind: "rows", Range: Range{Limit: 5}}, Projection{Reads: []string{"id"}})
	}
	for _, c := range []struct {
		name  string
		read  func(w *skipWorld) *RegisteredRead
		write func(sessions, teams, pins *TableChanges)
		skips bool
		probe float64
	}{
		{"joined: a partner row no fetched row pairs with, its order column touched: one probe, a miss", joined, func(s, _, _ *TableChanges) {
			must(t, s.Update(Row{"id": "s3"}, SetOf{Assign("lastTouch", int64(99))}))
		}, true, 1},
		{"joined: a partner row of a fetched row runs", joined, func(s, _, _ *TableChanges) {
			must(t, s.Update(Row{"id": "s1"}, SetOf{Assign("lastTouch", int64(99))}))
		}, false, 0},
		{"joined: the delete of a partner that pairs with no fetched row cannot enter", joined, func(s, _, _ *TableChanges) {
			must(t, s.Delete(Row{"id": "s3"}))
		}, true, 0},
		{"exists: a team row of a session the read did not fetch: the probe finds it in the range", existsTeam, func(_, teams, _ *TableChanges) {
			must(t, teams.Put(Row{"team": "u99", "session": "s3"}))
		}, false, 1},
		{"exists: a team row of a session out of the read's range: a miss", existsTeam, func(_, teams, _ *TableChanges) {
			must(t, teams.Put(Row{"team": "u99", "session": "s2"}))
			must(t, teams.Delete(Row{"team": "u99", "session": "s2"}))
		}, true, 2},
		{"exists: a team row of a fetched session runs", existsTeam, func(_, teams, _ *TableChanges) {
			must(t, teams.Put(Row{"team": "u99", "session": "s0"}))
		}, false, 0},
		{"exists: a match on an own column no index serves cannot be probed: the read runs", existsUnkeyed, func(_, teams, _ *TableChanges) {
			must(t, teams.Put(Row{"team": "u99", "session": "zz"}))
		}, false, 0},
		{"exists: an update of a column outside the match and the inner condition skips", existsPin, func(_, _, pins *TableChanges) {
			must(t, pins.Update(Row{"id": int64(1)}, SetOf{Assign("at", int64(3))}))
		}, true, 0},
		{"exists: a pin re-pointed (its matched column is no key column) runs", existsPin, func(_, _, pins *TableChanges) {
			must(t, pins.Update(Row{"id": int64(1)}, SetOf{Assign("session", "s0")}))
		}, false, 0},
		{"exists: a pin put runs too: what it paired with before is unknown", existsPin, func(_, _, pins *TableChanges) {
			must(t, pins.Put(Row{"id": int64(1), "session": "s2", "at": int64(2)}))
		}, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := skipFixture(t)
			read := c.read(w)
			changes := w.write(t, c.write)
			skipped, probes := w.skips(t, read, changes)
			if skipped != c.skips || probes != c.probe {
				t.Fatalf("skipped %v after %v probes, want %v after %v", skipped, probes, c.skips, c.probe)
			}
		})
	}
}
