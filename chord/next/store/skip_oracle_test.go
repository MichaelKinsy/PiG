package store

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/telemetry"

	"context"
	"sync"
)

// Ports packages/chord/test/store/skip.test.ts: the skip test's oracle (slice 14 §3.3, test 13a): in randomized runs of
// writes over own, joined and `exists` tables, with reads of every shape the slice's screens use, every read's adopted
// result equals a fresh run of its statement after every commit, whether the commit ran it again or skipped it. And the
// deterministic cases of the rule's table: what skips without a probe, what a probe decides, what always runs.
//
// Pi asserts the counters `chord.query.skips`, `chord.query.probes` and `chord.query.runs` here: at least 200 skips, 100
// probes and 100 runs in each of its 8 seeded runs, and the exact runs, probes and skips of each deterministic case.
// (`chord.query.rows` is asserted by no Pi test.) Test names share the prefix `Skip`.
//
// Go forms: the generator is mulberry32 with Pi's constants, and the writes are made in Pi's order of random draws, so a
// seed is the same run; a group of commits is enlisted in order (`store.enlist`: its place in the queue is taken at the
// call, as Pi's `commit` does) and awaited together; an `undefined` field of a Pi `update` is NULL; `only` nil is every
// read, an empty slice none.

type oracleDefs struct {
	SessionColumns, TeamSessions, ShareColumns, PinColumns *ColumnSet
	Sessions, Teams, Shares, Pins, Notes                   *TableDefinition
}

func newOracleDefs(t *testing.T) *oracleDefs {
	t.Helper()
	d := &oracleDefs{}
	var err error
	d.SessionColumns, err = DefineColumns("sessions.columns", ColumnSetOptions{
		Columns: []ColumnProp{
			Col("id", Str()), Col("app", Str()), Col("creator", Str()), Col("private", Bool()), Col("state", Str()),
			Col("lastTouch", Int()), Col("archivedAt", Opt(Int())),
		},
		Key: []string{"id"},
	})
	must(t, err)
	d.Sessions = qDefine(t, "sessions", TableOptions{
		Version: 1,
		Columns: append(slices.Clone(d.SessionColumns.Schemas), Col("title", Str())),
		Key:     []string{"id"},
		Indexes: IndexRecords{
			{Name: "by_app", Columns: []string{"app", "lastTouch"}},
			{Name: "by_state", Columns: []string{"state", "lastTouch"}},
			{Name: "by_creator", Columns: []string{"app", "creator", "lastTouch"}},
		},
		Exposes: d.SessionColumns,
	})
	pair := func(name, first, second string, key []string) *ColumnSet {
		set, err := DefineColumns(name, ColumnSetOptions{
			Columns: []ColumnProp{Col(first, Str()), Col(second, Str())},
			Key:     key,
			Indexes: IndexRecords{{Name: "ofSession", Columns: []string{"session"}}},
		})
		must(t, err)
		return set
	}
	d.TeamSessions = pair("club.team-sessions", "team", "session", []string{"team", "session"})
	d.Teams = qDefine(t, "teams", TableOptions{
		Version: 1, Columns: slices.Clone(d.TeamSessions.Schemas), Key: []string{"team", "session"},
		Indexes: IndexRecords{{Name: "of_session", Columns: []string{"session"}}}, Exposes: d.TeamSessions,
	})
	d.ShareColumns = pair("people.shares", "person", "session", []string{"person", "session"})
	d.Shares = qDefine(t, "shares", TableOptions{
		Version: 1, Columns: slices.Clone(d.ShareColumns.Schemas), Key: []string{"person", "session"},
		Indexes: IndexRecords{{Name: "of_session", Columns: []string{"session"}}}, Exposes: d.ShareColumns,
	})
	// A pin's session is no key column: an `update` or a replacing `put` re-points it.
	d.PinColumns, err = DefineColumns("pins.columns", ColumnSetOptions{
		Columns: []ColumnProp{Col("id", Int()), Col("person", Str()), Col("session", Str())},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "ofSession", Columns: []string{"session"}}},
	})
	must(t, err)
	d.Pins = qDefine(t, "pins", TableOptions{
		Version: 1, Columns: append(slices.Clone(d.PinColumns.Schemas), Col("at", Int())), Key: []string{"id"},
		Indexes: IndexRecords{{Name: "of_session", Columns: []string{"session"}}}, Exposes: d.PinColumns,
	})
	d.Notes = qDefine(t, "notes", TableOptions{
		Version: 1,
		Columns: []ColumnProp{Col("id", Int()), Col("person", Str()), Col("session", Opt(Str())), Col("at", Int())},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "of_person", Columns: []string{"person", "at"}}},
	})
	return d
}

