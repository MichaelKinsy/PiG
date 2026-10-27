package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// pi-ai's createProvider (packages/ai/src/models.ts:1077): builds a [Provider] from parts. A single API streams every chat model, a map of APIs dispatches on `model.api`, and a model whose api has no entry
// produces a stream error; one-shot operation maps dispatch on `model.api` the same way. The returned Provider is the object RegisterNativeProvider takes.

// ProviderStreams is pi-ai's ProviderStreams: the chat implementation of one API. FetchDeferred and CancelDeferred are optional.
type ProviderStreams struct {
	Stream         ProviderStreamFunc
	StreamSimple   ProviderStreamFunc
	FetchDeferred  ProviderStreamFunc
	CancelDeferred func(model, handle map[string]any, options ProviderStreamOptions) error
}

// CreateProviderOptions is pi-ai's CreateProviderOptions. A zero member is Pi's undefined. Pi's `api` is either one ProviderStreams or a map keyed by `model.api`: set API for the first and APIs for the second, not both.
type CreateProviderOptions struct {
	ID      string
	Name    string
	BaseURL *string
	Headers map[string]string
	// Auth is required: every provider has auth semantics, even ambient or keyless ones.
	Auth ProviderAuth
	// Models is the static baseline of every type (empty for purely dynamic providers). A model without `type` is a chat model.
	Models []map[string]any
	// FetchModels fetches a dynamic model overlay of every type. CreateProvider restores and publishes it transactionally and drops models of unknown types.
	FetchModels     func(RefreshModelsContext) ([]map[string]any, error)
	FilterModels    func([]map[string]any, *Credential) ([]map[string]any, error)
	FilterAllModels func([]map[string]any, *Credential) ([]map[string]any, error)
	API             *ProviderStreams
	APIs            map[string]ProviderStreams
	// Images and Classifiers are the implementations of the one-shot operations, keyed by `model.api`.
	Images      map[string]ProviderImagesFunc
	Classifiers map[string]ProviderClassifyFunc
}

