package ai

import "time"

// Ports packages/ai/src/utils/model-operations.ts and the model-type parts of packages/ai/src/types.ts.

// ModelType names what a catalog entry is for. It decides which Models operation accepts the entry.
type ModelType string

const (
	ModelTypeChat       ModelType = "chat"
	ModelTypeImage      ModelType = "image"
	ModelTypeClassifier ModelType = "classifier"
)

// ImageAPI identifies an image-generation provider API.
type ImageAPI string

// ClassifierAPI identifies a classifier provider API.
type ClassifierAPI string

// AnyModel is the closed union of everything a provider can list: *Model (chat), *ImageModel and *ClassifierModel.
type AnyModel interface {
	ModelType() ModelType
	ModelID() string
	ProviderID() string
	// CostRates returns the model's per-million-token prices (types.ts BaseModel.cost). A nil model has no price.
	CostRates() ModelCost
	anyModel()
}

// ImageModel is an image-generation model. It is usable with Models.GenerateImages only.
type ImageModel struct {
	ID       string
	Name     string
	API      ImageAPI
	Provider string
	BaseURL  string
	Headers  map[string]string
	Input    []string
	// InputLimits carries provider input limits and cache-safe preprocessing metadata.
	InputLimits *ModelInputLimits
	// Output always includes "image"; "text" means the model can also return text blocks.
	Output  []string
	Cost    ModelCost
	catalog *catalogShape
}

// ClassifierModel is a structured classifier model. It is usable with Models.Classify only.
type ClassifierModel struct {
	ID            string
	Name          string
	API           ClassifierAPI
	Provider      string
	BaseURL       string
	Headers       map[string]string
	Input         []string
	InputLimits   *ModelInputLimits
	Cost          ModelCost
	ContextWindow int
	catalog       *catalogShape
}

func (*Model) anyModel()           {}
func (*ImageModel) anyModel()      {}
func (*ClassifierModel) anyModel() {}

// ModelType returns the explicit Type, or chat for models without one.
func (m *Model) ModelType() ModelType {
	if m.Type == "" {
		return ModelTypeChat
	}
	return m.Type
}
func (*ImageModel) ModelType() ModelType      { return ModelTypeImage }
func (*ClassifierModel) ModelType() ModelType { return ModelTypeClassifier }

// CostRates returns the image model's per-million-token prices. A nil model has no price.
func (m *ImageModel) CostRates() ModelCost {
	if m == nil {
		return ModelCost{}
	}
	return m.Cost
}

// CostRates returns the classifier model's per-million-token prices. A nil model has no price.
func (m *ClassifierModel) CostRates() ModelCost {
	if m == nil {
		return ModelCost{}
	}
	return m.Cost
}

func (m *Model) ModelID() string           { return m.ID }
func (m *ImageModel) ModelID() string      { return m.ID }
func (m *ClassifierModel) ModelID() string { return m.ID }

func (m *Model) ProviderID() string           { return modelProviderID(m) }
func (m *ImageModel) ProviderID() string      { return m.Provider }
func (m *ClassifierModel) ProviderID() string { return m.Provider }

// anyModelHeaders returns the model's own request headers. Every model type has them (packages/ai/src/types.ts BaseModel.headers).
func anyModelHeaders(model AnyModel) map[string]string {
	switch typed := model.(type) {
	case *Model:
		return typed.ProviderMeta.Headers
	case *ImageModel:
		return typed.Headers
	case *ClassifierModel:
		return typed.Headers
	}
	return nil
}

// GetModelType returns the type of a model. Chat models without an explicit Type are chat models.
func GetModelType(model AnyModel) ModelType { return model.ModelType() }

// IsModelType reports whether model has the given type, including chat models without an explicit Type.
func IsModelType(model AnyModel, modelType ModelType) bool { return model.ModelType() == modelType }

// AnyModels widens a typed model list to the AnyModel union.
func AnyModels[T AnyModel](models []T) []AnyModel {
	out := make([]AnyModel, len(models))
	for i, model := range models {
		out[i] = model
	}
	return out
}

// hasKnownModelType reports whether this version knows the model's type. Models from stores and remote sources may
// have types that only newer versions know.
func hasKnownModelType(model AnyModel) bool { return knownModelType(GetModelType(model)) }

func knownModelType(modelType ModelType) bool {
	switch modelType {
	case ModelTypeChat, ModelTypeImage, ModelTypeClassifier:
		return true
	}
	return false
}

// imageErrorResult reports a failed image request as a result instead of an error.
func imageErrorResult(model *ImageModel, err error, aborted bool) AssistantImages {
	reason := ImagesStopReasonError
	if aborted {
		reason = ImagesStopReasonAborted
	}
	return AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, Output: []ContentBlock{}, StopReason: reason, ErrorMessage: err.Error(), Timestamp: time.Now().UnixMilli()}
}
