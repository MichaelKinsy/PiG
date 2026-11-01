package coding

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/ai/src/api/*.ts call options.onPayload(params, model) with the selected model, whose api names the wire protocol. The
// session's before_provider_request hook is installed as that option, so a model resolved from models.json must reach it with its api.
func TestModelRuntimeStreamPassesSelectedModelAPIToPayloadHook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusBadRequest) }))
	defer server.Close()
	token := "aaa." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc_test"}}`)) + ".bbb"
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses, ai.APIOpenAICodexResponses, ai.APIGoogleGenerativeAI, ai.APIAnthropicMessages, ai.APIMistralConversations} {
		t.Run(string(api), func(t *testing.T) {
			agentDir := t.TempDir()
			config := fmt.Sprintf(`{"providers":{"hooked":{"api":%q,"baseUrl":%q,"apiKey":%q,"models":[{"id":"hook-model","name":"Hook","input":["text"],"contextWindow":100000,"maxTokens":8000}]}}}`, api, server.URL, token)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			model, err := BuildModel("hooked/hook-model", services)
			if err != nil {
				t.Fatal(err)
			}
			var apis []ai.API
			services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}}, ai.StreamOptions{APIKey: token, OnPayload: func(_ any, selected *ai.Model) (any, error) {
				apis = append(apis, selected.ProviderMeta.API)
				return nil, nil
			}}).Result()
			if len(apis) != 1 || apis[0] != api {
				t.Fatalf("hook models' api=%v, want exactly [%s]", apis, api)
			}
		})
	}
}
