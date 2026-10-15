package coding

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestModelRuntimeGoogleThinkingMapReachesProvider(t *testing.T) {
	for _, api := range []ai.API{ai.APIGoogleGenerativeAI, ai.APIGoogleVertex} {
		t.Run(string(api), func(t *testing.T) {
			agentDir := t.TempDir()
			providerID := "google"
			if api == ai.APIGoogleVertex {
				providerID = "google-vertex"
			}
			config := fmt.Sprintf(`{"providers":{%q:{"api":%q,"baseUrl":"https://example.invalid/v1","apiKey":"test","models":[{"id":"gemini-3.8-flash","name":"Mapped Google","reasoning":true,"input":["text"],"contextWindow":128000,"maxTokens":4096,"thinkingLevelMap":{"off":null,"minimal":null,"low":"LOW","medium":"MEDIUM","high":"HIGH","xhigh":null,"max":null}}]}}}`, providerID, api)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			model, err := BuildModel(providerID+"/gemini-3.8-flash", services)
			if err != nil {
				t.Fatal(err)
			}
			var captured struct {
				Config struct {
					ThinkingConfig map[string]any `json:"thinkingConfig"`
				} `json:"config"`
			}
			result := services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}, ai.StreamOptions{APIKey: "test", OnPayload: func(payload any, _ *ai.Model) (any, error) {
				data, err := json.Marshal(payload)
				if err != nil {
					return nil, err
				}
				if err = json.Unmarshal(data, &captured); err != nil {
					return nil, err
				}
				return nil, errors.New("payload captured")
			}}).Result()
			if !strings.Contains(result.ErrorMessage, "payload captured") {
				t.Fatalf("result=%#v", result)
			}
			thinking := captured.Config.ThinkingConfig
			if len(thinking) != 1 || thinking["thinkingLevel"] != "LOW" {
				t.Fatalf("thinking config=%#v", thinking)
			}
		})
	}
}
