package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func TestModelRuntimeForwardsConfiguredThinkingMap(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			dir := t.TempDir()
			config := fmt.Sprintf(`{"providers":{"mapped":{"api":%q,"baseUrl":"https://example.invalid","apiKey":"test-key","compat":{"thinkingFormat":"openrouter"},"models":[{"id":"model","name":"Model","reasoning":true,"thinkingLevelMap":{"off":null,"minimal":null,"low":"vendor-low","medium":null,"high":"vendor-high","xhigh":null,"max":null}}]}}}`, api)
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			model := services.ModelRuntime().GetModel("mapped", "model")
			if model == nil {
				t.Fatal("missing configured model")
			}
			var captured map[string]any
			services.ModelRuntime().Complete(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}, ai.StreamOptions{Thinking: ai.ThinkingLevelLow, IsReasoning: true, OnPayload: func(payload any, _ *ai.Model) (any, error) {
				raw, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(raw, &captured); err != nil {
					return nil, err
				}
				return nil, errors.New("captured")
			}})
			if captured == nil {
				t.Fatal("provider was not reached")
			}
			reasoning, ok := captured["reasoning"].(map[string]any)
			if !ok || reasoning["effort"] != "vendor-low" {
				t.Fatalf("configured map was lost: %#v", captured)
			}
		})
	}
}
