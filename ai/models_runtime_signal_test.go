package ai

import (
	"context"
	"errors"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// Completed refresh signals that no collection has freed yet follow their caller's cancellation through one forwarder per caller, not one goroutine each: with no GC cycle, a print run's exit started a goroutine for every refresh of the run (82 in test/perfgate TestPrintReadToolsProcessGate).
func TestModelsRefreshSignalsForwardCancellationWithoutAGoroutineEach(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	const refreshes = 64
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(context.Canceled)
	models := CreateModels()
	signals := map[context.Context]struct{}{} // A refresh's offline and network phases share its signal.
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(refresh RefreshModelsContext) error {
		signals[refresh.Signal] = struct{}{}
		return nil
	}}))
	for range refreshes {
		if result := models.Refresh(ctx); result.Aborted || len(result.Errors) != 0 {
			t.Fatalf("result=%+v", result)
		}
	}
	models.operations.Wait()
	if len(signals) != refreshes {
		t.Fatalf("%d refresh signals, want %d", len(signals), refreshes)
	}
	// Take the Done channels first: a signal syncs with its caller whenever it is read, so only a channel taken before the cancellation shows that the forwarder closes it.
	dones := map[context.Context]<-chan struct{}{}
	for signal := range signals {
		dones[signal] = signal.Done()
	}
	cause := errors.New("caller cancelled")
	before := goroutinesCreated()
	cancel(cause)
	if created := goroutinesCreated() - before; created > 1 {
		t.Errorf("cancelling the caller of %d completed refreshes started %d goroutines, want at most 1", refreshes, created)
	}
	for signal, done := range dones {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("a completed refresh signal did not follow its caller")
		}
		if context.Cause(signal) != cause { //nolint:errorlint // AbortSignal.any preserves the identical reason.
			t.Fatalf("signal cause=%v", context.Cause(signal))
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for refreshForwarderFor(ctx.Done()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("a forwarder outlived its cancelled caller")
		}
		time.Sleep(time.Millisecond)
	}
}

// A forwarder lives only while a signal it forwards to does: once the collector frees every signal of a caller that is never cancelled, the forwarder and its registration on the caller go.
func TestModelsRefreshForwarderReleasedWithItsSignals(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	models := CreateModels()
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "dynamic", refreshModels: func(RefreshModelsContext) error { return nil }}))
	for range 8 {
		if result := models.Refresh(ctx); result.Aborted || len(result.Errors) != 0 {
			t.Fatalf("result=%+v", result)
		}
	}
	models.operations.Wait()
	if refreshForwarderFor(ctx.Done()) == nil {
		t.Fatal("no forwarder for the caller of live refresh signals")
	}
	deadline := time.Now().Add(10 * time.Second)
	for refreshForwarderFor(ctx.Done()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the forwarder outlived every refresh signal it forwards to")
		}
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
}

func refreshForwarderFor(done <-chan struct{}) *refreshForwarder {
	refreshForwarders.Lock()
	defer refreshForwarders.Unlock()
	return refreshForwarders.byDone[done]
}
