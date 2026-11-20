package llama

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The ai classifier model the provider publishes carries the input and System One compat of a decision model that takes images.
func TestAIClassifierModelCarriesImageInputOfDecisionModels(t *testing.T) {
	model := toPiClassifierModel(LlamaModelInfo{ID: "clef", Architecture: &LlamaModelArchitecture{InputModalities: []string{"text", "image"}, OutputModalities: []string{"decisions"}}}, "http://localhost:8080", 0)
	got := model.aiModel()
	if len(got.Input) != 2 || got.Input[1] != "image" || got.Compat == nil || got.Compat.ImageInput != ai.SystemOneImageInputImages || got.Compat.SendImageOptInHeader == nil || *got.Compat.SendImageOptInHeader {
		t.Fatalf("ai model = %+v compat=%+v", got, got.Compat)
	}
	text := toPiClassifierModel(LlamaModelInfo{ID: "kev", Architecture: &LlamaModelArchitecture{InputModalities: []string{"text"}, OutputModalities: []string{"decisions"}}}, "http://localhost:8080", 0)
	if got := text.aiModel(); len(got.Input) != 1 || got.Compat != nil {
		t.Fatalf("text-only ai model = %+v", got)
	}
}
