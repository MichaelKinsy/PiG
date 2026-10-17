package ai

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

// forwardEvents ends its iteration, on the done delivery and on a failed push, so the iteration's one cancellation registration on a cancelable context is released.
// The turn loop it replaces released it when its range loop ended; a forwarder's own context never cancels (context.WithoutCancel), so only a caller's cancelable context shows the difference.
func TestForwardEventsReleasesTheIterationsCancellation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		push func(AssistantMessageEvent) error
		fail bool
	}{
		{"the stream ends", func(AssistantMessageEvent) error { return nil }, false},
		{"a push fails", func(AssistantMessageEvent) error { return errors.New("push failed") }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				executor := &continuationExecutor{}
				base, cancel := context.WithCancel(context.WithValue(context.Background(), continuationExecutorKey{}, executor))
				defer cancel()
				source := NewAssistantMessageEventStream()
				ctx := &afterFuncCountingContext{armed: make(chan struct{}, 8)}
				partial := lazyProbeMessage(StopReasonPending)
				go func() {
					waitForStreamWaiters(t, source, 1)
					_ = source.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: partial})
					if tc.fail {
						return
					}
					waitForStreamWaiters(t, source, 1)
					_ = source.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
					source.End()
				}()
				var err error
				executor.run(func(turn *continuationTurn) {
					ctx.Context = context.WithValue(base, continuationTurnKey{}, turn)
					_, err = forwardEvents(turn, ctx, source, tc.push)
				})
				if (err != nil) != tc.fail {
					t.Fatalf("forwardEvents error = %v, want failure %t", err, tc.fail)
				}
				if registered := len(ctx.armed); registered != 1 {
					t.Fatalf("the iteration armed %d cancellations, want 1", registered)
				}
				if live := ctx.live.Load(); live != 0 {
					t.Fatalf("%d cancellations stayed armed after forwardEvents returned", live)
				}
			})
		})
	}
}
