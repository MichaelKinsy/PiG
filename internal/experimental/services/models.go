// Package services defines opt-in experimental service contracts and local providers.
package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// ModelRef identifies a model within its provider.
type ModelRef struct {
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
}

// ModelSummary is the catalog metadata exposed to clients.
type ModelSummary struct {
	ModelRef
	Name      string `json:"name"`
	Reasoning bool   `json:"reasoning"`
}

// ModelsCatalog preserves runtime order and includes an otherwise unavailable selected model.
type ModelsCatalog struct {
	Revision        int            `json:"revision"`
	AvailableModels []ModelSummary `json:"availableModels"`
}

// ModelsConfiguration uses a nil Model for an unconfigured lane.
type ModelsConfiguration struct {
	Model         *ModelRef             `json:"model"`
	ThinkingLevel ai.ModelThinkingLevel `json:"thinkingLevel"`
}

// ModelsRefresh reports idle, refreshing, done, or warning with provider errors.
type ModelsRefresh struct {
	Status string            `json:"status"`
	Errors map[string]string `json:"errors,omitzero"`
}

// ModelsState is the replicated model catalog, lane configuration, and refresh outcome.
type ModelsState struct {
	Catalog       ModelsCatalog       `json:"catalog"`
	Configuration ModelsConfiguration `json:"configuration"`
	Refresh       ModelsRefresh       `json:"refresh"`
}

// Models is the model-selection service. Each call waits for its publications and propagates errors to the caller.
type Models interface {
	State() chord.ReplicatedStateOf[*ModelsState]
	CycleThinking(context.Context) error
	GetThinkingLevels(context.Context) ([]ai.ModelThinkingLevel, error)
	Refresh(context.Context) error
	Select(context.Context, ModelRef) error
	SelectThinking(context.Context, ai.ModelThinkingLevel) error
}

// ModelsDefinition is the pi.models token; Go types and values share a namespace.
var ModelsDefinition = chord.DefineService[Models]("pi.models")

func init() {
	chord.RegisterServiceView(ModelsDefinition, func(resolve func() (Models, error)) Models { return modelsView{resolve: resolve} })
	chord.RegisterRemoteClient(ModelsDefinition, func(service *chord.RemoteService) Models { return remoteModels{service: service} })
}
