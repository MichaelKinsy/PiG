package codingagent

import (
	"context"
	"testing"
	"testing/synctest"
)

// Pi's reload steps are awaited calls in one process: what a session_start handler asked of the UI, an extension's setEditorComponent first, has happened when
// the step returns, so the autocomplete rebuild that follows sees the editor. A step's posts reach the loop ahead of its completion, and the loop must not
// return before running them, whichever of the two its select takes: the test leaves it both, every time.
func TestReloadStepRunsTheUITasksItsHandlersQueued(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx := newPhaseShutdownMode(t)
		ran := 0
		for i := 1; i <= 64; i++ {
			// A task queued ahead of the step holds the loop until the step's handler has posted its own and finished, so the loop then finds both the
			// handler's completion and its task ready.
			posted := make(chan struct{})
			if !m.postUITask(func() { <-posted; synctest.Wait() }) {
				t.Fatal("the task was not queued")
			}
			if err := m.awaitReloadStep(ctx, func(context.Context) error {
				if !m.postUITask(func() { ran++ }) {
					t.Error("the task was not queued")
				}
				close(posted)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if ran != i {
				t.Fatalf("after reload step %d the posted UI task had run %d times, want %d: the step returned before the editor install it queued", i, ran, i)
			}
		}
	})
}
