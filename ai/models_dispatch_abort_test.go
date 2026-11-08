package ai

import (
	"context"
	"strings"
	"testing"
)

// models.ts passes the request signal to lazyStream in the dispatch's "no API implementation" stream and in fetchDeferred (Pi lazy.ts:83): a rejection
// ends as aborted, with the setup message, once the request's signal is aborted, and as an error for a live request.
func TestModelsDispatchSetupErrorsAreAbortedWhenTheRequestSignalIsAborted(t *testing.T) {
	provider := CreateProvider(CreateProviderOptions{
		ID: "demo",
		API: ProviderAPIMap{
			"api-a": &ProviderStreams{
				Stream: func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
					return nil, nil
				},
				StreamSimple: func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
					return nil, nil
				},
				FetchDeferred: func(context.Context, *Model, DeferredHandle, DeferredFetchOptions) (*AssistantMessageEventStream, error) {
					return nil, nil
				},
			},
			"api-b": &ProviderStreams{
				Stream: func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
					return nil, nil
				},
			},
		},
	})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name    string
		api     API
		call    func(context.Context, *Model) (*AssistantMessageEventStream, error)
		message string
	}{
		{"stream without an API implementation", "api-missing", func(ctx context.Context, model *Model) (*AssistantMessageEventStream, error) {
			return provider.Stream(ctx, model, TranscriptContext{}, StreamOptions{})
		}, `Provider demo has no API implementation for "api-missing"`},
		{"simple stream without an API implementation", "api-missing", func(ctx context.Context, model *Model) (*AssistantMessageEventStream, error) {
			return provider.StreamSimple(ctx, model, TranscriptContext{}, StreamOptions{})
		}, `Provider demo has no API implementation for "api-missing"`},
		{"fetchDeferred without an API implementation", "api-missing", func(ctx context.Context, model *Model) (*AssistantMessageEventStream, error) {
			return provider.FetchDeferred(ctx, model, DeferredHandle{}, DeferredFetchOptions{})
		}, `Provider demo does not support deferred responses for "api-missing"`},
		{"fetchDeferred of an API without FetchDeferred", "api-b", func(ctx context.Context, model *Model) (*AssistantMessageEventStream, error) {
			return provider.FetchDeferred(ctx, model, DeferredHandle{}, DeferredFetchOptions{})
		}, `Provider demo does not support deferred responses for "api-b"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			model := &Model{ID: "m1", ProviderMeta: ProviderMetadata{ProviderID: "demo", API: c.api}}
			live, err := c.call(context.Background(), model)
			if err != nil {
				t.Fatal(err)
			}
			if result := live.Result(); result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, c.message) {
				t.Fatalf("live request = %+v, want an error containing %q", result, c.message)
			}
			aborted, err := c.call(cancelled, model)
			if err != nil {
				t.Fatal(err)
			}
			if result := aborted.Result(); result.StopReason != StopReasonAborted || !strings.Contains(result.ErrorMessage, c.message) {
				t.Fatalf("aborted request = %+v, want aborted with a message containing %q", result, c.message)
			}
		})
	}
}
