package codingagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// packages/ai/src/types.ts ProviderRequestOptions: an extension's ModelRegistry.generateImages options carry maxRetries and maxRetryDelayMs to the provider request (api/openrouter-images.ts passes both to retryProviderRequest). Every SDK serializes maxRetryDelayMs, so the host must not drop it.
func TestExtensionGenerateImagesForwardsRetryOptions(t *testing.T) {
	var got ai.ModelsImagesOptions
	generate := func(_ context.Context, model *ai.ImageModel, _ ai.ImagesContext, options ...ai.ModelsImagesOptions) ai.AssistantImages {
		got = options[0]
		return ai.AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, StopReason: ai.ImagesStopReasonStop}
	}
	model := map[string]any{"type": "image", "id": "m", "api": "openrouter-images", "provider": "openrouter"}
	options := map[string]any{"maxRetries": 2, "maxRetryDelayMs": 1500, "timeoutMs": 900, "apiKey": "k"}
	encoded := extensionGenerateImages(t.Context(), generate, model, json.RawMessage(`{"input":[]}`), options)
	var result ai.AssistantImages
	if err := json.Unmarshal(encoded, &result); err != nil || result.StopReason != ai.ImagesStopReasonStop {
		t.Fatalf("result = %s, %v", encoded, err)
	}
	if got.MaxRetries == nil || *got.MaxRetries != 2 || got.MaxRetryDelayMs == nil || *got.MaxRetryDelayMs != 1500 || got.TimeoutMs != 900 || got.APIKey != "k" || !got.APIKeySet {
		t.Errorf("options = maxRetries %v maxRetryDelayMs %v timeoutMs %d apiKey %q/%v", got.MaxRetries, got.MaxRetryDelayMs, got.TimeoutMs, got.APIKey, got.APIKeySet)
	}
}
