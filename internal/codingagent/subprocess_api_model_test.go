package codingagent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi core/model-runtime.ts:573-647 keeps the supplied model after auth resolution.
// A private child runtime can contain a model absent from the main catalog.
func TestSubprocessAPIStreamKeepsIndependentModel(t *testing.T) {
	handle := &modelRequestCaptureHandle{recordingCompactHandle: &recordingCompactHandle{}}
	ctx := extension.WithModelStreamRequest(t.Context(), extension.ModelStreamRequest{API: true})
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		model := map[string]any{
			"id": "private/model", "provider": "child-only", "api": api, "name": "Private",
			"baseUrl": "https://child.invalid/v1", "reasoning": false, "contextWindow": 32768, "maxTokens": 321,
			"headers": map[string]string{"X-Child": "model"}, "input": []string{"text", "image"},
			"cost": map[string]any{"input": 1.5, "output": 2.5, "cacheRead": 0.25, "cacheWrite": 0.75},
		}
		_, err := streamModelForSubprocess(ctx, model, map[string]any{"messages": []any{}, "apiKey": "child-key", "timeoutMs": 1234, "websocketConnectTimeoutMs": 5678, "cacheRetention": "long", "toolChoice": "required", "metadata": map[string]any{"child": true}, "maxRetries": 2, "maxRetryDelayMs": 2000}, ModelOperationBindings{
			ModelBuilder: func(string) (*ai.Model, error) { return nil, errors.New("not in parent catalog") }, SessionHandle: handle,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := handle.model; got.ID != "private/model" || got.ProviderMeta.ProviderID != "child-only" || got.ProviderMeta.API != api || got.ProviderMeta.BaseURL != "https://child.invalid/v1" || got.Capabilities.InputCostPer1M != 1.5 || !got.Capabilities.SupportsImages {
			t.Fatalf("API model was replaced: %#v", got)
		}
		if handle.options.APIKey != "child-key" {
			t.Fatalf("API key = %q", handle.options.APIKey)
		}
		if handle.options.TimeoutMs == nil || *handle.options.TimeoutMs != 1234 || handle.options.WebSocketConnectTimeoutMs == nil || *handle.options.WebSocketConnectTimeoutMs != 5678 || handle.options.CacheRetention != ai.CacheRetentionLong || handle.options.ToolChoice != "required" || handle.options.Metadata["child"] != true || ai.ProviderMaxRetries(handle.ctx) != 2 {
			t.Fatalf("child request options lost: %+v, retries=%d", handle.options, ai.ProviderMaxRetries(handle.ctx))
		}
	}
	_, err := streamModelForSubprocess(context.Background(), map[string]any{"provider": "missing", "modelId": "missing"}, nil, ModelOperationBindings{
		ModelBuilder: func(string) (*ai.Model, error) { return nil, errors.New("catalog error") }, SessionHandle: handle,
	})
	if err == nil || err.Error() != "catalog error" {
		t.Fatalf("registry call error = %v", err)
	}
}
