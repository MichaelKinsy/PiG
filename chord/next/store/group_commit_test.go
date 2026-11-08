package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/store/store.test.ts, "group commit" (Pi main 8889e623d, decision S18): the commits queued
// behind a running commit step are written in one backend transaction, each with its own step, seq, outbox commit and
// adoption in queue order, the metadata once; a refusal rolls the group back and runs its commits one by one.
//
// Go forms: Pi holds the next commit's step with `holdNextCommit`; here a gate holds the next backend transaction at its
// first statement (`controlledStore`, `arm`), and a commit's place in the queue is taken by `enlist` on the test's
// goroutine, as Pi's `store.commit(...)` call takes it, then waited for on a goroutine of its own. Go callbacks do not
// throw: an Adopt returns an error, and the Go form of what a callback throws is a panic, which stops the store and goes
// on in the caller that ran the turn (Pi rejects that commit's promise instead). Pi's three cases that assert a throw
// have no exact Go form and are drafted as designed out in the test mapping, for owner review: "a grouped commit whose
// admission throws" (Admit returns a bool; a panic out of it is the unknown outcome tested below) and the two
// "metrics adapter throws" ones, whose closest forms are the panicking metrics adapter cases below. Pi's int() key typing
// (d7704516e) is TypeScript typing: designed out.

// groupOutcome is what a commit settled with.
type groupOutcome = chordctx.Settled[int64]

// panickingMetrics is a telemetry context whose metrics adapter panics when a counter adds: Go's form of a metrics
// adapter that throws (its instruments return nothing, so a failure of one is a panic).
func panickingMetrics(thrown any) context.Context {
	return chordctx.WithTelemetry(background, panickingTelemetry{TelemetrySpan: telemetry.NoopTelemetryContext, thrown: thrown})
}

type panickingTelemetry struct {
	telemetry.TelemetrySpan
	thrown any
}

func (adapter panickingTelemetry) Metrics() telemetry.TelemetryMetrics {
	return panickingInstruments{TelemetryMetrics: telemetry.NoopTelemetryMetrics, thrown: adapter.thrown}
}

type panickingInstruments struct {
	telemetry.TelemetryMetrics
	thrown any
}

func (instruments panickingInstruments) Counter(string, telemetry.InstrumentOptions) telemetry.TelemetryCounter {
	return panickingCounter{thrown: instruments.thrown}
}

type panickingCounter struct{ thrown any }

func (counter panickingCounter) Add(float64, telemetry.SpanAttributes) { panic(counter.thrown) }

// startCommit enlists a commit of writes and waits for it on a goroutine: the commit has its place in the queue when it
// returns.
func startCommit(store *ChordStore, fence CommitFence, writes ...StagedWrite) <-chan groupOutcome {
	return startWait(store.enlist(writes, fence, background))
}

// startWait waits for an enlisted commit on a goroutine.
func startWait(pending *pendingCommit) <-chan groupOutcome {
	settled := make(chan groupOutcome, 1)
	go func() {
		seq, err := pending.Wait()
		settled <- groupOutcome{Value: seq, Err: err}
	}()
	return settled
}

// idsText prints ids as Pi's template does a number array.
func idsText(ids []int64) string {
	text := make([]string, len(ids))
	for index, id := range ids {
		text[index] = fmt.Sprint(id)
	}
	return strings.Join(text, ",")
}

// traced is a fence recording `name`'s admission and adoption into events (its callers run one after another on the
// goroutines of the commit queue, which order them).
func traced(events *[]string, name string, admit bool) CommitFence {
	return CommitFence{
		Admit: func() bool {
			*events = append(*events, "admit "+name)
			return admit
		},
		Adopt: func(seq int64, ids []int64) error {
			*events = append(*events, fmt.Sprintf("adopt %s %d %s", name, seq, idsText(ids)))
			return nil
		},
	}
}

