package ai

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// ProviderAPIs is the closed single-module or API-keyed implementation union.
type ProviderAPIs interface {
	implementationFor(API) *ProviderStreams
	implementations() []*ProviderStreams
}

type ProviderStreams struct {
	Stream         ModelsStreamFunction
	StreamSimple   ModelsStreamFunction
	FetchDeferred  func(context.Context, *Model, DeferredHandle, DeferredFetchOptions) (*AssistantMessageEventStream, error)
	CancelDeferred func(context.Context, *Model, DeferredHandle, DeferredCancelOptions) error
}

func (streams *ProviderStreams) implementationFor(API) *ProviderStreams { return streams }
func (streams *ProviderStreams) implementations() []*ProviderStreams {
	return []*ProviderStreams{streams}
}

type ProviderAPIMap map[API]*ProviderStreams

func (apis ProviderAPIMap) implementationFor(api API) *ProviderStreams { return apis[api] }
func (apis ProviderAPIMap) implementations() []*ProviderStreams {
	out := make([]*ProviderStreams, 0, len(apis))
	for _, streams := range apis {
		if streams != nil {
			out = append(out, streams)
		}
	}
	return out
}

// ProviderImages is an image API module. Rejections are returned as errors.
type ProviderImages struct {
	GenerateImages func(context.Context, *ImageModel, ImagesContext, ImagesOptions) (AssistantImages, error)
}

// ProviderImageAPIMap keys image implementations by ImageModel.API.
type ProviderImageAPIMap map[ImageAPI]*ProviderImages

type CreateProviderOptions struct {
	ID      string
	Name    *string
	BaseURL string
	Headers ProviderHeaders
	Auth    ProviderAuth
	// Models are the static baseline models of every type. Models without a type are chat models.
	Models []AnyModel
	// FetchModels returns a dynamic overlay of every type.
	FetchModels     func(RefreshModelsContext) ([]AnyModel, error)
	FilterModels    func([]*Model, *Credential) []*Model
	FilterAllModels func([]AnyModel, *Credential) []AnyModel
	// API is the chat implementation. It is optional when Images or Classifiers is given.
	API         ProviderAPIs
	Images      ProviderImageAPIMap
	Classifiers ProviderClassifierMap
}

// chatModels keeps the chat models of a mixed list.
func chatModels(models []AnyModel) []*Model {
	out := make([]*Model, 0, len(models))
	for _, model := range models {
		if chat, ok := model.(*Model); ok && IsModelType(chat, ModelTypeChat) {
			out = append(out, chat)
		}
	}
	return out
}

// sameProviderModel reports whether two models are the same entry within a provider: ids are unique per model type.
func sameProviderModel(a, b AnyModel) bool {
	return GetModelType(a) == GetModelType(b) && a.ModelID() == b.ModelID()
}

