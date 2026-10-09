package ai

import (
	"context"
	"testing"
)

// TestLazyAPICancelDeferredCapabilityForwardsAfterLoading follows packages/ai/src/api/lazy.ts:89-93: a lazyApi declared with
// cancelDeferred exposes cancelDeferred (and not fetchDeferred), loads on the first call, forwards the model, handle and
// options, and throws "API cannot cancel deferred responses" when the loaded API has no cancelDeferred.
func TestLazyAPICancelDeferredCapabilityForwardsAfterLoading(t *testing.T) {
	model := &Model{ID: "model-a", ProviderMeta: ProviderMetadata{API: "api-a", ProviderID: "mixed"}}
	handle := DeferredHandle{Provider: "mixed", ModelID: "model-a", API: "api-a", ID: "response-1"}
	var cancelled []DeferredHandle
	loads := 0
	streams := recordingProviderStreams("deferred", nil)
	streams.CancelDeferred = func(_ context.Context, got *Model, h DeferredHandle, _ DeferredCancelOptions) error {
		if got != model {
			t.Errorf("cancel model = %v", got)
		}
		cancelled = append(cancelled, h)
		return nil
	}
	api := LazyAPI(func(context.Context) (*ProviderStreams, error) { loads++; return streams, nil }, LazyAPICapabilities{CancelDeferred: true})
	if api.CancelDeferred == nil || api.FetchDeferred != nil || loads != 0 {
		t.Fatalf("capabilities: cancel=%v fetch=%v loads=%d", api.CancelDeferred != nil, api.FetchDeferred != nil, loads)
	}
	if err := api.CancelDeferred(t.Context(), model, handle, DeferredCancelOptions{}); err != nil {
		t.Fatal(err)
	}
	if loads != 1 || len(cancelled) != 1 || cancelled[0] != handle {
		t.Fatalf("loads=%d cancelled=%v", loads, cancelled)
	}

	without := LazyAPI(func(context.Context) (*ProviderStreams, error) { return recordingProviderStreams("plain", nil), nil }, LazyAPICapabilities{CancelDeferred: true})
	if err := without.CancelDeferred(t.Context(), model, handle, DeferredCancelOptions{}); err == nil || err.Error() != "API cannot cancel deferred responses" {
		t.Fatalf("err = %v", err)
	}
}