// created is the creation of the document at key, of the address of storeAddress.
func created(key string) StagedWrite {
	address := storeAddress
	address.Key = &key
	return CreateDocument(address, storeBase, "{}", nil)
}

// outcomeOf is the commit's settled seq and error.
func outcomeOf(t *testing.T, settled <-chan groupOutcome) (int64, error) {
	t.Helper()
	return settledValue(t, settled)
}

// seqOf is the seq of a commit that must have succeeded.
func seqOf(t *testing.T, settled <-chan groupOutcome) int64 {
	t.Helper()
	seq, err := outcomeOf(t, settled)
	noError(t, err)
	return seq
}

// failureOf is the error of a commit that must have failed.
func failureOf(t *testing.T, settled <-chan groupOutcome) error {
	t.Helper()
	seq, err := outcomeOf(t, settled)
	if err == nil {
		t.Fatalf("the commit succeeded at seq %d, want a failure", seq)
	}
	return err
}

func sameEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

// slowSecondApplication is a write applied twice (a group's attempt, then its commit's own run): its second application
// signals solo and waits for gate.
func slowSecondApplication(solo chan struct{}, gate <-chan struct{}) StagedWrite {
	var applications atomic.Int32
	return stagedWrite{order: OrderWrite, apply: func(*Step) ([]StoreWrite, error) {
		if applications.Add(1) == 2 {
			close(solo)
			<-gate
		}
		return nil, nil
	}}
}

// closeBegun starts closing the store and returns at once (its context is already aborted: the close goes on, as Pi's
// un-awaited `store.close(context)`): the store is closed from here on.
func closeBegun(store *ChordStore) {
	ctx, cancel := context.WithCancel(background)
	cancel()
	_ = store.Close(ctx)
}

