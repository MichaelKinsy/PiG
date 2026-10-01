package coding

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// piIdentity matches the values Pi sends to identify itself: pi.dev, "pi",
// "Pi" and pi-coding-agent as a whole header value, and pi/<version> or
// pi (<platform>...) user agents.
var piIdentity = regexp.MustCompile(`(?i)(^pi$|^pi[ /(]|pi\.dev|^pi-coding-agent$)`)

// D26 extends to the whole Services-owned request path: a model call through
// ModelRuntime reaches the provider's HTTP endpoint carrying PiG's attribution
// (provider-attribution.ts values with PiG branding), never Pi's. The recorder
// stands in for OpenRouter-, NVIDIA- and OpenCode-shaped endpoints selected by
// provider id, exactly as Pi selects them (provider-attribution.ts:
// isOpenRouterModel, isNvidiaNimModel, getSessionHeaders).
func TestModelRuntimeRequestsCarryPiGAttributionNeverPis(t *testing.T) {
	type wire struct {
		provider string
		enabled  bool
		want     map[string]string
		absent   []string
	}
	cases := []wire{
		{provider: "openrouter", enabled: true, want: map[string]string{
			"HTTP-Referer": pigidentity.OpenRouterReferer, "X-OpenRouter-Title": pigidentity.OpenRouterTitle, "X-OpenRouter-Categories": pigidentity.OpenRouterCategories,
		}},
		{provider: "openrouter", enabled: false, absent: []string{"HTTP-Referer", "X-OpenRouter-Title", "X-OpenRouter-Categories"}},
		{provider: "nvidia", enabled: true, want: map[string]string{"X-BILLING-INVOKE-ORIGIN": pigidentity.NvidiaBillingOrigin}},
		{provider: "nvidia", enabled: false, absent: []string{"X-BILLING-INVOKE-ORIGIN"}},
		// The OpenCode pair is unconditional on the telemetry gate (provider-attribution.ts getSessionHeaders).
		{provider: "opencode", enabled: true, want: map[string]string{"x-opencode-session": "session-1", "x-opencode-client": pigidentity.OpenCodeClient}},
		{provider: "opencode", enabled: false, want: map[string]string{"x-opencode-session": "session-1", "x-opencode-client": pigidentity.OpenCodeClient}},
		{provider: "opencode-go", enabled: true, want: map[string]string{"x-opencode-session": "session-1", "x-opencode-client": pigidentity.OpenCodeClient}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/telemetry=%v", tc.provider, tc.enabled), func(t *testing.T) {
			if tc.enabled {
				t.Setenv("PI_TELEMETRY", "1")
			} else {
				t.Setenv("PI_TELEMETRY", "0")
			}
			requests := make(chan http.Header, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				requests <- r.Header.Clone()
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			agentDir := t.TempDir()
			config := fmt.Sprintf(`{"providers":{%q:{"apiKey":"fixture","baseUrl":%q,"api":"openai-completions","models":[{"id":"m","name":"M"}]}}}`, tc.provider, server.URL)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			model, err := BuildModel(tc.provider+"/m", services)
			if err != nil {
				t.Fatal(err)
			}
			stream := services.ModelRuntime().Stream(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{SessionID: "session-1"})
			if result := stream.Result(); result == nil || result.StopReason != ai.StopReasonStop {
				t.Fatalf("result = %#v", result)
			}
			headers := <-requests
			for name, want := range tc.want {
				if got := headers.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			for _, name := range tc.absent {
				if got := headers.Get(name); got != "" {
					t.Errorf("%s = %q, want it absent", name, got)
				}
			}
			if got := headers.Get("User-Agent"); !strings.HasPrefix(got, pigidentity.UserAgentProduct+"/") {
				t.Errorf("User-Agent = %q, want %s/<version> (D65)", got, pigidentity.UserAgentProduct)
			}
			for name, values := range headers {
				for _, value := range values {
					if piIdentity.MatchString(value) {
						t.Errorf("%s = %q identifies the request as Pi", name, value)
					}
				}
			}
		})
	}
}
