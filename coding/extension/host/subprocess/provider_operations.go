package subprocess

// Ports the image and classifier implementations of a provider (packages/coding-agent/src/core/extensions/types.ts:1896-1898 ProviderConfig.images and ProviderConfig.classifiers; packages/ai/src/models.ts createProvider) across the extension boundary.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// RequestProviderOperation runs an image or classifier implementation of a provider config in its owning extension. The request carries the provider in Tool and {kind, api, model, context, options} in Args; the response is the AssistantImages or ClassifierResult, and a callback failure is the request's error.
const RequestProviderOperation = "provider_operation"

// The kinds of a provider operation.
const (
	ProviderOperationImages      = "images"
	ProviderOperationClassifiers = "classifiers"
)

func imagesOptionsWire(o ai.ImagesOptions) map[string]any {
	values := map[string]any{}
	if o.APIKey != "" || o.APIKeySet {
		values["apiKey"] = o.APIKey
	}
	if o.Headers != nil {
		values["headers"] = o.Headers
	}
	if o.Env != nil {
		values["env"] = o.Env
	}
	if o.Metadata != nil {
		values["metadata"] = o.Metadata
	}
	if o.TimeoutMs != 0 {
		values["timeoutMs"] = o.TimeoutMs
	}
	if o.MaxRetries != nil {
		values["maxRetries"] = *o.MaxRetries
	}
	if o.MaxRetryDelayMs != nil {
		values["maxRetryDelayMs"] = *o.MaxRetryDelayMs
	}
	return values
}

func classifierOptionsWire(o ai.ClassifierOptions) map[string]any {
	values := map[string]any{}
	if o.APIKey != "" || o.APIKeySet {
		values["apiKey"] = o.APIKey
	}
	if o.Headers != nil {
		values["headers"] = o.Headers
	}
	if o.Env != nil {
		values["env"] = o.Env
	}
	if o.TimeoutMs != nil {
		values["timeoutMs"] = *o.TimeoutMs
	}
	if o.MaxRetries != nil {
		values["maxRetries"] = *o.MaxRetries
	}
	if o.MaxRetryDelayMs != nil {
		values["maxRetryDelayMs"] = *o.MaxRetryDelayMs
	}
	if o.Temperature != nil {
		values["temperature"] = *o.Temperature
	}
	return values
}

// attachProviderOperations wires the implementations a provider declaration names to the extension that owns them: the streamSimple callback, and the image and classifier APIs.
// pig additive (D19): connection-owned callbacks carry the image and classifier implementations across the SDK boundary without changing registry scope.
func (h *Host) attachProviderOperations(me *managedExt, provider ProviderDecl, cfg *extension.ProviderConfig) {
	if provider.StreamSimple {
		cfg.StreamSimple = h.providerStreamCallback(me, provider.Name)
	}
	if len(provider.ImageAPIs) > 0 {
		cfg.Images = make(ai.ProviderImageAPIMap, len(provider.ImageAPIs))
		for _, api := range provider.ImageAPIs {
			cfg.Images[ai.ImageAPI(api)] = &ai.ProviderImages{GenerateImages: func(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
				return remoteOperation[ai.AssistantImages](ctx, me, provider.Name, ProviderOperationImages, api, extension.AnyModelInfo(model), request, imagesOptionsWire(options))
			}}
		}
	}
	if len(provider.ClassifierAPIs) > 0 {
		cfg.Classifiers = make(ai.ProviderClassifierMap, len(provider.ClassifierAPIs))
		for _, api := range provider.ClassifierAPIs {
			cfg.Classifiers[ai.ClassifierAPI(api)] = &ai.ProviderClassifier{Classify: func(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
				return remoteOperation[ai.ClassifierResult](ctx, me, provider.Name, ProviderOperationClassifiers, api, extension.AnyModelInfo(model), request, classifierOptionsWire(options))
			}}
		}
	}
}

func remoteOperation[T any](ctx context.Context, me *managedExt, provider, kind, api string, model map[string]any, request any, options map[string]any) (T, error) {
	var result T
	args, err := json.Marshal(map[string]any{"kind": kind, "api": api, "model": model, "context": request, "options": options})
	if err != nil {
		return result, err
	}
	conn := me.connection()
	if conn == nil {
		return result, fmt.Errorf("provider %q extension is disconnected", provider)
	}
	response, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestProviderOperation, Tool: provider, Args: args}})
	if err != nil {
		return result, err
	}
	if response == nil || response.Response == nil {
		return result, fmt.Errorf("provider %q returned no response", provider)
	}
	if response.Response.Error != nil {
		return result, response.Response.Error.ToError()
	}
	err = json.Unmarshal(response.Response.Result, &result)
	return result, err
}

// generateImages runs the provider object's generateImages in its extension.
func (p *nativeProviderProxy) generateImages(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ai.ImagesOptions) (ai.AssistantImages, error) {
	return nativeObjectValue[ai.AssistantImages](p, ctx, "generateImages", map[string]any{"model": extension.AnyModelInfo(model), "context": request, "options": imagesOptionsWire(options)}, nil)
}

// classify runs the provider object's classify in its extension.
func (p *nativeProviderProxy) classify(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ai.ClassifierOptions) (ai.ClassifierResult, error) {
	return nativeObjectValue[ai.ClassifierResult](p, ctx, "classify", map[string]any{"model": extension.AnyModelInfo(model), "context": request, "options": classifierOptionsWire(options)}, nil)
}

// operations wires the image and classifier methods a provider object declares.
func (p *nativeProviderProxy) operations(carrier *extension.NativeProvider) {
	if slices.Contains(p.declaration.Methods, "generateImages") {
		carrier.GenerateImages = p.generateImages
	}
	if slices.Contains(p.declaration.Methods, "classify") {
		carrier.Classify = p.classify
	}
	if slices.Contains(p.declaration.Methods, "getModels") {
		carrier.GetModels = p.getModels
	}
	if slices.Contains(p.declaration.Methods, "getAllModels") {
		carrier.GetAllModels = p.allModels
	}
	if slices.Contains(p.declaration.Methods, "filterAllModels") {
		carrier.FilterAllModels = p.filterAllModels
	}
	if slices.Contains(p.declaration.Methods, "fetchDeferred") {
		carrier.FetchDeferred = p.fetchDeferred
	}
	if slices.Contains(p.declaration.Methods, "cancelDeferred") {
		carrier.CancelDeferred = p.cancelDeferred
	}
}
