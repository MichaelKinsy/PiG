package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// awaitWithContext (chord context/index.ts:98-116) rejects a caller whose signal is already aborted before it looks at
// the awaited Promise, so runtime.agent() with a cancelled caller rejects even when the phase's agent has resolved
// (scheduler.ts:1079-1080). A select with both cases ready chooses at random; the repeated calls make a random choice
// fail with near certainty.
func TestTaskRuntimeAgentRejectsAnAlreadyCancelledCallerAfterResolution(t *testing.T) {
	const calls = 64
	var resolvedErr error
	failures := 0
	agent := tkOneStep("test.agent-cancelled-after-resolution", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		if _, err := runtime.Agent(ctx); err != nil {
			resolvedErr = err
			return err
		}
		for range calls {
			caller, cancel := context.WithCancelCause(ctx)
			cancel(errors.New("caller gone"))
			if _, err := runtime.Agent(caller); err == nil || err.Error() != "caller gone" {
				failures++
			}
		}
		return tkComplete(ctx, runtime)
	})
	opened := tkOpenRoot(t, []durable.AnyTask{agent})
	tkWaitOutcome(t, opened.harness, tkStart(t, opened.root, agent))
	if resolvedErr != nil {
		t.Fatal(resolvedErr)
	}
	if failures != 0 {
		t.Fatalf("%d of %d agent() calls with a cancelled caller resolved, want all rejected with the caller's cause", failures, calls)
	}
	mustClose(t, opened.harness)
}
