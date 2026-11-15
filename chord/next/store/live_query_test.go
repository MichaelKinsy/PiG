package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// pi: packages/chord/src/store/live-query.ts
// Ports packages/chord/test/store/live-query.test.ts as slice 14 changed it (Pi 92e1db6ca): "a refresh applied again" now
// runs on the store's reads (`ReadRefresh`, `Reads`); the other cases of that file run on a host
// (chord/next/host/host_live_query_test.go). The cases below are this port's own contract tests of live-query.ts over an
// open query (Pi's file reaches `LiveQuery` only through a host): its load, its commits, a count, a read that replaces
// another, its detach, its failures, and `DecodedRows`.

func TestLiveQueryAppliedAgainAdoptsItsLastApplicationsRunsOnlyAGroupedCommitThatRolledBackAppliesAgainAlone(t *testing.T) {
	w := newReadsWorld(t)
	table := w.notes.Sql.Table
	// One row stored first.
	w.put(t, w.notes, Row{"id": int64(1), "text": "t"}, state.NewBatch(func(state.BatchError) {}, telemetry.NoopTelemetryMetrics))
	// A read over the written table without a result: the refresh never skips it, so each application runs it.
	plan, err := NewReadPlan(NewQuery(w.notes.Definition), table, Projection{}, ReadCount, w.store.Reads)
	must(t, err)
	log := &runLog{}
	w.store.Reads.Add(NewRegisteredRead(plan, ReadKind{Kind: "count"}, log))
	changes := func() []Change { return []Change{{Table: table, Kind: ChangeBulk}} }
	refresh := NewReadRefresh(w.store.Reads, []string{table}, changes, background)
	_, err = Read(w.store, func(executor sqlite.SqliteExecutor) (struct{}, error) {
		for application := range 2 {
			if application == 1 {
				// What the second application sees that the first did not: its run is the one adopted.
				if err := executor.Run(`DELETE FROM "` + table + `"`); err != nil {
					return struct{}{}, err
				}
			}
			// Each application is of another transaction: the first one's tentative run is not the second's latest.
			step := NewStep(&struct{ sqlite.SqliteExecutor }{executor}, 0, nil, nil)
			if _, err := refresh.Write.Apply(step); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	}, background)
	must(t, err)
	must(t, refresh.Adopt())
	if got := log.len(); got != 1 {
		t.Fatalf("%d runs adopted after two applications, want 1", got)
	}
	if run := log.last(); run.Kind != "count" || run.Count != 0 {
		t.Fatalf("adopted %+v, want the second application's count of 0 (the first saw 1 row)", run)
	}
}

// liveOwner is who a live query belongs to: what it is told of its failures after its first value.
type liveOwner struct {
	mu      sync.Mutex
	reports []error
}

func (*liveOwner) Label() string { return "app.store" }

func (o *liveOwner) Report(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reports = append(o.reports, err)
}

func (o *liveOwner) told() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.reports)
}

// liveWorld is a live query over the notes table of a reads world.
type liveWorld struct {
	*readsWorld
	batch *state.Batch
	owner *liveOwner
	query *LiveQuery
	// broken makes the decoding of a row fail.
	broken bool
}

func newLiveWorld(t *testing.T, shape LiveShape) *liveWorld {
	t.Helper()
	w := &liveWorld{readsWorld: newReadsWorld(t), owner: &liveOwner{}}
	w.batch = state.NewBatch(func(state.BatchError) {}, telemetry.NoopTelemetryMetrics)
	w.query = NewLiveQuery(ReadSite{Store: w.store, Batch: w.batch}, w.owner, shape)
	return w
}

// rowsShape is the owner's own window: the rows themselves, the newest `limit`.
func rowsShape(limit int) LiveShape {
	return LiveShape{Kind: "rows", Range: Range{Limit: limit}, Present: func(rows []delta.JsonValue, _ bool) delta.JsonValue { return rows }}
}

// target is a read of the notes' texts; `prefix` tells one target's rows from another's.
func (w *liveWorld) target(prefix string) LiveTarget {
	return LiveTarget{
		Spec: QuerySpec{Query: NewQuery(w.notes.Definition), Table: w.notes.Sql.Table, Projection: Projection{}, Owner: w.notes},
		Decode: func(stored StoredRead, _ context.Context) (delta.JsonValue, error) {
			if w.broken {
				return nil, errors.New("a row the code cannot read")
			}
			return prefix + stored["text"].(string), nil
		},
	}
}

func (w *liveWorld) put(t *testing.T, id int64, text string) {
	t.Helper()
	w.readsWorld.put(t, w.notes, Row{"id": id, "text": text}, w.batch)
}

func (w *liveWorld) value(t *testing.T) delta.JsonValue {
	t.Helper()
	value, err := w.query.Value()
	must(t, err)
	return value
}

