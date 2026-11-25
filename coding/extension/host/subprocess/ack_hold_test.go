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
		hold.applied(false)
		if *acknowledged != 1 {
			t.Fatalf("the applied call left the invocation acknowledged %d times, want 1", *acknowledged)
		}
		hold.applied(false)
		hold.release()
		if *acknowledged != 1 {
			t.Fatalf("later applied calls and the release acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("the call applied before the report is read", func(t *testing.T) {
		hold, acknowledged := newRequest()
		hold.announce()
		hold.applied(false)
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
		hold.applied(false)
		if *acknowledged != 0 {
			t.Fatal("the first of two announced calls released the acknowledgment")
		}
		hold.applied(false)
		if *acknowledged != 1 {
			t.Fatalf("both calls applied and the invocation was acknowledged %d times, want 1", *acknowledged)
		}
	})
}

// A runtime in RPC mode reports a handler's suspension after the calls the handler made, which the host applies later on a lane. The read loop counts each call
// frame it reads, so the acknowledgment the report would have made waits for the calls sent before it, with no report announcing them.
func TestBlockedAcknowledgmentWaitsForTheCallsReadBeforeIt(t *testing.T) {
	newRequest := func() (*ackHold, *int) {
		count := new(int)
		return ackHoldOf(withAckHold(invocation.WithAcknowledgment(t.Context(), func() { *count++ }))), count
	}
	t.Run("the call applies after the report is read", func(t *testing.T) {
		hold, acknowledged := newRequest()
		_, _ = hold.sent()
		acknowledgeBlocked(hold.ctx)
		if *acknowledged != 0 {
			t.Fatal("the blocked report acknowledged the invocation before the call sent ahead of it applied")
		}
		hold.applied(true)
		if *acknowledged != 1 {
			t.Fatalf("the applied call left the invocation acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("the call applied before the report is read", func(t *testing.T) {
		hold, acknowledged := newRequest()
		_, _ = hold.sent()
		hold.applied(true)
		acknowledgeBlocked(hold.ctx)
		if *acknowledged != 1 {
			t.Fatalf("a report whose call applied acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("a progress report after the first of two calls", func(t *testing.T) {
		hold, acknowledged := newRequest()
		_, _ = hold.sent()
		_, _ = hold.sent()
		acknowledgeBlocked(hold.ctx)
		hold.applied(true)
		hold.wentOn()
		if *acknowledged != 0 {
			t.Fatal("a progress report released the acknowledgment while the second call had not applied")
		}
		hold.applied(true)
		if *acknowledged != 1 {
			t.Fatalf("both calls applied and the invocation was acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("an announced call that never comes", func(t *testing.T) {
		hold, acknowledged := newRequest()
		hold.announce()
		acknowledgeBlocked(hold.ctx)
		hold.wentOn()
		if *acknowledged != 1 {
			t.Fatalf("a handler that went on past the report left the invocation acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("the request ends before a counted call applies", func(t *testing.T) {
		hold, acknowledged := newRequest()
		_, _ = hold.sent()
		acknowledgeBlocked(hold.ctx)
		hold.release()
		if *acknowledged != 1 {
			t.Fatalf("a request that ended left the invocation acknowledged %d times, want 1", *acknowledged)
		}
		hold.applied(true)
		if *acknowledged != 1 {
			t.Fatalf("a call applied after the end acknowledged the invocation again: %d", *acknowledged)
		}
	})
}

// A Node runtime outside RPC mode reports a handler's wait on each host call it makes, before the call, and the handler makes its calls in one turn: notify, then setStatus. The
// first call can apply before the read loop read the second, so the acknowledgment waits for the handler's progress report, which was written after the whole turn, as well as
// for the calls.
func TestBlockedAcknowledgmentOfAnEffectCallWaitsForTheHandlersProgress(t *testing.T) {
	newRequest := func() (*ackHold, *int) {
		count := new(int)
		return ackHoldOf(withAckHold(invocation.WithAcknowledgment(t.Context(), func() { *count++ }))), count
	}
	t.Run("the first call applied before the second was read", func(t *testing.T) {
		hold, acknowledged := newRequest()
		hold.announce()
		_, _ = hold.sent()
		acknowledgeBlocked(hold.ctx)
		hold.applied(true)
		if *acknowledged != 0 {
			t.Fatal("the first effect call applied and released the acknowledgment although the handler's next call was not read")
		}
		hold.announce()
		_, _ = hold.sent()
		hold.wentOn()
		if *acknowledged != 0 {
			t.Fatal("the progress report released the acknowledgment while the second call had not applied")
		}
		hold.applied(true)
		if *acknowledged != 1 {
			t.Fatalf("both calls applied and the progress was read: acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("the calls applied before the progress was read", func(t *testing.T) {
		hold, acknowledged := newRequest()
		hold.announce()
		_, _ = hold.sent()
		acknowledgeBlocked(hold.ctx)
		hold.applied(true)
		hold.wentOn()
		if *acknowledged != 1 {
			t.Fatalf("an applied call and the handler's progress left the invocation acknowledged %d times, want 1", *acknowledged)
		}
	})
	t.Run("the call is one the handler awaits", func(t *testing.T) {
		hold, acknowledged := newRequest()
		hold.announce()
		_, _ = hold.sent()
		acknowledgeBlocked(hold.ctx)
		hold.applied(false)
		if *acknowledged != 1 {
			t.Fatalf("an applied call the handler awaits left the invocation acknowledged %d times, want 1", *acknowledged)
		}
	})
}