// CreateProvider is `createProvider(options)`.
func CreateProvider(options CreateProviderOptions) (*Provider, error) {
	if options.API != nil && options.APIs != nil {
		return nil, errors.New("CreateProviderOptions: API and APIs are Pi's two forms of `api`; set one")
	}
	var streams []ProviderStreams
	if options.API != nil {
		streams = []ProviderStreams{*options.API}
	} else {
		for _, entry := range options.APIs {
			streams = append(streams, entry)
		}
	}
	if len(streams) == 0 && len(options.Images) == 0 && len(options.Classifiers) == 0 {
		return nil, fmt.Errorf(`Provider %s: at least one of "api", "images", or "classifiers" is required.`, options.ID)
	}
	var mu sync.Mutex
	var dynamicModels []map[string]any
	currentModels := func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		merged := append([]map[string]any{}, options.Models...)
		for _, model := range dynamicModels {
			replaced := false
			for i, entry := range merged {
				if GetModelType(entry) == GetModelType(model) && entry["id"] == model["id"] {
					merged[i], replaced = model, true
					break
				}
			}
			if !replaced {
				merged = append(merged, model)
			}
		}
		return merged
	}
	apiFor := func(model map[string]any) (ProviderStreams, bool) {
		if options.API != nil {
			return *options.API, true
		}
		api, _ := model["api"].(string)
		entry, ok := options.APIs[api]
		return entry, ok
	}
	provider := &Provider{
		ID: options.ID, Name: options.Name, BaseURL: options.BaseURL, Headers: options.Headers, Auth: options.Auth,
		GetModels: func() ([]map[string]any, error) {
			chat := []map[string]any{}
			for _, model := range currentModels() {
				if IsModelType(model, "chat") {
					chat = append(chat, model)
				}
			}
			return chat, nil
		},
		GetAllModels:    func() ([]map[string]any, error) { return currentModels(), nil },
		FilterModels:    options.FilterModels,
		FilterAllModels: options.FilterAllModels,
	}
	if provider.Name == "" {
		provider.Name = options.ID
	}
	if fetch := options.FetchModels; fetch != nil {
		provider.RefreshModels = func(refresh RefreshModelsContext) error {
			if refresh.Stored != nil {
				restored := []map[string]any{}
				stored, _ := refresh.Stored["models"].([]any)
				for _, entry := range stored {
					if model, ok := entry.(map[string]any); ok && model["provider"] == options.ID {
						restored = append(restored, model)
					}
				}
				published, err := refresh.Publish(ModelsPublication{Update: func() error {
					mu.Lock()
					dynamicModels = restored
					mu.Unlock()
					return nil
				}})
				if err != nil || !published {
					return err
				}
			}
			if !refresh.AllowNetwork || signalErr(refresh.Signal) != nil {
				return nil
			}
			fetched, err := fetch(refresh)
			if err != nil {
				return err
			}
			if signalErr(refresh.Signal) != nil {
				return nil
			}
			refreshed := []map[string]any{}
			for _, model := range fetched {
				switch GetModelType(model) {
				case "chat", "image", "classifier":
					refreshed = append(refreshed, model)
				}
			}
			persist, err := json.Marshal(map[string]any{"models": refreshed, "checkedAt": time.Now().UnixMilli()})
			if err != nil {
				return err
			}
			_, err = refresh.Publish(ModelsPublication{Persist: persist, Update: func() error {
				mu.Lock()
				dynamicModels = refreshed
				mu.Unlock()
				return nil
			}})
			return err
		}
	}
	dispatch := func(simple bool) ProviderStreamFunc {
		return func(model, context map[string]any, opts ProviderStreamOptions) (*ModelEventStream, error) {
			implementation, ok := apiFor(model)
			if !ok {
				return providerSetupErrorStream(model, fmt.Errorf(`Provider %s has no API implementation for "%s"`, options.ID, apiText(model))), nil
			}
			if simple {
				return implementation.StreamSimple(model, context, opts)
			}
			return implementation.Stream(model, context, opts)
		}
	}
	provider.Stream, provider.StreamSimple = dispatch(false), dispatch(true)
	anyDeferredFetch, anyDeferredCancel := false, false
	for _, entry := range streams {
		anyDeferredFetch = anyDeferredFetch || entry.FetchDeferred != nil
		anyDeferredCancel = anyDeferredCancel || entry.CancelDeferred != nil
	}
	if anyDeferredFetch {
		provider.FetchDeferred = func(model, handle map[string]any, opts ProviderStreamOptions) (*ModelEventStream, error) {
			implementation, ok := apiFor(model)
			if !ok || implementation.FetchDeferred == nil {
				return providerSetupErrorStream(model, fmt.Errorf(`Provider %s does not support deferred responses for "%s"`, options.ID, apiText(model))), nil
			}
			stream, err := implementation.FetchDeferred(model, handle, opts)
			if err != nil {
				return providerSetupErrorStream(model, err), nil
			}
			return stream, nil
		}
	}
	if anyDeferredCancel {
		provider.CancelDeferred = func(model, handle map[string]any, opts ProviderStreamOptions) error {
			implementation, ok := apiFor(model)
			if !ok || implementation.CancelDeferred == nil {
				return fmt.Errorf(`Provider %s cannot cancel deferred responses for "%s"`, options.ID, apiText(model))
			}
			return implementation.CancelDeferred(model, handle, opts)
		}
	}
	if len(options.Images) > 0 {
		provider.GenerateImages = func(model, request map[string]any, opts ProviderOperationOptions) (map[string]any, error) {
			api, _ := model["api"].(string)
			implementation, ok := options.Images[api]
			if !ok {
				return map[string]any{
					"api": model["api"], "provider": model["provider"], "model": model["id"], "output": []any{}, "stopReason": "error",
					"errorMessage": fmt.Sprintf(`Provider %s has no image generation implementation for "%s"`, options.ID, apiText(model)), "timestamp": time.Now().UnixMilli(),
				}, nil
			}
			return implementation(model, request, opts)
		}
	}
	if len(options.Classifiers) > 0 {
		provider.Classify = func(model map[string]any, request ClassifierContext, opts ProviderOperationOptions) (ClassifierResult, error) {
			api, _ := model["api"].(string)
			implementation, ok := options.Classifiers[api]
			if !ok {
				name, _ := model["provider"].(string)
				id, _ := model["id"].(string)
				return ClassifierResult{
					API: api, Provider: name, Model: id, Answers: NewOrderedObject(), StopReason: "error", Timestamp: time.Now().UnixMilli(),
					ErrorMessage: fmt.Sprintf(`Provider %s has no classifier implementation for "%s"`, options.ID, apiText(model)),
				}, nil
			}
			return implementation(model, request, opts)
		}
	}
	return provider, nil
}

// providerSetupErrorStream is pi-ai's lazyStream (api/lazy.ts) of a setup that threw: the stream ends with an error event carrying the error message of an empty assistant message.
func providerSetupErrorStream(model map[string]any, err error) *ModelEventStream {
	now := time.Now().UnixMilli()
	message := map[string]any{
		"role": "assistant", "content": []any{}, "api": model["api"], "provider": model["provider"], "model": model["id"],
		"usage": map[string]any{
			"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
			"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
		},
		"stopReason": "error", "errorMessage": err.Error(), "timestamp": now,
	}
	stream := CreateAssistantMessageEventStream()
	stream.Push(map[string]any{"type": "error", "reason": "error", "error": message})
	return stream
}

// apiText is a model's api as a JavaScript template literal prints it: "undefined" for an absent one.
func apiText(model map[string]any) string {
	if api, ok := model["api"]; ok && api != nil {
		return fmt.Sprint(api)
	}
	return "undefined"
}
