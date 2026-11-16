package chordctx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Runs Pi's withCancel, withAbortSignal, withoutAbortSignal and abort listeners (context/index.ts:111-130, 138-153;
// testdata/abort_cause_oracle.mjs, on Node, over Pi's own source) and this package's WithCancel, WithAbortSignal,
// WithoutAbortSignal and OnAbort over the same scenarios, and compares the listeners that ran, in the order they ran, with the
// reason each heard. The rules measured: a context with no abort signal (withoutAbortSignal's) is never aborted by what is
// behind it, so a context derived from it and from another signal takes the reason of whichever of those two aborted first;
// an abort of a signal that is aborted already runs nothing and leaves its reason; a listener that aborts its own signal again
// does not run the listeners after it a second time.

type causeOp struct {
	Op     string      `json:"op"`
	Name   string      `json:"name"`
	Parent string      `json:"parent,omitempty"`
	Signal string      `json:"signal,omitempty"`
	On     string      `json:"on,omitempty"`
	Reason string      `json:"reason,omitempty"`
	Abort  *causeAbort `json:"abort,omitempty"`
}

type causeAbort struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type causeScenario struct {
	Name string    `json:"name"`
	Ops  []causeOp `json:"ops"`
	// Quiet marks a scenario in which no listener is to run; it is not sent to Pi.
	Quiet bool `json:"-"`
}

type causeRun struct {
	Listener string `json:"listener"`
	Reason   string `json:"reason"`
}

func causeScenarios() []causeScenario {
	cancel := func(name, parent string) causeOp { return causeOp{Op: "cancel", Name: name, Parent: parent} }
	signal := func(name, parent, sig string) causeOp {
		return causeOp{Op: "signal", Name: name, Parent: parent, Signal: sig}
	}
	without := func(name, parent string) causeOp { return causeOp{Op: "without", Name: name, Parent: parent} }
	listen := func(name, on string) causeOp { return causeOp{Op: "listen", Name: name, On: on} }
	abort := func(name string) causeOp { return causeOp{Op: "abort", Name: name, Reason: "reason of " + name} }
	return []causeScenario{
		{Name: "a late listener hears the independent source, not the source behind a detached ancestor whose parent aborted first", Ops: []causeOp{
			cancel("x", "root"), without("detached", "x"), cancel("behind", "detached"), cancel("other", "root"), signal("composite", "behind", "other"),
			abort("x"), abort("other"), abort("behind"), listen("late", "composite"),
		}},
		{Name: "a late listener hears the source behind the detached ancestor when it aborted before the independent one", Ops: []causeOp{
			cancel("x", "root"), without("detached", "x"), cancel("behind", "detached"), cancel("other", "root"), signal("composite", "behind", "other"),
			abort("x"), abort("behind"), abort("other"), listen("late", "composite"),
		}},
		{Name: "a listener added before the aborts hears the independent source", Ops: []causeOp{
			cancel("x", "root"), without("detached", "x"), cancel("behind", "detached"), cancel("other", "root"), signal("composite", "behind", "other"),
			listen("early", "composite"), abort("x"), abort("other"), abort("behind"),
		}},
		{Name: "a context derived from a detached one hears its own abort only", Ops: []causeOp{
			cancel("x", "root"), without("detached", "x"), cancel("k", "detached"),
			listen("k", "k"), abort("x"), abort("k"),
		}},
		{Name: "a detached context has no abort signal to listen to", Quiet: true, Ops: []causeOp{
			cancel("x", "root"), without("detached", "x"), listen("detached", "detached"), abort("x"),
		}},
		{Name: "a second abort runs nothing and leaves the first reason", Ops: []causeOp{
			cancel("a", "root"), listen("early", "a"), abort("a"), {Op: "abort", Name: "a", Reason: "second"}, listen("late", "a"),
		}},
		{Name: "a listener that aborts its own context again leaves the listeners after it to run once", Ops: []causeOp{
			cancel("a", "root"),
			{Op: "listen", Name: "first", On: "a", Abort: &causeAbort{Name: "a", Reason: "again"}},
			listen("second", "a"), listen("third", "a"), abort("a"),
		}},
	}
}

// runCauseScenario is the scenario over this package.
func runCauseScenario(scenario causeScenario) []causeRun {
	contexts := map[string]context.Context{"root": chordctx.Background()}
	cancels := map[string]func(error){}
	var log []causeRun
	for _, op := range scenario.Ops {
		switch op.Op {
		case "cancel":
			contexts[op.Name], cancels[op.Name] = chordctx.WithCancel(contexts[op.Parent])
		case "signal":
			contexts[op.Name] = chordctx.WithAbortSignal(contexts[op.Parent], contexts[op.Signal])
		case "without":
			contexts[op.Name] = chordctx.WithoutAbortSignal(contexts[op.Parent])
		case "listen":
			chordctx.OnAbort(contexts[op.On], func(cause error) {
				log = append(log, causeRun{Listener: op.Name, Reason: cause.Error()})
				if op.Abort != nil {
					cancels[op.Abort.Name](errors.New(op.Abort.Reason))
				}
			})
		case "abort":
			cancels[op.Name](errors.New(op.Reason))
		}
	}
	return log
}

func TestAbortCauseMatchesPi(t *testing.T) {
	scenarios := causeScenarios()
	payload, _ := json.Marshal(scenarios)
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/abort_cause_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi struct {
		Scenarios []struct {
			Name string     `json:"name"`
			Log  []causeRun `json:"log"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(output, &pi); err != nil {
		t.Fatal(err)
	}
	if len(pi.Scenarios) != len(scenarios) {
		t.Fatalf("Pi answered %d scenarios, want %d", len(pi.Scenarios), len(scenarios))
	}
	for i, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			want, got := pi.Scenarios[i].Log, runCauseScenario(scenario)
			if (len(want) == 0) != scenario.Quiet {
				t.Fatalf("Pi ran %d listeners (%v): the scenario is not what it says", len(want), want)
			}
			if !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
				t.Fatalf("listeners ran %v, Pi's %v", got, want)
			}
		})
	}
}
