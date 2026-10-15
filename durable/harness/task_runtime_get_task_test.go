package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi packages/durable/src/harness/scheduler.ts:1196-1199 getTask: the runtime read is refused once the invocation ended (#read) and otherwise reads the task record on the Session line, behind a job that already holds it.
func TestTaskRuntimeGetTaskReadsOnTheSessionLineAndRefusesAnEndedInvocation(t *testing.T) {
	captured := &syncValue[stepRuntime]{}
	hold := deferred()
	task := tkOneStep("test.get-task", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		captured.put(runtime)
		if err := hold.wait(ctx); err != nil {
			return err
		}
		return tkComplete(ctx, runtime)
	})
	opened := tkOpenRoot(t, []durable.AnyTask{task.Erased()})
	id := tkStart(t, opened.root, task.Erased())
	opened.harness.Resume()
	var runtime stepRuntime
	eventually(t, func() bool { runtime, _ = captured.get(); return runtime != nil })

	release := deferred()
	started := make(chan struct{})
	blocking := asyncErr(func() error {
		_, err := opened.root.Commit(testContext, func(durable.Tx) (any, error) {
			close(started)
			return nil, release.wait(context.Background())
		})
		return err
	})
	<-started
	var reading *future[*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]]
	queueOnLine(t, opened.harness, func() <-chan struct{} {
		reading = async(func() (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
			return runtime.GetTask(testContext, id)
		})
		return reading.done
	})
	if settled(reading.done) {
		t.Fatal("GetTask returned while a job held the Session line")
	}
	release.resolve()
	if _, err := blocking.wait(); err != nil {
		t.Fatal(err)
	}
	record, err := reading.wait()
	if err != nil || record == nil || record.Id != id || record.Kind != "test.get-task" {
		t.Fatalf("GetTask = %+v, %v; want the record of task %d", record, err, id)
	}
	if missing, err := runtime.GetTask(testContext, id+100); err != nil || missing != nil {
		t.Fatalf("GetTask of an absent task = %+v, %v; want nil", missing, err)
	}

	hold.resolve()
	tkWaitOutcome(t, opened.harness, id)
	eventually(t, func() bool {
		_, err = runtime.GetTask(testContext, id)
		return err != nil
	})
	tkContainsError(t, err, "invocation has ended")
	if err := opened.harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}
