package ai

import (
	"errors"
	"testing"
)

// Ports packages/ai/src/utils/model-operations.ts: getModelType, isModelType, the three assertions and the two error
// results.
func TestModelOperationsUpstream(t *testing.T) {
	chat := &Model{ID: "chat", ProviderMeta: ProviderMetadata{ProviderID: "p"}}
	image := &ImageModel{ID: "image", Provider: "p"}
	classifier := &ClassifierModel{ID: "cls", Provider: "p"}

	t.Run("models without a type are chat models", func(t *testing.T) {
		if GetModelType(chat) != ModelTypeChat || !IsModelType(chat, ModelTypeChat) || IsModelType(chat, ModelTypeImage) {
			t.Fatalf("chat type = %q", GetModelType(chat))
		}
		if GetModelType(image) != ModelTypeImage || GetModelType(classifier) != ModelTypeClassifier {
			t.Fatalf("types = %q %q", GetModelType(image), GetModelType(classifier))
		}
	})
	t.Run("an assertion rejects another type with a provider ModelsError", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			err     error
			message string
		}{
			{"chat", AssertChatModel(&Model{Type: ModelTypeImage, ID: "x", ProviderMeta: ProviderMetadata{ProviderID: "p"}}), "Model p/x is not a chat model"},
			// model-operations.ts:25 takes any model, so an image or classifier model fails the chat assertion.
			{"chat given an image model", AssertChatModel(image), "Model p/image is not a chat model"},
			{"chat given a classifier model", AssertChatModel(classifier), "Model p/cls is not a chat model"},
			{"image", AssertImageModel(classifier), "Model p/cls is not an image model"},
			{"classifier", AssertClassifierModel(image), "Model p/image is not a classifier model"},
		} {
			var modelsError *ModelsError
			if !errors.As(tc.err, &modelsError) || modelsError.Code != ModelsErrorProvider || modelsError.Message != tc.message {
				t.Errorf("%s: err = %v", tc.name, tc.err)
			}
		}
		if AssertChatModel(chat) != nil || AssertImageModel(image) != nil || AssertClassifierModel(classifier) != nil {
			t.Fatal("an assertion rejected its own model type")
		}
	})
	t.Run("error results carry the model identity and the error text", func(t *testing.T) {
		images := ImageErrorResult(image, errors.New("boom"), false)
		if images.API != image.API || images.Provider != "p" || images.Model != "image" || images.StopReason != ImagesStopReasonError || images.ErrorMessage != "boom" || len(images.Output) != 0 || images.Timestamp == 0 {
			t.Fatalf("image error result = %+v", images)
		}
		if aborted := ImageErrorResult(image, errors.New("x"), true); aborted.StopReason != ImagesStopReasonAborted {
			t.Fatalf("aborted image stop reason = %q", aborted.StopReason)
		}
		result := ClassifierErrorResult(classifier, errors.New("bang"), false)
		if result.Provider != "p" || result.Model != "cls" || result.StopReason != ClassifierStopReasonError || result.ErrorMessage != "bang" || len(result.Answers) != 0 || result.Timestamp == 0 {
			t.Fatalf("classifier error result = %+v", result)
		}
		if aborted := ClassifierErrorResult(classifier, errors.New("x"), true); aborted.StopReason != ClassifierStopReasonAborted {
			t.Fatalf("aborted classifier stop reason = %q", aborted.StopReason)
		}
	})
}

// Ports packages/ai/src/utils/models-error.ts: the cause's message joins the message unless the message already
// contains it, and a blank or absent cause leaves the message alone.
func TestModelsErrorCauseDetailUpstream(t *testing.T) {
	cause := errors.New("  disk full \n")
	for _, tc := range []struct {
		name, message string
		cause         error
		want          string
	}{
		{"appends the trimmed cause", "Store write failed", cause, "Store write failed: disk full"},
		{"keeps a message that already says it", "failed: disk full", cause, "failed: disk full"},
		{"ignores a blank cause", "Store write failed", errors.New(" \u00a0 "), "Store write failed"},
		{"ignores a missing cause", "Store write failed", nil, "Store write failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := NewModelsError(ModelsErrorModelSource, tc.message, tc.cause)
			if err.Error() != tc.want || err.Code != ModelsErrorModelSource || !errors.Is(err, tc.cause) && tc.cause != nil {
				t.Fatalf("error = %q (%s), want %q", err.Error(), err.Code, tc.want)
			}
		})
	}
}