func TestGroupCommit(t *testing.T) {
	t.Run("writes the commits queued behind a running step in one transaction, one seq, outbox commit and adoption each", func(t *testing.T) {
		ctl := controlledStore(t, true)
		var events []string
		gate := ctl.arm()
		running := startCommit(ctl.store, traced(&events, "a", true), created("a"))
		<-gate.Entered()
		var queued []<-chan groupOutcome
		for _, key := range []string{"b", "c", "d"} {
			queued = append(queued, startCommit(ctl.store, traced(&events, key, true), created(key)))
		}
		// Admitted at the group's turn, not when queued.
		sameEvents(t, events, "admit a")
		gate.Release()
		for index, settled := range append([]<-chan groupOutcome{running}, queued...) {
			if seq := seqOf(t, settled); seq != int64(index+1) {
				t.Fatalf("commit %d: seq = %d, want %d", index, seq, index+1)
			}
		}
		sameEvents(t, events, "admit a", "adopt a 1 2", "admit b", "admit c", "admit d", "adopt b 2 3", "adopt c 3 4", "adopt d 4 5")
		if got := ctl.transactions(); got != 2 {
			t.Fatalf("transactions = %d, want 2", got)
		}
		if ctl.store.LastSeq() != 4 || ctl.store.NextID() != 6 {
			t.Fatalf("[lastSeq, nextId] = [%d, %d], want [4, 6]", ctl.store.LastSeq(), ctl.store.NextID())
		}
		commits, err := ctl.store.Outbox().Read(0, 10, background)
		noError(t, err)
		var got [][2]int64
		for _, commit := range commits {
			got = append(got, [2]int64{commit.Seq, commit.NextID})
		}
		if want := [][2]int64{{1, 3}, {2, 4}, {3, 5}, {4, 6}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("outbox [seq, nextId] = %v, want %v", got, want)
		}
		meta, err := Read(ctl.store, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
			return executor.Get("SELECT last_seq, next_id FROM chord_meta")
		}, background)
		noError(t, err)
		if fmt.Sprint(meta["last_seq"], meta["next_id"]) != "4 6" {
			t.Fatalf("meta = %v, want last_seq 4, next_id 6", meta)
		}
	})

	t.Run("writes the metadata once per group: three statements per delta with its outbox commit, plus one (cost guard)", func(t *testing.T) {
		ctl := controlledStore(t, true)
		id := storeCreated(t, ctl.store)
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("held"))
		<-gate.Entered()
		telemetry := metered(t)
		var deltas []<-chan groupOutcome
		for n := 1; n <= 3; n++ {
			ops := []delta.Op{{"s", []any{"n"}, float64(n)}}
			content := DocumentContent{Kind: "delta", Version: 1, Ops: ops}
			_, fence := recording()
			pending := ctl.store.enlist([]StagedWrite{ChangeDocument(id, int64(n), content, fmt.Sprintf(`[["s",["n"],%d]]`, n))}, fence, telemetry.Context)
			deltas = append(deltas, startWait(pending))
		}
		gate.Release()
		seqOf(t, running)
		for _, settled := range deltas {
			seqOf(t, settled)
		}
		if want := map[string]float64{"chord.store.commits": 3, "chord.store.statements": 3*3 + 1}; !reflect.DeepEqual(telemetry.Counts(), want) {
			t.Fatalf("counts = %v, want %v", telemetry.Counts(), want)
		}
	})

	t.Run("rolls a group back when one commit's writes throw, then runs each alone: only that one is refused", func(t *testing.T) {
		ctl := controlledStore(t)
		var told []error
		noError(t, ctl.store.Attach(Attachment{Failed: func(err error) { told = append(told, err) }, Batch: newBatch()}))
		var events []string
		gate := ctl.arm()
		running := startCommit(ctl.store, traced(&events, "a", true), created("a"))
		<-gate.Entered()
		before := startCommit(ctl.store, traced(&events, "b", true), created("b"))
		broken := startCommit(ctl.store, traced(&events, "x", true), created("x"), brokenWrite)
		after := startCommit(ctl.store, traced(&events, "c", true), created("c"))
		gate.Release()
		if seq := seqOf(t, running); seq != 1 {
			t.Fatalf("running = %d, want 1", seq)
		}
		if seq := seqOf(t, before); seq != 2 {
			t.Fatalf("before = %d, want 2", seq)
		}
		if err := failureOf(t, broken); !strings.Contains(err.Error(), "no such table") {
			t.Fatalf("broken = %v, want no such table", err)
		}
		if seq := seqOf(t, after); seq != 3 {
			t.Fatalf("after = %d, want 3", seq)
		}
		// Admitted once, at the group's entry; the solo runs do not admit again. The rolled back ids stay unused (D3).
		sameEvents(t, events, "admit a", "adopt a 1 2", "admit b", "admit x", "admit c", "adopt b 2 5", "adopt c 3 7")
		// The held step, the rolled back group, then b, x (refused) and c alone.
		if got := ctl.transactions(); got != 5 || len(told) != 0 || ctl.store.LastSeq() != 3 {
			t.Fatalf("[transactions, told, lastSeq] = [%d, %v, %d], want [5, [], 3]", got, told, ctl.store.LastSeq())
		}
		rows, err := Read(ctl.store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
			return executor.All("SELECT key FROM chord_documents ORDER BY id")
		}, background)
		noError(t, err)
		var keys []string
		for _, row := range rows {
			key, _ := row["key"].(string)
			keys = append(keys, key)
		}
		if !reflect.DeepEqual(keys, []string{"a", "b", "c"}) {
			t.Fatalf("keys = %q, want a, b, c", keys)
		}
	})

	t.Run("rejects a commit its fence refuses at the group's entry released, writing nothing; the others commit", func(t *testing.T) {
		ctl := controlledStore(t)
		var events []string
		gate := ctl.arm()
		running := startCommit(ctl.store, traced(&events, "a", true), created("a"))
		<-gate.Entered()
		first := startCommit(ctl.store, traced(&events, "b", true), created("b"))
		cut := startCommit(ctl.store, traced(&events, "x", false), created("x"))
		last := startCommit(ctl.store, traced(&events, "c", true), created("c"))
		gate.Release()
		for index, settled := range []<-chan groupOutcome{running, first, last} {
			if seq := seqOf(t, settled); seq != int64(index+1) {
				t.Fatalf("commit %d: seq = %d, want %d", index, seq, index+1)
			}
		}
		_ = rejected(t, failureOf(t, cut), data.CodeReleased, data.ReasonDiscarded)
		sameEvents(t, events[2:], "admit b", "admit x", "admit c", "adopt b 2 3", "adopt c 3 4")
	})

	t.Run("keeps queue order: a series or an empty commit queued after a group ends it", func(t *testing.T) {
		ctl := controlledStore(t)
		var events []string
		gate := ctl.arm()
		running := startCommit(ctl.store, traced(&events, "a", true), created("a"))
		<-gate.Entered()
		grouped := startCommit(ctl.store, traced(&events, "b", true), created("b"))
		series := startQueued(t, ctl.store, func() (SeriesResult, error) {
			return ctl.store.CommitSeries([]SeriesCommit{{Check: func(*Step) error { return nil }, Writes: []StagedWrite{created("s")}}}, func(_ int, seq int64, _ []int64) error {
				events = append(events, fmt.Sprintf("receive s %d", seq))
				return nil
			}, background)
		})
		afterSeries := startCommit(ctl.store, traced(&events, "c", true), created("c"))
		empty := startCommit(ctl.store, traced(&events, "empty", true))
		afterEmpty := startCommit(ctl.store, traced(&events, "d", true), created("d"))
		gate.Release()
		for _, settled := range []<-chan groupOutcome{running, grouped, afterSeries, empty, afterEmpty} {
			seqOf(t, settled)
		}
		if _, err := settledValue(t, series); err != nil {
			t.Fatalf("series: %v", err)
		}
		sameEvents(t, events,
			"admit a", "adopt a 1 2", "admit b", "adopt b 2 3", "receive s 3",
			"admit c", "adopt c 4 5", "admit empty", "adopt empty 4 ", "admit d", "adopt d 5 6")
	})

	t.Run("runs a commit issued while a group runs at the next turn: a running group takes no more commits", func(t *testing.T) {
		ctl := controlledStore(t)
		var events []string
		gate := ctl.arm()
		running := startCommit(ctl.store, traced(&events, "a", true), created("a"))
		<-gate.Entered()
		var late *pendingCommit
		issuing := CommitFence{
			Admit: func() bool { return true },
			Adopt: func(seq int64, _ []int64) error {
				events = append(events, fmt.Sprintf("adopt b %d", seq))
				late = ctl.store.enlist([]StagedWrite{created("late")}, traced(&events, "late", true), background)
				return nil
			},
		}
		grouped := []<-chan groupOutcome{
			startCommit(ctl.store, issuing, created("b")),
			startCommit(ctl.store, traced(&events, "c", true), created("c")),
		}
		gate.Release()
		seqOf(t, running)
		for _, settled := range grouped {
			seqOf(t, settled)
		}
		if seq := seqOf(t, startWait(late)); seq != 4 {
			t.Fatalf("late = %d, want 4", seq)
		}
		sameEvents(t, events, "admit a", "adopt a 1 2", "admit c", "adopt b 2", "adopt c 3 4", "admit late", "adopt late 4 5")
	})

	t.Run("lets a later commit of a group see the table records an earlier one changed", func(t *testing.T) {
		ctl := controlledStore(t)
		edits, err := DefineTable("edits", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Str()), Col("at", Int())}, Key: []string{"id"}})
		noError(t, err)
		const tableName = "app.store:edits"
		table := StageTable(&TableDefinitionWrite{
			Table: tableName, Version: edits.Version, State: StateReady, Columns: edits.Record, Key: edits.Key, Fingerprint: edits.Fingerprint,
		})
		var seen []int
		sees := stagedWrite{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
			version := 0
			if record := step.Table(tableName); record != nil {
				version = record.Version
			}
			seen = append(seen, version)
			return nil, nil
		}}
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("a"))
		<-gate.Entered()
		commits := []<-chan groupOutcome{startCommit(ctl.store, fence, table), startCommit(ctl.store, fence, sees)}
		gate.Release()
		seqOf(t, running)
		for _, settled := range commits {
			seqOf(t, settled)
		}
		if !reflect.DeepEqual(seen, []int{1}) {
			t.Fatalf("the later commit saw table versions %v, want [1]", seen)
		}
		if record := ctl.store.Table(tableName); record == nil || record.Version != 1 {
			t.Fatalf("table record = %+v, want version 1", record)
		}
	})

	for _, c := range []struct {
		at    string
		write StagedWrite
	}{{failBegin, nil}, {failCommit, nil}, {failRollback, brokenWrite}} {
		t.Run(fmt.Sprintf("a failed %s of a group is an unknown outcome: every commit rejects storage_failed, the host told once", c.at), func(t *testing.T) {
			ctl := controlledStore(t)
			var told []error
			noError(t, ctl.store.Attach(Attachment{Failed: func(err error) { told = append(told, err) }, Batch: newBatch()}))
			gate := ctl.arm()
			// Armed after the held step's COMMIT, before the group's turn: the group's statement fails.
			arming := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error {
				ctl.fail(c.at)
				return nil
			}}
			running := startCommit(ctl.store, arming, created("a"))
			<-gate.Entered()
			_, fence := recording()
			second := created("c")
			if c.write != nil {
				second = c.write
			}
			group := []<-chan groupOutcome{startCommit(ctl.store, fence, created("b")), startCommit(ctl.store, fence, second)}
			gate.Release()
			if seq := seqOf(t, running); seq != 1 {
				t.Fatalf("running = %d, want 1", seq)
			}
			for _, settled := range group {
				_ = rejected(t, failureOf(t, settled), data.CodeUnavailable, data.ReasonStorageFailed)
			}
			if len(told) != 1 || ctl.store.LastSeq() != 1 {
				t.Fatalf("[told, lastSeq] = [%d, %d], want [1, 1]", len(told), ctl.store.LastSeq())
			}
		})
	}

	t.Run("stops the store when an adoption in a group fails: it and the ones after it reject, the ones before stand", func(t *testing.T) {
		ctl := controlledStore(t)
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("a"))
		<-gate.Entered()
		throwing := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return errors.New("adopt threw") }}
		first := startCommit(ctl.store, fence, created("b"))
		second := startCommit(ctl.store, throwing, created("c"))
		third := startCommit(ctl.store, fence, created("d"))
		gate.Release()
		if seqOf(t, running) != 1 || seqOf(t, first) != 2 {
			t.Fatal("the commits before the failing adoption did not stand")
		}
		_ = rejected(t, failureOf(t, second), data.CodeUnavailable, data.ReasonStorageFailed)
		_ = rejected(t, failureOf(t, third), data.CodeUnavailable, data.ReasonStorageFailed)
		_, err := Read(ctl.store, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
		_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
	})

	t.Run("finishes the admitted commits of a group when the store closes during their solo runs; queued ones are released", func(t *testing.T) {
		ctl := controlledStore(t)
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("a"))
		<-gate.Entered()
		// Its second application (the solo run after the group's rollback) waits for the close to begin.
		solo, release := make(chan struct{}), make(chan struct{})
		admitted := startCommit(ctl.store, fence, created("b"), slowSecondApplication(solo, release))
		broken := startCommit(ctl.store, fence, brokenWrite)
		later := startCommit(ctl.store, fence, created("c"))
		gate.Release()
		<-solo
		queued := startCommit(ctl.store, fence, created("q"))
		closeBegun(ctl.store)
		close(release)
		if seqOf(t, running) != 1 || seqOf(t, admitted) != 2 {
			t.Fatal("the running and admitted commits did not stand")
		}
		if err := failureOf(t, broken); !strings.Contains(err.Error(), "no such table") {
			t.Fatalf("broken = %v, want no such table", err)
		}
		if seq := seqOf(t, later); seq != 3 {
			t.Fatalf("later = %d, want 3", seq)
		}
		_ = rejected(t, failureOf(t, queued), data.CodeReleased, "")
		noError(t, ctl.store.Close(background))
	})

	t.Run("refuses the admitted commits after a backend failure while the store closes", func(t *testing.T) {
		ctl := controlledStore(t)
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("a"))
		<-gate.Entered()
		// Its second application is its solo run after the group's rollback: the close and the failure land there.
		solo, release := make(chan struct{}), make(chan struct{})
		first := startCommit(ctl.store, fence, created("b"), slowSecondApplication(solo, release))
		broken := startCommit(ctl.store, fence, brokenWrite)
		last := startCommit(ctl.store, fence, created("c"))
		gate.Release()
		if seq := seqOf(t, running); seq != 1 {
			t.Fatalf("running = %d, want 1", seq)
		}
		<-solo
		ctl.fail(failCommit)
		closeBegun(ctl.store)
		close(release)
		for _, settled := range []<-chan groupOutcome{first, broken, last} {
			_ = rejected(t, failureOf(t, settled), data.CodeUnavailable, data.ReasonStorageFailed)
		}
		noError(t, ctl.store.Close(background))
		if ctl.store.LastSeq() != 1 {
			t.Fatalf("lastSeq = %d, want 1", ctl.store.LastSeq())
		}
	})

	t.Run("runs a group that would pass the end of the seq range alone: the commits that fit are stored", func(t *testing.T) {
		parts := exported(t, memoryStore(t), background)
		manifest := parts[0].(*ManifestPart)
		values := []any{&ManifestPart{Format: manifest.Format, Schema: manifest.Schema, LastSeq: MaxSafeInteger - 2, NextID: manifest.NextID}}
		for _, part := range parts[1:] {
			values = append(values, part)
		}
		db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
		noError(t, err)
		imported, err := ImportStore(SqliteBackend(db), SlicePartSource(values), background, StoreOptions{})
		noError(t, err)
		store := imported.(*ChordStore)
		t.Cleanup(func() { _ = store.Close(background) })
		held, entered, release := holdingWrite()
		_, fence := recording()
		running := startCommit(store, fence, created("a"), held)
		<-entered
		fits := startCommit(store, fence, created("b"))
		past := startCommit(store, fence, created("c"))
		release()
		if seq := seqOf(t, running); seq != MaxSafeInteger-1 {
			t.Fatalf("running = %d, want Number.MAX_SAFE_INTEGER - 1", seq)
		}
		if seq := seqOf(t, fits); seq != MaxSafeInteger {
			t.Fatalf("fits = %d, want Number.MAX_SAFE_INTEGER", seq)
		}
		_ = rejected(t, failureOf(t, past), data.CodeUnavailable, data.ReasonStorageFailed)
	})

	t.Run("refuses the admitted commits after a maintenance write fails while the store closes", func(t *testing.T) {
		ctl := controlledStore(t)
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("a"))
		<-gate.Entered()
		solo, release := make(chan struct{}), make(chan struct{})
		first := startCommit(ctl.store, fence, created("b"), slowSecondApplication(solo, release))
		broken := startCommit(ctl.store, fence, brokenWrite)
		last := startCommit(ctl.store, fence, created("c"))
		gate.Release()
		if seq := seqOf(t, running); seq != 1 {
			t.Fatalf("running = %d, want 1", seq)
		}
		<-solo
		// Queued behind the solo run's transaction; its COMMIT fails once the store is closing.
		called := ctl.db.transactions()
		maintenance := make(chan error, 1)
		go func() {
			maintenance <- ctl.store.Maintain(func(executor sqlite.SqliteExecutor) error {
				if err := executor.Run("SELECT 1"); err != nil {
					return err
				}
				ctl.fail(failCommit)
				return nil
			})
		}()
		ctl.db.waitEntered(called + 1)
		closeBegun(ctl.store)
		close(release)
		if seq := seqOf(t, first); seq != 2 {
			t.Fatalf("first = %d, want 2", seq)
		}
		_ = rejected(t, <-maintenance, data.CodeReleased, "")
		failureOf(t, broken)
		_ = rejected(t, failureOf(t, last), data.CodeUnavailable, data.ReasonStorageFailed)
		noError(t, ctl.store.Close(background))
		if ctl.store.LastSeq() != 2 {
			t.Fatalf("lastSeq = %d, want 2", ctl.store.LastSeq())
		}
	})

	t.Run("a panic out of a group's turn after COMMIT rejects its commits storage_failed, stops the store and goes on in the leader's caller", func(t *testing.T) {
		ctl := controlledStore(t)
		thrown := errors.New("listener threw")
		noError(t, ctl.store.Attach(Attachment{Failed: func(error) { panic(thrown) }, Batch: newBatch()}))
		throwing := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { return errors.New("adopt threw") }}
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("a"))
		<-gate.Entered()
		leader := ctl.store.enlist([]StagedWrite{created("b")}, throwing, background)
		panicked := make(chan any, 1)
		go func() {
			defer func() { panicked <- recover() }()
			_, _ = leader.Wait()
		}()
		follower := startCommit(ctl.store, fence, created("c"))
		gate.Release()
		seqOf(t, running)
		if got := <-panicked; !panickedWith(got, thrown) {
			t.Fatalf("the leader's caller recovered %v, want the listener's panic", got)
		}
		_ = rejected(t, failureOf(t, follower), data.CodeUnavailable, data.ReasonStorageFailed)
		// Memory took the whole group (it is durable): no seq repeats.
		if ctl.store.LastSeq() != 3 {
			t.Fatalf("lastSeq = %d, want 3", ctl.store.LastSeq())
		}
		_, err := ctl.store.Commit([]StagedWrite{created("d")}, fence, background)
		_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
	})

	t.Run("advances memory commit by commit while a group adopts, as lone steps do", func(t *testing.T) {
		ctl := controlledStore(t)
		var seen [][3]int64
		fence := CommitFence{Admit: func() bool { return true }, Adopt: func(seq int64, _ []int64) error {
			seen = append(seen, [3]int64{seq, ctl.store.LastSeq(), ctl.store.NextID()})
			return nil
		}}
		gate := ctl.arm()
		_, held := recording()
		running := startCommit(ctl.store, held, created("a"))
		<-gate.Entered()
		group := []<-chan groupOutcome{startCommit(ctl.store, fence, created("b")), startCommit(ctl.store, fence, created("c"))}
		gate.Release()
		seqOf(t, running)
		for _, settled := range group {
			seqOf(t, settled)
		}
		if want := [][3]int64{{2, 2, 4}, {3, 3, 5}}; !reflect.DeepEqual(seen, want) {
			t.Fatalf("seen [seq, lastSeq, nextId] = %v, want %v", seen, want)
		}
	})

	t.Run("a panic out of a lone commit's adoption stops the store too: it is durable, memory stands at it", func(t *testing.T) {
		store := memoryStore(t)
		panicking := CommitFence{Admit: func() bool { return true }, Adopt: func(int64, []int64) error { panic("adopt panicked") }}
		pending := store.enlist([]StagedWrite{created("a")}, panicking, background)
		panicked := make(chan any, 1)
		go func() {
			defer func() { panicked <- recover() }()
			_, _ = pending.Wait()
		}()
		if got := <-panicked; got != "adopt panicked" {
			t.Fatalf("recovered %v, want the adoption's panic", got)
		}
		if store.LastSeq() != 1 {
			t.Fatalf("lastSeq = %d, want 1", store.LastSeq())
		}
		_, err := Read(store, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
		_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
	})

	t.Run("stops the store when a metrics adapter panics after a group's COMMIT: memory takes the whole group, no seq repeats", func(t *testing.T) {
		ctl := controlledStore(t)
		thrown := errors.New("metrics threw")
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("a"))
		<-gate.Entered()
		leader := ctl.store.enlist([]StagedWrite{created("b")}, fence, panickingMetrics(thrown))
		panicked := make(chan any, 1)
		go func() {
			defer func() { panicked <- recover() }()
			_, _ = leader.Wait()
		}()
		follower := startCommit(ctl.store, fence, created("c"))
		gate.Release()
		if seqOf(t, running) != 1 {
			t.Fatal("the running commit did not take seq 1")
		}
		if got := <-panicked; !panickedWith(got, thrown) {
			t.Fatalf("the leader's caller recovered %v, want the metrics adapter's panic", got)
		}
		_ = rejected(t, failureOf(t, follower), data.CodeUnavailable, data.ReasonStorageFailed)
		if ctl.store.LastSeq() != 3 {
			t.Fatalf("lastSeq = %d, want 3", ctl.store.LastSeq())
		}
		_, err := ctl.store.Commit([]StagedWrite{created("d")}, fence, background)
		_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
	})

	t.Run("stops the store when a metrics adapter panics after a lone commit's COMMIT too", func(t *testing.T) {
		store := memoryStore(t)
		thrown := errors.New("metrics threw")
		adopted, fence := recording()
		pending := store.enlist([]StagedWrite{created("a")}, fence, panickingMetrics(thrown))
		panicked := make(chan any, 1)
		go func() {
			defer func() { panicked <- recover() }()
			_, _ = pending.Wait()
		}()
		if got := <-panicked; !panickedWith(got, thrown) {
			t.Fatalf("recovered %v, want the metrics adapter's panic", got)
		}
		if len(*adopted) != 0 || store.LastSeq() != 1 {
			t.Fatalf("adopted %v, lastSeq %d, want none adopted and lastSeq 1", *adopted, store.LastSeq())
		}
		_, err := Read(store, func(sqlite.SqliteExecutor) (int, error) { return 1, nil }, background)
		_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
	})

	t.Run("a panic out of a fence's Admit at a group's entry stops the store: nothing of the group is written, the panic goes on in the leader's caller", func(t *testing.T) {
		ctl := controlledStore(t)
		thrown := errors.New("admit threw")
		throwing := CommitFence{Admit: func() bool { panic(thrown) }, Adopt: func(int64, []int64) error { return nil }}
		gate := ctl.arm()
		_, fence := recording()
		running := startCommit(ctl.store, fence, created("a"))
		<-gate.Entered()
		leader := ctl.store.enlist([]StagedWrite{created("x")}, throwing, background)
		panicked := make(chan any, 1)
		go func() {
			defer func() { panicked <- recover() }()
			_, _ = leader.Wait()
		}()
		follower := startCommit(ctl.store, fence, created("b"))
		gate.Release()
		if seqOf(t, running) != 1 {
			t.Fatal("the running commit did not take seq 1")
		}
		if got := <-panicked; !panickedWith(got, thrown) {
			t.Fatalf("the leader's caller recovered %v, want Admit's panic", got)
		}
		_ = rejected(t, failureOf(t, follower), data.CodeUnavailable, data.ReasonStorageFailed)
		if ctl.store.LastSeq() != 1 {
			t.Fatalf("lastSeq = %d, want 1: the group wrote nothing", ctl.store.LastSeq())
		}
		_, err := ctl.store.Commit([]StagedWrite{created("c")}, fence, background)
		_ = rejected(t, err, data.CodeUnavailable, data.ReasonStorageFailed)
	})
}

// panickedWith reports whether a recovered value is want.
func panickedWith(recovered any, want error) bool {
	err, ok := recovered.(error)
	return ok && errors.Is(err, want)
}
