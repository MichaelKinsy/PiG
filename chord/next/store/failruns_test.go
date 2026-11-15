package store

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/internal/storeseam"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// The test seam storeseam.FailRuns: what Pi's tests get by assigning `read.run` (test/host/tiling.test.ts, "a failed page ..."):
// the runs of one read in a commit's step fail on demand, the other reads' runs and the read's first load do not.

func TestFailRuns(t *testing.T) {
	store, f, table, bindings := readFixture(t)
	visible := NewQuery(f.Sessions).Where(func(s, _ ColumnRefs) Condition {
		return Exists(f.TeamSessions, Match{{"session", s["id"]}}, nil)
	})
	must(t, visible.Err())
	plan, err := NewReadPlan(visible, table, Projection{Reads: []string{"id"}}, ReadRows, bindings)
	must(t, err)
	newRead := func() *RegisteredRead {
		return NewRegisteredRead(plan, ReadKind{Kind: "rows", Range: Range{Limit: 1}}, &adoptedRuns{})
	}
	step := func(read *RegisteredRead) ReadRun {
		t.Helper()
		run, err := Read(store, func(executor sqlite.SqliteExecutor) (ReadRun, error) { return read.Run(executor, background), nil }, background)
		must(t, err)
		return run
	}
	broken := errors.New("page failure")

	t.Run("fails the runs of a step of that read only, until it is restored: the others' runs and its first load run for real", func(t *testing.T) {
		read, other := newRead(), newRead()
		if run := step(read); run.Kind != "rows" {
			t.Fatalf("before: run = %#v", run)
		}
		storeseam.FailRuns(read, func() error { return broken })
		for range 2 {
			if run := step(read); run.Kind != "failed" || run.Err != broken {
				t.Fatalf("failing: run = %#v", run)
			}
		}
		if run := step(other); run.Kind != "rows" {
			t.Fatalf("another read: run = %#v", run)
		}
		if run := fetched(t, store, read); run.Kind != "rows" {
			t.Fatalf("first load: run = %#v", run)
		}
		storeseam.FailRuns(read, nil)
		if run := step(read); run.Kind != "rows" {
			t.Fatalf("restored: run = %#v", run)
		}
	})

	t.Run("asks the function once per run, and a nil error lets the run be the real one: a read that fails once and heals", func(t *testing.T) {
		read := newRead()
		asked := 0
		storeseam.FailRuns(read, func() error {
			asked++
			if asked == 1 {
				storeseam.FailRuns(read, nil) // restores from inside: it fails once
				return broken
			}
			return nil
		})
		if run := step(read); run.Kind != "failed" {
			t.Fatalf("first: run = %#v", run)
		}
		if run := step(read); run.Kind != "rows" {
			t.Fatalf("second: run = %#v", run)
		}
		if asked != 1 {
			t.Fatalf("asked = %d, want 1", asked)
		}
		// A function that answers nil is asked at every run and the run is real.
		storeseam.FailRuns(read, func() error { asked++; return nil })
		step(read)
		step(read)
		if asked != 3 {
			t.Fatalf("asked = %d, want 3", asked)
		}
		if run := step(read); run.Kind != "rows" {
			t.Fatalf("answering nil: run = %#v", run)
		}
	})
}
