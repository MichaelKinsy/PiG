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
	Input []ContentBlock
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
	Output       []ContentBlock
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
	APIKeySet  bool
	Headers    ProviderHeaders
	Env        map[string]string
	Metadata   map[string]any
	TimeoutMs  int
	MaxRetries int
	OnPayload  func(payload any, model ImageModel) (any, bool, error)
	OnResponse func(response ProviderResponse, model ImageModel) error
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

var (
	imagesAPIProviderMu       sync.RWMutex
	imagesAPIProviderRegistry = map[ImageAPI]ImagesAPIProvider{}
)

// RegisterImagesAPIProvider registers or replaces an image API provider.
func RegisterImagesAPIProvider(provider ImagesAPIProvider) {
	imagesAPIProviderMu.Lock()
	defer imagesAPIProviderMu.Unlock()
	imagesAPIProviderRegistry[provider.API] = provider
}

// GetImagesAPIProvider returns the registered provider for api.
func GetImagesAPIProvider(api ImageAPI) (ImagesAPIProvider, bool) {
	imagesAPIProviderMu.RLock()
	defer imagesAPIProviderMu.RUnlock()
	provider, ok := imagesAPIProviderRegistry[api]
	return provider, ok
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