func TestLiveQueryLoadReadsTheRowsAndACommitOverItsTableRunsItAgain(t *testing.T) {
	w := newLiveWorld(t, rowsShape(5))
	meter := metered(t)
	must(t, w.query.Load(w.target(""), meter.Context))
	qEqual(t, w.value(t), []delta.JsonValue{})
	w.put(t, 1, "a")
	qEqual(t, w.value(t), []delta.JsonValue{"a"})
	w.put(t, 2, "b")
	qEqual(t, w.value(t), []delta.JsonValue{"a", "b"})
	// The load's run counted through its context; the commits' through theirs (not metered here).
	if got := meter.Counts()["chord.query.runs"]; got != 1 {
		t.Fatalf("chord.query.runs = %v after the load, want 1", got)
	}
}

func TestLiveQueryCountsTheRowsOfItsQuery(t *testing.T) {
	w := newLiveWorld(t, LiveShape{Kind: "count"})
	must(t, w.query.Load(w.target(""), background))
	qEqual(t, w.value(t), float64(0))
	w.put(t, 1, "a")
	w.put(t, 2, "b")
	qEqual(t, w.value(t), float64(2))
}

func TestLiveQueryReadTakesAnotherTargetAndTheOnePreviousLeavesTheIndex(t *testing.T) {
	w := newLiveWorld(t, rowsShape(5))
	must(t, w.query.Load(w.target("A:"), background))
	w.put(t, 1, "a")
	qEqual(t, w.value(t), []delta.JsonValue{"A:a"})
	run := w.query.Read(w.target("B:"), background)
	if run == nil || run.Kind != "rows" {
		t.Fatalf("Read = %+v, want its first run", run)
	}
	qEqual(t, w.value(t), []delta.JsonValue{"B:a"})
	// One read of the table now: the first target's left.
	if got := len(w.store.Reads.On(w.notes.Sql.Table)); got != 1 {
		t.Fatalf("%d reads over the table, want 1", got)
	}
	w.put(t, 2, "b")
	qEqual(t, w.value(t), []delta.JsonValue{"B:a", "B:b"})
}

func TestLiveQueryARunOfAReadItClosedIsDroppedWhenItsCommitAdoptsItAfterwards(t *testing.T) {
	w := newLiveWorld(t, rowsShape(5))
	must(t, w.query.Load(w.target(""), background))
	// A commit's step runs the read; before the commit adopts that run, the query fails (its arguments were refused) and
	// closes its reads.
	release, committed := w.pausedPut(t, w.notes, Row{"id": int64(1), "text": "a"}, w.batch)
	cause := errors.New("arguments refused")
	w.query.Fail(cause, background)
	release()
	must(t, <-committed)
	// The run is neither the current read's nor a pending one's: it cannot heal the failure.
	if _, err := w.query.Value(); !errors.Is(err, cause) {
		t.Fatalf("Value error = %v, want the failure still", err)
	}
}

func TestLiveQueryDetachEndsItsReadsAndItsValueIsTheEnd(t *testing.T) {
	w := newLiveWorld(t, rowsShape(5))
	must(t, w.query.Load(w.target(""), background))
	ended := errors.New("the attempt ended")
	w.query.Detach(ended)
	if reads := w.store.Reads.On(w.notes.Sql.Table); len(reads) != 0 {
		t.Fatalf("%d reads left over the table, want none", len(reads))
	}
	if _, err := w.query.Value(); !errors.Is(err, ended) {
		t.Fatalf("Value error = %v, want the attempt's end", err)
	}
	if run := w.query.Read(w.target(""), background); run != nil {
		t.Fatalf("a detached query read %+v, want nothing", run)
	}
	w.put(t, 1, "a")
	if _, err := w.query.Value(); !errors.Is(err, ended) {
		t.Fatalf("Value error = %v after a commit, want the attempt's end", err)
	}
}

func TestLiveQueryAFailureAtLoadIsReturnedNotReportedAFailureAfterItIsReportedOncePerEpisode(t *testing.T) {
	w := newLiveWorld(t, rowsShape(10))
	w.put(t, 1, "a")
	w.broken = true
	err := w.query.Load(w.target(""), background)
	if err == nil || err.Error() != "a row the code cannot read" {
		t.Fatalf("Load = %v, want the decoding failure", err)
	}
	if got := w.owner.told(); got != 0 {
		t.Fatalf("owner told %d times of a failure its activation returns, want 0", got)
	}
	// The next good run heals it; a failure after the first value is the owner's to hear, once per episode.
	w.broken = false
	w.put(t, 2, "b")
	qEqual(t, w.value(t), []delta.JsonValue{"a", "b"})
	w.broken = true
	w.put(t, 3, "c")
	w.put(t, 4, "d")
	if _, err := w.query.Value(); err == nil {
		t.Fatal("a query whose run failed has a value")
	}
	if got := w.owner.told(); got != 1 {
		t.Fatalf("owner told %d times of one episode, want 1", got)
	}
	w.broken = false
	w.put(t, 5, "e")
	qEqual(t, w.value(t), []delta.JsonValue{"a", "b", "c", "d", "e"})
	w.broken = true
	w.put(t, 6, "f")
	if got := w.owner.told(); got != 2 {
		t.Fatalf("owner told %d times of two episodes, want 2", got)
	}
}

