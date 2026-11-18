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

// D36: how a models.json provider entry's `insecure` and a registration of the same provider combine at the request (rule R2, owner decision 29): an explicit
// `true` or `false` of the registration wins; a registration that says nothing keeps the file's opt-in only while it keeps the file's endpoint, and one that
// names another endpoint verifies certificates there. The rule is one function (providerTLSOptIn) that every read site calls; the two routes a request takes
// must agree on every row:
//   - the model runtime streams a registry-bound model through a provider it rebuilds from ProviderInsecure (coding/services.go, model_runtime_typed.go);
//   - a provider object built straight from an entry (BuildModel, coding/model.go) takes the entry's value, resolved by the registry.
//
// A registration states `insecure` true, false or nothing (an SDK's raw config decodes the three states; the exported Go field says true or nothing).
func TestFileInsecureAndRegistrationReachTheRequest(t *testing.T) {
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"secured\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	other := httptest.NewTLSServer(handler)
	defer other.Close()
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}})
	for _, tc := range []struct {
		name         string
		file         string // the entry's insecure member
		registration string // "none", "omitted", "true" or "false"
		endpoint     string // the registration's baseUrl: "" (none), "own" (another endpoint) or "file" (the file's endpoint again)
		reaches      bool   // the request reaches the self-signed endpoint, on the model runtime and on the provider object BuildModel built
	}{
		{"1 file opt-in, no registration", `,"insecure":true`, "none", "", true},
		{"2 file opt-in, registration that states nothing", `,"insecure":true`, "omitted", "", true},
		{"2b file opt-in, registration that states nothing and names its own endpoint", `,"insecure":true`, "omitted", "own", false},
		{"2c file opt-in, registration that states nothing and names the file's endpoint", `,"insecure":true`, "omitted", "file", true},
		{"3 file opt-in, registration that opts in", `,"insecure":true`, "true", "", true},
		{"4 file without the opt-in, no registration", ``, "none", "", false},
		{"5 file without the opt-in, registration that states nothing", ``, "omitted", "", false},
		{"6 file without the opt-in, registration that opts in", ``, "true", "", true},
		{"7 file false, registration that opts in", `,"insecure":false`, "true", "", true},
		{"8 file opt-in, registration that turns it off", `,"insecure":true`, "false", "", false},
		{"9 file without the opt-in, registration that turns it off", ``, "false", "", false},
		{"10 file opt-in, registration that opts in at its own endpoint", `,"insecure":true`, "true", "own", true},
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
				switch tc.endpoint {
				case "own":
					raw += `,"baseUrl":"` + other.URL + `"`
				case "file":
					raw += `,"baseUrl":"` + server.URL + `"`
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
			reaches("model runtime", tc.reaches, func() *ai.AssistantMessage {
				model := services.ModelRuntime().GetModel("corp", "m")
				if model == nil {
					t.Fatal("corp/m is not listed")
				}
				return services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
			})
			reaches("provider object built from the entry", tc.reaches, func() *ai.AssistantMessage {
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
