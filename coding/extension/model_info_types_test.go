package extension

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func roundTrip(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// packages/ai/src/types.ts ImageModel and ClassifierModel: an extension reads a non-chat model with its type, its own API, its provider and
// the fields of its variant, and nothing of a chat model's (reasoning, maxTokens, ...).
func TestAnyModelInfoProjectsImageAndClassifierModels(t *testing.T) {
	image := &ai.ImageModel{ID: "flux", Name: "Flux", API: "openrouter-images", Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Input: []string{"text", "image"}, Output: []string{"image", "text"}, Headers: map[string]string{"X-Title": "pi"}, Cost: ai.ModelCost{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}}
	got := roundTrip(t, AnyModelInfo(image))
	want := map[string]any{
		"type": "image", "id": "flux", "modelId": "flux", "name": "Flux", "api": "openrouter-images", "provider": "openrouter", "baseUrl": "https://openrouter.ai/api/v1",
		"input": []any{"text", "image"}, "output": []any{"image", "text"}, "headers": map[string]any{"X-Title": "pi"},
		"cost": map[string]any{"input": float64(1), "output": float64(2), "cacheRead": float64(3), "cacheWrite": float64(4)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("image = %v\nwant %v", got, want)
	}

	classifier := &ai.ClassifierModel{ID: "jev-latest", Name: "Jev", API: "typesafe-system-one", Provider: "typesafe", BaseURL: "https://api.typesafe.ai/v1/", Input: []string{"text"}, ContextWindow: 64000, InputLimits: &ai.ModelInputLimits{}}
	got = roundTrip(t, AnyModelInfo(classifier))
	if got["type"] != "classifier" || got["contextWindow"] != float64(64000) || got["api"] != "typesafe-system-one" || got["provider"] != "typesafe" || got["cost"] == nil {
		t.Fatalf("classifier = %v", got)
	}
	for _, key := range []string{"reasoning", "maxTokens", "output", "headers", "thinkingLevelMap", "compat"} {
		if _, ok := got[key]; ok {
			t.Errorf("a classifier carries the %q of another variant: %v", key, got)
		}
	}
	// A chat model is ModelInfo, with the type a chat entry has.
	chat := &ai.Model{ID: "gpt", DisplayName: "GPT", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "openai", API: "openai-completions"}}
	if info := AnyModelInfo(chat); info["id"] != "gpt" || info["provider"] != "openai" || info["reasoning"] != false {
		t.Fatalf("chat = %v", info)
	}
	if AnyModelInfo(nil) != nil || AnyModelInfo((*ai.ImageModel)(nil)) != nil {
		t.Fatal("a nil model must project to nil")
	}
}
