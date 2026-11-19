package extensionconformance

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestConformance_InputAndBeforeAgentStartDoNotHoldTheCallerWhileTheyWait pins what RPC mode needs of the `input` and `before_agent_start` events
// (Pi's rpc-mode.ts starts every stdin line without waiting for the one before): while a handler awaits a socket read, the host's caller, given an
// acknowledgment, is released at the handler's first await, the handler's own effect (its notification) follows when its wait ends, and the emission
// ends once the peer answers. The host runs in RPC mode. Every SDK, in the isolated, packed and fused realizations.
func TestConformance_InputAndBeforeAgentStartDoNotHoldTheCallerWhileTheyWait(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	events := []struct {
		name    string
		command string
		notify  string
		emit    func(context.Context, *harness) error
	}{
		{
			name: "input", command: "io_wait_input", notify: "input:waited",
			emit: func(ctx context.Context, h *harness) error {
				_, err := h.runner.Emit(ctx, extension.InputEvent{Type: "input", Text: "waited", Source: "rpc"})
				return err
			},
		},
		{
			name: "before_agent_start", command: "io_wait_start", notify: "before_agent_start:waited",
			emit: func(ctx context.Context, h *harness) error {
				_, err := h.runner.EmitBeforeAgentStart(ctx, extension.UserPromptTrigger("waited", nil), extension.BuildSystemPromptOptions{Cwd: "/w", CustomPrompt: "base"})
				return err
			},
		},
	}
	// RPC mode is where stdin waits on these handlers; the Node runtime reports a handler's wait only there. rpcRowMode is read by every harness factory:
	// this test is not parallel, so it owns the value while it runs.
	rpcRowMode = "rpc"
	t.Cleanup(func() { rpcRowMode = "" })
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, event := range events {
		for _, tc := range cases {
			t.Run(event.name+"/"+tc.name, func(t *testing.T) {
				h := tc.make(t)
				t.Cleanup(func() {
					if h.cleanup != nil {
						h.cleanup()
					}
					h.host.Shutdown("test done")
				})
				budget := testbudget.Wait(t)
				// The caller must be released while the handler waits, so a missing release is a hang: bound it, not the run's budget.
				releaseBound := min(budget, 20*time.Second)
				peer := newIOWaitPeer(t)
				runConformanceCommandArgs(t, h, event.command, peer.addr())
				h.ui.ClearRecorded()

				acknowledged := make(chan struct{})
				ctx := invocation.WithAcknowledgment(t.Context(), func() { close(acknowledged) })
				done := make(chan error, 1)
				go func() { done <- event.emit(ctx, h) }()
				conn := peer.accept()
				select {
				case <-acknowledged:
				case err := <-done:
					t.Fatalf("the emission ended before the caller was released: %v", err)
				case <-time.After(releaseBound):
					t.Fatalf("the caller of an %s event was not released while its handler waits on a socket read", event.name)
				}
				select {
				case err := <-done:
					t.Fatalf("releasing the caller ended the handler: %v", err)
				default:
				}
				if slices.Contains(h.ui.Recorded(), event.notify+":info") {
					t.Fatalf("the handler's notification arrived before its wait ended: %v", h.ui.Recorded())
				}
				if _, err := conn.Write([]byte("go\n")); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(budget):
					t.Fatal("the emission did not end after the peer answered")
				}
				waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), event.notify+":info") })
			})
		}
	}
}
