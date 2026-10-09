package ai

import (
	"fmt"
	"slices"
)

// Ports the exports of packages/ai/src/utils/model-operations.ts that packages/coding-agent/src/core/model-runtime.ts imports: assertChatModel and imageErrorResult.

// AssertChatModel rejects a model whose type is not chat (model-operations.ts assertChatModel).
func AssertChatModel(model AnyModel) error {
	if !IsModelType(model, ModelTypeChat) {
		return NewModelsError(ModelsErrorProvider, fmt.Sprintf("Model %s/%s is not a chat model", model.ProviderID(), model.ModelID()), nil)
	}
	return nil
}

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

// AssertClassifierInputSupported rejects classifier images for a model whose catalog entry does not accept image input (model-operations.ts assertClassifierInputSupported).
func AssertClassifierInputSupported(model *ClassifierModel, request ClassifierContext) error {
	if len(request.Images) > 0 && !slices.Contains(model.Input, "image") {
		return NewModelsError(ModelsErrorProvider, fmt.Sprintf("Model %s/%s does not accept image input", model.Provider, model.ID), nil)
	}
	return nil
}

// ClassifierErrorResult reports a failed classification as an error or aborted result instead of an error (model-operations.ts classifierErrorResult).
func ClassifierErrorResult(model *ClassifierModel, err error, aborted bool) ClassifierResult {
	return classifierErrorResult(model, err, aborted)
}

// BuiltinClassifiers returns the classifier implementations of one built-in provider by classifier API (all.ts provider assembly).
func BuiltinClassifiers(provider string) ProviderClassifierMap { return builtinClassifiers(provider) }

// AssertImageModel rejects a model whose type is not image (model-operations.ts assertImageModel).
func AssertImageModel(model AnyModel) error {
	if !IsModelType(model, ModelTypeImage) {
		return NewModelsError(ModelsErrorProvider, fmt.Sprintf("Model %s/%s is not an image model", model.ProviderID(), model.ModelID()), nil)
	}
	return nil
}
