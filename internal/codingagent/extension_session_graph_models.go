// pig-specific: conversions the host-owned ModelRuntime of the session graph (coding/session_graph_model_runtime.go) needs from this package's extension wire code.
package codingagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// GraphModelStreamRequest turns an extension's model request (the wire map of a modelStream call) into the provider call's context, transcript and options, as the Session's stream does.
func GraphModelStreamRequest(ctx context.Context, model *ai.Model, request map[string]any, bridgeRequest extension.ModelStreamRequest) (context.Context, ai.Context, ai.StreamOptions, error) {
	return decodeSubprocessStream(ctx, model, request, "", bridgeRequest)
}

// GraphAPIModel decodes a model object an extension passed that its runtime does not list (Pi's stream takes any Model object).
func GraphAPIModel(info map[string]any) (*ai.Model, error) {
	return subprocessAPIModel(info)
}

// GraphProviderInput is the ProviderConfigInput of the declarative ProviderConfig JSON an extension passed to registerProvider.
func GraphProviderInput(id string, raw json.RawMessage) (ProviderConfigInput, error) {
	var config extension.ProviderConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return ProviderConfigInput{}, fmt.Errorf("provider %s: %w", id, err)
	}
	registered, ok := providerConfigFromRegistration(config)
	if !ok {
		return ProviderConfigInput{}, fmt.Errorf("provider %s: the configuration is invalid", id)
	}
	return legacyProviderInput(id, registered), nil
}

// GraphProviderConfig is the JSON of a registered ProviderConfigInput, as Pi's getRegisteredProviderConfig returns it for a configuration without functions.
func GraphProviderConfig(input *ProviderConfigInput) (map[string]any, error) {
	if input == nil {
		return nil, nil
	}
	out := map[string]any{}
	if input.Name != "" {
		out["name"] = input.Name
	}
	if input.BaseURL != "" {
		out["baseUrl"] = input.BaseURL
	}
	if input.APIKey != "" {
		out["apiKey"] = input.APIKey
	}
	if input.API != "" {
		out["api"] = input.API
	}
	if input.Headers != nil {
		out["headers"] = input.Headers
	}
	if input.AuthHeader != nil {
		out["authHeader"] = *input.AuthHeader
	}
	if input.Insecure {
		out["insecure"] = true
	}
	if input.Models != nil {
		models, err := nativeModelConfigs(input.Models)
		if err != nil {
			return nil, err
		}
		out["models"] = models
	}
	return out, nil
}

// GraphClassify is ModelRuntime.classify for a model object, context and options an extension sent. It never fails: a failure is the classifier result Pi returns.
func GraphClassify(ctx context.Context, classify func(context.Context, *ai.ClassifierModel, ai.ClassifierContext, ...ai.ModelsClassifierOptions) ai.ClassifierResult, model map[string]any, request json.RawMessage, options map[string]any) json.RawMessage {
	return extensionClassify(ctx, classify, model, request, options)
}

// GraphGenerateImages is ModelRuntime.generateImages for a model object, context and options an extension sent. It never fails: a failure is the images result Pi returns.
func GraphGenerateImages(ctx context.Context, generate func(context.Context, *ai.ImageModel, ai.ImagesContext, ...ai.ModelsImagesOptions) ai.AssistantImages, model map[string]any, request json.RawMessage, options map[string]any) json.RawMessage {
	return extensionGenerateImages(ctx, generate, model, request, options)
}

// GraphMessages decodes the messages of a request an extension sent (Pi's Message objects as decoded JSON).
func GraphMessages(raw any) ([]ai.Message, error) {
	return subprocessRequestMessages(raw)
}
