package codingagent

import (
	"context"
	"runtime/metrics"
	"testing"
)

// The availability pass awaits 45 provider tasks twice and a three-task pass around them. Each await ran a coordinator goroutine beside its tasks;
// the caller now collects the results itself, and the task owner still joins every task on Close.
func TestAwaitModelTasksStartsOneGoroutinePerTask(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	t.Cleanup(r.CloseModelTasks)
	const tasks = 45
	work := make([]func(context.Context) error, tasks)
	for i := range work {
		work[i] = func(context.Context) error { return nil }
	}
	sample := []metrics.Sample{{Name: "/sched/goroutines-created:goroutines"}}
	metrics.Read(sample)
	before := sample[0].Value.Uint64()
	if err := r.AwaitModelTasks(context.Background(), work...); err != nil {
		t.Fatal(err)
	}
	metrics.Read(sample)
	if created := sample[0].Value.Uint64() - before; created > tasks {
		t.Fatalf("awaiting %d tasks started %d goroutines, want %d", tasks, created, tasks)
	}
}