func TestLiveQueryFailWithoutAReadIsAFailedMemberAndClosesItsReads(t *testing.T) {
	w := newLiveWorld(t, rowsShape(5))
	must(t, w.query.Load(w.target(""), background))
	// What it should read cannot be read (its arguments threw): a failed member; no read runs for it any more.
	cause := errors.New("arguments refused")
	w.query.Fail(cause, background)
	if _, err := w.query.Value(); !errors.Is(err, cause) {
		t.Fatalf("Value error = %v, want the failure", err)
	}
	if reads := w.store.Reads.On(w.notes.Sql.Table); len(reads) != 0 {
		t.Fatalf("%d reads left over the table, want none", len(reads))
	}
	if got := w.owner.told(); got != 1 {
		t.Fatalf("owner told %d times, want 1", got)
	}
}

// told is a live query's owner that signals each failure it is told of: the one thing a test can wait on after a binding
// changed (the read it starts again runs on a goroutine of its own).
type told struct {
	*liveOwner
	failed chan error
}

func (o told) Report(err error) {
	o.liveOwner.Report(err)
	o.failed <- err
}

func TestLiveQueryASetUnboundFailsItsMember(t *testing.T) {
	// Pi: the open query's rebound reads again; its failed run (`column set ... is not served`) is the member's failure.
	w := newLiveWorld(t, rowsShape(5))
	owner := told{&liveOwner{}, make(chan error, 4)}
	// The batch has the host's one-at-a-time sections: the read a binding starts runs on a goroutine of its own.
	var section sync.Mutex
	w.batch.UseSection(func(work func()) { section.Lock(); defer section.Unlock(); work() }, nil)
	w.query = NewLiveQuery(ReadSite{Store: w.store, Batch: w.batch}, owner, rowsShape(5))
	w.bind(t)
	target := w.target("")
	target.Spec.Query = w.tagged
	must(t, w.query.Load(target, background))
	w.readsWorld.put(t, w.tags, Row{"note": int64(1), "tag": "red"}, w.batch)
	w.put(t, 1, "a")
	qEqual(t, w.value(t), []delta.JsonValue{"a"})
	w.store.Reads.Unbind(w.set.Name, w.tags)
	err := <-owner.failed
	if got := rejected(t, err, data.CodeUnavailable, "").Message; got != `column set "tags.columns" is not served` {
		t.Fatalf("reported %q", got)
	}
	// The read ran on a goroutine of its own, in a section of the batch: read the value in one.
	var failure error
	w.batch.Section(func() { _, failure = w.query.Value() })
	if failure == nil || !strings.Contains(failure.Error(), `column set "tags.columns" is not served`) {
		t.Fatalf("Value error = %v, want the unserved set", failure)
	}
}

func TestLiveQueryOpenOpensItsReadAtOnceAndABindingThatChangedBeforeItsLoadFailsItThen(t *testing.T) {
	// Pi's `read` runs up to its first await when it is called: the open query is registered and follows its set's binding.
	// A reader's move opens its read in the section of the move and loads it on a task later; the set unbound in between
	// fails the member at the unbinding, not when that task runs (the reader's attempt may have ended by then, and a
	// closed query fails nothing).
	w := newLiveWorld(t, rowsShape(5))
	owner := told{&liveOwner{}, make(chan error, 4)}
	var section sync.Mutex
	w.batch.UseSection(func(work func()) { section.Lock(); defer section.Unlock(); work() }, nil)
	w.query = NewLiveQuery(ReadSite{Store: w.store, Batch: w.batch}, owner, rowsShape(5))
	w.bind(t)
	target := w.target("")
	target.Spec.Query = w.tagged
	load := w.query.Open(target, background)
	w.store.Reads.Unbind(w.set.Name, w.tags)
	select {
	case err := <-owner.failed:
		if got := rejected(t, err, data.CodeUnavailable, "").Message; got != `column set "tags.columns" is not served` {
			t.Fatalf("reported %q", got)
		}
	default:
		t.Fatal("the member did not fail when its set was unbound: the read was not registered by Open")
	}
	// The load that runs later finds the failure its query compiled to, and the member's failure stays.
	if run := load(); run == nil || run.Kind != "failed" {
		t.Fatalf("load = %+v, want a failed run", run)
	}
	var failure error
	w.batch.Section(func() { _, failure = w.query.Value() })
	if failure == nil || !strings.Contains(failure.Error(), `column set "tags.columns" is not served`) {
		t.Fatalf("Value error = %v, want the unserved set", failure)
	}
	// Detached before a load: nothing to load.
	w.batch.Section(func() { w.query.Detach(errors.New("ended")) })
	if run := w.query.Open(target, background)(); run != nil {
		t.Fatalf("a detached query loaded %+v", run)
	}
}

