package experimental

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
)

// session-worker.ts:589-592: every live Harness task, background work included, keeps the worker and its Session open; the worker retires once the graph is empty and no client demands it. The fake coordinator never attaches a client, so only the initial demand grace stands between the worker and retirement.
func TestWorkerStaysOpenWhileTheHarnessHasLiveTasks(t *testing.T) {
	isolateExperimentalTest(t)
	t.Setenv(SessionWorkerInitialDemandGraceEnv, "50")
	step, reached := durabletest.Pending()
	openedHarness := make(chan *durabletest.FauxConversation, 1)
	// running closes once the recovered turn is inside its tool step, so the abort below ends a task that is live,
	// not one a slow runner has not started yet (an abort before the step leaves <-reached waiting forever).
	running := make(chan struct{})
	finished := make(chan error, 1)
	helperDone := make(chan struct{})
	// The helper swaps os.Stderr: the test ends only after it restored it.
	t.Cleanup(func() { <-helperDone })
	go func() {
		defer close(helperDone)
		_, _, _ = runWorkerAgainstFakeCoordinator(t, func(_ context.Context, databasePath string, _ SessionWorkerOptions) (SessionWorkerRuntime, error) {
			opened, err := durabletest.OpenFile(databasePath, step)
			if err != nil {
				return SessionWorkerRuntime{}, err
			}
			openedHarness <- opened
			// An interrupted turn's recovered work is already live when the worker subscribes.
			if _, err := opened.Conversation.Submit(context.Background(), ai.UserText("recovered"), durable.WhenBusyReject); err != nil {
				return SessionWorkerRuntime{}, err
			}
			select {
			case <-reached:
			case <-time.After(time.Minute):
				return SessionWorkerRuntime{}, errors.New("the recovered turn never reached its tool step")
			}
			close(running)
			return SessionWorkerRuntime{Harness: opened.Harness, Conversation: opened.Conversation}, nil
		}, false, finished)
	}()
	select {
	case <-running:
	case err := <-finished:
		t.Fatalf("worker ended before its recovered task started: %v", err)
	}
	select {
	case err := <-finished:
		t.Fatalf("worker retired with a live task: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := (<-openedHarness).Conversation.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("worker result = %v, want clean retirement", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker stayed open after its last task ended")
	}
}

// Without a live task the same worker retires after the initial grace, so the test above holds the worker open through the task graph and not through any other path.
func TestWorkerRetiresAfterTheInitialGraceWithoutLiveTasks(t *testing.T) {
	isolateExperimentalTest(t)
	t.Setenv(SessionWorkerInitialDemandGraceEnv, "50")
	finished := make(chan error, 1)
	helperDone := make(chan struct{})
	// The helper swaps os.Stderr: the test ends only after it restored it.
	t.Cleanup(func() { <-helperDone })
	go func() {
		defer close(helperDone)
		_, _, _ = runWorkerAgainstFakeCoordinator(t, func(_ context.Context, databasePath string, _ SessionWorkerOptions) (SessionWorkerRuntime, error) {
			opened, err := durabletest.OpenFile(databasePath)
			if err != nil {
				return SessionWorkerRuntime{}, err
			}
			return SessionWorkerRuntime{Harness: opened.Harness, Conversation: opened.Conversation}, nil
		}, false, finished)
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("worker result = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an idle worker with no demand did not retire")
	}
}
