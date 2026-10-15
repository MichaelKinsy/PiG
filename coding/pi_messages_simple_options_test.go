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

// pi-messages.ts streamSimple (:432-444) passes the caller's options to stream unchanged; unlike every other API module it does not
// call buildBaseOptions, so a request without maxTokens sends options.maxTokens undefined (JSON.stringify drops it). Every other API
// gets the model's maxTokens clamped to the context window.
func TestPiMessagesSimpleRequestSendsOnlyTheCallersMaxTokensLikePi(t *testing.T) {
	for _, tc := range []struct {
		name      string
		maxTokens int
		want      any
	}{
		{"omitted", 0, nil},
		{"requested", 77, float64(77)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var payload struct {
					Options map[string]any `json:"options"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Errorf("payload: %v", err)
				}
				received <- payload.Options
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			agentDir := t.TempDir()
			config := fmt.Sprintf(`{"providers":{"myp":{"apiKey":"k","baseUrl":%q,"api":"pi-messages","models":[{"id":"m1","maxTokens":16384}]}}}`, server.URL)
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
			services.ModelRuntime().StreamSimple(t.Context(), model, input, ai.StreamOptions{MaxTokens: tc.maxTokens}).Result()
			var options map[string]any
			select {
			case options = <-received:
			default:
				t.Fatal("no request reached the server")
			}
			if got := options["maxTokens"]; got != tc.want {
				t.Fatalf("options.maxTokens = %v, want %v", got, tc.want)
			}
		})
	}
}
