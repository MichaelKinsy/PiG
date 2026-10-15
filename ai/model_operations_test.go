package ai

import (
	"errors"
	"testing"
)

// packages/ai/src/utils/model-operations.ts:14-76 (getModelType, isModelType, assert*Model, imageErrorResult, classifierErrorResult).
func TestModelOperations(t *testing.T) {
	chat := &Model{ID: "c", ProviderMeta: ProviderMetadata{ProviderID: "p"}}
	typedChat := &Model{ID: "c", ProviderMeta: ProviderMetadata{ProviderID: "p"}, Type: ModelTypeChat}
	image := &ImageModel{ID: "i", Provider: "p", API: "img-api"}
	classifier := &ClassifierModel{ID: "k", Provider: "p", API: "cls-api"}

	// :14 a model without a type is a chat model.
	for _, tc := range []struct {
		model AnyModel
		want  ModelType
	}{{chat, ModelTypeChat}, {typedChat, ModelTypeChat}, {image, ModelTypeImage}, {classifier, ModelTypeClassifier}} {
		if got := GetModelType(tc.model); got != tc.want {
			t.Errorf("GetModelType(%T)=%q want %q", tc.model, got, tc.want)
		}
		if !IsModelType(tc.model, tc.want) || IsModelType(tc.model, "other") {
			t.Errorf("IsModelType(%T, %q) wrong", tc.model, tc.want)
		}
	}

	// :23-41 each assertion names the model and the missing type with a provider-coded ModelsError.
	for _, tc := range []struct {
		name string
		got  error
		want string
	}{
		{"chat accepts chat", AssertChatModel(chat), ""},
		{"chat rejects", AssertChatModel(&Model{ID: "x", ProviderMeta: ProviderMetadata{ProviderID: "p"}, Type: ModelTypeImage}), "Model p/x is not a chat model"},
		{"image accepts image", AssertImageModel(image), ""},
		{"image rejects chat", AssertImageModel(chat), "Model p/c is not an image model"},
		{"classifier accepts classifier", AssertClassifierModel(classifier), ""},
		{"classifier rejects image", AssertClassifierModel(image), "Model p/i is not a classifier model"},
	} {
		if tc.want == "" {
			if tc.got != nil {
				t.Errorf("%s: %v", tc.name, tc.got)
			}
			continue
		}
		var modelsErr *ModelsError
		if !errors.As(tc.got, &modelsErr) || modelsErr.Code != ModelsErrorProvider || tc.got.Error() != tc.want {
			t.Errorf("%s: %v", tc.name, tc.got)
		}
	}

	// :43-76 failures become results: error by default, aborted when cancelled, with the model identity and an empty payload.
	cause := errors.New("boom")
	images := ImageErrorResult(image, cause, false)
	if images.StopReason != ImagesStopReasonError || images.ErrorMessage != "boom" || images.API != "img-api" || images.Provider != "p" || images.Model != "i" || images.Output == nil || len(images.Output) != 0 || images.Timestamp == 0 {
		t.Errorf("image error result=%+v", images)
	}
	if got := ImageErrorResult(image, cause, true); got.StopReason != ImagesStopReasonAborted {
		t.Errorf("image aborted result=%+v", got)
	}
	result := ClassifierErrorResult(classifier, cause, false)
	if result.StopReason != ClassifierStopReasonError || result.ErrorMessage != "boom" || result.API != "cls-api" || result.Provider != "p" || result.Model != "k" || len(result.Answers) != 0 || result.Timestamp == 0 {
		t.Errorf("classifier error result=%+v", result)
	}
	if got := ClassifierErrorResult(classifier, cause, true); got.StopReason != ClassifierStopReasonAborted {
		t.Errorf("classifier aborted result=%+v", got)
	}
}
