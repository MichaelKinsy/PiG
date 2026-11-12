package workers

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Pi's `close keeps the base's files until its workers exited: a closer's first lazy import still loads (Astra)`: a worker's closers
// run while the component closes, and what they need of the base is still there. A Go worker needs its program, the base's copy it was
// forked from (a closer that looked for it at its end finds it; on Windows a running program cannot be removed at all): the copy goes
// after every worker exited. The closer is the fixture's `ProgramOnClose`, the last to run, after one that takes 300 ms (the removal
// of the copy before the exit would have happened by then).
func TestTheWorkerBaseCloseKeepsTheBasesCopyUntilItsWorkersExitedACloserStillFindsItsProgram(t *testing.T) {
	file := filepath.Join(t.TempDir(), "closed")
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{ProgramOnClose: file, CloseMs: 300})
	f.get(fixtureNotes, "S1").Release()
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, file)); got != "present" {
		t.Fatalf("the closer found its program: %q, want present", got)
	}
	if errs := f.errors.all(); !reflect.DeepEqual(errs, []WorkersError(nil)) {
		t.Fatalf("errors = %+v, want none", errs)
	}
}

// Pi's closing re-check after `awaitWithContext(#built(), #closingContext)`: a build that ended and a close are both ready; the close wins.
func TestTheWorkerBaseAStartsWaitForTheBuildEndsClosedWhenTheBuildAndTheCloseAreBothDone(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	if err := buildError(f.workers.Build()); err != nil {
		t.Fatal(err)
	}
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		if _, open := f.workers.awaitBuild(); open {
			t.Fatalf("awaitBuild after Close reported open at try %d", i)
		}
	}
}
