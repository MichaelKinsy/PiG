package workers

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
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
	// Windows also reports the plugin directory's own last-write time (the label is renamed into it) as a change of it, as
	// Node's fs.watch does, at a moment after the rollout that the test cannot bound: that plugin is dirty again, and only
	// it. The rollout is asserted to have cleared its marks where the platform does not do that.
	for _, name := range f.dirty() {
		if runtime.GOOS != "windows" || name != "@test/notes" {
			t.Fatalf("dirty = %v, want none (on Windows, at most the plugin whose directory the label was written into)", f.dirty())
		}
	}
	for _, reference := range []interface{ Release() }{one, two, failed, control} {
		reference.Release()
	}
}

// A failed worker is listed when its address already has a start running as the rollout retries it, as Pi lists whatever
// `routed.starting` the retry left (workers.ts:598-601): the route starts a resolve itself when a worker's link closes, and a
// resolve ends only when its start settles, so a rollout that clears the failure meanwhile finds `Retry` starting none (it
// answers false) and the address with a start of its own running. The entry waits for that start, as for one the retry made.
func TestTheRolloutStepListsAFailedWorkerWhoseStartWasAlreadyRunningWhenItRetriedIt(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	one := f.get(fixtureNotes, "S1")
	control := f.get(fixtureControl, "S3")
	failed := f.get(fixtureNotes, "S3")
	if _, err := control.Call("crash", float64(1), background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return f.stateOf("S3") != nil && f.stateOf("S3").Status == WorkerFailed })
	f.label("v2")
	f.untilDirty()
	built := f.build(f.dirty())
	retry := f.workers.route.Retry
	f.workers.route.Retry = func(address contracts.Address) bool {
		if address.Key != session("S3").Key {
			return retry(address)
		}
		// The resolve the route would be running: the first Retry starts it, so the rollout's own finds it running.
		if !retry(address) {
			t.Error("Retry of the failed address started no resolve")
		}
		refused := retry(address)
		if refused {
			t.Error("a second Retry of the address started another resolve while the first runs")
		}
		return refused
	}
	hosts := f.rollout(f.workers.Step("workers"), built)
	requireListed(t, listed(hosts),
		"session|S1 "+changedNotes,
		"session|S3 added=[@test/notes] changed=[] removed=[]")
	until(t, func() bool { return labelOf(t, one) == "S1 v2" && labelOf(t, failed) == "S3 v2" })
	for _, reference := range []interface{ Release() }{one, failed, control} {
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

// The component's loop and the server's loop are taken in one order only: the server's, then the component's (a worker's
// link the host closes in its own section tells the component, which runs a section of its loop). A rollout that held the
// component's loop while it called the route (`Retry` enters the server's loop) took them the other way round: with the
// server's loop held by a section that closes a worker's link, neither could go on.
func TestARolloutCallsTheRouteOutsideTheComponentLoopSoAServerSectionClosingAWorkersLinkCannotDeadlockIt(t *testing.T) {
	var armed atomic.Bool
	inServer, retrying := make(chan struct{}), make(chan struct{})
	var closeLink func()
	f := newFleet(t, fleetOptions{onAuthorize: func(address contracts.Address) {
		if address.Key == session("S4").Key && armed.CompareAndSwap(true, false) {
			// A hold the route authorizes: this runs in a section of the server's loop. It waits until the rollout is in the
			// route, then the host closes a worker's link here, as its heartbeat does.
			close(inServer)
			<-retrying
			closeLink()
		}
	}})
	one := f.get(fixtureNotes, "S1")
	control := f.get(fixtureControl, "S3")
	failed := f.get(fixtureNotes, "S3")
	if _, err := control.Call("crash", float64(1), background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return f.stateOf("S3") != nil && f.stateOf("S3").Status == WorkerFailed })
	f.label("v2")
	f.untilDirty()
	built := f.build(f.dirty())
	f.workers.loop.Run(func() { closeLink = f.workers.addresses[session("S1").Key].child.process.Link.Close })
	retry := f.workers.route.Retry
	var once sync.Once
	f.workers.route.Retry = func(address contracts.Address) bool {
		once.Do(func() { close(retrying) })
		return retry(address)
	}
	armed.Store(true)
	held := make(chan *host.Reference, 1)
	go func() {
		reference, err := f.server.Hold(background, fixtureNotes, jsonObject("id", "S4"), host.GetOptions{})
		if err != nil {
			t.Error(err)
		}
		held <- reference
	}()
	<-inServer
	rolled := make(chan error, 1)
	go func() {
		_, err := f.plugins.Rollout([]plugins.RolloutStep{f.workers.Step("workers")}, built, background)
		rolled <- err
	}()
	select {
	case err := <-rolled:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: the rollout holds the component's loop for the route, and the server's section closing a worker's link waits for it")
	}
	if reference := <-held; reference != nil {
		reference.Release()
	}
	for _, reference := range []interface{ Release() }{one, failed, control} {
		reference.Release()
	}
}
