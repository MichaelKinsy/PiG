package coding

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestCustomModelReasoningSurvivesRuntimeWire(t *testing.T) {
	for _, api := range []string{"openai-completions", "openai-responses"} {
		t.Run(api, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"fixture request captured"}}`))
			}))
			defer server.Close()
			services := newTestServices(t)
			if err := services.Registry().RegisterProvider("neuralwatt", extension.ProviderConfig{BaseURL: server.URL, API: ai.API(api), APIKey: "test-key"}); err != nil {
				t.Error(err)
			}
			model, err := BuildModel("neuralwatt/custom", services)
			if err != nil {
				t.Fatal(err)
			}
			model.ProviderMeta.Reasoning = true
			model.Capabilities.MaxThinking = ai.ThinkingHigh
			message := services.ModelRuntime().CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{Thinking: ai.ThinkingHigh})
			if message.StopReason != ai.StopReasonError {
				t.Fatalf("fixture error response = %+v", message)
			}
			select {
			case request := <-requests:
				effort := request["reasoning_effort"]
				if api == "openai-responses" {
					if reasoning, ok := request["reasoning"].(map[string]any); ok {
						effort = reasoning["effort"]
					}
				}
				if request["model"] != "custom" || effort != "high" {
					t.Fatalf("request = %#v", request)
				}
			default:
				t.Fatal("runtime failed before provider request")
			}
		})
	}
}

func TestCustomModelReasoningSurvivesRequestPreparation(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/model-runtime.ts:622-647 passes the selected model to provider dispatch, including resolver-supplied reasoning metadata.
	for _, api := range []string{"openai-completions", "openai-responses"} {
		t.Run(api, func(t *testing.T) {
			services := newTestServices(t)
			if err := services.Registry().RegisterProvider("neuralwatt", extension.ProviderConfig{BaseURL: "https://fixture.invalid/v1", API: ai.API(api), APIKey: "test-key"}); err != nil {
				t.Error(err)
			}
			model, err := BuildModel("neuralwatt/zai-org/GLM-5.1-FP8", services)
			if err != nil {
				t.Fatal(err)
			}
			model.ProviderMeta.Reasoning = true
			model.Capabilities.MaxThinking = ai.ThinkingHigh
			_, _, options, err := services.ModelRuntime().prepareRequest(t.Context(), model, ai.StreamOptions{Thinking: ai.ThinkingHigh})
			if err != nil {
				t.Fatal(err)
			}
			if !options.IsReasoning {
				t.Fatal("request preparation discarded the selected custom model's reasoning metadata")
			}
		})
	}
}
