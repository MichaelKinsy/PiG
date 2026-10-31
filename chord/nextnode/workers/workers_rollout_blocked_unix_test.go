//go:build unix

package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports the rollout tests of workers.test.ts that block a worker's event loop (freeze: a worker process stopped for the
// time Pi's busy loop takes, which has no Windows form).

// asyncRollout starts `plugins.rollout([step], built, ctx)`; the hosts of its first step arrive on the channel.
func (f *fleet) asyncRollout(step plugins.RolloutStep, built *plugins.Built, ctx context.Context) <-chan []plugins.RolloutHost {
	hosts := make(chan []plugins.RolloutHost, 1)
	go func() {
		result, err := f.plugins.Rollout([]plugins.RolloutStep{step}, built, ctx)
		if err != nil {
			f.t.Error(err)
			hosts <- nil
			return
		}
		hosts <- result.Steps[0].Hosts
	}()
	return hosts
}

func TestTheRolloutStepListsAWorkerStoppedCleanlyDuringItAsStoppedAndOneThatCrashedWithItsError(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	one := f.get(fixtureNotes, "S1")
	control := f.get(fixtureControl, "S2")
	f.label("v2")
	f.untilDirty()
	built := f.build(f.dirty())
	// Both workers block their event loops: their reloads wait; S1 is stopped meanwhile, S2 crashes before reading it.
	first := f.get(fixtureControl, "S1")
	if _, err := first.Call("block", float64(400), background); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Call("blockThenCrash", float64(400), background); err != nil {
		t.Fatal(err)
	}
	// Their event loops are blocked once their timers ran.
	time.Sleep(50 * time.Millisecond)
	rolling := f.asyncRollout(f.workers.Step("workers"), built, background)
	time.Sleep(50 * time.Millisecond)
	stopped := make(chan error, 1)
	go func() { stopped <- f.workers.Stop(session("S1"), background) }()
	hosts := <-rolling
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	requireListed(t, listed(hosts), "session|S1 stopped", "session|S2 worker: its link is gone")
	if dirty := f.dirty(); len(dirty) != 1 || dirty[0] != "@test/notes" {
		t.Fatalf("dirty = %v", dirty)
	}
	for _, reference := range []interface{ Release() }{one, first, control} {
		reference.Release()
	}
}

func TestTheRolloutStepAnAbortedWaitListsTheWorkerAbortedItsLateReplyKeepsTheChildsSlotUntilItArrives(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	control := f.get(fixtureControl, "S1")
	notes := f.get(fixtureNotes, "S1")
	f.label("v2")
	f.untilDirty()
	built := f.build(f.dirty())
	if _, err := control.Call("block", float64(300), background); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	aborting, abort := chordctx.WithCancel(background)
	step := f.workers.Step("workers")
	waited := f.asyncRollout(step, built, aborting)
	time.AfterFunc(50*time.Millisecond, func() { abort(errors.New("stop waiting")) })
	requireListed(t, listed(<-waited), "session|S1 stop waiting")
	if dirty := f.dirty(); len(dirty) != 1 || dirty[0] != "@test/notes" {
		t.Fatalf("dirty = %v", dirty)
	}
	f.label("v3")
	f.untilDirty()
	requireListed(t, listed(f.rollout(step, f.build(f.dirty()))), "session|S1 "+changedNotes)
	until(t, func() bool { return labelOf(t, notes) == "S1 v3" })
	control.Release()
	notes.Release()
}

func TestFixRound1AReloadQueuedBehindOneTheChildNeverAnswersSettlesWhenTheChildExits(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	control := f.get(fixtureControl, "S1")
	f.label("v2")
	until(t, func() bool { return len(f.dirty()) > 0 })
	built := f.build(f.dirty())
	if _, err := control.Call("blockThenCrash", float64(600), background); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	first := f.asyncRollout(f.workers.Step("one"), built, background)
	time.Sleep(50 * time.Millisecond)
	second := f.asyncRollout(f.workers.Step("two"), built, background)
	requireListed(t, listed(<-first), "session|S1 worker: its link is gone")
	requireListed(t, listed(<-second), "session|S1 worker: its link is gone")
	control.Release()
}
