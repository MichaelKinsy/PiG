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

type CreateProviderOptions struct {
	ID           string
	Name         *string
	BaseURL      string
	Headers      ProviderHeaders
	Auth         ProviderAuth
	Models       []*Model
	FetchModels  func(RefreshModelsContext) ([]*Model, error)
	FilterModels func([]*Model, *Credential) []*Model
	API          ProviderAPIs
}

// CreateProvider merges a dynamic overlay over its baseline and routes each request through the model's API implementation.
func CreateProvider(input CreateProviderOptions) *ModelsProvider {
	name := input.ID
	if input.Name != nil {
		name = *input.Name
	}
	baseline := slices.Clone(input.Models)
	var dynamic []*Model
	var mu sync.Mutex
	provider := &ModelsProvider{ID: input.ID, Name: name, BaseURL: input.BaseURL, Headers: input.Headers, Auth: input.Auth, FilterModels: input.FilterModels}
	provider.GetModels = func() ([]*Model, error) {
		mu.Lock()
		defer mu.Unlock()
		merged := append([]*Model{}, baseline...)
		for _, model := range dynamic {
			index := slices.IndexFunc(merged, func(candidate *Model) bool { return candidate.ID == model.ID })
			if index < 0 {
				merged = append(merged, model)
			} else {
				merged[index] = model
			}
		}
		return merged, nil
	}
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
			models, err := input.FetchModels(refresh)
			if err != nil {
				return err
			}
			if refresh.Signal.Err() != nil {
				return nil
			}
			raw, err := encodeModelsCatalog(models)
			if err != nil {
				return err
			}
			_, err = refresh.Publish(ModelsPublication{Persist: &ModelsStoreEntry{Models: raw, CheckedAt: new(float64(time.Now().UnixMilli()))}, Update: func() { mu.Lock(); dynamic = models; mu.Unlock() }})
			return err
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
