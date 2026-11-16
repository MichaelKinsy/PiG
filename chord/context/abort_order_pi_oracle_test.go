package chordctx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Runs Pi's withCancel, withAbortSignal and abort listeners (context/index.ts:111-130, 138-153; testdata/abort_order_oracle.mjs, on
// Node, over Pi's own source) and this package's WithCancel, WithAbortSignal and OnAbort over the same scenarios, and compares the
// listeners in the order they ran, the abort state of every context at that moment, and the reason. Pi's order, measured on Node
// 24.22.2 with `AbortSignal.any`: when a source aborts, every composite derived from it is marked aborted before any listener runs;
// then the source's own listeners run in the order they were added; then each composite's, in the order the composites were
// created. Listeners are added before the abort here, as `awaitWithContext` adds them after its `signal.aborted` check.

type oracleOp struct {
	Op     string `json:"op"`
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
	Signal string `json:"signal,omitempty"`
	On     string `json:"on,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type oracleScenario struct {
	Name string     `json:"name"`
	Ops  []oracleOp `json:"ops"`
}

type oracleRun struct {
	Listener string          `json:"listener"`
	Aborted  map[string]bool `json:"aborted"`
	Reason   string          `json:"reason"`
}

func oracleScenarios() []oracleScenario {
	cancel := func(name, parent string) oracleOp { return oracleOp{Op: "cancel", Name: name, Parent: parent} }
	signal := func(name, parent, sig string) oracleOp {
		return oracleOp{Op: "signal", Name: name, Parent: parent, Signal: sig}
	}
	listen := func(name, on string) oracleOp { return oracleOp{Op: "listen", Name: name, On: on} }
	abort := func(name string) oracleOp { return oracleOp{Op: "abort", Name: name, Reason: "reason of " + name} }
	return []oracleScenario{
		{"the measurement: a source, a composite of two sources, a composite of that", []oracleOp{
			cancel("a", "root"), cancel("b", "root"), signal("c", "a", "b"), cancel("d", "c"),
			listen("a1", "a"), listen("c1", "c"), listen("a2", "a"), listen("d1", "d"), listen("c2", "c"), listen("a3", "a"),
			abort("a"),
		}},
		{"a child's listener added before its parent's runs after it", []oracleOp{
			cancel("p", "root"), cancel("k", "p"), listen("child", "k"), listen("parent", "p"), abort("p"),
		}},
		{"descendants run in creation order, not in tree order", []oracleOp{
			cancel("p", "root"), cancel("k1", "p"), cancel("k2", "p"), cancel("k1a", "k1"),
			listen("k1a", "k1a"), listen("k2", "k2"), listen("k1", "k1"), listen("p", "p"), abort("p"),
		}},
		{"a child's own abort leaves its parent's listeners alone", []oracleOp{
			cancel("p", "root"), cancel("k", "p"), cancel("g", "k"), listen("g", "g"), listen("p", "p"), listen("k", "k"), abort("k"),
		}},
		{"a composite follows its signal after the signal's own listeners", []oracleOp{
			cancel("p", "root"), cancel("s", "root"), signal("d", "p", "s"),
			listen("d", "d"), listen("s", "s"), listen("p", "p"), abort("s"),
		}},
		{"a composite follows its parent after the parent's own listeners", []oracleOp{
			cancel("p", "root"), cancel("s", "root"), signal("d", "p", "s"),
			listen("d", "d"), listen("s", "s"), listen("p", "p"), abort("p"),
		}},
		{"a signal alias of a parent with no signal is the signal itself", []oracleOp{
			cancel("s", "root"), signal("alias", "root", "s"), cancel("k", "alias"),
			listen("k", "k"), listen("alias", "alias"), listen("s", "s"), abort("s"),
		}},
		{"two aborts: the second finds nothing to run", []oracleOp{
			cancel("p", "root"), cancel("k", "p"), listen("k", "k"), listen("p", "p"), abort("k"), abort("p"),
		}},
	}
}

// runOracleScenario is the scenario over this package.
func runOracleScenario(scenario oracleScenario) []oracleRun {
	contexts := map[string]context.Context{"root": Background()}
	cancels := map[string]func(error){}
	var log []oracleRun
	snapshot := func() map[string]bool {
		aborted := map[string]bool{}
		for name, ctx := range contexts {
			if name != "root" {
				aborted[name] = ctx.Err() != nil
			}
		}
		return aborted
	}
	for _, op := range scenario.Ops {
		switch op.Op {
		case "cancel":
			contexts[op.Name], cancels[op.Name] = WithCancel(contexts[op.Parent])
		case "signal":
			contexts[op.Name] = WithAbortSignal(contexts[op.Parent], contexts[op.Signal])
		case "listen":
			OnAbort(contexts[op.On], func(cause error) {
				log = append(log, oracleRun{Listener: op.Name, Aborted: snapshot(), Reason: cause.Error()})
			})
		case "abort":
			cancels[op.Name](errors.New(op.Reason))
		}
	}
	return log
}

func TestAbortListenerOrderMatchesPi(t *testing.T) {
	scenarios := oracleScenarios()
	payload, _ := json.Marshal(scenarios)
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/abort_order_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi struct {
		Scenarios []struct {
			Name string      `json:"name"`
			Log  []oracleRun `json:"log"`
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
			want, got := pi.Scenarios[i].Log, runOracleScenario(scenario)
			if len(want) == 0 {
				t.Fatal("Pi ran no listener: the scenario proves nothing")
			}
			names := func(log []oracleRun) []string {
				out := make([]string, len(log))
				for i, run := range log {
					out[i] = run.Listener
				}
				return out
			}
			if !reflect.DeepEqual(names(got), names(want)) {
				t.Fatalf("listeners ran in order %v, Pi's %v", names(got), names(want))
			}
			for i := range want {
				if !maps.Equal(got[i].Aborted, want[i].Aborted) {
					t.Errorf("listener %s saw contexts aborted %v, Pi's %v", want[i].Listener, got[i].Aborted, want[i].Aborted)
				}
				if got[i].Reason != want[i].Reason {
					t.Errorf("listener %s saw reason %q, Pi's %q", want[i].Listener, got[i].Reason, want[i].Reason)
				}
			}
		})
	}
}
