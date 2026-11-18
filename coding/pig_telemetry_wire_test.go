package coding

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// Owner decision 2026-11-17 (divergence record D2): PIG_TELEMETRY wins over PI_TELEMETRY when both are set. A request to the
// OpenRouter-shaped endpoint, which sends attribution headers while telemetry is on, carries them when the PiG name says on beside a PI
// name that says off, and carries none when the PiG name says off beside a PI name that says on.
func TestAttributionHeadersFollowPigTelemetryOverPi(t *testing.T) {
	for _, tc := range []struct {
		pig, pi string
		sent    bool
	}{{"0", "1", false}, {"1", "0", true}} {
		t.Run(fmt.Sprintf("PIG=%s PI=%s", tc.pig, tc.pi), func(t *testing.T) {
			t.Setenv("PIG_TELEMETRY", tc.pig)
			t.Setenv("PI_TELEMETRY", tc.pi)
			requests := make(chan http.Header, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				requests <- r.Header.Clone()
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			agentDir := t.TempDir()
			config := fmt.Sprintf(`{"providers":{"openrouter":{"apiKey":"fixture","baseUrl":%q,"api":"openai-completions","models":[{"id":"m","name":"M"}]}}}`, server.URL)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			model, err := BuildModel("openrouter/m", services)
			if err != nil {
				t.Fatal(err)
			}
			options := ai.StreamOptions{SessionID: "session-1", Identity: getClientIdentity(services.SettingsManager())}
			stream := services.ModelRuntime().Stream(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, options)
			if result := stream.Result(); result == nil || result.StopReason != ai.StopReasonStop {
				t.Fatalf("result = %#v", result)
			}
			headers := <-requests
			got := headers.Get("X-OpenRouter-Title")
			if tc.sent && got != pigidentity.AttributionName {
				t.Errorf("X-OpenRouter-Title = %q, want %q", got, pigidentity.AttributionName)
			}
			if !tc.sent {
				for _, name := range []string{"HTTP-Referer", "X-OpenRouter-Title", "X-OpenRouter-Categories"} {
					if v := headers.Get(name); v != "" {
						t.Errorf("%s = %q, want it absent", name, v)
					}
				}
			}
		})
	}
}
