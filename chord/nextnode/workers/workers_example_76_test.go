package workers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// pi: packages/chord/test/examples/76-worker-reports/main.ts (Pi main f57cd9484)
// Ports example 76 (main.ts, contract.ts, worker.ts), "Reports from a worker that exits": a worker reports to the server and
// exits in the same turn; its link is closed before its exit is shown in the state, so the report being handled is aborted and
// its transaction does not commit, and what the server keeps across runs it tags with `ctx.incarnation` and compares with the
// incarnation in the workers' state. The twin runs the example's three steps and asserts the lines Pi prints, token by token
// (captured from Pi f57cd9484, `node --conditions=source --experimental-strip-types test/examples/76-worker-reports/main.ts` in packages/chord, 3 of 3 runs identical, including the two `   reported:` lines of the exit and the failure it records).
//
// Forced differences: the example's 50 ms `sleep` in the server's handler is a gate released when the exit is published (the
// component's onError for the exit, which is Pi's `   reported: worker_exited` line), so the handler's transaction runs after the
// publication, as in Pi, with no time in it; the example's `Last` state (a map of session to `{ text, incarnation }`) is the
// fixture's journal of lines, each `<text> <incarnation>`, whose last line is the session's last report (the example has one
// session); the fixture's `Reporter` is the example's `Probe`; the `0.2 s` wait after the exit is the report's outcome.

// lastReportsFacet is the example's facet: `Reports` into a journal of `<text> <incarnation>` lines, the transaction under the
// report's ctx, after `gate`, if one is set. outcomes gets each report's text once its transaction ended: `committed` or the code.
func lastReportsFacet(gate *atomic.Pointer[chan struct{}], outcomes *collector[string]) host.HostFacet {
	return host.HostFacet{Name: "@example/reports", Facet: host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		if err := h.Serve(fixtureJournal, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			document := self.State(fixtureLines).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"lines": self.Derive([]any{document}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("lines"), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"append": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := document.Change(ctx, func(draft *delta.Object) (any, error) {
							_, err := draft.Array("lines").Push(args)
							return nil, err
						})
						return nil, err
					},
				},
			}}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(fixtureReports, func(_ *delta.JsonObject, _ data.Principal, self host.Self) (contracts.Implementation, error) {
			journal := self.Get(fixtureJournal, delta.NewJsonObject(0))
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				// No check that the worker still runs: a report of a worker that exited never gets this far, or is aborted
				// before its transaction (under `ctx`) commits.
				"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					text, _ := args.(string)
					if wait := gate.Load(); wait != nil {
						// A wait for a gate, which the call's abort does not end.
						chordctx.Suspend(ctx)
						<-*wait
					}
					outcome := "committed"
					if _, err := journal.Call("append", text+" "+ctx.(host.Ctx).Incarnation(), ctx); err != nil {
						outcome = "threw"
						if chordErr, ok := data.IsChordError(err); ok {
							outcome = string(chordErr.Code)
						}
					}
					outcomes.add(text + " " + outcome)
					return nil, nil
				},
			}}}, nil
		}, host.ServeOptions{})
	})}
}

func TestExample76WorkerReports(t *testing.T) {
	var gate atomic.Pointer[chan struct{}]
	published := make(chan struct{})
	var once atomic.Bool
	printed := &collector[string]{}
	releaseGate := func() {
		if !once.Swap(true) {
			close(published)
		}
	}
	outcomes := &collector[string]{}
	f := newFleet(t, fleetOptions{
		facets: []host.HostFacet{lastReportsFacet(&gate, outcomes)},
		onError: func(err WorkersError) {
			printed.add("   reported: " + errorKind(err))
			if _, exited := err.(WorkerExitedError); exited {
				releaseGate()
			}
		},
	})
	// A test that fails before the exit was published must not leave a report waiting for it.
	t.Cleanup(releaseGate)
	// The channel outlives the worker: nothing but the component closes its link at the exit (as in case 1 of the exit tests).
	f.behaviours.set("S1", workerBehaviour{HoldsChannel: true})
	journal := f.journalOf()
	defer journal.Release()
	running := func() *WorkerState { return f.stateOf("S1") }
	// lastReport is the session's last report, `text incarnation`: "" and "" when none.
	lastReport := func() (string, string) {
		lines := linesOf(t, journal)
		if len(lines) == 0 {
			return "", ""
		}
		text, incarnation, _ := strings.Cut(lines[len(lines)-1], " ")
		return text, incarnation
	}
	// current is whether the session's last report is of the worker that runs now.
	current := func() bool {
		state := running()
		_, incarnation := lastReport()
		return incarnation != "" && state != nil && state.Status == WorkerRunning && state.Incarnation == incarnation
	}
	print := func(format string, args ...any) { printed.add(fmt.Sprintf(format, args...)) }

	// 1. A session's worker runs and reports: the server keeps the report with the run that sent it.
	probe := f.get(fixtureReporter, "S1")
	defer probe.Release()
	reportsCall(t, probe, "report", "indexing")
	until(t, func() bool { text, _ := lastReport(); return text == "indexing" })
	text, _ := lastReport()
	print("1. last report: %q, of the running worker: %t", text, current())

	// 2. It reports again and exits in the same turn. The server was still handling the report: the worker's link is closed
	//    first, the report's transaction is refused, and only then the state says the worker is gone.
	gate.Store(&published)
	go func() { _, _ = probe.Call("reportAndExit", "done", background) }()
	until(t, func() bool { state := running(); return state != nil && state.Status == WorkerFailed })
	// The exit is published: the report that was waiting ended, by its transaction refused.
	until(t, func() bool { return slices.Contains(outcomes.all(), "done unavailable") })
	until(t, func() bool { return slices.Contains(printed.all(), "   reported: worker_failed") })
	text, _ = lastReport()
	print("2. the worker exited; last report still: %q", text)

	// 3. Retry: another run of the worker is another incarnation, so the report kept from the earlier run is not its.
	if err := f.workers.Retry(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { state := running(); return state != nil && state.Status == WorkerRunning })
	print("3. a new worker runs; the kept report is of the running worker: %t", current())

	want := []string{
		`1. last report: "indexing", of the running worker: true`,
		"   reported: worker_exited",
		"   reported: worker_failed",
		`2. the worker exited; last report still: "indexing"`,
		`3. a new worker runs; the kept report is of the running worker: false`,
	}
	if got := printed.all(); !slices.Equal(got, want) {
		t.Fatalf("printed = %q, want %q", got, want)
	}
}
