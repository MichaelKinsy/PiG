package workers

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports the rollout tests of packages/chord/test/next-node/workers/workers.test.ts (the rollout step, §4, and the "fix round 1"
// cases that ship a build): the set shipped to running workers, held failed ones retried, a worker starting during a
// rollout, a plugin whose evaluation failed shipped again, two rollouts together, no reload report kept.

// listed is `listed(hosts)`: each host with what it is: its plugins' changes, `stopped`, or its error's message; sorted.
func listed(hosts []plugins.RolloutHost) []string {
	lines := make([]string, len(hosts))
	for index, entry := range hosts {
		switch entry.Kind {
		case plugins.RolloutReported:
			lines[index] = fmt.Sprintf("%s added=%v changed=%v removed=%v", entry.Host, entry.Report.Plugins.Added, entry.Report.Plugins.Changed, entry.Report.Plugins.Removed)
		case plugins.RolloutStopped:
			lines[index] = entry.Host + " stopped"
		default:
			lines[index] = entry.Host + " " + entry.Error.Error()
		}
	}
	slices.Sort(lines)
	return lines
}

// rollout is `plugins.rollout(steps, built, context)`: the hosts of its first step.
func (f *fleet) rollout(step plugins.RolloutStep, built *plugins.Built) []plugins.RolloutHost {
	f.t.Helper()
	result, err := f.plugins.Rollout([]plugins.RolloutStep{step}, built, background)
	if err != nil {
		f.t.Fatal(err)
	}
	return result.Steps[0].Hosts
}

// untilDirty waits until `plugins.state.value.dirty` includes "@test/notes".
func (f *fleet) untilDirty() {
	f.t.Helper()
	until(f.t, func() bool { return slices.Contains(f.dirty(), "@test/notes") })
}

func requireListed(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts = %q, want %q", got, want)
	}
}

const changedNotes = "added=[] changed=[@test/notes] removed=[]"

func TestTheRolloutStepShipsTheCurrentSetToRunningWorkersRetriesHeldFailedOnesListsEach(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	one, two := f.get(fixtureNotes, "S1"), f.get(fixtureNotes, "S2")
	control := f.get(fixtureControl, "S3")
	failed := f.get(fixtureNotes, "S3")
	if _, err := control.Call("crash", float64(1), background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return f.stateOf("S3") != nil && f.stateOf("S3").Status == WorkerFailed })
	f.label("v2")
	f.untilDirty()
	hosts := f.rollout(f.workers.Step("workers"), f.build(f.dirty()))
	requireListed(t, listed(hosts),
		"session|S1 "+changedNotes,
		"session|S2 "+changedNotes,
		"session|S3 added=[@test/notes] changed=[] removed=[]")
	until(t, func() bool {
		return labelOf(t, one) == "S1 v2" && labelOf(t, two) == "S2 v2" && labelOf(t, failed) == "S3 v2"
	})
	if dirty := f.dirty(); len(dirty) != 0 {
		t.Fatalf("dirty = %v, want none", dirty)
	}
	for _, reference := range []interface{ Release() }{one, two, failed, control} {
		reference.Release()
	}
}

func TestTheRolloutStepAWorkerStartingDuringItGetsTheNewSetAStartAfterTheRolloutLoadsIt(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	f.label("v2")
	f.untilDirty()
	f.rollout(f.workers.Step("workers"), f.build(f.dirty()))
	notes := f.get(fixtureNotes, "S9")
	defer notes.Release()
	if label := labelOf(t, notes); label != "S9 v2" {
		t.Fatalf("label = %q", label)
	}
}

func TestFixRound1APluginWhoseEvaluationFailedIsShippedAgainByTheNextRolloutAndStaysDirty(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	if err := os.WriteFile(filepath.Join(f.root, "notes", "label.ts"), []byte("throw new Error(\"evaluation failed\");\nexport const label = \"v2\";\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return len(f.dirty()) > 0 })
	built := f.build(f.dirty())
	failedPlugins := func(hosts []plugins.RolloutHost) any {
		if len(hosts) == 0 || hosts[0].Kind != plugins.RolloutReported {
			return hosts
		}
		var names []string
		for _, failure := range hosts[0].Report.Failed {
			names = append(names, failure.Plugin)
		}
		return names
	}
	for _, name := range []string{"one", "two"} {
		if got := failedPlugins(f.rollout(f.workers.Step(name), built)); !reflect.DeepEqual(got, []string{"@test/notes"}) {
			t.Fatalf("rollout %s failed = %v, want [@test/notes]", name, got)
		}
	}
	if dirty := f.dirty(); !reflect.DeepEqual(dirty, []string{"@test/notes"}) {
		t.Fatalf("dirty = %v", dirty)
	}
	if label := labelOf(t, notes); label != "S1 v1" {
		t.Fatalf("label = %q", label)
	}
}

func TestFixRound1TwoRolloutsStartedTogetherEachShipTheirOwnBuildTheLaterOneStays(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	f.label("v2")
	v2 := f.build([]string{"@test/notes"})
	f.label("v3")
	v3 := f.build([]string{"@test/notes"})
	// Pi's `Promise.all([rollout(v2), rollout(v3)])` runs the first rollout up to its first await before the second starts:
	// the first rollout's reload is out before the second's, so the later build is the one that stays.
	results := make(chan []plugins.RolloutHost, 2)
	run := func(name string, built *plugins.Built) {
		go func() {
			result, err := f.plugins.Rollout([]plugins.RolloutStep{f.workers.Step(name)}, built, background)
			if err != nil {
				t.Error(err)
				results <- nil
				return
			}
			results <- result.Steps[0].Hosts
		}()
	}
	run("v2", v2)
	until(t, func() bool { return f.reloadOutstanding("S1") || len(results) > 0 })
	run("v3", v3)
	for range 2 {
		requireListed(t, listed(<-results), "session|S1 "+changedNotes)
	}
	until(t, func() bool { return labelOf(t, notes) == "S1 v3" })
}

func TestFixRound1KeepsNoReloadReportOnceConsumedTheReloadQueueSettlesToNothing(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	report := func() weak.Pointer[plugins.RolloutHost] {
		f.label("v2")
		built := f.build([]string{"@test/notes"})
		hosts := f.rollout(f.workers.Step("workers"), built)
		if len(hosts) == 0 || hosts[0].Kind != plugins.RolloutReported {
			t.Fatalf("hosts = %+v, want a report", hosts)
		}
		return weak.Make(&hosts[0])
	}()
	expectCollected(t, report)
}
