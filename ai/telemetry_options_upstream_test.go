package ai

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/ai/test/telemetry-options.test.ts. TelemetryContext is upstream ProviderRequestOptions.telemetryContext
// (types.ts:135), inherited by StreamOptions, DeferredFetchOptions, DeferredCancelOptions and ImagesOptions.
// Go has no separate buildBaseOptions: StreamSimple takes the same StreamOptions value, so the simple-stream
// conversion (simple-options.ts:32) is the value copy asserted below.

var telemetryOptionsContext telemetry.TelemetryContext = telemetry.NoopTelemetryContext

func telemetryTestModel() *Model {
	return &Model{ID: "model", DisplayName: "Model", ProviderMeta: ProviderMetadata{API: "telemetry-test", ProviderID: "telemetry-provider", BaseURL: "https://example.test"}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
}

func telemetryCompletedStream(model *Model) (*AssistantMessageEventStream, error) {
	message := &AssistantMessage{API: model.ProviderMeta.API, Provider: model.ProviderMeta.ProviderID, Model: model.ID, StopReason: StopReasonStop}
	stream := NewAssistantMessageEventStream()
	if err := stream.Push(DoneEvent{Reason: StopReasonStop, Message: message}); err != nil {
		return nil, err
	}
	return stream, nil
}

// .upstream/v0.99.2/packages/ai/test/telemetry-options.test.ts:63
func TestTelemetryOptionsInheritedByEveryRequestOptionSurfaceUpstream(t *testing.T) {
	if got := (StreamOptions{TelemetryContext: telemetryOptionsContext}).TelemetryContext; got != telemetryOptionsContext {
		t.Errorf("StreamOptions.TelemetryContext = %v", got)
	}
	if got := (DeferredFetchOptions{StreamOptions: StreamOptions{TelemetryContext: telemetryOptionsContext}}).TelemetryContext; got != telemetryOptionsContext {
		t.Errorf("DeferredFetchOptions.TelemetryContext = %v", got)
	}
	if got := (DeferredCancelOptions{TelemetryContext: telemetryOptionsContext}).TelemetryContext; got != telemetryOptionsContext {
		t.Errorf("DeferredCancelOptions.TelemetryContext = %v", got)
	}
	if got := (ImagesOptions{TelemetryContext: telemetryOptionsContext}).TelemetryContext; got != telemetryOptionsContext {
		t.Errorf("ImagesOptions.TelemetryContext = %v", got)
	}
}

// .upstream/v0.99.2/packages/ai/test/telemetry-options.test.ts:70
func TestTelemetryOptionsSurviveProviderAndModelsDispatchUpstream(t *testing.T) {
	var observed []telemetry.TelemetryContext
	model := telemetryTestModel()
	handle := DeferredHandle{Provider: model.ProviderMeta.ProviderID, ModelID: model.ID, API: model.ProviderMeta.API, ID: "response"}
	provider := CreateProvider(CreateProviderOptions{
		ID: model.ProviderMeta.ProviderID, Auth: configuredTestAuth(), Models: []AnyModel{model},
		API: &ProviderStreams{
			Stream: func(_ context.Context, requestModel *Model, _ TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
				observed = append(observed, options.TelemetryContext)
				return telemetryCompletedStream(requestModel)
			},
			StreamSimple: func(_ context.Context, requestModel *Model, _ TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
				observed = append(observed, options.TelemetryContext)
				return telemetryCompletedStream(requestModel)
			},
			FetchDeferred: func(_ context.Context, requestModel *Model, _ DeferredHandle, options DeferredFetchOptions) (*AssistantMessageEventStream, error) {
				observed = append(observed, options.TelemetryContext)
				return telemetryCompletedStream(requestModel)
			},
			CancelDeferred: func(_ context.Context, _ *Model, _ DeferredHandle, options DeferredCancelOptions) error {
				observed = append(observed, options.TelemetryContext)
				return nil
			},
		},
	})
	request := NormalizeContext(Context{Messages: []Message{}})
	streamOptions := StreamOptions{TelemetryContext: telemetryOptionsContext}
	for _, call := range []func() error{
		func() error {
			s, err := provider.Stream(t.Context(), model, request, streamOptions)
			drain(t, s)
			return err
		},
		func() error {
			s, err := provider.StreamSimple(t.Context(), model, request, streamOptions)
			drain(t, s)
			return err
		},
		func() error {
			s, err := provider.FetchDeferred(t.Context(), model, handle, DeferredFetchOptions{StreamOptions: streamOptions})
			drain(t, s)
			return err
		},
		func() error { return provider.CancelDeferred(t.Context(), model, handle, streamOptions) },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}

	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(provider)
	request2 := Context{Messages: []Message{}}
	if result := models.Stream(t.Context(), model, request2, streamOptions).Result(); result.StopReason != StopReasonStop {
		t.Fatalf("models.Stream = %s", result.ErrorMessage)
	}
	if result := models.StreamSimple(t.Context(), model, request2, streamOptions).Result(); result.StopReason != StopReasonStop {
		t.Fatalf("models.StreamSimple = %s", result.ErrorMessage)
	}
	if result := models.FetchDeferred(t.Context(), model, handle, DeferredFetchOptions{StreamOptions: streamOptions}); result.StopReason != StopReasonStop {
		t.Fatalf("models.FetchDeferred = %s", result.ErrorMessage)
	}
	if err := models.CancelDeferred(t.Context(), model, handle, streamOptions); err != nil {
		t.Fatal(err)
	}

	if len(observed) != 8 {
		t.Fatalf("observed %d contexts, want 8", len(observed))
	}
	for i, got := range observed {
		if got != telemetryOptionsContext {
			t.Errorf("observed[%d] = %v, want the request's telemetry context", i, got)
		}
	}
}

func drain(t *testing.T, stream *AssistantMessageEventStream) {
	t.Helper()
	if stream != nil {
		stream.Result()
	}
}

// .upstream/v0.99.2/packages/ai/test/telemetry-options.test.ts:121
func TestTelemetryOptionsSurviveImageDispatchUpstream(t *testing.T) {
	var observed []telemetry.TelemetryContext
	imageModel := ImageModel{ID: "image-model", Name: "Image Model", API: "telemetry-test-images", Provider: "telemetry-image-provider", BaseURL: "https://example.test", Input: []string{"text"}, Output: []string{"image"}}
	imagesRequest := ImagesContext{Input: []ContentBlock{TextContent{Text: "circle"}}}
	RegisterImagesAPIProvider(ImagesAPIProvider{API: imageModel.API, GenerateImages: func(_ context.Context, requestModel ImageModel, _ ImagesContext, options ProviderImagesOptions) AssistantImages {
		observed = append(observed, options.TelemetryContext)
		return AssistantImages{API: requestModel.API, Provider: requestModel.Provider, Model: requestModel.ID, Output: []ContentBlock{}, StopReason: ImagesStopReasonStop}
	}})
	t.Cleanup(func() {
		imagesAPIProviderMu.Lock()
		defer imagesAPIProviderMu.Unlock()
		delete(imagesAPIProviderRegistry, imageModel.API)
	})
	if _, err := GenerateImages(t.Context(), imageModel, imagesRequest, ImagesOptions{TelemetryContext: telemetryOptionsContext}); err != nil {
		t.Fatal(err)
	}

	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(CreateProvider(CreateProviderOptions{
		ID: imageModel.Provider, Auth: configuredTestAuth(), Models: []AnyModel{&imageModel},
		Images: ProviderImageAPIMap{imageModel.API: {GenerateImages: func(_ context.Context, requestModel *ImageModel, _ ImagesContext, options ImagesOptions) (AssistantImages, error) {
			observed = append(observed, options.TelemetryContext)
			return AssistantImages{API: requestModel.API, Provider: requestModel.Provider, Model: requestModel.ID, Output: []ContentBlock{}, StopReason: ImagesStopReasonStop}, nil
		}}},
	}))
	result := models.GenerateImages(t.Context(), &imageModel, imagesRequest, ModelsImagesOptions{ImagesOptions: ImagesOptions{TelemetryContext: telemetryOptionsContext}})
	if result.StopReason != ImagesStopReasonStop {
		t.Fatalf("models.GenerateImages = %s", result.ErrorMessage)
	}
	if len(observed) != 2 || observed[0] != telemetryOptionsContext || observed[1] != telemetryOptionsContext {
		t.Fatalf("observed = %v, want both contexts", observed)
	}
}

// ClassifierOptions extends ProviderRequestOptions too (.upstream/v0.99.2/packages/ai/src/types.ts:324), so
// Models.classify forwards telemetryContext with the rest of the request options (models.ts classify). Upstream
// telemetry-options.test.ts has no classifier case; this guards the sibling surface of the image case above.
func TestTelemetryOptionsSurviveClassifierDispatch(t *testing.T) {
	var observed []telemetry.TelemetryContext
	classifier := classifierTestModel("telemetry-classifier-provider", "classifier-model")
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(CreateProvider(CreateProviderOptions{
		ID: classifier.Provider, Auth: configuredTestAuth(), Models: []AnyModel{classifier},
		Classifiers: ProviderClassifierMap{classifier.API: {Classify: func(_ context.Context, model *ClassifierModel, _ ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
			observed = append(observed, options.TelemetryContext)
			return ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, StopReason: ClassifierStopReasonStop}, nil
		}}},
	}))
	result := models.Classify(t.Context(), classifier, classifierTestContext(), ModelsClassifierOptions{ClassifierOptions: ClassifierOptions{TelemetryContext: telemetryOptionsContext}})
	if result.StopReason != ClassifierStopReasonStop {
		t.Fatalf("models.Classify = %s", result.ErrorMessage)
	}
	if len(observed) != 1 || observed[0] != telemetryOptionsContext {
		t.Fatalf("observed = %v, want the request's telemetry context", observed)
	}
}
