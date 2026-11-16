package workers

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// pi: packages/chord/test/node/workers/workers.test.ts
// Ports `describe("a worker's incarnation")`: "is in the state before its first call is handled: a report its startup sends finds it,
// long before ready" (Pi main f57cd9484). The state entry's `incarnation` is the incarnation of the host at the worker's end of its
// link, told by the route's `opened` at the handshake (the first thing the host does when the link opens), so the worker's first
// report, which the host handles after, finds it in the state; a worker that failed has none, a retry's is another.
func TestAWorkersIncarnationIsInTheStateBeforeItsFirstCallIsHandledAReportItsStartupSendsFindsItLongBeforeReady(t *testing.T) {
	log := &reportLog{}
	var f *fleet
	// What a report finds of its own call's incarnation in the state at the instant the server handles it: `status known?`.
	seen := func(incarnation string) string {
		state := f.stateOf("S1")
		if state == nil {
			return "none unknown"
		}
		known := "unknown"
		if state.Incarnation == incarnation {
			known = "known"
		}
		return fmt.Sprintf("%s %s", state.Status, known)
	}
	f = newFleet(t, fleetOptions{facets: []host.HostFacet{reporting(log, reportOptions{seen: seen})}})
	f.behaviours.set("S1", workerBehaviour{ReportAtStart: "startup"})
	// What the state's watchers are told, as `status known?`.
	published := &collector[string]{}
	watch, err := f.workers.State().Watch(background)
	if err != nil {
		t.Fatal(err)
	}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		object, _ := call.Value.(*delta.JsonObject)
		if entry, ok := object.Value("session|S1").(*delta.JsonObject); ok {
			published.add(fmt.Sprintf("%s %t", entry.Value("status"), entry.Value("incarnation") != nil))
		}
		return nil
	})
	stopped := false
	defer func() {
		if !stopped {
			_, _ = watch.Stop()
		}
	}()
	reporter := f.get(fixtureReporter, "S1")
	defer reporter.Release()
	control := f.get(fixtureControl, "S1")
	defer control.Release()
	// The startup's call was answered: the worker went on to `ready`.
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerRunning })
	reportsCall(t, reporter, "report", "later")
	until(t, func() bool { return len(log.received.all()) == 2 })
	if got, want := log.received.all(), []string{"startup starting known", "later running known"}; !slices.Equal(got, want) {
		t.Fatalf("received = %q, want %q", got, want)
	}
	// Published at the handshake, not only with the next change of the worker's status.
	until(t, func() bool { return slices.Contains(published.all(), "starting true") })
	if _, err := watch.Stop(); err != nil {
		t.Fatal(err)
	}
	stopped = true
	first := f.stateOf("S1").Incarnation
	if first == "" {
		t.Fatal("a running worker's state has no incarnation")
	}
	// Another run of the worker is another incarnation.
	if _, err := control.Call("crash", 7.0, background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerFailed })
	if entry := f.entryOf("S1"); entry.Value("incarnation") != nil {
		t.Fatalf("a failed worker's entry has an incarnation: %v", entry.Value("incarnation"))
	}
	if err := f.workers.Retry(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerRunning })
	if second := f.stateOf("S1").Incarnation; second == "" || second == first {
		t.Fatalf("incarnation after the retry = %q, want another than %q", second, first)
	}
}