var (
	oracleSessions = func() []string {
		names := make([]string, 24)
		for index := range names {
			names[index] = fmt.Sprintf("s%02d", index)
		}
		return names
	}()
	oraclePersons = []string{"anna", "ben", "cleo"}
	oracleTeams   = []string{"u10", "u12", "u14"}
	oracleApps    = []string{"club", "family"}
	oracleStates  = []string{"working", "waiting", "idle"}
)

// conditionOf is what a person may see: the family's public sessions and their own, the club's by team, and what is shared.
func (d *oracleDefs) conditionOf(t *testing.T, person string, teams []string) WhereNode {
	t.Helper()
	s := d.SessionColumns.Refs
	node, err := Compile(d.SessionColumns, Or(
		And(Eq(s["app"], "family"), Or(Eq(s["private"], false), Eq(s["creator"], person))),
		And(Eq(s["app"], "club"), Exists(d.TeamSessions, Match{{"session", s["id"]}}, func(team ColumnRefs) Condition { return InArray(team["team"], teams) })),
		And(InArray(s["app"], oracleApps), Exists(d.ShareColumns, Match{{"session", s["id"]}}, func(share ColumnRefs) Condition { return Eq(share["person"], person) })),
	))
	must(t, err)
	return node
}

// oracleShape is one of the reads of the slice's screens (§8), by name: a query and how much of it is read.
type oracleShape struct {
	name  string
	query *Query
	table *TableDefinition
	what  ReadKind
}

