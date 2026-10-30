package extensionconformance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// makeInprocOwnerHarness is the in-process reference: a factory that owns thinking runs while it loads and changes that through the
// API it kept, with the same two commands every SDK fixture registers.
func makeInprocOwnerHarness(t *testing.T) *harness {
	t.Helper()
	runtime := extension.CreateExtensionRuntime()
	loaded, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		pi.OwnThinkingRuns(true)
		pi.RegisterCommand("thinking_runs_off", extension.CommandOptions{Handler: func(context.Context, string) error { pi.OwnThinkingRuns(false); return nil }})
		pi.RegisterCommand("thinking_runs_on", extension.CommandOptions{Handler: func(context.Context, string) error { pi.OwnThinkingRuns(true); return nil }})
		return nil
	}, t.TempDir(), extension.CreateEventBus(), runtime, "inproc-owner")
	if err != nil {
		t.Fatal(err)
	}
	return &harness{runner: inproc.NewRunner([]extension.Extension{loaded}, t.TempDir(), runtime)}
}

// pig additive (D214): pi.ownThinkingRuns(enabled) declares at load, travels in the register frame, and changes later through a host
// call that applies in call order. The fallback of an SDK that never makes either is "not owned", so the load value (true) and
// each change (false, then true) distinguish a working SDK from a defaulting one.
func TestConformance_OwnThinkingRunsAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	cases := append([]harnessCase{{name: "inproc-go", make: makeInprocOwnerHarness}}, append(sdkHarnessCases(), packedSDKHarnessCases()...)...)
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
			var changes atomic.Int32
			stop := h.runner.ObserveThinkingRuns(func() { changes.Add(1) })
			defer stop()
			// The load value rode in the register frame (or the factory body): owned before any call.
			if !h.runner.OwnsThinkingRuns() {
				t.Fatal("an extension that declared ownership while loading does not own thinking runs")
			}
			run := func(command string) {
				t.Helper()
				if !h.runner.ExecuteCommand(context.Background(), command, "") {
					t.Fatalf("fixture has no %s command", command)
				}
			}
			run("thinking_runs_off")
			pollUntilConformance(t, "thinking_runs_off: the host still sees ownership", func() bool { return !h.runner.OwnsThinkingRuns() })
			run("thinking_runs_on")
			pollUntilConformance(t, "thinking_runs_on: the host does not see ownership", func() bool { return h.runner.OwnsThinkingRuns() })
			// Calls apply in order: off, on, off, on ends owned, with every transition reported.
			for _, command := range []string{"thinking_runs_off", "thinking_runs_on", "thinking_runs_off", "thinking_runs_on"} {
				run(command)
			}
			pollUntilConformance(t, "four calls in order: ownership or its changes missing", func() bool {
				return h.runner.OwnsThinkingRuns() && changes.Load() == 6
			})
		})
	}
}
