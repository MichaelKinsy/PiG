package extensionconformance

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's ctx.abort() reaches agent.abort() synchronously (agent-session.ts:2464-2474, 3472-3478), which aborts the active run's AbortController before it returns
// (agent.ts:340-342), so ctx.signal, read again right after the abort, is aborted in the same turn: `const held = ctx.signal; ctx.abort(); held.aborted` is true.
// A guard that aborts and then checks the signal without yielding takes the early-return branch. Every realization's signal-abort command holds the signal,
// aborts the run and reports the state of the held signal and of ctx.signal afterwards. The state a realization falls back to without the call is "live" (an abort the
// signal never saw) or "none" (no signal at all), so neither matches. The host's abort cancels the run's context, as agent.abort() does, and the rounds repeat so a
// frame that races the abort's reply fails some round.
func TestContextAbortAbortsTheRunSignalInTheSameTickInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := allHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	const rounds = 20
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var run runControl
			h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetSignal: run.current, Abort: run.abort}, nil)
			changed := func() {}
			if h.bridge != nil {
				h.bridge.SetHostAction("getSignal", run.current)
				h.bridge.SetHostAction("abort", run.abort)
				changed = h.bridge.RunSignalChanged
			}
			command, ok := findCommand(h.runner, "signal-abort")
			if !ok {
				t.Fatal("signal-abort not registered")
			}
			for round := range rounds {
				run.begin(t, changed)
				h.ui.ClearRecorded()
				cc := h.runner.CreateCommandContext()
				ctx := extension.WithCommandContext(extension.WithContext(context.Background(), cc.Context), cc)
				if err := command.Handler(ctx, ""); err != nil {
					t.Fatalf("round %d: signal-abort: %v", round, err)
				}
				if got := h.ui.Recorded(); !slices.Equal(got, []string{"abort:aborted:aborted:info"}) {
					t.Fatalf("round %d: signal-abort = %v, want the held signal and ctx.signal aborted by the time ctx.abort() returned", round, got)
				}
				run.end(changed)
			}
		})
	}
}