func TestDecodedRowsKeepAnUnchangedRowTheSameObjectAChangedOneNew(t *testing.T) {
	w := newLiveWorld(t, rowsShape(5))
	decoded := 0
	target := w.target("")
	// Each row decodes to a fresh object, as a table's rows do: identity tells a reused row from a decoded one.
	target.Decode = func(stored StoredRead, _ context.Context) (delta.JsonValue, error) {
		decoded++
		return &struct{ text any }{stored["text"]}, nil
	}
	one := StoredRead{"id": int64(1), "text": "a"}
	two := StoredRead{"id": int64(2), "text": "b"}
	rows := &DecodedRows{}
	first, err := rows.Decode([]StoredRead{one, two}, target, background)
	must(t, err)
	// The same stored values in a new run: both rows are the objects of the last value, nothing decodes.
	again, err := rows.Decode([]StoredRead{{"id": int64(1), "text": "a"}, {"id": int64(2), "text": "b"}}, target, background)
	must(t, err)
	if decoded != 2 || again[0] != first[0] || again[1] != first[1] {
		t.Fatalf("decoded %d times, rows kept = [%v %v]; want 2 decodes and both rows kept", decoded, again[0] == first[0], again[1] == first[1])
	}
	// One column changed: that row is decoded again, the other is kept.
	changed, err := rows.Decode([]StoredRead{{"id": int64(1), "text": "a"}, {"id": int64(2), "text": "B"}}, target, background)
	must(t, err)
	if decoded != 3 || changed[0] != first[0] || changed[1] == first[1] {
		t.Fatalf("decoded %d times, kept = [%v %v]; want 3 decodes, the first kept and the second new", decoded, changed[0] == first[0], changed[1] == first[1])
	}
	// Another target decodes its own way: nothing of the last value is reused.
	rows.Reset()
	fresh, err := rows.Decode([]StoredRead{{"id": int64(1), "text": "a"}}, target, background)
	must(t, err)
	if decoded != 4 || fresh[0] == first[0] {
		t.Fatalf("decoded %d times, kept = %v after a reset; want 4 decodes and none kept", decoded, fresh[0] == first[0])
	}
}

// Pi has no test of `chord.query.rows` (the third counter of slice 14 §29, counted at read.ts:104: `add(stored.length)`, the
// rows a run's statement returned to Chord, the one past the limit included; a count returns a number, no row). This pins
// its count for one query with a known number of rows.
func TestReadCountsTheRowsItsStatementReturnedOneMoreThanTheLimit(t *testing.T) {
	w := newLiveWorld(t, rowsShape(5))
	for id := int64(1); id <= 5; id++ {
		w.put(t, id, "n")
	}
	rowsOf := func(what ReadKind) (float64, ReadRun) {
		t.Helper()
		plan, err := NewReadPlan(NewQuery(w.notes.Definition), w.notes.Sql.Table, Projection{}, ReadShape(what.Kind), w.store.Reads)
		must(t, err)
		read := NewRegisteredRead(plan, what, &runLog{})
		meter := metered(t)
		run, err := Read(w.store, func(executor sqlite.SqliteExecutor) (ReadRun, error) { return read.Fetch(executor, meter.Context) }, meter.Context)
		must(t, err)
		return meter.Counts()["chord.query.rows"], run
	}
	// Five rows stored, a window of three: the statement asks for four (the fourth tells `more`).
	rows, run := rowsOf(ReadKind{Kind: "rows", Range: Range{Limit: 3}})
	if rows != 4 || len(run.Result.Rows) != 3 || !run.Result.More {
		t.Fatalf("limit 3: %v rows counted, %d adopted, more %v; want 4, 3, true", rows, len(run.Result.Rows), run.Result.More)
	}
	// A window wider than the table: every row, nothing past it.
	rows, run = rowsOf(ReadKind{Kind: "rows", Range: Range{Limit: 10}})
	if rows != 5 || len(run.Result.Rows) != 5 || run.Result.More {
		t.Fatalf("limit 10: %v rows counted, %d adopted, more %v; want 5, 5, false", rows, len(run.Result.Rows), run.Result.More)
	}
	// A count returns a number: no row counted.
	rows, run = rowsOf(ReadKind{Kind: "count"})
	if rows != 0 || run.Count != 5 {
		t.Fatalf("count: %v rows counted, count %d; want 0 and 5", rows, run.Count)
	}
}
