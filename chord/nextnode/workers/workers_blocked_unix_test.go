//go:build unix

package workers

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports the tests of workers.test.ts that block a worker's event loop: a worker process stopped for the time its busy loop
// takes (freezeWorker), which has no Windows form.

func TestFailuresStayARunningWorkersLostLinkABlockedWorkerPastTheHeartbeatIsKilledAfterTheGraceItsHoldersGetANewWorkerASecondWithinAMinuteFailsTheAddress(t *testing.T) {
	ping, closeAfter := 100*time.Millisecond, 300*time.Millisecond
	f := newFleet(t, fleetOptions{heartbeat: &host.HeartbeatOptions{Ping: &ping, Close: &closeAfter}})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	f.freezeWorker("S1", 20*time.Second, false)
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerStopping }, 5_000)
	// SIGKILL skipped the child's exit hooks: on macOS and the BSDs its lock's socket file was left, and the next start
	// cleared it (else the new child would fail closed on it).
	until(t, func() bool { return len(f.starts.all()) == 2 && refKind(notes) == host.RefStateLive }, 15_000)
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerRunning })
	lost := f.errors.all()
	if len(lost) != 1 {
		t.Fatalf("errors = %v, want one worker_link_lost", lost)
	}
	if got, ok := lost[0].(WorkerLinkLostError); !ok || got.Address != "session|S1" || got.Error.Error() != "worker: link lost" {
		t.Fatalf("errors[0] = %#v, want worker_link_lost { session|S1, worker: link lost }", lost[0])
	}
	// The new worker loses its link too: no restart loop, the address fails until `retry`.
	f.freezeWorker("S1", 20*time.Second, false)
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerFailed }, 15_000)
	if message := f.stateOf("S1").Error.Message; message != "worker lost its link" {
		t.Fatalf("message = %q", message)
	}
	failure, ok := f.firstFailure()
	if !ok || failure.Error.Error() != "worker: link lost" {
		t.Fatalf("worker_failed = %+v", failure)
	}
	until(t, func() bool { return refKind(notes) == host.RefStateFailed })
	if starts := f.starts.all(); len(starts) != 2 {
		t.Fatalf("starts = %v", starts)
	}
}

func TestFixRound1ReapsAWorkerThatClosedItsOwnIPCChannelAndLivesOnKilledAfterTheGraceALostLinkItsHolderGetsANewWorkerFor(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{DisconnectAndBlock: true})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	f.behaviours.delete("S1")
	until(t, func() bool {
		for _, each := range f.errors.all() {
			if _, ok := each.(WorkerLinkLostError); ok {
				return true
			}
		}
		return false
	}, 10_000)
	until(t, func() bool { return len(f.starts.all()) == 2 && refKind(notes) == host.RefStateLive }, 10_000)
	if status := f.stateOf("S1"); status != nil && status.Status == WorkerFailed {
		t.Fatalf("state = %v, want not failed", status)
	}
}
