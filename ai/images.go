package ai

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/MichaelKinsy/PiG/telemetry"
)

const APIImagesOpenRouter ImageAPI = "openrouter-images"

const ProviderImagesOpenRouter = "openrouter"

// ImagesContext is the input to an image-generation request.
type ImagesContext struct {
	Input []ImagesInputContent
}

// ImagesStopReason mirrors upstream ImagesStopReason.
type ImagesStopReason string

const (
	ImagesStopReasonStop    ImagesStopReason = "stop"
	ImagesStopReasonError   ImagesStopReason = "error"
	ImagesStopReasonAborted ImagesStopReason = "aborted"
)

// AssistantImages is the final result of an image-generation request.
type AssistantImages struct {
	API          ImageAPI
	Provider     string
	Model        string
	Output       []ImagesOutputContent
	ResponseID   string
	Usage        *Usage
	StopReason   ImagesStopReason
	ErrorMessage string
	Timestamp    int64
}

// ImagesOptions configures image-generation provider requests.
type ImagesOptions struct {
	// TelemetryContext parents provider request spans; nil means no recording backend.
	TelemetryContext telemetry.TelemetryContext
	// Fetch replaces HTTP execution without changing the caller's request context or redirect policy.
	Fetch  *http.Client
	APIKey string
	// APIKeySet distinguishes an explicit empty key from an omitted override.
	APIKeySet bool
	Headers   ProviderHeaders
	Env       map[string]string
	Metadata  map[string]any
	TimeoutMs int
	// MaxRetries is the retry budget after a retryable failure. Nil uses retry.provider.maxRetries; an explicit zero disables retries.
	MaxRetries *int
	// MaxRetryDelayMs caps a server-requested retry delay. Nil means 60000; 0 disables the cap.
	MaxRetryDelayMs *int
	OnPayload       func(payload any, model ImageModel) (any, bool, error)
	OnResponse      func(response ProviderResponse, model ImageModel) error
}

// ProviderImagesOptions is the image API options shape.
type ProviderImagesOptions = ImagesOptions

// ImagesFunction is an image-generation provider function.
type ImagesFunction func(context.Context, ImageModel, ImagesContext, ProviderImagesOptions) AssistantImages

// ImagesAPIProvider registers a provider implementation for an ImageAPI.
type ImagesAPIProvider struct {
	API            ImageAPI
	GenerateImages ImagesFunction
}

// registeredImagesAPIProvider is one registry entry: the provider and the optional source that registered it
// (images-api-registry.ts RegisteredImagesApiProvider).
type registeredImagesAPIProvider struct {
	provider ImagesAPIProvider
	sourceID string
}

var (
	imagesAPIProviderMu       sync.RWMutex
	imagesAPIProviderRegistry = map[ImageAPI]registeredImagesAPIProvider{}
)

// RegisterImagesAPIProvider registers or replaces an image API provider. The optional sourceID names the registering
// source and is stored on the entry (registerImagesApiProvider(provider, sourceId?)); Pi keeps it without reading it.
func RegisterImagesAPIProvider(provider ImagesAPIProvider, sourceID ...string) {
	imagesAPIProviderMu.Lock()
	defer imagesAPIProviderMu.Unlock()
	entry := registeredImagesAPIProvider{provider: provider}
	if len(sourceID) > 0 {
		entry.sourceID = sourceID[0]
	}
	imagesAPIProviderRegistry[provider.API] = entry
}

// GetImagesAPIProvider returns the registered provider for api.
func GetImagesAPIProvider(api ImageAPI) (ImagesAPIProvider, bool) {
	imagesAPIProviderMu.RLock()
	defer imagesAPIProviderMu.RUnlock()
	entry, ok := imagesAPIProviderRegistry[api]
	return entry.provider, ok
}

// GenerateImages dispatches an image-generation request to the model's API
// provider. It mirrors upstream generateImages(), returning an error only when
// no provider is registered for the model API.
func GenerateImages(ctx context.Context, model ImageModel, imagesCtx ImagesContext, options ProviderImagesOptions) (AssistantImages, error) {
	provider, ok := GetImagesAPIProvider(model.API)
	if !ok {
		return AssistantImages{}, fmt.Errorf("No API provider registered for api: %s", model.API)
	}
	return provider.GenerateImages(ctx, model, imagesCtx, options), nil
}

func init() {
	RegisterBuiltInImagesAPIProviders()
}
