package ai

import (
	"context"
	"reflect"
	"testing"
)

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:542
func TestModelsMixedAPIDispatchUpstream(t *testing.T) {
	calls := []string{}
	provider := CreateProvider(CreateProviderOptions{ID: "mixed", Auth: configuredTestAuth(), Models: []AnyModel{dispatchTestModel("api-a", "model-a"), dispatchTestModel("api-b", "model-b")}, API: ProviderAPIMap{"api-a": recordingProviderStreams("a", &calls), "api-b": recordingProviderStreams("b", &calls)}})
	models := CreateModels(CreateModelsOptions{})
	models.SetProvider(provider)
	for _, pair := range [][2]string{{"api-a", "model-a"}, {"api-b", "model-b"}} {
		result := models.CompleteSimple(t.Context(), dispatchTestModel(API(pair[0]), pair[1]), Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{})
		if result.StopReason != StopReasonStop {
			t.Fatal(result.ErrorMessage)
		}
	}
	if !reflect.DeepEqual(calls, []string{"a:model-a", "b:model-b"}) {
		t.Fatalf("calls=%v", calls)
	}
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:558
func TestModelsResolvedEnvironmentAndRequestKeyUpstream(t *testing.T) {
	model := dispatchTestModel("api-a", "model-a")
	model.ProviderMeta.ProviderID = "env-provider"
	var captured StreamOptions
	record := recordingProviderStreams("a", nil)
	capture := func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		captured = options
		return record.Stream(ctx, model, transcript, options)
	}
	provider := CreateProvider(CreateProviderOptions{ID: "env-provider", Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
		return &AuthResult{Auth: ModelAuth{APIKey: "provider-key"}, Env: map[string]string{"PROVIDER_ONLY": "provider", "SHARED": "provider"}}, nil
	}}}, Models: []AnyModel{model}, API: &ProviderStreams{Stream: capture, StreamSimple: capture}})
	models := CreateModels(CreateModelsOptions{})
	models.SetProvider(provider)
	result := models.CompleteSimple(t.Context(), model, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{APIKey: "request-key", Env: ProviderEnv{"REQUEST_ONLY": "request", "SHARED": "request"}})
	if result.StopReason != StopReasonStop {
		t.Fatal(result.ErrorMessage)
	}
	if captured.APIKey != "request-key" || !reflect.DeepEqual(captured.Env, ProviderEnv{"PROVIDER_ONLY": "provider", "REQUEST_ONLY": "request", "SHARED": "request"}) {
		t.Fatalf("captured=%#v", captured)
	}
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:599; the 1.0.4 form of the same case passes `wait: 50` to models.fetchDeferred and expects the provider to receive it.
// mutation-checked: dropping the reads and writes of DeferredFetchOptions.Wait fails it
// Pi: packages/ai/test/providers.test.ts:662 (wait)
func TestModelsDeferredResolvedRequestOptionsUpstream(t *testing.T) {
	model := dispatchTestModel("api-a", "model-a")
	model.ProviderMeta.ProviderID = "deferred-provider"
	var fetchedModel *Model
	var fetched DeferredFetchOptions
	var cancelled DeferredCancelOptions
	streams := recordingProviderStreams("deferred", nil)
	streams.FetchDeferred = func(ctx context.Context, model *Model, _ DeferredHandle, options DeferredFetchOptions) (*AssistantMessageEventStream, error) {
		fetchedModel = model
		fetched = options
		return streams.StreamSimple(ctx, model, NormalizeContext(Context{}), StreamOptions{})
	}
	streams.CancelDeferred = func(_ context.Context, _ *Model, _ DeferredHandle, options DeferredCancelOptions) error {
		cancelled = options
		return nil
	}
	provider := CreateProvider(CreateProviderOptions{ID: "deferred-provider", Auth: ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) {
		return &AuthResult{Auth: ModelAuth{APIKey: "provider-key", BaseURL: "https://resolved.test/v1", Headers: ProviderHeaders{"Authorization": new("Bearer provider"), "X-Shared": new("provider")}}, Env: map[string]string{"PROVIDER_ONLY": "provider", "SHARED": "provider"}}, nil
	}}}, Models: []AnyModel{model}, API: streams})
	models := CreateModels(CreateModelsOptions{})
	models.SetProvider(provider)
	handle := DeferredHandle{Provider: "deferred-provider", ModelID: "model-a", API: "api-a", ID: "response-1"}
	result := models.FetchDeferred(t.Context(), model, handle, DeferredFetchOptions{Wait: new(50.0), StreamOptions: StreamOptions{TimeoutMs: new(100), APIKey: "request-key", Headers: ProviderHeaders{"X-Request": new("request"), "x-shared": new("request")}, Env: ProviderEnv{"REQUEST_ONLY": "request", "SHARED": "request"}, TransformHeaders: func(_ context.Context, headers ProviderHeaders) (ProviderHeaders, error) {
		headers["X-Transformed"] = new("yes")
		return headers, nil
	}}})
	if result.StopReason != StopReasonStop {
		t.Fatal(result.ErrorMessage)
	}
	if err := models.CancelDeferred(t.Context(), model, handle, DeferredCancelOptions{TimeoutMs: new(200), TransformHeaders: func(_ context.Context, headers ProviderHeaders) (ProviderHeaders, error) {
		headers["X-Cancel"] = new("yes")
		return headers, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if fetchedModel == nil || fetchedModel.ProviderMeta.BaseURL != "https://resolved.test/v1" || fetched.Wait == nil || *fetched.Wait != 50 || fetched.TimeoutMs == nil || *fetched.TimeoutMs != 100 || fetched.APIKey != "request-key" {
		t.Fatalf("fetch=%#v model=%#v", fetched, fetchedModel)
	}
	if !reflect.DeepEqual(fetched.Headers, ProviderHeaders{"Authorization": new("Bearer provider"), "X-Request": new("request"), "x-shared": new("request"), "X-Transformed": new("yes")}) || !reflect.DeepEqual(fetched.Env, ProviderEnv{"PROVIDER_ONLY": "provider", "REQUEST_ONLY": "request", "SHARED": "request"}) || fetched.TransformHeaders != nil {
		t.Fatalf("fetch options=%#v", fetched)
	}
	if cancelled.TimeoutMs == nil || *cancelled.TimeoutMs != 200 || cancelled.APIKey != "provider-key" || !reflect.DeepEqual(cancelled.Headers, ProviderHeaders{"Authorization": new("Bearer provider"), "X-Shared": new("provider"), "X-Cancel": new("yes")}) || !reflect.DeepEqual(cancelled.Env, ProviderEnv{"PROVIDER_ONLY": "provider", "SHARED": "provider"}) || cancelled.TransformHeaders != nil {
		t.Fatalf("cancel options=%#v", cancelled)
	}
}
