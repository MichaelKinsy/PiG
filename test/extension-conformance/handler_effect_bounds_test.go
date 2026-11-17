package extensionconformance

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// effectOrderHarnessCases is every SDK realization the effect-order rows run on, isolated and packed.
func effectOrderHarnessCases() []harnessCase {
	return append(sdkHarnessCases(), packedSDKHarnessCases()...)
}

// forEachEffectOrderHarness runs body on a fresh harness for every SDK, in both the TUI and the RPC mode, with the event_probe_on probe subscribed to thinking_level_select. The harness is shut
// down by cleanup, which body may call itself.
func forEachEffectOrderHarness(t *testing.T, body func(t *testing.T, h *harness, cleanup func())) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Cleanup(func() { rpcRowMode = "" })
	for _, mode := range []string{"tui", "rpc"} {
		rpcRowMode = mode
		for _, tc := range effectOrderHarnessCases() {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				h := tc.make(t)
				var once sync.Once
				cleanup := func() {
					once.Do(func() {
						if h.cleanup != nil {
							h.cleanup()
						}
						h.host.Shutdown("test done")
					})
				}
				t.Cleanup(cleanup)
				runConformanceCommandArgs(t, h, "event_probe_on", "thinking_level_select")
				body(t, h, cleanup)
			})
		}
	}
}

// within fails the test unless ch is closed or receives within the wait budget.
func within[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("%s did not happen within %s", what, testbudget.Wait(t))
		panic("unreachable")
	}
}

// emitThinkingLevelSelect emits thinking_level_select under an acknowledgment, as Session.dispatchUnawaitedEvent does, and returns the channel that receives
// the acknowledgment and the one that receives the emission's result.
func emitThinkingLevelSelect(ctx context.Context, h *harness) (acknowledged chan struct{}, done chan error) {
	acknowledged = make(chan struct{}, 4)
	done = make(chan error, 1)
	ctx = invocation.WithAcknowledgment(ctx, func() {
		select {
		case acknowledged <- struct{}{}:
		default:
		}
	})
	go func() {
		_, err := h.runner.Emit(ctx, extension.ThinkingLevelSelectEvent{Type: "thinking_level_select", Level: "high", PreviousLevel: "off"})
		done <- err
	}()
	return acknowledged, done
}

// goroutinesAfter reports the goroutines left once the count stopped changing, when there are more than baseline.
func goroutinesAfter(t *testing.T, baseline int) {
	t.Helper()
	if now := hybridSettledGoroutines(); now > baseline {
		buf := make([]byte, 1<<20)
		t.Errorf("goroutines = %d after the session ended, %d before it started\n%s", now, baseline, buf[:runtime.Stack(buf, true)])
	}
}

// TestConformance_HandlerEffectWaitEndsWithTheSession pins what bounds a caller's wait for a handler's effects: the effect is the consumer's to apply, so a consumer that blocks holds the
// caller as Pi's synchronous call holds it (the call returns when the effect is written); the session's end releases the caller and ends the emission (the runner reports a handler's failure
// and returns no error), and leaves no goroutine behind once the consumer unblocks. The consumer is blocked before the handler's notify applies, so the caller is held behind a queued call.
func TestConformance_HandlerEffectWaitEndsWithTheSession(t *testing.T) {
	forEachEffectOrderHarness(t, func(t *testing.T, h *harness, cleanup func()) {
		baseline := runtime.NumGoroutine()
		gate := newStallGate()
		t.Cleanup(gate.open)
		h.ui.stall.Store(gate)
		acknowledged, done := emitThinkingLevelSelect(context.Background(), h)
		within(t, "the handler's notify reaching the blocked consumer", gate.entered)
		select {
		case <-acknowledged:
			t.Fatal("the caller was released while the handler's notify was blocked in the consumer")
		case err := <-done:
			t.Fatalf("the emission ended while the handler's notify was blocked in the consumer: %v", err)
		case <-time.After(150 * time.Millisecond):
		}
		shutdown := make(chan struct{})
		go func() {
			h.host.Shutdown("session ended")
			close(shutdown)
		}()
		within(t, "the caller's release at the session's end", acknowledged)
		_ = within(t, "the emission's end at the session's end", done)
		gate.open()
		within(t, "Shutdown after the consumer unblocked", shutdown)
		cleanup()
		goroutinesAfter(t, baseline)
	})
}

// A caller that gives up releases itself: the emission returns while the consumer is blocked, and the blocked call is not left holding it.
func TestConformance_HandlerEffectWaitEndsWithTheCallersContext(t *testing.T) {
	forEachEffectOrderHarness(t, func(t *testing.T, h *harness, cleanup func()) {
		baseline := runtime.NumGoroutine()
		gate := newStallGate()
		t.Cleanup(gate.open)
		h.ui.stall.Store(gate)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, done := emitThinkingLevelSelect(ctx, h)
		within(t, "the handler's notify reaching the blocked consumer", gate.entered)
		cancel()
		_ = within(t, "the emission's end at the caller's cancel", done)
		gate.open()
		cleanup()
		goroutinesAfter(t, baseline)
	})
}

// TestConformance_HandlerEffectsPrecedeTheReleaseUnderLoad repeats the unawaited event 200 times, one after the other, while other callers keep the same connection busy with commands: every release
// of the caller follows the handler's effect of its own emission, never an earlier one's, with the consumer slow to apply.
func TestConformance_HandlerEffectsPrecedeTheReleaseUnderLoad(t *testing.T) {
	const emits = 200
	forEachEffectOrderHarness(t, func(t *testing.T, h *harness, cleanup func()) {
		h.ui.applyDelay.Store(int64(200 * time.Microsecond))
		stop := make(chan struct{})
		var busy sync.WaitGroup
		var commands atomic.Int64
		for range 3 {
			busy.Go(func() {
				cmd, ok := findCommand(h.runner, "status")
				if !ok {
					return
				}
				for {
					select {
					case <-stop:
						return
					default:
					}
					if err := cmd.Handler(context.Background(), ""); err == nil {
						commands.Add(1)
					}
				}
			})
		}
		defer func() {
			close(stop)
			busy.Wait()
		}()
		count := func() int {
			return len(slices.DeleteFunc(h.ui.Recorded(), func(s string) bool { return s != "event_probe_on:thinking_level_select:info" }))
		}
		for i := 1; i <= emits; i++ {
			acknowledged, done := emitThinkingLevelSelect(context.Background(), h)
			within(t, "the caller's release", acknowledged)
			if got := count(); got != i {
				t.Fatalf("emission %d released its caller with %d of its effects applied, want %d", i, got, i)
			}
			if err := within(t, "the emission's end", done); err != nil {
				t.Fatalf("emission %d: %v", i, err)
			}
		}
		if commands.Load() == 0 {
			t.Error("no command ran beside the emissions: the load did not run")
		}
	})
}