func (d *oracleDefs) shapes(t *testing.T) []oracleShape {
	t.Helper()
	anna := d.conditionOf(t, "anna", []string{"u10", "u12"})
	ben := d.conditionOf(t, "ben", []string{"u14"})
	first := func(limit int) ReadKind { return ReadKind{Kind: "rows", Range: Range{Limit: limit}} }
	desc := func(column string) func(row, partner ColumnRefs) *ColumnRef {
		return func(row, _ ColumnRefs) *ColumnRef { return row[column] }
	}
	partnerDesc := func(column string) func(row, partner ColumnRefs) *ColumnRef {
		return func(_, session ColumnRefs) *ColumnRef { return session[column] }
	}
	visible := func(app string, condition WhereNode) *Query {
		return NewQuery(d.Sessions).
			Where(func(s, _ ColumnRefs) Condition { return Eq(s["app"], app) }).
			WhereNode(d.SessionColumns, condition).
			OrderBy(desc("lastTouch"), "desc").
			OrderBy(desc("id"), "desc")
	}
	ofTeams := func(teams []string, condition WhereNode) *Query {
		return NewQuery(d.Teams).
			Where(func(team, _ ColumnRefs) Condition { return InArray(team["team"], teams) }).
			Join(d.SessionColumns, func(team ColumnRefs) Match { return Match{{"id", team["session"]}} }).
			Where(func(_, session ColumnRefs) Condition {
				return And(Eq(session["app"], "club"), IsNull(session["archivedAt"]))
			}).
			WhereNode(d.SessionColumns, condition).
			OrderBy(partnerDesc("lastTouch"), "desc").
			OrderBy(partnerDesc("id"), "desc")
	}
	all := NewQuery(d.Sessions).OrderBy(desc("lastTouch"), "desc")
	return []oracleShape{
		{"visible club, anna", visible("club", anna), d.Sessions, first(4)},
		{"visible family, ben", visible("family", ben), d.Sessions, first(3)},
		{"one", NewQuery(d.Sessions).
			Where(func(s, _ ColumnRefs) Condition { return Eq(s["id"], "s03") }).
			WhereNode(d.SessionColumns, anna), d.Sessions, first(1)},
		{"team page u12", ofTeams([]string{"u12"}, anna), d.Teams, first(3)},
		{"club list, two teams", ofTeams([]string{"u10", "u12"}, anna), d.Teams, first(5)},
		{"team count", ofTeams([]string{"u14"}, ben), d.Teams, ReadKind{Kind: "count"}},
		{"shared with ben", NewQuery(d.Shares).
			Where(func(x, _ ColumnRefs) Condition { return Eq(x["person"], "ben") }).
			Join(d.SessionColumns, func(x ColumnRefs) Match { return Match{{"id", x["session"]}} }).
			WhereNode(d.SessionColumns, ben).
			OrderBy(partnerDesc("lastTouch"), "desc"), d.Shares, first(3)},
		{"created by anna", NewQuery(d.Sessions).
			Where(func(s, _ ColumnRefs) Condition { return And(Eq(s["app"], "club"), Eq(s["creator"], "anna")) }).
			OrderBy(desc("lastTouch"), "desc"), d.Sessions, first(3)},
		{"active", NewQuery(d.Sessions).
			Where(func(s, _ ColumnRefs) Condition { return InArray(s["state"], []string{"working", "waiting"}) }).
			WhereNode(d.SessionColumns, anna).
			OrderBy(desc("state"), "desc").
			OrderBy(desc("lastTouch"), "desc"), d.Sessions, first(4)},
		{"pinned by anyone", NewQuery(d.Sessions).
			Where(func(s, _ ColumnRefs) Condition { return Exists(d.PinColumns, Match{{"session", s["id"]}}, nil) }).
			OrderBy(desc("lastTouch"), "desc"), d.Sessions, first(4)},
		{"pinned by nobody", NewQuery(d.Sessions).Where(func(s, _ ColumnRefs) Condition {
			return And(Eq(s["app"], "family"), Not(Exists(d.PinColumns, Match{{"session", s["id"]}}, nil)))
		}), d.Sessions, first(4)},
		{"shared with nobody", NewQuery(d.Sessions).
			Where(func(s, _ ColumnRefs) Condition {
				return And(Eq(s["app"], "family"), Not(Exists(d.ShareColumns, Match{{"session", s["id"]}}, nil)))
			}).
			OrderBy(desc("lastTouch"), "desc"), d.Sessions, first(3)},
		{"anna's pins", NewQuery(d.Pins).
			Where(func(p, _ ColumnRefs) Condition { return Eq(p["person"], "anna") }).
			Join(d.SessionColumns, func(p ColumnRefs) Match { return Match{{"id", p["session"]}} }).
			WhereNode(d.SessionColumns, anna).
			OrderBy(desc("at"), "desc"), d.Pins, first(3)},
		{"anna's notes", NewQuery(d.Notes).
			Where(func(n, _ ColumnRefs) Condition {
				return And(Eq(n["person"], "anna"), Or(IsNull(n["session"]), ExistsNodeOf(d.SessionColumns, Match{{"id", n["session"]}}, anna)))
			}).
			OrderBy(desc("at"), "desc"), d.Notes, first(4)},
		{"a page", all, d.Sessions, ReadKind{Kind: "rows", Range: Range{After: Position{int64(5000), "s20"}, Through: Position{int64(2000), "s02"}, Limit: 6}}},
		{"an open page", all, d.Sessions, ReadKind{Kind: "rows", Range: Range{After: Position{int64(3000), "s10"}, Limit: 5}}},
		{"all sessions count", NewQuery(d.Sessions).Where(func(s, _ ColumnRefs) Condition { return Eq(s["app"], "club") }), d.Sessions, ReadKind{Kind: "count"}},
	}
}

// oracleRandom is a small deterministic generator (mulberry32): a failing seed reproduces.
type oracleRandom struct{ state uint32 }

func (r *oracleRandom) next() float64 {
	r.state += 0x6d2b79f5
	t := r.state
	t = (t ^ t>>15) * (t | 1)
	t ^= t + (t^t>>7)*(t|61)
	return float64(t^t>>14) / 4294967296
}

func (r *oracleRandom) int(below int) int { return int(r.next() * float64(below)) }

func (r *oracleRandom) chance(p float64) bool { return r.next() < p }

func pick[T any](r *oracleRandom, items []T) T { return items[int(r.next()*float64(len(items)))] }

// oracleRead is an open read of the world and the run it adopted last.
type oracleRead struct {
	name  string
	query *OpenQuery
	open  *OpenRead
	mu    sync.Mutex
	run   *ReadRun
}

func (read *oracleRead) Adopt(run ReadRun) {
	read.mu.Lock()
	read.run = &run
	read.mu.Unlock()
}

func (read *oracleRead) latest() *ReadRun {
	read.mu.Lock()
	defer read.mu.Unlock()
	return read.run
}

