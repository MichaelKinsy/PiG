package harness

// Pi main 35dfa757: after a commit that changed a task, the scheduler re-checked every conversation with a kept context range by
// walking its subtree, scanned them again for the next expiry, and wake() scanned every live task, so the cost of a run grew
// with the number of conversations read in the last contextRetentionMs. These benchmarks run one-task runs and read wake()
// with N kept ranges or N live tasks: ns/op and allocs/op must not grow with N.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// idleBenchOpen opens a resumed Harness over memory storage whose registry holds tasks, and returns it with its root and
// scheduler.
func idleBenchOpen(b *testing.B, tasks ...durable.AnyTask) (Harness, Conversation, *TaskScheduler) {
	b.Helper()
	registry := CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: tasks}); err != nil {
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
	return harness, root, harness.(*harnessImpl).tasks
}

// idleBenchReader reads the context of the conversation its input names, then completes.
func idleBenchReader() stepTask {
	return tkOneStep("test.idle-bench.reader", func(ctx context.Context, task stepRecord, runtime stepRuntime) error {
		if id, ok := task.Input.(float64); ok {
			if _, err := runtime.Context(ctx, durable.ConversationId(id), nil); err != nil {
				return err
			}
		}
		return tkComplete(ctx, runtime)
	})
}

func idleBenchRun(tb testing.TB, root Conversation, harness Harness, task durable.AnyTask, input durable.JsonValue) {
	tb.Helper()
	id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
		return tx.CreateTaskErased(task, input, durable.TaskOptions{Ownership: conversationOwned})
	})
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := harness.WaitForTask(testContext, id); err != nil {
		tb.Fatal(err)
	}
}

// BenchmarkRunWithKeptContexts is one one-task run (a task that reads nothing and completes: two commits that change a task)
// in a Session where `contexts` conversations keep an idle context range.
func BenchmarkRunWithKeptContexts(b *testing.B) {
	for _, contexts := range []int{100, 1000, 4000} {
		b.Run(fmt.Sprintf("contexts=%d", contexts), func(b *testing.B) {
			reader := idleBenchReader().Erased()
			harness, root, scheduler := idleBenchOpen(b, reader)
			for range contexts {
				conversation, err := harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.EntryRecord, error) {
					return tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "note"})
				}); err != nil {
					b.Fatal(err)
				}
				idleBenchRun(b, root, harness, reader, float64(conversation.Id()))
			}
			if kept := scheduler.keptContextCount(); kept != contexts {
				b.Fatalf("%d ranges kept, want %d", kept, contexts)
			}
			b.ReportAllocs()
			for b.Loop() {
				idleBenchRun(b, root, harness, reader, nil)
			}
		})
	}
}

// BenchmarkWakeWithLiveTasks is wake() while one task runs, with `tasks` other live tasks: the answer is active with no
// walk over them.
func BenchmarkWakeWithLiveTasks(b *testing.B) {
	for _, tasks := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("tasks=%d", tasks), func(b *testing.B) {
			gate := deferred()
			held := tkGated("test.idle-bench.held", gate).Erased()
			harness, root, scheduler := idleBenchOpen(b, held)
			ids := make([]durable.TaskId, 0, tasks)
			for range tasks {
				id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
					return tx.CreateTaskErased(held, nil, durable.TaskOptions{Ownership: conversationOwned})
				})
				if err != nil {
					b.Fatal(err)
				}
				ids = append(ids, id)
			}
			pollUntilBench(b, func() bool { return scheduler.invocationCount() > 0 })
			if !scheduler.Wake().Active {
				b.Fatal("not active")
			}
			b.ReportAllocs()
			for b.Loop() {
				scheduler.Wake()
			}
			b.StopTimer()
			gate.resolve()
			for _, id := range ids {
				if _, err := harness.WaitForTask(testContext, id); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func pollUntilBench(b *testing.B, check func() bool) {
	b.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if check() {
			return
		}
	}
	b.Fatal("condition was not reached")
}

// wake() while a task runs costs the same with any number of live tasks: it names the earliest sleep from the sleeping tasks and
// scans the live ones only while nothing runs (it once walked every live task, 100 times slower from 100 to 10,000 tasks).
func TestWakeWhileATaskRunsCostsTheSameAtAnyNumberOfLiveTasks(t *testing.T) {
	cost := func(tasks int) float64 {
		result := testing.Benchmark(func(b *testing.B) {
			gate := deferred()
			held := tkGated("test.idle-bench.held", gate).Erased()
			harness, root, scheduler := idleBenchOpen(b, held)
			ids := make([]durable.TaskId, 0, tasks)
			for range tasks {
				id, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
					return tx.CreateTaskErased(held, nil, durable.TaskOptions{Ownership: conversationOwned})
				})
				if err != nil {
					b.Fatal(err)
				}
				ids = append(ids, id)
			}
			pollUntilBench(b, func() bool { return scheduler.invocationCount() > 0 })
			b.ResetTimer()
			for b.Loop() {
				scheduler.Wake()
			}
			b.StopTimer()
			gate.resolve()
			for _, id := range ids {
				if _, err := harness.WaitForTask(testContext, id); err != nil {
					b.Fatal(err)
				}
			}
		})
		return float64(result.NsPerOp())
	}
	small, large := cost(100), cost(5_000)
	if large > 5*small {
		t.Fatalf("wake() takes %.0f ns with 100 live tasks and %.0f ns with 5,000: it walks the live tasks while one runs", small, large)
	}
}
