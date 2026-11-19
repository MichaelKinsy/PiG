package extensionconformance

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestConformance_UnawaitedEventDoesNotWaitForAWaitingHandler pins what Pi's `void this._extensionRunner.emit(event)` does for session_info_changed
// (agent-session.ts:4122) when the first handler awaits a socket read (runner.ts:1110): the caller goes on at the handler's first await, and the
// handler's own effect, its notification, follows when its wait ends. The test emits the way Session.dispatchUnawaitedEvent does (an acknowledgment the
// caller waits for), in every SDK and in the isolated, packed and fused realizations.
func TestConformance_UnawaitedEventDoesNotWaitForAWaitingHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			runConformanceCommandArgs(t, h, "io_wait_name", peer.addr())
			h.ui.ClearRecorded()

			acknowledged := make(chan struct{})
			ctx := invocation.WithAcknowledgment(t.Context(), func() { close(acknowledged) })
			done := make(chan error, 1)
			go func() {
				_, err := h.runner.Emit(ctx, extension.SessionInfoChangedEvent{Type: "session_info_changed", Name: "waited"})
				done <- err
			}()
			conn := peer.accept()
			select {
			case <-acknowledged:
			case err := <-done:
				t.Fatalf("the emission ended before the caller was released: %v", err)
			case <-time.After(releaseBound):
				t.Fatal("the caller of an unawaited event was not released while the first handler waits on a socket read")
			}
			select {
			case err := <-done:
				t.Fatalf("releasing the caller ended the handler: %v", err)
			default:
			}
			if got := slices.Contains(h.ui.Recorded(), "session_info_changed:waited:info"); got {
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
			waitFor(t, func() bool { return strings.Join(h.ui.Recorded(), ",") == "session_info_changed:waited:info" })
		})
	}
}