// oracleWorld is the tables of one store, their sets bound, and the open reads with the run each adopted last.
type oracleWorld struct {
	d       *oracleDefs
	store   *ChordStore
	handles map[string]*TableHandle
	reads   []*oracleRead
	batch   *state.Batch
}

func (d *oracleDefs) opened(t *testing.T, ctx context.Context, only []string) *oracleWorld {
	t.Helper()
	store := memoryStore(t)
	w := &oracleWorld{d: d, store: store, handles: map[string]*TableHandle{}, batch: state.NewBatch(func(state.BatchError) {}, telemetry.NoopTelemetryMetrics)}
	owner := tableOwner()
	for _, definition := range []*TableDefinition{d.Sessions, d.Teams, d.Shares, d.Pins, d.Notes} {
		handle := tableIn(t, store, definition)
		w.handles[definition.Name] = handle
		if definition.Exposes != nil {
			must(t, store.Reads.Bind(definition.Exposes.Name, Bound{Table: handle.Sql.Table, Definition: definition}, owner))
		}
	}
	t.Cleanup(func() {
		for _, read := range w.reads {
			read.query.Close()
		}
	})
	for _, shape := range d.shapes(t) {
		if only != nil && !slices.Contains(only, shape.name) {
			continue
		}
		must(t, shape.query.Err())
		spec := QuerySpec{Query: shape.query, Table: w.handles[shape.table.Name].Sql.Table, Projection: Projection{}, Owner: owner}
		shapeKind := ReadRows
		if shape.what.Kind == "count" {
			shapeKind = ReadCount
		}
		read := &oracleRead{name: shape.name}
		read.query = NewOpenQuery(ReadSite{Store: store, Batch: w.batch}, spec, shapeKind, func() {}, ctx)
		read.open = read.query.Read(shape.what, read)
		w.reads = append(w.reads, read)
		read.open.Load()
	}
	return w
}

// commit stages write's table writes as one commit with the refresh a transaction stages; the commit is enlisted at the
// call, and its outcome arrives on the channel once adopted.
func (w *oracleWorld) commit(t *testing.T, ctx context.Context, write func(changes func(table *TableDefinition) *TableChanges)) <-chan groupOutcome {
	t.Helper()
	var staged []StagedWrite
	byTable := map[string]*TableChanges{}
	var written []*TableChanges
	changes := func(table *TableDefinition) *TableChanges {
		found := byTable[table.Name]
		if found == nil {
			found = NewTableChanges(w.handles[table.Name], &staged)
			byTable[table.Name] = found
			written = append(written, found)
		}
		return found
	}
	write(changes)
	tables := make([]string, len(written))
	for index, each := range written {
		tables[index] = each.Handle.Sql.Table
	}
	refresh := NewReadRefresh(w.store.Reads, tables, func() []Change {
		var all []Change
		for _, each := range written {
			all = append(all, each.Changes()...)
		}
		return all
	}, ctx)
	staged = append(staged, refresh.Write)
	fence := CommitFence{
		Admit: func() bool { return true },
		Adopt: func(int64, []int64, int) error { return w.batch.Commit(refresh.Adopt, ctx) },
	}
	return startWait(w.store.enlist(staged, fence, ctx))
}

func (w *oracleWorld) commitAndWait(t *testing.T, ctx context.Context, write func(changes func(table *TableDefinition) *TableChanges)) {
	t.Helper()
	if outcome := <-w.commit(t, ctx, write); outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
}

// oracleShown is what a run shows: its rows and `more`, or its count.
func oracleShown(run *ReadRun) any {
	switch {
	case run == nil:
		return "no run"
	case run.Kind == "rows":
		return struct {
			Rows []StoredRead
			More bool
		}{run.Result.Rows, run.Result.More}
	case run.Kind == "count":
		return run.Count
	}
	return fmt.Sprintf("failed: %v", run.Err)
}

// expectFresh compares every read's adopted run with a fresh run of its statement.
func (w *oracleWorld) expectFresh(t *testing.T, ctx context.Context, step string) {
	t.Helper()
	for _, read := range w.reads {
		registered := read.open.Read()
		fresh, err := Read(w.store, func(executor sqlite.SqliteExecutor) (ReadRun, error) { return registered.Fetch(executor, ctx) }, ctx)
		must(t, err)
		if got, want := oracleShown(read.latest()), oracleShown(&fresh); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %s: adopted %v, a fresh run shows %v", step, read.name, got, want)
		}
	}
}

