package ai

import "fmt"

// Ports the exports of packages/ai/src/utils/model-operations.ts that packages/coding-agent/src/core/model-runtime.ts imports: assertChatModel and imageErrorResult.

// AssertChatModel rejects a model whose type is not chat (model-operations.ts assertChatModel).
func AssertChatModel(model *Model) error { return assertChatModel(model) }

// ImageErrorResult reports a failed image request as an error or aborted result instead of an error (model-operations.ts imageErrorResult).
func ImageErrorResult(model *ImageModel, err error, aborted bool) AssistantImages {
	return imageErrorResult(model, err, aborted)
}

// AssertClassifierModel rejects a model whose type is not classifier (model-operations.ts assertClassifierModel).
func AssertClassifierModel(model AnyModel) error {
	if !IsModelType(model, ModelTypeClassifier) {
		return NewModelsError(ModelsErrorProvider, fmt.Sprintf("Model %s/%s is not a classifier model", model.ProviderID(), model.ModelID()), nil)
	}
	return nil
}

// ClassifierErrorResult reports a failed classification as an error or aborted result instead of an error (model-operations.ts classifierErrorResult).
func ClassifierErrorResult(model *ClassifierModel, err error, aborted bool) ClassifierResult {
	return classifierErrorResult(model, err, aborted)
}

// BuiltinClassifiers returns the classifier implementations of one built-in provider by classifier API (all.ts provider assembly).
func BuiltinClassifiers(provider string) ProviderClassifierMap { return builtinClassifiers(provider) }
