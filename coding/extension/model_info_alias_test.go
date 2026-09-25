package extension

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestModelInfoDoesNotAliasMutableMetadata(t *testing.T) {
	strict := false
	model := &ai.Model{
		ID:             "model",
		SamplingParams: map[string]any{"nested": map[string]any{"value": "original"}},
		ProviderMeta: ai.ProviderMetadata{
			ProviderID: "provider",
			Compat: &ai.OpenAICompat{
				SupportsStrictMode: &strict,
				OpenRouterRouting:  map[string]any{"nested": map[string]any{"value": "original"}},
			},
		},
	}
	projected := ModelInfo(model)
	compat := projected["compat"].(*ai.OpenAICompat)
	*compat.SupportsStrictMode = true
	compat.OpenRouterRouting["nested"].(map[string]any)["value"] = "changed"
	projected["samplingParams"].(map[string]any)["nested"].(map[string]any)["value"] = "changed"
	if *model.ProviderMeta.Compat.SupportsStrictMode {
		t.Error("projected compat bool aliases runtime metadata")
	}
	if got := model.ProviderMeta.Compat.OpenRouterRouting["nested"].(map[string]any)["value"]; got != "original" {
		t.Errorf("projected compat map aliases runtime metadata: %v", got)
	}
	if got := model.SamplingParams["nested"].(map[string]any)["value"]; got != "original" {
		t.Errorf("projected sampling map aliases runtime metadata: %v", got)
	}
}

func TestModelInfoInputLimitsProjection(t *testing.T) {
	model := &ai.Model{InputLimits: &ai.ModelInputLimits{MaxRequestBytes: 12345, Images: &ai.ModelImageInputLimits{MaxPerMessage: 7, MaxPerRequest: 11, Resize: &ai.ModelImageResizeOptions{MaxWidth: 321, MaxHeight: 123, MaxBytes: 45678, JPEGQuality: 67}}}}
	projected := ModelInfo(model)
	limits, ok := projected["inputLimits"].(*ai.ModelInputLimits)
	if !ok || limits == nil {
		t.Fatalf("inputLimits missing: %#v", projected)
	}
	if limits.MaxRequestBytes != 12345 || limits.Images.MaxPerMessage != 7 || limits.Images.MaxPerRequest != 11 || *limits.Images.Resize != *model.InputLimits.Images.Resize {
		t.Fatalf("inputLimits = %+v", limits)
	}
	limits.Images.Resize.MaxWidth = 1
	if model.InputLimits.Images.Resize.MaxWidth != 321 {
		t.Fatal("projection aliases model resize options")
	}
	if _, exists := ModelInfo(&ai.Model{})["inputLimits"]; exists {
		t.Fatal("omitted limits must remain absent")
	}
}
