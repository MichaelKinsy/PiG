package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Pi packages/chord/test/store has no test of read.ts alone: its `Read` is exercised through skip.test.ts, reads.test.ts
// and open-query's tests, which come with those files. These cases are the contract those rely on: a run's rows,
// keys, pairs and the row past the limit; a count; a failure that is a run; what the skip test keeps (`ran`,
// `latest`, `adopt`, `know`, `forget`); the probe's range. Go form: the class `Read` is `RegisteredRead` (the free
// function `Read` of the store's guarded read has the name).

type adoptedRuns struct{ runs []ReadRun }

func (sink *adoptedRuns) Adopt(run ReadRun) { sink.runs = append(sink.runs, run) }

// readFixture is the sessions table with four rows and a plan of it.
func readFixture(t *testing.T) (*ChordStore, rpSets, string, rpBindings) {
	t.Helper()
	f := rpFixtures(t)
	store := memoryStore(t)
	sessions, teams := tableIn(t, store, f.Sessions), tableIn(t, store, f.Teams)
	var staged []StagedWrite
	changes := NewTableChanges(sessions, &staged)
	for index, app := range []string{"club", "club", "family", "club"} {
		must(t, changes.Put(Row{"id": "s" + string(rune('0'+index)), "app": app, "private": index == 2, "lastTouch": int64(10 * index), "title": "t"}))
	}
	team := NewTableChanges(teams, &staged)
	for _, pair := range [][2]string{{"u12", "s0"}, {"u12", "s1"}, {"u14", "s1"}} {
		must(t, team.Put(Row{"team": pair[0], "session": pair[1]}))
	}
	fence := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64, int) error { return nil }}
	if _, err := store.Commit(staged, fence, background); err != nil {
		t.Fatal(err)
	}
	return store, f, sessions.Sql.Table, rpBindings{
		f.SessionColumns.Name: {Table: sessions.Sql.Table, Definition: f.Sessions},
		f.TeamSessions.Name:   {Table: teams.Sql.Table, Definition: f.Teams},
	}
}

func fetched(t *testing.T, store *ChordStore, read *RegisteredRead) ReadRun {
	t.Helper()
	run, err := Read(store, func(executor sqlite.SqliteExecutor) (ReadRun, error) { return read.Fetch(executor, background) }, background)
	must(t, err)
	return run
}

