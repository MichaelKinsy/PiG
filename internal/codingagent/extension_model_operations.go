package codingagent

// Ports the typed operations of ctx.modelRegistry that reach the host (packages/coding-agent/src/core/model-registry.ts:135-143 getAvailableOfType and :170-177 classify, through model-runtime.ts:800-815 classify).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// extensionAvailableOfType lists the models of one type whose provider has working credentials, as extension model objects. It is the wire form of ModelRuntime.GetAvailableOfType: chat models follow the provider's chat availability and every other type is kept. A type no model has lists nothing, as Models.getAvailableOfType filters getAllAvailable by type without validating it (packages/ai/src/models.ts:708-716).
func extensionAvailableOfType(ctx context.Context, registry *ModelRegistry, modelType, providerID string) ([]map[string]any, error) {
	models := []map[string]any{}
	if registry == nil {
		return models, nil
	}
	registry.StartRegistrationRefresh(ctx)
	all, err := registry.GetAvailableAllModelDataContext(ctx, providerID)
	if err != nil {
		return nil, err
	}
	for _, model := range all {
		if ai.IsModelType(model, ai.ModelType(modelType)) {
			models = append(models, extension.AnyModelInfo(model))
		}
	}
	return models, nil
}

// classifierWireModel is the extension wire form of a model object the caller passes to classify.
type classifierWireModel struct {
	Type          ai.ModelType         `json:"type"`
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	API           ai.ClassifierAPI     `json:"api"`
	Provider      string               `json:"provider"`
	BaseURL       string               `json:"baseUrl"`
	Headers       map[string]string    `json:"headers"`
	Input         []string             `json:"input"`
	InputLimits   *ai.ModelInputLimits `json:"inputLimits"`
	Cost          ai.ModelCost         `json:"cost"`
	ContextWindow int                  `json:"contextWindow"`
}

// classifierWireOptions is the serializable part of ai.ModelsClassifierOptions; the request's context carries the signal.
type classifierWireOptions struct {
	APIKey          *string            `json:"apiKey"`
	Headers         ai.ProviderHeaders `json:"headers"`
	Env             ai.ProviderEnv     `json:"env"`
	TimeoutMs       *int               `json:"timeoutMs"`
	MaxRetries      *int               `json:"maxRetries"`
	MaxRetryDelayMs *int               `json:"maxRetryDelayMs"`
	Temperature     *float64           `json:"temperature"`
}

func (o classifierWireOptions) options() ai.ModelsClassifierOptions {
	options := ai.ModelsClassifierOptions{ClassifierOptions: ai.ClassifierOptions{Headers: o.Headers, Env: o.Env, TimeoutMs: o.TimeoutMs, MaxRetries: o.MaxRetries, MaxRetryDelayMs: o.MaxRetryDelayMs, Temperature: o.Temperature}}
	if o.APIKey != nil {
		options.APIKey, options.APIKeySet = *o.APIKey, true
	}
	return options
}

// extensionClassify classifies with the model object the extension passes. It never returns an error: a request it cannot read, a model that is not a classifier, and every failure of the runtime are error results, as ModelRuntime.classify reports them (model-runtime.ts:812).
func extensionClassify(ctx context.Context, classify func(context.Context, *ai.ClassifierModel, ai.ClassifierContext, ...ai.ModelsClassifierOptions) ai.ClassifierResult, model map[string]any, request json.RawMessage, options map[string]any) json.RawMessage {
	var wire classifierWireModel
	encodedModel, _ := json.Marshal(model)
	_ = json.Unmarshal(encodedModel, &wire)
	target := &ai.ClassifierModel{ID: wire.ID, Name: wire.Name, API: wire.API, Provider: wire.Provider, BaseURL: wire.BaseURL, Headers: wire.Headers, Input: wire.Input, InputLimits: wire.InputLimits, Cost: wire.Cost, ContextWindow: wire.ContextWindow}
	result := func() ai.ClassifierResult {
		if wire.Type != ai.ModelTypeClassifier {
			// upstream: model-operations.ts:assertClassifierModel names the model that is not a classifier.
			return ai.ClassifierErrorResult(target, ai.AssertClassifierModel(&ai.Model{ID: wire.ID, ProviderMeta: ai.ProviderMetadata{ProviderID: wire.Provider}}), ctx.Err() != nil)
		}
		var input ai.ClassifierContext
		if err := json.Unmarshal(request, &input); err != nil {
			return ai.ClassifierErrorResult(target, fmt.Errorf("invalid classifier context: %w", err), ctx.Err() != nil)
		}
		var wireOptions classifierWireOptions
		if options != nil {
			encodedOptions, _ := json.Marshal(options)
			if err := json.Unmarshal(encodedOptions, &wireOptions); err != nil {
				return ai.ClassifierErrorResult(target, fmt.Errorf("invalid classifier options: %w", err), ctx.Err() != nil)
			}
		}
		if classify == nil {
			return ai.ClassifierErrorResult(target, fmt.Errorf("classification is not available"), false)
		}
		return classify(ctx, target, input, wireOptions.options())
	}()
	encoded, err := json.Marshal(result)
	if err != nil {
		encoded, _ = json.Marshal(ai.ClassifierErrorResult(target, err, false))
	}
	return encoded
}
