package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	codingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestCustomOpenAIAPISelectsMatchingProvider(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			type requestSnapshot struct {
				path string
				body map[string]any
			}
			requests := make(chan requestSnapshot, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests <- requestSnapshot{path: request.URL.Path, body: body}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"constructor-probe"}}`)
			}))
			defer server.Close()
			dir := t.TempDir()
			models := `{"providers":{"fixture":{"api":"` + string(api) + `","baseUrl":"` + server.URL + `/v1","apiKey":"fixture","models":[{"id":"test","name":"Test"}]}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			model, err := buildModelFromRef(t.Context(), "fixture", "test", services)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = model.Provider.Close() }()
			stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("constructor-input")}}}), ai.StreamOptions{MaxRetries: new(0)})
			failure := ""
			if err != nil {
				failure = err.Error()
			} else if result := stream.Result(); result.StopReason == ai.StopReasonError {
				failure = result.ErrorMessage
			}
			if !strings.Contains(failure, "constructor-probe") {
				t.Fatalf("request did not reach the fixture: %q", failure)
			}
			var got requestSnapshot
			select {
			case got = <-requests:
			default:
				t.Fatal("constructor did not issue a request")
			}
			path, field, absent := "/v1/chat/completions", "messages", "input"
			if api == ai.APIOpenAIResponses {
				path, field, absent = "/v1/responses", "input", "messages"
			}
			if got.path != path || got.body["model"] != "test" || got.body["stream"] != true || got.body[field] == nil || got.body[absent] != nil {
				t.Fatalf("%s constructor request: path=%q body=%#v", api, got.path, got.body)
			}
			content, err := json.Marshal(got.body[field])
			if err != nil || !strings.Contains(string(content), "constructor-input") {
				t.Fatalf("constructor lost user content: %s, %v", content, err)
			}
		})
	}
}

func TestHTTPProxyGlobalSettingsReachDispatcher(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"httpProxy":"http://127.0.0.1:7890"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := ai.ConfiguredHTTPIdleTimeoutMs()
	t.Cleanup(func() {
		if err := ai.ConfigureHTTPDispatcher(before); err != nil {
			t.Error(err)
		}
	})
	manager := codingagent.NewSettingsManager(t.TempDir(), dir)
	if err := configureHTTPDispatcherFromSettings(manager); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		if got := os.Getenv(key); got != "http://127.0.0.1:7890" {
			t.Fatalf("%s=%q", key, got)
		}
	}
}