// randomWrite is one random write to one random table, drawing in Pi's order.
func randomWrite(t *testing.T, d *oracleDefs, r *oracleRandom, changes func(table *TableDefinition) *TableChanges, clock int64) {
	t.Helper()
	session := pick(r, oracleSessions)
	kind := r.int(12)
	switch {
	case kind < 3:
		// A touch: the write of the slice's mix.
		must(t, changes(d.Sessions).Update(Row{"id": session}, Row{"lastTouch": clock, "state": pick(r, oracleStates)}))
	case kind == 3:
		row := Row{"id": session, "app": pick(r, oracleApps), "creator": pick(r, oraclePersons), "private": r.chance(0.3), "state": pick(r, oracleStates)}
		if r.chance(0.5) {
			row["lastTouch"] = clock
		} else {
			row["lastTouch"] = int64(r.int(6000))
		}
		row["title"] = fmt.Sprintf("t%d", clock)
		if r.chance(0.2) {
			row["archivedAt"] = clock
		}
		must(t, changes(d.Sessions).Put(row))
	case kind == 4:
		archived := func() any {
			if r.chance(0.5) {
				return clock
			}
			return Undefined
		}
		// Pi builds all five candidates before it picks one.
		set := []Row{
			{"title": fmt.Sprintf("renamed %d", clock)},
			{"app": pick(r, oracleApps)},
			{"private": r.chance(0.5)},
			{"archivedAt": archived()},
			{"creator": pick(r, oraclePersons)},
		}
		must(t, changes(d.Sessions).Update(Row{"id": session}, pick(r, set)))
	case kind == 5:
		if r.chance(0.3) {
			must(t, changes(d.Sessions).Delete(Row{"id": session}))
		} else {
			must(t, changes(d.Teams).Delete(Row{"team": pick(r, oracleTeams), "session": session}))
		}
	case kind == 6:
		must(t, changes(d.Teams).Put(Row{"team": pick(r, oracleTeams), "session": session}))
	case kind == 7:
		if r.chance(0.6) {
			must(t, changes(d.Shares).Put(Row{"person": pick(r, oraclePersons), "session": session}))
		} else {
			must(t, changes(d.Shares).Delete(Row{"person": pick(r, oraclePersons), "session": session}))
		}
	case kind == 8:
		id := int64(r.int(6))
		switch r.int(4) {
		case 0:
			must(t, changes(d.Pins).Put(Row{"id": id, "person": pick(r, oraclePersons), "session": session, "at": clock}))
		case 1:
			must(t, changes(d.Pins).Update(Row{"id": id}, Row{"session": session}))
		case 2:
			must(t, changes(d.Pins).Update(Row{"id": id}, Row{"at": clock}))
		default:
			must(t, changes(d.Pins).Delete(Row{"id": id}))
		}
	case kind == 9:
		id := int64(r.int(8))
		if r.chance(0.7) {
			row := Row{"id": id, "person": pick(r, oraclePersons), "at": clock}
			if r.chance(0.7) {
				row["session"] = session
			}
			must(t, changes(d.Notes).Put(row))
		} else {
			must(t, changes(d.Notes).Delete(Row{"id": id}))
		}
	case kind == 10:
		var set Row
		if r.chance(0.5) {
			set = Row{"title": "bulk"}
		} else {
			set = Row{"state": pick(r, oracleStates)}
		}
		must(t, changes(d.Sessions).UpdateWhere(func(s ColumnRefs) Condition { return Eq(s["app"], pick(r, oracleApps)) }, set))
	default:
		must(t, changes(d.Teams).DeleteWhere(func(team ColumnRefs) Condition { return Eq(team["team"], pick(r, oracleTeams)) }))
	}
}