// CreateProvider merges a dynamic overlay over its baseline and routes each request through the model's API implementation.
// It panics when no chat, image or classifier implementation is given.
func CreateProvider(input CreateProviderOptions) *ModelsProvider {
	imageImplementations := 0
	for _, implementation := range input.Images {
		if implementation != nil {
			imageImplementations++
		}
	}
	classifierImplementations := 0
	for _, implementation := range input.Classifiers {
		if implementation != nil {
			classifierImplementations++
		}
	}
	chatImplementations := 0
	if single, ok := input.API.(*ProviderStreams); ok {
		// Pi treats an api object without a stream function as an api map and counts its defined values.
		if single != nil && (single.Stream != nil || single.StreamSimple != nil || single.FetchDeferred != nil || single.CancelDeferred != nil) {
			chatImplementations = 1
		}
	} else if input.API != nil {
		chatImplementations = len(input.API.implementations())
	}
	if chatImplementations == 0 && imageImplementations == 0 && classifierImplementations == 0 {
		panic(fmt.Errorf("Provider %s: at least one of \"api\", \"images\", or \"classifiers\" is required.", input.ID))
	}
	name := input.ID
	if input.Name != nil {
		name = *input.Name
	}
	baseline := slices.Clone(input.Models)
	var dynamic []AnyModel
	var mu sync.Mutex
	provider := &ModelsProvider{ID: input.ID, Name: name, BaseURL: input.BaseURL, Headers: input.Headers, Auth: input.Auth, FilterModels: input.FilterModels, FilterAllModels: input.FilterAllModels}
	currentModels := func() []AnyModel {
		mu.Lock()
		defer mu.Unlock()
		merged := append([]AnyModel{}, baseline...)
		for _, model := range dynamic {
			index := slices.IndexFunc(merged, func(candidate AnyModel) bool { return sameProviderModel(candidate, model) })
			if index < 0 {
				merged = append(merged, model)
			} else {
				merged[index] = model
			}
		}
		return merged
	}
	provider.GetModels = func() ([]*Model, error) { return chatModels(currentModels()), nil }
	provider.GetAllModels = func() ([]AnyModel, error) { return currentModels(), nil }
	if input.FetchModels != nil {
		provider.RefreshModels = func(refresh RefreshModelsContext) error {
			if refresh.Stored != nil {
				restored, err := decodeModelsCatalog(refresh.Stored.Models, input.ID)
				if err != nil {
					return err
				}
				published, err := refresh.Publish(ModelsPublication{Update: func() { mu.Lock(); dynamic = restored; mu.Unlock() }})
				if err != nil || !published {
					return err
				}
			}
			if !refresh.AllowNetwork || refresh.Signal.Err() != nil {
				return nil
			}
			fetched, err := input.FetchModels(refresh)
			if err != nil {
				return err
			}
			models := slices.DeleteFunc(slices.Clone(fetched), func(model AnyModel) bool { return !hasKnownModelType(model) })
			if refresh.Signal.Err() != nil {
				return nil
			}
			_, err = refresh.Publish(ModelsPublication{Persist: &ModelsStoreEntry{Models: models, CheckedAt: new(float64(time.Now().UnixMilli()))}, Update: func() { mu.Lock(); dynamic = models; mu.Unlock() }})
			return err
		}
	}
	if imageImplementations > 0 {
		provider.GenerateImages = func(ctx context.Context, model *ImageModel, request ImagesContext, options ImagesOptions) (AssistantImages, error) {
			implementation := input.Images[model.API]
			if implementation == nil || implementation.GenerateImages == nil {
				return imageErrorResult(model, NewModelsError(ModelsErrorProvider, fmt.Sprintf("Provider %s has no image generation implementation for %q", input.ID, model.API), nil), false), nil
			}
			return implementation.GenerateImages(ctx, model, request, options)
		}
	}
	if classifierImplementations > 0 {
		provider.Classify = func(ctx context.Context, model *ClassifierModel, request ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
			implementation := input.Classifiers[model.API]
			if implementation == nil || implementation.Classify == nil {
				return classifierErrorResult(model, NewModelsError(ModelsErrorProvider, fmt.Sprintf("Provider %s has no classifier implementation for %q", input.ID, model.API), nil), false), nil
			}
			return implementation.Classify(ctx, model, request, options)
		}
	}
	apiFor := func(model *Model) *ProviderStreams {
		if input.API == nil {
			return nil
		}
		return input.API.implementationFor(model.ProviderMeta.API)
	}
	dispatch := func(ctx context.Context, model *Model, request TranscriptContext, options StreamOptions, simple bool) (*AssistantMessageEventStream, error) {
		streams := apiFor(model)
		if streams == nil {
			stream := NewAssistantMessageEventStream()
			pushModelsSetupError(stream, model, NewModelsError(ModelsErrorStream, fmt.Sprintf("Provider %s has no API implementation for %q", input.ID, model.ProviderMeta.API), nil))
			return stream, nil
		}
		implementation := streams.Stream
		if simple {
			implementation = streams.StreamSimple
		}
		if implementation == nil {
			return nil, NewModelsError(ModelsErrorStream, "Provider "+input.ID+" has no stream implementation", nil)
		}
		return implementation(ctx, model, request, options)
	}
	provider.Stream = func(ctx context.Context, model *Model, request TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		return dispatch(ctx, model, request, options, false)
	}
	provider.StreamSimple = func(ctx context.Context, model *Model, request TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		return dispatch(ctx, model, request, options, true)
	}
	var canFetch, canCancel bool
	if input.API != nil {
		for _, streams := range input.API.implementations() {
			if streams != nil {
				canFetch = canFetch || streams.FetchDeferred != nil
				canCancel = canCancel || streams.CancelDeferred != nil
			}
		}
	}
	if canFetch {
		provider.FetchDeferred = func(ctx context.Context, model *Model, handle DeferredHandle, options DeferredFetchOptions) (*AssistantMessageEventStream, error) {
			streams := apiFor(model)
			if streams == nil || streams.FetchDeferred == nil {
				stream := NewAssistantMessageEventStream()
				pushModelsSetupError(stream, model, NewModelsError(ModelsErrorProvider, fmt.Sprintf("Provider %s does not support deferred responses for %q", input.ID, model.ProviderMeta.API), nil))
				return stream, nil
			}
			return streams.FetchDeferred(ctx, model, handle, options)
		}
	}
	if canCancel {
		provider.CancelDeferred = func(ctx context.Context, model *Model, handle DeferredHandle, options DeferredCancelOptions) error {
			streams := apiFor(model)
			if streams == nil || streams.CancelDeferred == nil {
				return NewModelsError(ModelsErrorProvider, fmt.Sprintf("Provider %s cannot cancel deferred responses for %q", input.ID, model.ProviderMeta.API), nil)
			}
			return streams.CancelDeferred(ctx, model, handle, options)
		}
	}
	return provider
}
