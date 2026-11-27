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

// Pi v1.7.0 removed install telemetry (484da3a86), so getClientIdentity() always carries attribution,
// while x-opencode-session remains independent of Identity.Attribution (packages/ai/src/utils/identity.ts:
// getAttributionHeaders returns {} when identity has no attribution, while getOpenCodeSessionHeaders
// returns {"x-opencode-session": sessionId} for OpenCode targets regardless of identity).
func TestOpenCodeSessionHeaderIsNotAttributionAndIgnoresAttribution(t *testing.T) {
	for _, provider := range []string{"opencode", "opencode-go"} {
		for _, tc := range []struct {
			name     string
			identity *ai.ClientIdentity
			wantAttr bool
		}{
			{"default client identity", getClientIdentity(), true},
			{"no attribution", &ai.ClientIdentity{}, false},
		} {
			t.Run(fmt.Sprintf("%s/%s", provider, tc.name), func(t *testing.T) {
				requests := make(chan http.Header, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					requests <- r.Header.Clone()
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				agentDir := t.TempDir()
				config := fmt.Sprintf(`{"providers":{%q:{"apiKey":"fixture","baseUrl":%q,"api":"openai-completions","models":[{"id":"m","name":"M"}]}}}`, provider, server.URL)
				if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(services.Close)
				model, err := BuildModel(provider+"/m", services)
				if err != nil {
					t.Fatal(err)
				}
				if (tc.identity.Attribution != nil) != tc.wantAttr {
					t.Fatalf("attribution=%v, want %v", tc.identity.Attribution != nil, tc.wantAttr)
				}
				options := ai.StreamOptions{SessionID: "session-1", Identity: tc.identity}
				stream := services.ModelRuntime().Stream(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, options)
				if result := stream.Result(); result == nil || result.StopReason != ai.StopReasonStop {
					t.Fatalf("result = %#v", result)
				}
				headers := <-requests
				if got := headers.Get("x-opencode-session"); got != "session-1" {
					t.Errorf("x-opencode-session = %q, want session-1", got)
				}
				got := headers.Get("x-opencode-client")
				switch {
				case tc.wantAttr && got != pigidentity.AttributionName:
					t.Errorf("x-opencode-client = %q, want %q", got, pigidentity.AttributionName)
				case !tc.wantAttr && got != "":
					t.Errorf("x-opencode-client = %q with no attribution, want empty", got)
				}
			})
		}
	}
}