// pi: packages/chord/src/store/read.ts
func TestRead(t *testing.T) {
	store, f, table, bindings := readFixture(t)
	visible := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
		return Exists(f.TeamSessions, Match{{"session", s["id"]}}, nil)
	})
	must(t, visible.Err())
	plan, err := NewReadPlan(visible, table, Projection{Reads: []string{"id"}}, ReadRows, bindings)
	must(t, err)
	sink := &adoptedRuns{}
	read := NewRegisteredRead(plan, ReadKind{Kind: "rows", Range: Range{Limit: 1}}, sink)

	t.Run("fetches the rows of its range, one more than asked: whether a row follows, where the range ends", func(t *testing.T) {
		run := fetched(t, store, read)
		if run.Kind != "rows" || run.Result == nil {
			t.Fatalf("run = %#v", run)
		}
		result := run.Result
		qEqual(t, result.Rows, []StoredRead{{"id": "s0"}})
		if !result.More {
			t.Fatal("More = false: s1 follows")
		}
		qEqual(t, result.Past, Position{"s1"})
		qEqual(t, result.Keys, map[any]bool{"s0": true, "s1": true})
		use := &plan.Uses[1]
		qEqual(t, result.Paired, map[*Use]map[any]bool{use: {"s0": true, "s1": true}})
	})

	t.Run("a count is the number of rows of its condition", func(t *testing.T) {
		countPlan, err := NewReadPlan(visible, table, Projection{}, ReadCount, bindings)
		must(t, err)
		run := fetched(t, store, NewRegisteredRead(countPlan, ReadKind{Kind: "count"}, sink))
		qEqual(t, run, ReadRun{Kind: "count", Count: 2})
	})

	t.Run("a failed statement is a run that failed, and never throws", func(t *testing.T) {
		broken, err := NewReadPlan(visible, "missing", Projection{}, ReadRows, bindings)
		must(t, err)
		bad := NewRegisteredRead(broken, ReadKind{Kind: "rows", Range: Range{Limit: 1}}, sink)
		run, err := Read(store, func(executor sqlite.SqliteExecutor) (ReadRun, error) { return bad.Run(executor, background), nil }, background)
		must(t, err)
		if run.Kind != "failed" || run.Err == nil {
			t.Fatalf("run = %#v", run)
		}
	})

	t.Run("keeps a step's run for its transaction until it is adopted; an adopted run is the latest of any other", func(t *testing.T) {
		step, other := new(struct{ sqlite.SqliteExecutor }), new(struct{ sqlite.SqliteExecutor })
		if read.Latest(step) != nil {
			t.Fatal("a read never adopted has no latest run")
		}
		run := fetched(t, store, read)
		read.Ran(step, run)
		if got := read.Latest(step); got == nil || got.Result != run.Result {
			t.Fatal("the step's own run is the latest of the step")
		}
		if read.Latest(other) != nil {
			t.Fatal("another transaction does not see a tentative run")
		}
		read.Adopt(run)
		if got := read.Latest(other); got == nil || got.Result != run.Result {
			t.Fatal("an adopted run is the latest of every transaction")
		}
		if len(sink.runs) != 1 {
			t.Fatalf("sink took %d runs", len(sink.runs))
		}
		read.Ran(step, ReadRun{Kind: "rows", Result: &ReadResult{}})
		read.Adopt(ReadRun{Kind: "failed", Err: errors.New("failed")})
		if read.Latest(other) != nil || read.Latest(step) == nil {
			t.Fatal("a failed run is not kept as adopted, and does not clear another step's tentative run")
		}
	})

	t.Run("a failure and one that wraps it are two runs: Pi compares the run objects, so the tentative run stays", func(t *testing.T) {
		// read.ts:115 `if (this.#tentative?.run === run) this.#tentative = undefined`: a failed run is the same run only when it
		// carries the very failure; the step's failure wrapping the adopted one (or the reverse) is another failure, another run.
		step := new(struct{ sqlite.SqliteExecutor })
		first := errors.New("first")
		wrapping := fmt.Errorf("wrapping: %w", first)
		read.Ran(step, ReadRun{Kind: "failed", Err: wrapping})
		read.Adopt(ReadRun{Kind: "failed", Err: first})
		if got := read.Latest(step); got == nil || !errors.Is(got.Err, wrapping) {
			t.Fatalf("the tentative run after the wrapped failure was adopted = %#v, want the step's own failed run", got)
		}
		read.Adopt(ReadRun{Kind: "failed", Err: wrapping})
		if got := read.Latest(step); got != nil {
			t.Fatalf("the tentative run after its own failure was adopted = %#v, want none", got)
		}
	})

	t.Run("knows rows without running, forgets them, and probes through the row past its limit", func(t *testing.T) {
		sink.runs = nil
		read.Know([]StoredRead{{"id": "s0"}, {"id": "s1"}, {"id": "s2"}})
		known := read.Latest(nil)
		if known == nil || len(known.Result.Rows) != 3 || known.Result.More || len(sink.runs) != 0 {
			t.Fatalf("known = %#v, sink %d", known, len(sink.runs))
		}
		read.Forget()
		if read.Latest(nil) != nil {
			t.Fatal("a forgotten read has no latest run")
		}
		qEqual(t, read.Probed(&ReadResult{}), Range{Limit: 1})
		qEqual(t, read.Probed(&ReadResult{Past: Position{"s1"}}), Range{Through: Position{"s1"}, Limit: 1})
	})

	t.Run("identity is the value of one column, the JSON of several", func(t *testing.T) {
		qEqual(t, Identity([]string{"a"}, StoredRead{"a": "x", "b": int64(1)}), any("x"))
		qEqual(t, Identity([]string{"a", "b"}, StoredRead{"a": "x", "b": int64(1)}), any(`["x",1]`))
		qEqual(t, IdentityOf([]sqlite.SqliteValue{"x"}), any("x"))
		qEqual(t, IdentityOf([]sqlite.SqliteValue{"x", int64(1)}), any(`["x",1]`))
	})
}