// pi: packages/chord/src/store/skip.ts
func TestSkipOracleASkippedReadEqualsAFreshRun(t *testing.T) {
	for _, seed := range []uint32{1, 2, 3, 4, 5, 6, 7, 8} {
		t.Run(fmt.Sprintf("randomized writes, single and grouped commits (seed %d)", seed), func(t *testing.T) {
			d := newOracleDefs(t)
			meter := metered(t)
			w := d.opened(t, meter.Context, nil)
			r := &oracleRandom{state: seed}
			clock := int64(6000)
			// Seed data, so reads have rows from the start.
			w.commitAndWait(t, meter.Context, func(changes func(*TableDefinition) *TableChanges) {
				for range 60 {
					randomWrite(t, d, r, changes, clock)
					clock++
				}
			})
			w.expectFresh(t, meter.Context, "seeded")
			for step := range 150 {
				// One commit of one to three writes, or a group of commits queued together (one backend transaction).
				commits := 1
				if r.chance(0.25) {
					commits = 1 + r.int(3)
				}
				var pending []<-chan groupOutcome
				for range commits {
					pending = append(pending, w.commit(t, meter.Context, func(changes func(*TableDefinition) *TableChanges) {
						for write := 1 + r.int(2); write > 0; write-- {
							randomWrite(t, d, r, changes, clock)
							clock++
						}
					}))
				}
				for _, each := range pending {
					if outcome := <-each; outcome.Err != nil {
						t.Fatal(outcome.Err)
					}
				}
				w.expectFresh(t, meter.Context, fmt.Sprintf("seed %d, step %d", seed, step))
			}
			counts := meter.Counts()
			// The run exercised all three outcomes.
			if counts["chord.query.skips"] <= 200 || counts["chord.query.probes"] <= 100 || counts["chord.query.runs"] <= 100 {
				t.Fatalf("skips %v, probes %v, runs %v: want more than 200, 100 and 100", counts["chord.query.skips"], counts["chord.query.probes"], counts["chord.query.runs"])
			}
		})
	}
}

type oracleCost struct{ Runs, Probes, Skips float64 }

// measured seeds a world with the listed reads and returns a function measuring one commit's runs, probes and skips.
func (d *oracleDefs) measured(t *testing.T, only []string, seed func(changes func(*TableDefinition) *TableChanges)) (*oracleWorld, func(write func(changes func(*TableDefinition) *TableChanges)) oracleCost) {
	t.Helper()
	meter := metered(t)
	w := d.opened(t, meter.Context, only)
	w.commitAndWait(t, meter.Context, seed)
	return w, func(write func(changes func(*TableDefinition) *TableChanges)) oracleCost {
		t.Helper()
		before := meter.Counts()
		w.commitAndWait(t, meter.Context, write)
		w.expectFresh(t, background, "after the write")
		after := meter.Counts()
		of := func(name string) float64 { return after[name] - before[name] }
		return oracleCost{of("chord.query.runs"), of("chord.query.probes"), of("chord.query.skips")}
	}
}

func oracleSession(id string, fields Row) Row {
	row := Row{"id": id, "app": "club", "creator": "anna", "private": false, "state": "idle", "lastTouch": int64(100), "title": id}
	maps.Copy(row, fields)
	return row
}

func wantCost(t *testing.T, got oracleCost, runs, probes, skips float64) {
	t.Helper()
	if want := (oracleCost{runs, probes, skips}); got != want {
		t.Errorf("cost = %+v, want %+v", got, want)
	}
}

