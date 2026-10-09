package ai

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// Ports packages/ai/src/api/lazy.ts lazyApi (capability flags fetchDeferred/cancelDeferred at :69-70, the fetchDeferred wrapper at :81-86): the capability flags decide which deferred operations exist, an
// implementation without the operation fails with Pi's text, and a load failure ends the stream with an error result
// carrying the model identity.
// mutation-checked: dropping the reads and writes of LazyAPICapabilities.CancelDeferred, LazyAPICapabilities.FetchDeferred fails it
// Pi: packages/ai/src/types.ts:300 (cancelDeferred)
// Pi: packages/ai/src/types.ts:295 (fetchDeferred)
// packages/ai/src/api/lazy.ts:70,89-90 LazyApiCapabilities.cancelDeferred: cancelDeferred exists on the lazy api only when the capability flag is set.
func TestLazyAPIDeferredCapabilitiesUpstream(t *testing.T) {
	model := &Model{ID: "m", ProviderMeta: ProviderMetadata{ProviderID: "p", API: "lazy-api"}}
	bare := func(context.Context) (*ProviderStreams, error) { return &ProviderStreams{}, nil }

	t.Run("operations without a capability flag are absent", func(t *testing.T) {
		api := LazyAPI(bare, LazyAPICapabilities{})
		if api.FetchDeferred != nil || api.CancelDeferred != nil || api.Stream == nil || api.StreamSimple == nil {
			t.Fatalf("api = %+v", api)
		}
	})
	t.Run("a loaded implementation without fetchDeferred fails the stream", func(t *testing.T) {
		api := LazyAPI(bare, LazyAPICapabilities{FetchDeferred: true, CancelDeferred: true})
		stream, err := api.FetchDeferred(t.Context(), model, DeferredHandle{}, DeferredFetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if result.StopReason != StopReasonError || result.ErrorMessage != "API does not support deferred responses" || result.Provider != "p" || result.Model != "m" || result.API != "lazy-api" {
			t.Fatalf("result = %+v", result)
		}
	})
	t.Run("a loaded implementation without cancelDeferred fails the call", func(t *testing.T) {
		api := LazyAPI(bare, LazyAPICapabilities{CancelDeferred: true})
		if err := api.CancelDeferred(t.Context(), model, DeferredHandle{}, DeferredCancelOptions{}); err == nil || err.Error() != "API cannot cancel deferred responses" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a load failure ends the stream with its message", func(t *testing.T) {
		api := LazyAPI(func(context.Context) (*ProviderStreams, error) { return nil, errors.New("cannot load module") }, LazyAPICapabilities{})
		stream, err := api.Stream(t.Context(), model, TranscriptContext{}, StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonError || result.ErrorMessage != "cannot load module" {
			t.Fatalf("result = %+v", result)
		}
	})
	// .upstream/v1.1.0/packages/ai/src/api/lazy.ts createSetupErrorMessage(model, error, startedAt) (#10549): the message of a failed setup carries the request start, not the failure time, and is timed from it.
	t.Run("a load failure keeps the request start as its timestamp", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			api := LazyAPI(func(context.Context) (*ProviderStreams, error) {
				time.Sleep(30 * time.Millisecond)
				return nil, errors.New("cannot load module")
			}, LazyAPICapabilities{})
			started := time.Now().UnixMilli()
			stream, err := api.Stream(t.Context(), model, TranscriptContext{}, StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if result.Timestamp != started {
				t.Fatalf("timestamp = %d, want the request start %d", result.Timestamp, started)
			}
			if result.DurationMs == nil || *result.DurationMs != 30 {
				t.Fatalf("durationMs = %v, want 30", result.DurationMs)
			}
		})
	})
}
