package harness

// BenchmarkOneTaskRun is one one-task run on a resumed Harness over memory storage: a task that completes at once (two commits
// that change a task: its reservation and its end), created and awaited. It measures what the group commit publication path
// costs one run, with no kept contexts.

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func BenchmarkOneTaskRun(b *testing.B) {
	task := tkOneStep("test.bench-one-task", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		return tkComplete(ctx, runtime)
	}).Erased()
	registry := CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{task}}); err != nil {
		b.Fatal(err)
	}
	harness, err := OpenHarness(context.Background(), storage.NewMemoryStorage(), HarnessOptions{Models: ai.CreateModels(), Registry: registry})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = harness.Close(context.Background()) })
	harness.Resume()
	root, err := harness.Root(context.Background(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return tx.CreateTaskErased(task, nil, durable.TaskOptions{Ownership: conversationOwned})
		})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := harness.WaitForTask(testContext, id); err != nil {
			b.Fatal(err)
		}
	}
}