func TestSkipOwnTable(t *testing.T) {
	d := newOracleDefs(t)
	type changesOf = func(*TableDefinition) *TableChanges

	t.Run("an update of a column the read does not depend on skips without a probe; a touch of another row probes", func(t *testing.T) {
		_, cost := d.measured(t, []string{"created by anna"}, func(changes changesOf) {
			for index, id := range []string{"s01", "s02", "s03", "s04", "s05"} {
				must(t, changes(d.Sessions).Put(oracleSession(id, Row{"lastTouch": int64(100 + index)})))
			}
			must(t, changes(d.Sessions).Put(oracleSession("s06", Row{"creator": "ben", "lastTouch": int64(50)})))
		})
		// The window holds s05, s04, s03 and, past its bound, s02.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).Update(Row{"id": "s01"}, Row{"title": "renamed"}))
		}), 0, 0, 1)
		// A row in the window: its mapped column changed.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).Update(Row{"id": "s04"}, Row{"title": "renamed"}))
		}), 1, 0, 0)
		// Ben's session touched: neither in the read before nor in its bucket (anna's) after, so the commit index
		// (slice 15 §3) leaves the read out: not even skip-tested (Pi v1.7.0 skip.test.ts:114-120).
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).Update(Row{"id": "s06"}, Row{"lastTouch": int64(999)}))
		}), 0, 0, 0)
		// A row past the window touched to the top: the probe finds it.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).Update(Row{"id": "s01"}, Row{"lastTouch": int64(1000)}))
		}), 1, 1, 0)
		// A put behind the row past the bound: a probe miss.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).Put(oracleSession("s07", Row{"lastTouch": int64(1)})))
		}), 0, 1, 1)
		// A delete of a row that was not fetched concerns no read (the commit index); of the row past the bound it runs (Pi v1.7.0 skip.test.ts:133-138).
		wantCost(t, cost(func(changes changesOf) { must(t, changes(d.Sessions).Delete(Row{"id": "s07"})) }), 0, 0, 0)
		wantCost(t, cost(func(changes changesOf) { must(t, changes(d.Sessions).Delete(Row{"id": "s03"})) }), 1, 0, 0)
	})

	t.Run("a bulk update skips by the columns it assigns; a bulk delete runs; a count skips only by its condition columns", func(t *testing.T) {
		_, cost := d.measured(t, []string{"created by anna", "all sessions count"}, func(changes changesOf) {
			must(t, changes(d.Sessions).Put(oracleSession("s01", nil)))
		})
		// The list maps every column, the count depends on `app` alone.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).UpdateWhere(func(s ColumnRefs) Condition { return Eq(s["app"], "club") }, Row{"private": true}))
		}), 1, 0, 1)
		// A touch: the list's order column, no condition column of the count.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).Update(Row{"id": "s01"}, Row{"lastTouch": int64(7)}))
		}), 1, 0, 1)
		// A put tells nothing: the count keeps no keys and runs.
		if got := cost(func(changes changesOf) { must(t, changes(d.Sessions).Put(oracleSession("s09", Row{"app": "family"}))) }); got.Runs != 1 {
			t.Errorf("put: %d runs, want 1", int(got.Runs))
		}
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).DeleteWhere(func(s ColumnRefs) Condition { return Eq(s["id"], "s09") }))
		}), 2, 0, 0)
	})
}

func TestSkipJoinedTable(t *testing.T) {
	d := newOracleDefs(t)
	type changesOf = func(*TableDefinition) *TableChanges

	t.Run("the team page of another team is skipped by one probe; the session's team runs", func(t *testing.T) {
		_, cost := d.measured(t, []string{"team page u12", "club list, two teams"}, func(changes changesOf) {
			for index, id := range []string{"s01", "s02", "s03"} {
				must(t, changes(d.Sessions).Put(oracleSession(id, Row{"lastTouch": int64(100 + index)})))
				team := "u12"
				if index == 2 {
					team = "u14"
				}
				must(t, changes(d.Teams).Put(Row{"team": team, "session": id}))
			}
			must(t, changes(d.Sessions).Put(oracleSession("s04", Row{"lastTouch": int64(90)})))
		})
		// s03 is in u14 only: both reads ask their probe, and neither runs.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).Update(Row{"id": "s03"}, Row{"lastTouch": int64(999), "state": "working"}))
		}), 0, 2, 2)
		// s01 is on both: its partner key is among their rows.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).Update(Row{"id": "s01"}, Row{"lastTouch": int64(1000)}))
		}), 2, 0, 0)
		// A partner column outside condition, order and the mapped ones: nothing to ask. Here every set column is mapped.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Sessions).Update(Row{"id": "s03"}, Row{"title": "x"}))
		}), 0, 0, 2)
		// A partner that is no row's partner, deleted: it cannot enter.
		wantCost(t, cost(func(changes changesOf) { must(t, changes(d.Sessions).Delete(Row{"id": "s03"})) }), 0, 0, 2)
		// A team row is a write of the own table, and of the table the person's condition tests `exists` in. s04 joins u10:
		// the u12 page asks both probes and skips, the two-team list's first probe finds the row.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Teams).Put(Row{"team": "u10", "session": "s04"}))
		}), 1, 3, 1)
		// s02 is on both pages: another team row of it may change its `exists`, so both run.
		if got := cost(func(changes changesOf) { must(t, changes(d.Teams).Put(Row{"team": "u10", "session": "s02"})) }); got.Runs != 2 {
			t.Errorf("runs = %v, want 2", got.Runs)
		}
	})
}

