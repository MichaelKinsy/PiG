package extensionconformance

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi gives an extension `ctx.signal` as `runner.getSignalFn()`, which AgentSession binds to `() => this.agent.signal` (runner.ts:917-920, agent-session.ts:3368, agent.ts:336-338):
// the signal of the current run, undefined while no run is active. It is the run's, not the handler request's: aborting the run aborts it while a handler that holds it is still in flight, and a handler sees the same object for the whole run.
// Every realization reports the same states through the fixtures' signal-probe and signal-wait commands.
func TestContextSignalIsTheActiveRunsSignalInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := allHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
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
			var mu sync.Mutex
			var signal context.Context
			current := func() context.Context {
				mu.Lock()
				defer mu.Unlock()
				return signal
			}
			setRun := func(run context.Context) {
				mu.Lock()
				defer mu.Unlock()
				signal = run
			}
			h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetSignal: current}, nil)
			if h.bridge != nil {
				h.bridge.SetHostAction("getSignal", current)
			}
			run := func(name string) {
				t.Helper()
				command, ok := findCommand(h.runner, name)
				if !ok {
					t.Fatalf("%s not registered", name)
				}
				cc := h.runner.CreateCommandContext()
				ctx := extension.WithCommandContext(extension.WithContext(context.Background(), cc.Context), cc)
				if err := command.Handler(ctx, ""); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
			}
			probe := func(want string) {
				t.Helper()
				h.ui.ClearRecorded()
				run("signal-probe")
				if got := h.ui.Recorded(); !slices.Equal(got, []string{"signal:" + want + ":info"}) {
					t.Fatalf("signal-probe = %v, want signal:%s", got, want)
				}
			}

			probe("none") // idle: no run, no signal.

			first, abortFirst := context.WithCancel(context.Background())
			defer abortFirst()
			setRun(first)
			probe("live")

			// The abort reaches a handler that is still in flight: its own request is never cancelled.
			h.ui.ClearRecorded()
			done := make(chan struct{})
			go func() {
				defer close(done)
				run("signal-wait")
			}()
			waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "wait:start:info") })
			abortFirst()
			<-done
			if got := h.ui.Recorded(); !slices.Equal(got, []string{"wait:start:info", "wait:aborted:info"}) {
				t.Fatalf("signal-wait = %v, want start then aborted", got)
			}
			probe("aborted") // the run's signal stays the aborted one until the run ends.

			setRun(nil) // the run ended.
			probe("none")

			second := t.Context()
			setRun(second)
			probe("live") // a new run has its own, unaborted signal.
		})
	}
}

// Pi's `ctx.signal` is a getter over `agent.signal` (runner.ts:917-920, agent.ts:336-338): a timer that reads it between requests sees the run that exists at that moment.
// A run that begins is visible to a handler already in flight, and a run that ends is gone from it, with no request reaching the runtime in between. The owner reports each change with UIBridge.RunSignalChanged.
// The fixtures' signal-poll command reads ctx.signal from a loop until it has the state asked for.
func TestContextSignalFollowsTheRunBetweenRequestsInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := allHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
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
			var mu sync.Mutex
			var signal context.Context
			current := func() context.Context {
				mu.Lock()
				defer mu.Unlock()
				return signal
			}
			setRun := func(run context.Context) {
				mu.Lock()
				signal = run
				mu.Unlock()
				if h.bridge != nil {
					h.bridge.RunSignalChanged()
				}
			}
			h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetSignal: current}, nil)
			if h.bridge != nil {
				h.bridge.SetHostAction("getSignal", current)
			}
			command, ok := findCommand(h.runner, "signal-poll")
			if !ok {
				t.Fatal("signal-poll not registered")
			}
			// poll starts the handler and returns once it is in flight; wait returns what it reported.
			poll := func(state string) (wait func() []string) {
				h.ui.ClearRecorded()
				done := make(chan struct{})
				go func() {
					defer close(done)
					cc := h.runner.CreateCommandContext()
					ctx := extension.WithCommandContext(extension.WithContext(context.Background(), cc.Context), cc)
					if err := command.Handler(ctx, state); err != nil {
						t.Errorf("signal-poll %s: %v", state, err)
					}
				}()
				waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "poll:start:info") })
				return func() []string {
					<-done
					return h.ui.Recorded()
				}
			}

			// A run begins while the handler is in flight.
			wait := poll("live")
			first := t.Context()
			setRun(first)
			if got := wait(); !slices.Equal(got, []string{"poll:start:info", "poll:live:info"}) {
				t.Fatalf("a run began during the handler: %v, want it to see the signal", got)
			}

			// The run ends while the handler is in flight, as `setTimeout(() => ctx.signal)` sees after agent_end.
			wait = poll("none")
			setRun(nil)
			if got := wait(); !slices.Equal(got, []string{"poll:start:info", "poll:none:info"}) {
				t.Fatalf("the run ended during the handler: %v, want it to see no signal", got)
			}

			// The next run is a new signal, again with no request in between.
			wait = poll("live")
			setRun(t.Context())
			if got := wait(); !slices.Equal(got, []string{"poll:start:info", "poll:live:info"}) {
				t.Fatalf("the next run began during the handler: %v, want it to see the signal", got)
			}
		})
	}
}
