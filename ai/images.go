package ai

import (
	"context"
	"fmt"
)

// ImagesAPI identifies an image-generation provider API.
type ImagesAPI string

const APIImagesOpenRouter ImagesAPI = "openrouter-images"

// ImagesProvider identifies an image-generation provider.
type ImagesProvider string

const ProviderImagesOpenRouter ImagesProvider = "openrouter"

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
	Provider ImagesProvider
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
	Provider     ImagesProvider
	Model        string
	Output       []ContentBlock
	ResponseID   string
	Usage        *Usage
	StopReason   ImagesStopReason
	ErrorMessage string
	Timestamp    int64
}

// ProviderResponse is the provider HTTP response metadata exposed to hooks.
type ProviderResponse struct {
	Status  int
	Headers map[string]string
}

// ProviderImagesOptions configures image-generation provider requests.
type ProviderImagesOptions struct {
	APIKey     string
	Headers    map[string]string
	TimeoutMs  int
	MaxRetries int
	OnPayload  func(payload any, model ImagesModel) (any, bool, error)
	OnResponse func(response ProviderResponse, model ImagesModel) error
}

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
