package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// scheduler.ts:1188-1195 builds the runtime's snapshot and snapshotAsOf (the DocumentReader of types.ts:886) through #read, which throws endedError once
// the invocation has ended (:1282-1285): inside the phase the reads return the committed value, afterwards both reject with "invocation has ended".
// mutation-checked: removing the invocation check from taskRuntime.SnapshotErased, or from SnapshotAsOfErased (scheduler.go), lets the late read succeed.
func TestTaskRuntimeSnapshotReadsRejectAfterTheInvocationEnds(t *testing.T) {
	notes := tkSessionNotes("test.reads-after-end")
	captured := &syncValue[stepRuntime]{}
	inPhase := &syncList[string]{}
	task := tkOneStep("test.reads-after-end", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		captured.put(runtime)
		value, err := runtime.SnapshotErased(ctx, notes)
		if err != nil {
			return err
		}
		inPhase.add(value.Value("text").(string))
		return tkComplete(ctx, runtime)
	})
	opened := tkOpenRoot(t, []durable.AnyTask{task.Erased()})
	tkSetNotes(t, opened.harness, notes, "committed")
	id := tkStart(t, opened.root, task.Erased())
	opened.harness.Resume()
	tkWaitOutcome(t, opened.harness, id)
	if got := inPhase.all(); len(got) != 1 || got[0] != "committed" {
		t.Fatalf("the read inside the phase returned %v, want the committed value", got)
	}
	runtime, _ := captured.get()
	// The end marks the invocation ended before it cancels the runtime signal (scheduler.ts:1100-1107).
	<-runtime.Signal().Done()
	_, err := runtime.SnapshotErased(testContext, notes)
	tkContainsError(t, err, "invocation has ended")
	_, err = runtime.SnapshotAsOfErased(testContext, notes, 1)
	tkContainsError(t, err, "invocation has ended")
	mustClose(t, opened.harness)
}
