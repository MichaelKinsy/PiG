package coding

// pi: packages/coding-agent/src/core/model-runtime.ts

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// D626 (the record's fourth claim): a request for a model the host built from a name takes the registry's current entry of that name, where Pi's prepareRequest sends the model it is given (model-runtime.ts:667-689). The
// model was built when models.json named the first endpoint and a maxTokens of 100; models.json then changes its endpoint and maxTokens and the registry reloads it. The request goes to the second endpoint with the new limit.
// A model object an extension set (an edited copy of the same entry) is sent as it is, as in Pi: its own endpoint and limit.
func TestRequestBuiltFromAModelNameTakesTheRegistrysCurrentEntry(t *testing.T) {
	for _, tc := range []struct {
		name string
		// object makes the model an extension set (an edited copy that names the first endpoint) instead of the one the host built from the name.
		object bool
	}{
		{name: "model built from a name", object: false},
		{name: "model object an extension set", object: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, firstRequests := modelRequestServer(t)
			second, secondRequests := modelRequestServer(t)
			agentDir := t.TempDir()
			t.Setenv("PIG_HOME", t.TempDir())
			writeModels := func(baseURL string, maxTokens int) {
				t.Helper()
				data := `{"providers":{"filed":{"baseUrl":"` + baseURL + `","apiKey":"registry-key","api":"openai-completions","models":[{"id":"chat-1","name":"Chat 1","contextWindow":5555,"maxTokens":` +
					strconv.Itoa(maxTokens) + `,"compat":{"maxTokensField":"max_tokens"}}]}}}`
				if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeModels(first.URL, 100)
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			runtime := services.ModelRuntime()
			model, err := BuildModel("filed/chat-1", services)
			if err != nil {
				t.Fatal(err)
			}
			if model.ProviderMeta.BaseURL != first.URL {
				t.Fatalf("the model built from the name has base URL %q, want the first endpoint %q", model.ProviderMeta.BaseURL, first.URL)
			}
			if tc.object {
				model = runtime.BindModelObject(editedModelObject(t, model, func(object map[string]any) { object["maxTokens"] = 321 }))
			}
			writeModels(second.URL, 777)
			services.Registry().ModelRegistry.Refresh()

			message := runtime.CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
			if message.StopReason != ai.StopReasonError || !strings.Contains(message.ErrorMessage, "fixture request captured") {
				t.Fatalf("the request did not reach a fixture endpoint: %+v", message)
			}
			wantServed, wantOther, wantMaxTokens := secondRequests, firstRequests, float64(777)
			if tc.object {
				wantServed, wantOther, wantMaxTokens = firstRequests, secondRequests, 321
			}
			if other := wantOther(); len(other) != 0 {
				t.Errorf("the endpoint the request must not use received %d request(s)", len(other))
			}
			seen := wantServed()
			if len(seen) != 1 {
				t.Fatalf("the endpoint of the request received %d requests, want 1", len(seen))
			}
			if got := seen[0].body["max_tokens"]; got != wantMaxTokens {
				t.Errorf("request max_tokens = %v, want %v (body %v)", got, wantMaxTokens, seen[0].body)
			}
		})
	}
}
