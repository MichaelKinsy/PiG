package extensionconformance

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestConformance_SuspendedCommandFlush checks the RPC stdin-end checkpoint for every SDK runtime that has no microtask continuation. Pi has only JavaScript extensions and its shutdown awaits one stdout write callback (rpc-mode.ts:728-744, output-guard.ts:105-108). A command that is still running at that checkpoint, here one waiting on a host call with no dialog, counts as suspended, so the flush stops joining it and does not hang. The host reports the suspension without an SDK message, and it must not cancel the command.
func TestConformance_SuspendedCommandFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := append([]harnessCase{}, sdkHarnessCases()...)
	for _, language := range []string{"go", "rust", "python"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		if tc.name == "subprocess-node" || tc.name == "subprocess-node-packed" {
			// A Node runtime reports its own suspension after one event-loop turn; the RPC shutdown tests in cmd/pig compare it with Pi.
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				h.host.Shutdown("test done")
			})
			release := make(chan struct{})
			entered := make(chan struct{})
			h.bridge.SetHostAction("waitForIdle", func(ctx context.Context) error {
				extension.CallInitiated(ctx)
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
				return nil
			})
			suspended := make(chan int, 16)
			h.host.SetCommandSuspendHandler(func(n int) { suspended <- n })
			done := make(chan bool, 1)
			go func() { done <- h.runner.ExecuteCommand(t.Context(), "liveness_host_call", "") }()
			budget := testbudget.Wait(t)
			select {
			case <-entered:
			case <-time.After(budget):
				t.Fatal("the command never reached its host call")
			}
			h.host.FlushCommands()
			deadline := time.After(budget)
			for got := 0; got != 1; {
				select {
				case got = <-suspended:
				case <-deadline:
					t.Fatal("the checkpoint never reported the running command as suspended")
				}
			}
			select {
			case <-done:
				t.Fatal("the checkpoint completed the command")
			default:
			}
			close(release)
			select {
			case ok := <-done:
				if !ok {
					t.Fatal("the command was not found")
				}
			case <-time.After(budget):
				t.Fatal("the command did not finish after its host call returned")
			}
		})
	}
}
