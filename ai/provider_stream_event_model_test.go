package ai

import "testing"

// google-generative-ai.ts:107 and google-vertex.ts:116 hand the observer the model the caller selected; the direct path keeps it.
func TestStreamSimpleHandsGoogleObserversTheSelectedModel(t *testing.T) {
	chunk := `{"responseId":"resp_google","candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`
	server := serveSSE(t, sseFrames(chunk))
	model := mustGeneratedModel(t, "google", "gemini-2.5-flash").ToModel()
	model.ProviderMeta.BaseURL = server.URL
	recorder := &providerEventRecorder{}
	stream, err := StreamSimple(t.Context(), model, helloTranscript(), StreamOptions{APIKey: "test-api-key", OnProviderStreamEvent: recorder.observe})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	_, models := recorder.snapshot()
	if len(models) != 1 || models[0] != model {
		t.Fatalf("observed models = %#v, want the selected model", models)
	}
}
