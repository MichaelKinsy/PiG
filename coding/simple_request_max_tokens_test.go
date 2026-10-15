package coding

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The agent loop sends no maxTokens (agent-loop.ts streamAssistantResponse), so a simple request without one gets the model's maxTokens
// from the API module's streamSimple: buildBaseOptions (simple-options.ts:46) clamps model.maxTokens to the context window, and
// openai-completions.ts:843-847 and openai-responses.ts:341 send it. For a models.json provider the model runtime applies that default.
func TestModelsJSONSimpleRequestSendsTheModelsMaxTokensLikePi(t *testing.T) {
	for _, tc := range []struct {
		api, field string
	}{
		{"openai-completions", "max_completion_tokens"},
		{"openai-responses", "max_output_tokens"},
	} {
		t.Run(tc.api, func(t *testing.T) {
			received := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Errorf("payload: %v", err)
				}
				select {
				case received <- payload:
				default:
				}
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			agentDir := t.TempDir()
			config := fmt.Sprintf(`{"providers":{"myp":{"apiKey":"k","baseUrl":%q,"api":%q,"models":[{"id":"m1","maxTokens":16384,"contextWindow":200000}]}}}`, server.URL, tc.api)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			model, err := BuildModel("myp/m1", services)
			if err != nil {
				t.Fatal(err)
			}
			input := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}
			services.ModelRuntime().StreamSimple(t.Context(), model, input, ai.StreamOptions{}).Result()
			var payload map[string]any
			select {
			case payload = <-received:
			default:
				t.Fatal("no request reached the server")
			}
			if got := payload[tc.field]; got != float64(16384) {
				t.Fatalf("%s = %v, want the model's 16384", tc.field, got)
			}
		})
	}
}