func TestSkipExistsTable(t *testing.T) {
	d := newOracleDefs(t)
	type changesOf = func(*TableDefinition) *TableChanges

	t.Run("a pin moved from one session to another runs the reads of both, also under not(exists)", func(t *testing.T) {
		w, cost := d.measured(t, []string{"pinned by anyone", "pinned by nobody"}, func(changes changesOf) {
			for _, id := range []string{"s01", "s02", "s03"} {
				must(t, changes(d.Sessions).Put(oracleSession(id, Row{"app": "family"})))
			}
			must(t, changes(d.Pins).Put(Row{"id": int64(1), "person": "anna", "session": "s01", "at": int64(1)}))
		})
		pinned := func() []any {
			run := w.reads[0].latest()
			ids := []any{}
			for _, row := range run.Result.Rows {
				ids = append(ids, row["id"])
			}
			return ids
		}
		qEqual(t, pinned(), []any{"s01"})
		// The matched column is no key column: what the pin paired with before is unknown, so both reads run.
		wantCost(t, cost(func(changes changesOf) { must(t, changes(d.Pins).Update(Row{"id": int64(1)}, Row{"session": "s02"})) }), 2, 0, 0)
		qEqual(t, pinned(), []any{"s02"})
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Pins).Put(Row{"id": int64(1), "person": "anna", "session": "s03", "at": int64(2)}))
		}), 2, 0, 0)
		qEqual(t, pinned(), []any{"s03"})
		// A column outside the match and the inner condition: nothing to ask.
		wantCost(t, cost(func(changes changesOf) { must(t, changes(d.Pins).Update(Row{"id": int64(1)}, Row{"at": int64(3)})) }), 0, 0, 2)
	})

	t.Run("a share of a session of another app is skipped by one probe; of a row in the read it runs", func(t *testing.T) {
		_, cost := d.measured(t, []string{"visible club, anna", "one"}, func(changes changesOf) {
			must(t, changes(d.Sessions).Put(oracleSession("s01", Row{"app": "family", "creator": "ben"})))
			must(t, changes(d.Sessions).Put(oracleSession("s03", nil)))
			must(t, changes(d.Teams).Put(Row{"team": "u10", "session": "s03"}))
		})
		// s01 is the family's: the club list's probe misses, the read of s03 asks its own and misses too.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Shares).Put(Row{"person": "anna", "session": "s01"}))
		}), 0, 2, 2)
		// s03 is in both reads: a share of it may change them.
		wantCost(t, cost(func(changes changesOf) {
			must(t, changes(d.Shares).Put(Row{"person": "ben", "session": "s03"}))
		}), 2, 0, 0)
	})
}

func TestSkipCost(t *testing.T) {
	d := newOracleDefs(t)
	type changesOf = func(*TableDefinition) *TableChanges

	t.Run("a transaction writing many rows asks a read a few probes, then runs it", func(t *testing.T) {
		// Fable S5: a bulk of puts must not probe one read once per row (Pi v1.7.0 skip.test.ts:264-279).
		_, cost := d.measured(t, []string{"created by anna"}, func(changes changesOf) {
			for _, id := range []string{"s01", "s02", "s03", "s04"} {
				must(t, changes(d.Sessions).Put(oracleSession(id, nil)))
			}
		})
		// Anna's rows behind the window's last one: each a candidate, each probe a miss.
		wantCost(t, cost(func(changes changesOf) {
			for index := range 100 {
				must(t, changes(d.Sessions).Put(oracleSession(fmt.Sprintf("b%d", index), Row{"lastTouch": int64(1)})))
			}
		}), 1, 8, 0)
		// Rows of another person's bucket are no candidates (slice 15 §3): the limit counts the read's candidate rows.
		wantCost(t, cost(func(changes changesOf) {
			for index := range 100 {
				must(t, changes(d.Sessions).Put(oracleSession(fmt.Sprintf("c%d", index), Row{"creator": "ben"})))
			}
		}), 0, 0, 0)
	})

	t.Run("builds no change of a transaction whose tables nothing reads", func(t *testing.T) {
		// Fable S4, Opus: a store without reads pays its writes nothing for them.
		meter := metered(t)
		w := d.opened(t, meter.Context, []string{})
		built := 0
		var staged []StagedWrite
		changes := NewTableChanges(w.handles[d.Sessions.Name], &staged)
		must(t, changes.Put(oracleSession("s01", nil)))
		refresh := NewReadRefresh(w.store.Reads, []string{changes.Handle.Sql.Table}, func() []Change {
			built++
			return changes.Changes()
		}, meter.Context)
		staged = append(staged, refresh.Write)
		fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return refresh.Adopt() }}
		if _, err := w.store.Commit(staged, fence, meter.Context); err != nil {
			t.Fatal(err)
		}
		if built != 0 {
			t.Fatalf("%d changes built, want none", built)
		}
	})
}
