package subprocess

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// A runtime reports a handler's suspension on a host call before it sends the call. The acknowledgment that report makes waits for the call the report
// announced, so a caller that goes on at the acknowledgment (the UI prompt drain releasing a dialog's answer) finds the handler's effect applied.
func TestBlockedAcknowledgmentWaitsForTheAnnouncedHostCall(t *testing.T) {
	newRequest := func() (*ackHold, *int) {
		count := new(int)
		return ackHoldOf(withAckHold(invocation.WithAcknowledgment(t.Context(), func() { *count++ }))), count
	}
	t.Run("the call applies after the report is read", func(t *testing.T) {
		hold, acknowledged := newRequest()
		hold.announce()
		acknowledgeBlocked(hold.ctx)
		if *acknowledged != 0 {
			t.Fatal("the blocked report acknowledged the invocation before the call it announced applied")
		}
		hold.applied()
		if *acknowledged != 1 {
			t.Fatalf("the applied call left the invocation acknowledged %d times, want 1", *acknowledged)
		}
		hold.applied()
		hold.release()
		if *acknowledged != 1 {
			t.Fatalf("later applied calls and the release acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("the call applied before the report is read", func(t *testing.T) {
		hold, acknowledged := newRequest()
		hold.announce()
		hold.applied()
		acknowledgeBlocked(hold.ctx)
		if *acknowledged != 1 {
			t.Fatalf("a report whose call applied acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("a report that announced no call", func(t *testing.T) {
		hold, acknowledged := newRequest()
		acknowledgeBlocked(hold.ctx)
		if *acknowledged != 1 {
			t.Fatalf("a report that announced no call acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("the handler goes on without the call", func(t *testing.T) {
		hold, acknowledged := newRequest()
		hold.announce()
		acknowledgeBlocked(hold.ctx)
		hold.release()
		if *acknowledged != 1 {
			t.Fatalf("a handler that went on past the report left the invocation acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("two announced calls", func(t *testing.T) {
		hold, acknowledged := newRequest()
		hold.announce()
		hold.announce()
		acknowledgeBlocked(hold.ctx)
		hold.applied()
		if *acknowledged != 0 {
			t.Fatal("the first of two announced calls released the acknowledgment")
		}
		hold.applied()
		if *acknowledged != 1 {
			t.Fatalf("both calls applied and the invocation was acknowledged %d times, want 1", *acknowledged)
		}
	})
}
