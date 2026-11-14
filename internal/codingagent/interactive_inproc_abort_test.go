package codingagent_test

import (
	"testing"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// An in-process extension's ctx.abort() in interactive mode is the mode's abort handler under every binding of its runner. Pi's Session keeps the handler
// the mode gave it (bindExtensions({ abortHandler }), interactive-mode.ts:2097-2099) and calls it from the abort it binds on each runner
// (agent-session.ts:3597-3603). The Session bound its own RequestAbort over the mode's action whenever it was handed a runner after the mode had bound it, as
// a reload does: the run was aborted, the queued message stayed queued and ran as a turn of its own.
func TestInProcessExtensionAbortRestoresTheQueueUnderEveryRunnerBinding(t *testing.T) {
	for _, binding := range []string{"the mode's binding at startup", "the Session's rebinding after a reload"} {
		t.Run(binding, func(t *testing.T) {
			f := newAbortTextFixture(t, "Request was aborted", 0)
			h := icodingagent.NewTestHarness(t, f.opts, nil)
			runner := h.Runner()
			if binding != "the mode's binding at startup" {
				// A reload hands the Session the runner after the mode bound its context actions (reloadResources).
				f.session.ReplaceRunner(runner)
			}
			h.Do(func() { h.Enter("HOLD the stream") })
			f.await(t, "the held request", f.provider.started)
			h.Do(func() { h.Enter("queued one") })
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				if steering, _ := h.QueuedMessages(); steering == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the message typed while the run streamed was not queued")
				}
				time.Sleep(5 * time.Millisecond)
			}
			extensionContext := runner.CreateCommandContext()
			if err := extensionContext.Abort(); err != nil {
				t.Fatal(err)
			}
			if queued, err := extensionContext.HasPendingMessages(); err != nil || queued {
				t.Errorf("ctx.hasPendingMessages() right after ctx.abort() = %v, %v; want false", queued, err)
			}
			h.WaitIdle(t, testbudget.Wait(t))
			if got := h.EditorText(); got != "queued one" {
				t.Errorf("editor after ctx.abort() = %q, want the queued message", got)
			}
			f.provider.mu.Lock()
			calls := f.provider.calls
			f.provider.mu.Unlock()
			if calls != 1 {
				t.Errorf("the provider received %d requests, want 1: the queued message must not run after the abort", calls)
			}
			if got := f.persistedAbortText(t); got != "Operation aborted" {
				t.Errorf("the session file's aborted entry has errorMessage %q, want %q", got, "Operation aborted")
			}
		})
	}
}
