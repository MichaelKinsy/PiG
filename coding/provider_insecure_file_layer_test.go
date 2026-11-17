package coding

// pi: packages/coding-agent/src/core/provider-composer.ts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// D36 pin: how a models.json provider entry's `insecure` and a registration of the same provider combine at the request. The two routes a request
// takes decide the transport differently (the table below is the behavior as shipped; the owner's decision on the file/registration rule is pending, and
// a change to it changes this table on purpose):
//   - the model runtime streams a registry-bound model through a provider it rebuilds from ProviderInsecure (coding/services.go, model_runtime_typed.go), where
//     a registration of the provider replaces the file's value and none leaves the file's;
//   - a provider object built straight from an entry (BuildModel, coding/model.go) takes the entry's value, and an entry for a model the file defines is the file's.
//
// A registration states `insecure` true, false or nothing (an SDK's raw config decodes the three states; the exported Go field says true or nothing).
func TestFileInsecureAndRegistrationReachTheRequestAsShipped(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"secured\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}})
	for _, tc := range []struct {
		name         string
		file         string // the entry's insecure member
		registration string // "none", "omitted", "true" or "false"
		runtimeRoute bool   // the request through the model runtime reaches the self-signed endpoint
		builtRoute   bool   // the request through the provider object BuildModel built reaches it
	}{
		{"file opt-in, no registration", `,"insecure":true`, "none", true, true},
		{"file opt-in, registration that states nothing", `,"insecure":true`, "omitted", false, true},
		{"file opt-in, registration that opts in", `,"insecure":true`, "true", true, true},
		{"file without the opt-in, no registration", ``, "none", false, false},
		{"file without the opt-in, registration that states nothing", ``, "omitted", false, false},
		{"file without the opt-in, registration that opts in", ``, "true", true, false},
		{"file false, registration that opts in", `,"insecure":false`, "true", true, false},
		{"file opt-in, registration that turns it off", `,"insecure":true`, "false", false, true},
		{"file without the opt-in, registration that turns it off", ``, "false", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			models := `{"providers":{"corp":{"baseUrl":"` + server.URL + `","api":"openai-completions","apiKey":"file-key","models":[{"id":"m","name":"M","contextWindow":128,"maxTokens":16}]` + tc.file + `}}}`
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			if tc.registration != "none" {
				var config extension.ProviderConfig
				raw := `{"apiKey":"registered-key"`
				if tc.registration != "omitted" {
					raw += `,"insecure":` + tc.registration
				}
				if err := json.Unmarshal([]byte(raw+`}`), &config); err != nil {
					t.Fatal(err)
				}
				if err := services.Registry().RegisterExtensionProvider("corp", config); err != nil {
					t.Fatal(err)
				}
			}
			reaches := func(route string, want bool, stream func() *ai.AssistantMessage) {
				t.Helper()
				before := requests.Load()
				result := stream()
				reached := result.StopReason == ai.StopReasonStop && requests.Load() == before+1
				refused := result.StopReason == ai.StopReasonError && requests.Load() == before
				if want && !reached || !want && !refused {
					t.Fatalf("%s: reaches the self-signed endpoint = %v, want %v: stop=%q error=%q requests=%d", route, reached, want, result.StopReason, result.ErrorMessage, requests.Load()-before)
				}
			}
			reaches("model runtime", tc.runtimeRoute, func() *ai.AssistantMessage {
				model := services.ModelRuntime().GetModel("corp", "m")
				if model == nil {
					t.Fatal("corp/m is not listed")
				}
				return services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
			})
			reaches("provider object built from the entry", tc.builtRoute, func() *ai.AssistantMessage {
				model, err := BuildModel("corp/m", services)
				if err != nil {
					t.Fatal(err)
				}
				stream, err := model.Provider.Stream(context.Background(), transcript, ai.StreamOptions{})
				if err != nil {
					t.Fatal(err)
				}
				return stream.Result()
			})
		})
	}
}
