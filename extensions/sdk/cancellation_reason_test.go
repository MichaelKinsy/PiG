package sdk

import "testing"

// The cancel frame names why the host cancelled a request; the Python and Rust SDKs return it from cancellation_reason and
// Node aborts the request's signal with it. Go returns it from CancellationReason, and "" for a request nobody cancelled
// and for a cancel frame that names no reason.
func TestCancellationReasonIsTheHostsCancelFrameReason(t *testing.T) {
	for _, tc := range []struct{ name, reason string }{
		{name: "named", reason: "context canceled"},
		{name: "unnamed", reason: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := New("cancel-reason")
			type seen struct {
				before string
				after  string
				err    error
			}
			got := make(chan seen, 1)
			started := make(chan struct{})
			ext.Command("wait", "", func(ctx Context, _ string) error {
				before := ctx.CancellationReason()
				close(started)
				<-ctx.Done()
				got <- seen{before: before, after: ctx.CancellationReason(), err: ctx.Err()}
				return nil
			})
			host, _, done := surfaceHost(t, ext, nil)
			defer surfaceShutdown(t, host, done)
			host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-wait", Request: &requestMsg{Method: "command", Tool: "wait"}})
			<-started
			host.writeEnvelope(t, envelope{Type: msgCancel, ID: "req-wait", Cancel: &cancelMsg{RequestID: "req-wait", Reason: tc.reason}})
			result := <-got
			if result.before != "" || result.after != tc.reason || result.err == nil {
				t.Fatalf("reason before = %q, after = %q, err = %v; want \"\", %q and the context's error", result.before, result.after, result.err, tc.reason)
			}
		})
	}
}
