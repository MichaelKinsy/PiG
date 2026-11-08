package status_test

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type blipState struct {
	Phase string `json:"phase"`
}

// blip is a task that completes in its first phase: each one makes the task graph change twice (it appears, it is gone).
var blip = durable.DefineTask(durable.TaskDefinition[durable.JsonValue, blipState, durable.JsonValue, any]{
	Name:    "test.status-blip",
	Version: 1,
	Initial: func(durable.JsonValue) blipState { return blipState{Phase: "run"} },
	Phases: map[string]durable.PhaseHandler[durable.JsonValue, blipState, durable.JsonValue, any]{
		"run": func(ctx context.Context, _ durable.RunningTask[durable.JsonValue, blipState, durable.JsonValue], runtime durable.TaskRuntime[durable.JsonValue, blipState, durable.JsonValue, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, blipState, durable.JsonValue]) (*durable.NextTaskState[blipState, durable.JsonValue], error) {
				var result durable.JsonValue
				return &durable.NextTaskState[blipState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}}, nil
			})
		},
	},
	Abort: func(context.Context, durable.RunningTask[durable.JsonValue, blipState, durable.JsonValue], durable.TaskRuntime[durable.JsonValue, blipState, durable.JsonValue, any]) error {
		return nil
	},
})

// A push reads the followed task graph in a section of the host, as Pi's synchronous read of `graph.value`: the Durable
// publications of the graph apply on the loop, so a read from the push's own goroutine is a data race with them (-race).
func TestPushReadsTheFollowedGraphInASectionWhileTheGraphChanges(t *testing.T) {
	fixture := newServerFixture(t)
	if err := fixture.SetCell(fixture.workers, workersOf("s1", worker(true, 1, ""))); err != nil {
		t.Fatal(err)
	}
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "test.status-blip", Tasks: []durable.AnyTask{blip.Erased()}}); err != nil {
		t.Fatal(err)
	}
	opened, err := harness.OpenHarness(p2test.Background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models: ai.CreateModels(ai.CreateModelsOptions{}), Registry: registry,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(context.WithoutCancel(p2test.Background)) })
	root, err := opened.Root(p2test.Background, nil)
	if err != nil {
		t.Fatal(err)
	}
	opened.Resume()
	parts := &p2runtime.WorkerParts{Liveness: p2runtime.NewLiveness()}
	client := fixture.WorkerHost("s1", 1, p2test.WorkerFacet(p2test.HarnessParts(opened), parts, nil), status.Worker)
	report, err := client.Hold(p2test.Background, contract.StatusReport, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer report.Release()
	if err := p2runtime.WhenLive(p2test.Background, report); err != nil {
		t.Fatal(err)
	}
	const rounds = 40
	var pushes sync.WaitGroup
	failures := make(chan error, rounds)
	for range rounds {
		pushes.Add(1)
		go func() {
			defer pushes.Done()
			_, err := report.Call("push", nil, p2test.Background)
			failures <- err
		}()
		if _, err := durable.Commit(p2test.Background, root, func(tx durable.Tx) (durable.TaskId, error) {
			return tx.CreateTaskErased(blip.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		}); err != nil {
			t.Fatal(err)
		}
	}
	pushes.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
}
