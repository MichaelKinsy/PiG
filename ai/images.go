package ai

import (
	"context"
	"fmt"
	"net/http"
)

// ImagesAPI identifies an image-generation provider API.
type ImagesAPI string

const APIImagesOpenRouter ImagesAPI = "openrouter-images"

// ImagesProviderId identifies an image-generation provider.
type ImagesProviderId string

const ProviderImagesOpenRouter ImagesProviderId = "openrouter"

// ImagesCost mirrors upstream image model cost fields, expressed in USD per
// million tokens where a provider reports token usage.
type ImagesCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// ImagesModel describes one generated-image model catalog entry.
type ImagesModel struct {
	ID       string
	Name     string
	API      ImagesAPI
	Provider ImagesProviderId
	BaseURL  string
	Headers  map[string]string
	Input    []string
	Output   []string
	Cost     ImagesCost
}

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
	API          ImagesAPI
	Provider     ImagesProviderId
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
	OnPayload  func(payload any, model ImagesModel) (any, bool, error)
	OnResponse func(response ProviderResponse, model ImagesModel) error
}

// ProviderImagesOptions is the image API options shape.
type ProviderImagesOptions = ImagesOptions

// ImagesFunction is an image-generation provider function.
type ImagesFunction func(context.Context, ImagesModel, ImagesContext, ProviderImagesOptions) AssistantImages

// ImagesAPIProvider registers a provider implementation for an ImagesAPI.
type ImagesAPIProvider struct {
	API            ImagesAPI
	GenerateImages ImagesFunction
}

var imagesAPIProviderRegistry = map[ImagesAPI]ImagesAPIProvider{}

// RegisterImagesAPIProvider registers or replaces an image API provider.
func RegisterImagesAPIProvider(provider ImagesAPIProvider) {
	imagesAPIProviderRegistry[provider.API] = provider
}

// GetImagesAPIProvider returns the registered provider for api.
func GetImagesAPIProvider(api ImagesAPI) (ImagesAPIProvider, bool) {
	provider, ok := imagesAPIProviderRegistry[api]
	return provider, ok
}

// GenerateImages dispatches an image-generation request to the model's API
// provider. It mirrors upstream generateImages(), returning an error only when
// no provider is registered for the model API.
func GenerateImages(ctx context.Context, model ImagesModel, imagesCtx ImagesContext, options ProviderImagesOptions) (AssistantImages, error) {
	provider, ok := GetImagesAPIProvider(model.API)
	if !ok {
		return AssistantImages{}, fmt.Errorf("No API provider registered for api: %s", model.API)
	}
	return provider.GenerateImages(ctx, model, imagesCtx, options), nil
}

func init() {
	RegisterBuiltInImagesAPIProviders()
}
