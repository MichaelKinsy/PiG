//go:build unix

package workers

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports the tests of workers.test.ts that block a worker's event loop: a worker process stopped for the time its busy loop
// takes (freezeWorker), which has no Windows form.

func TestFailuresStayALinkLostWhileItsChildLivesABlockedWorkerPastTheHeartbeatIsKilledAfterTheGraceFailed(t *testing.T) {
	ping, closeAfter := 100*time.Millisecond, 300*time.Millisecond
	f := newFleet(t, fleetOptions{heartbeat: &host.HeartbeatOptions{Ping: &ping, Close: &closeAfter}})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	f.freezeWorker("S1", 20*time.Second, false)
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerStopping }, 5_000)
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerFailed }, 10_000)
	if message := f.stateOf("S1").Error.Message; message != "worker exited (SIGKILL)" {
		t.Fatalf("message = %q", message)
	}
	failure, ok := f.firstFailure()
	if !ok || failure.Error.Error() != "worker: link lost" {
		t.Fatalf("worker_failed = %+v", failure)
	}
	if starts := f.starts.all(); len(starts) != 1 {
		t.Fatalf("starts = %v", starts)
	}
}

func TestFixRound1ReapsAWorkerThatClosedItsOwnIPCChannelAndLivesOnKilledAfterTheGraceFailed(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{DisconnectAndBlock: true})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerFailed }, 10_000)
	if message := f.stateOf("S1").Error.Message; message != "worker exited (SIGKILL)" {
		t.Fatalf("message = %q", message)
	}
	until(t, func() bool { return refKind(notes) == host.RefStateFailed })
}
