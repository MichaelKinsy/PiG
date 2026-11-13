package ai

import (
	"context"
	"errors"
	"testing"
	"time"
)

// packages/ai/src/api/lazy.ts:131-136: cancelDeferred awaits load() inside the call, so a cancel issued while the module is still loading is neither dropped nor answered early: the call waits for the load, forwards the model, handle and options to the loaded cancelDeferred, and surfaces what that returns.
func TestLazyAPICancelDeferredIssuedDuringLoadWaitsForTheLoadThenForwardsAndReportsItsError(t *testing.T) {
	model := &Model{ID: "model-a", ProviderMeta: ProviderMetadata{API: "api-a", ProviderID: "mixed"}}
	handle := DeferredHandle{Provider: "mixed", ModelID: "model-a", API: "api-a", ID: "response-1"}
	refused := errors.New("provider refused the cancel")
	var cancelled []DeferredHandle
	streams := recordingProviderStreams("deferred", nil)
	streams.CancelDeferred = func(_ context.Context, _ *Model, h DeferredHandle, _ DeferredCancelOptions) error {
		cancelled = append(cancelled, h)
		return refused
	}
	loading := make(chan struct{})
	release := make(chan struct{})
	api := LazyAPI(func(context.Context) (*ProviderStreams, error) {
		close(loading)
		<-release
		return streams, nil
	}, LazyAPICapabilities{CancelDeferred: true})
	returned := make(chan error, 1)
	go func() { returned <- api.CancelDeferred(t.Context(), model, handle, DeferredCancelOptions{}) }()
	<-loading
	select {
	case err := <-returned:
		t.Fatalf("cancel returned %v before the load settled", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-returned:
		if !errors.Is(err, refused) {
			t.Fatalf("cancel err = %v, want the loaded implementation's error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel never settled after the load finished")
	}
	if len(cancelled) != 1 || cancelled[0] != handle {
		t.Fatalf("forwarded cancels = %v, want exactly one for %v", cancelled, handle)
	}
}
