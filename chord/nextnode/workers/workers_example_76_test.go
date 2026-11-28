package workers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
// exits in the same turn; its link is closed before its exit is shown in the state, so a report still on its way is dropped and
// one being handled is aborted (its transaction does not commit), and what the server keeps across runs it tags with
// `ctx.incarnation` and compares with the incarnation in the workers' state. The twin runs the example's three steps and asserts
// the lines Pi prints, token by token (captured from Pi f57cd9484,
// `node --conditions=source --experimental-strip-types test/examples/76-worker-reports/main.ts` in packages/chord, 3 of 3 runs
// identical, including the two `   reported:` lines of the exit and the failure it records).
//
// Forced differences: the example's 50 ms `sleep` in the server's handler is a gate released when the exit is published (the
// component's onError for the exit, which is Pi's `   reported: worker_exited` line), so a handler that received the report runs
// its transaction after the publication, as in Pi, with no time in it; the example's `Last` state (a map of session to
// `{ text, incarnation }`) is the fixture's journal of lines, each `<text> <incarnation>`, whose last line is the session's last
// report (the example has one session); the fixture's `Reporter` is the example's `Probe`; the `0.2 s` wait after the exit is
// every received report settling (if `"done"` reached the server before the link closed, it must end `"done unavailable"`, as in
// Pi main.ts:52 and the sibling exit test).

// lastReportsFacet is the example's facet: `Reports` into a journal of `<text> <incarnation>` lines, the transaction under the
// report's ctx, after `gate`, if one is set. log.received records each report that enters the handler and log.outcomes records
// its text once its transaction ended: `committed` or the code.
func lastReportsFacet(gate *atomic.Pointer[chan struct{}], log *reportLog) host.HostFacet {
	return host.HostFacet{Name: "@example/reports", Facet: host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		if err := h.Serve(fixtureJournal, func(_ *delta.JsonObject, self host.Self) (contracts.MembersImplementation, error) {
			document := self.State(fixtureLines).(*documents.Singleton)
			return contracts.MembersImplementation{
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
			}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(fixtureReports, func(_ *delta.JsonObject, _ data.Principal, self host.Self) (contracts.MembersImplementation, error) {
			journal := self.Get(fixtureJournal, delta.NewJsonObject(0))
			return contracts.MembersImplementation{Methods: map[string]contracts.MethodFunction{
				// No check that the worker still runs: a report of a worker that exited never gets this far, or is aborted
				// before its transaction (under `ctx`) commits.
				"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					text, _ := args.(string)
					log.received.add(text)
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
					log.outcomes.add(text + " " + outcome)
					return nil, nil
				},
			}}, nil
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
	log := &reportLog{}
	f := newFleet(t, fleetOptions{
		facets: []host.HostFacet{lastReportsFacet(&gate, log)},
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
	waitUntil := func(what string, ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				t.Fatalf("until (%s): timed out: state=%+v received=%q outcomes=%q printed=%q journal=%q",
					what, running(), log.received.all(), log.outcomes.all(), printed.all(), linesOf(t, journal))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// 1. A session's worker runs and reports: the server keeps the report with the run that sent it.
	probe := f.get(fixtureReporter, "S1")
	defer probe.Release()
	reportsCall(t, probe, "report", "indexing")
	waitUntil("indexing committed", func() bool { text, _ := lastReport(); return text == "indexing" })
	waitUntil("indexing outcome", func() bool { return slices.Contains(log.outcomes.all(), "indexing committed") })
	text, _ := lastReport()
	print("1. last report: %q, of the running worker: %t", text, current())

	// 2. It reports again and exits in the same turn. The worker's link is closed before the exit is published: a report
	//    still on its way is dropped and one already being handled is aborted so its transaction does not commit (Pi
	//    packages/chord/test/examples/76-worker-reports/main.ts:52,113-116).
	gate.Store(&published)
	go func() { _, _ = probe.Call("reportAndExit", "done", background) }()
	waitUntil("worker failed", func() bool { state := running(); return state != nil && state.Status == WorkerFailed })
	// The exit is published: no frame of the exited worker is handled from now on, so what was received is all there will be;
	// every received report settles, and a received "done" may only end "done unavailable" (never committed).
	waitUntil("received reports settled", func() bool { return len(log.outcomes.all()) == len(log.received.all()) })
	var outcomes []string
	for _, outcome := range log.outcomes.all() {
		if outcome != "done unavailable" {
			outcomes = append(outcomes, outcome)
		}
	}
	if !slices.Equal(outcomes, []string{"indexing committed"}) {
		t.Fatalf("outcomes = %q, want [indexing committed] (and maybe done unavailable)", log.outcomes.all())
	}
	waitUntil("worker_failed reported", func() bool { return slices.Contains(printed.all(), "   reported: worker_failed") })
	if lines := linesOf(t, journal); len(lines) != 1 || !strings.HasPrefix(lines[0], "indexing ") {
		t.Fatalf("journal = %q, want only [indexing <incarnation>]", lines)
	}
	text, _ = lastReport()
	print("2. the worker exited; last report still: %q", text)

	// 3. Retry: another run of the worker is another incarnation, so the report kept from the earlier run is not its.
	if err := f.workers.Retry(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	waitUntil("worker running after retry", func() bool { state := running(); return state != nil && state.Status == WorkerRunning })
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
