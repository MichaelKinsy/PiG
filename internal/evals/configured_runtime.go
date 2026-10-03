package evals

// Ports packages/evals/evals/configured-runtime.ts.

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// ModelCostFields is a model's per-token pricing.
type ModelCostFields struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// ModelFields is the model metadata an eval compares.
type ModelFields struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Provider      string          `json:"provider"`
	Reasoning     bool            `json:"reasoning"`
	Input         []string        `json:"input"`
	Cost          ModelCostFields `json:"cost"`
	ContextWindow int             `json:"contextWindow"`
	MaxTokens     int             `json:"maxTokens"`
}

// ProviderProbeResponse summarizes the probe completion.
type ProviderProbeResponse struct {
	Text         string `json:"text"`
	StopReason   string `json:"stopReason"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
}

// ProviderProbe is a completed probe of a configured provider.
type ProviderProbe struct {
	ValidRequestReceived bool                  `json:"validRequestReceived"`
	Model                ModelFields           `json:"model"`
	Response             ProviderProbeResponse `json:"response"`
}

// ProviderRuntimeResult holds either a probe or an error message.
type ProviderRuntimeResult struct {
	*ProviderProbe
	Error string `json:"error,omitempty"`
}

// ProviderRuntimeOutput is the structured output of a provider eval.
type ProviderRuntimeOutput struct {
	Result ProviderRuntimeResult `json:"result"`
}

// AddedModel is a model that models.json added to a built-in provider.
type AddedModel struct {
	Model                   ModelFields `json:"model"`
	ExistingModelsPreserved bool        `json:"existingModelsPreserved"`
}

// AddedModelResult holds either the added model or an error message.
type AddedModelResult struct {
	*AddedModel
	Error string `json:"error,omitempty"`
}

// AddedModelOutput is the structured output of an added-model eval.
type AddedModelOutput struct {
	Result AddedModelResult `json:"result"`
}

// ProviderScenario names the configured model to probe and how to recognize the fixture's probe request.
type ProviderScenario struct {
	ProviderID           string
	ModelID              string
	CreateContext        func() ai.Context
	Options              ai.StreamOptions
	ValidRequestReceived func() bool
}

func modelFields(model *ai.Model) ModelFields {
	cost := model.CostRates()
	return ModelFields{
		ID:            model.ID,
		Name:          model.DisplayName,
		Provider:      model.ProviderID(),
		Reasoning:     model.ProviderMeta.Reasoning,
		Input:         slices.Clone(model.Input),
		Cost:          ModelCostFields{Input: cost.Input, Output: cost.Output, CacheRead: cost.CacheRead, CacheWrite: cost.CacheWrite},
		ContextWindow: model.Capabilities.ContextWindow,
		MaxTokens:     model.Capabilities.MaxOutputTokens,
	}
}

// LoadConfiguredModelRuntime creates a ModelRuntime from agentDir's models.json, auth.json and models-store.json
// without model network access. The caller owns ModelRuntime.Close.
func LoadConfiguredModelRuntime(ctx context.Context, agentDir string) (*coding.ModelRuntime, error) {
	modelsPath := new(filepath.Join(agentDir, "models.json"))
	return coding.CreateModelRuntime(ctx, coding.CreateModelRuntimeOptions{
		ModelsPath:      &modelsPath,
		AuthPath:        filepath.Join(agentDir, "auth.json"),
		ModelsStorePath: filepath.Join(agentDir, "models-store.json"),
	})
}

// InspectProvider reloads the runtime's configuration offline, then completes the scenario's context with the
// configured model. Configuration and lookup failures are structured errors.
func InspectProvider(ctx context.Context, runtime *coding.ModelRuntime, scenario ProviderScenario) ProviderRuntimeOutput {
	runtime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if configurationError := runtime.GetError(); configurationError != "" {
		return ProviderRuntimeOutput{Result: ProviderRuntimeResult{Error: configurationError}}
	}
	model := runtime.GetModel(scenario.ProviderID, scenario.ModelID)
	if model == nil {
		return ProviderRuntimeOutput{Result: ProviderRuntimeResult{Error: fmt.Sprintf("Model %s/%s is unavailable after reload.", scenario.ProviderID, scenario.ModelID)}}
	}
	response := runtime.CompleteSimple(ctx, model, scenario.CreateContext(), scenario.Options)
	return ProviderRuntimeOutput{Result: ProviderRuntimeResult{ProviderProbe: &ProviderProbe{
		ValidRequestReceived: scenario.ValidRequestReceived(),
		Model:                modelFields(model),
		Response: ProviderProbeResponse{
			Text:         ai.ContentText(response.Content),
			StopReason:   string(response.StopReason),
			InputTokens:  response.Usage.Input,
			OutputTokens: response.Usage.Output,
		},
	}}}
}

// InspectAddedModel checks that models.json added modelID to the built-in provider providerID without dropping
// any of the provider's built-in models.
func InspectAddedModel(ctx context.Context, runtime *coding.ModelRuntime, providerID, modelID string) (AddedModelOutput, error) {
	var noModelsPath *string
	pristineRuntime, err := coding.CreateModelRuntime(ctx, coding.CreateModelRuntimeOptions{ModelsPath: &noModelsPath})
	if err != nil {
		return AddedModelOutput{}, err
	}
	defer pristineRuntime.Close()
	pristineProvider := pristineRuntime.GetProvider(providerID)
	if pristineProvider == nil {
		return AddedModelOutput{Result: AddedModelResult{Error: fmt.Sprintf("Built-in provider %s is unavailable.", providerID)}}, nil
	}
	var existingModelIDs []string
	if pristineProvider.GetModels != nil {
		models, err := pristineProvider.GetModels()
		if err != nil {
			return AddedModelOutput{}, err
		}
		for _, model := range models {
			existingModelIDs = append(existingModelIDs, model.ID)
		}
	}
	if len(existingModelIDs) == 0 {
		return AddedModelOutput{Result: AddedModelResult{Error: fmt.Sprintf("Built-in provider %s has no models.", providerID)}}, nil
	}
	runtime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if configurationError := runtime.GetError(); configurationError != "" {
		return AddedModelOutput{Result: AddedModelResult{Error: configurationError}}, nil
	}
	model := runtime.GetModel(providerID, modelID)
	if model == nil {
		return AddedModelOutput{Result: AddedModelResult{Error: fmt.Sprintf("Model %s/%s is unavailable after reload.", providerID, modelID)}}, nil
	}
	preserved := true
	for _, id := range existingModelIDs {
		if runtime.GetModel(providerID, id) == nil {
			preserved = false
			break
		}
	}
	return AddedModelOutput{Result: AddedModelResult{AddedModel: &AddedModel{Model: modelFields(model), ExistingModelsPreserved: preserved}}}, nil
}
