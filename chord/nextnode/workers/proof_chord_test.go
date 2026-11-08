package workers

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Proof of WorkerMain (main.ts workerMain) through the real process: the fleet forks the fixture worker (this test binary, whose
// entry is WorkerMain), the start message reaches the app's function, and the worker answers its load and goes idle.

func TestWorkerMainRunsTheForkedWorkerFromItsStartMessageToItsFirstServedCall(t *testing.T) {
	var _ func(func(WorkerStart, context.Context) (*WorkerResult, error), WorkerMainOptions) = WorkerMain
	f := newFleet(t, fleetOptions{})
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return refKind(notes) == host.RefStateLive })
	if label := labelOf(t, notes); label != "S1 v1" {
		t.Fatalf("a worker run by WorkerMain answered %q, want S1 v1", label)
	}
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerRunning })
	if starts := f.starts.all(); !reflect.DeepEqual(starts, []string{"session|S1"}) {
		t.Fatalf("the app's start function ran for %v, want session|S1 once", starts)
	}
}
