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

// pig divergence (D26): "The OpenCode `x-opencode-session` header is not attribution and stays independent of the
// telemetry gate."
//
// Pi, packages/ai/src/utils/identity.ts (the pinned version): getAttributionHeaders (lines 107-109) returns {} when the
// identity carries no attribution, and getOpenCodeSessionHeaders (lines 130-135) returns { "x-opencode-session": sessionId }
// for an OpenCode target with a session id, whatever the identity holds. A request to an OpenCode-shaped endpoint with
// install telemetry off therefore still carries the session header and no attribution header; with it on, both. The gate
// is closed by PI_TELEMETRY=0 or by the enableInstallTelemetry setting.
//
// "Is not attribution" holds by absence, in PiG as in Pi: the session header is a function of the request target and the session id
// alone (ai/identity.go GetOpenCodeSessionHeaders, 277-282, which takes no identity; its callers ai/models_runtime_stream.go:74,
// coding/model.go:633 and 652, and ai/opencode_headers.go:24-33 read options.SessionID and the target, never options.Identity).
// So no single mutation of an Identity.Attribution check can turn the session half red: the header is also added at several
// sites at once (removing GetOpenCodeSessionHeaders alone, or the opencode_headers.go wrapper alone, leaves the header
// present), and only removing all of them is red. The attribution half is the x-opencode-client assertion, which is red when
// attributionPairs (ai/identity.go) stops emitting it or getClientIdentity ignores the gate.
func TestOpenCodeSessionHeaderIsNotAttributionAndIgnoresTheTelemetryGate(t *testing.T) {
	for _, provider := range []string{"opencode", "opencode-go"} {
		for _, gate := range []struct {
			name    string
			env     string
			setting string
			on      bool
		}{
			{"PI_TELEMETRY=1", "1", "", true},
			{"PI_TELEMETRY=0", "0", "", false},
			{"enableInstallTelemetry unset", "", "", true},
			{"enableInstallTelemetry false", "", `"enableInstallTelemetry":false`, false},
		} {
			t.Run(fmt.Sprintf("%s/%s", provider, gate.name), func(t *testing.T) {
				// Pi's name wins over PiG's when both are set, and a variable that is set but empty is a closed gate: unset both.
				for _, name := range []string{"PI_TELEMETRY", "PIG_TELEMETRY"} {
					t.Setenv(name, "")
					if gate.env == "" || name == "PIG_TELEMETRY" {
						_ = os.Unsetenv(name)
					} else {
						t.Setenv(name, gate.env)
					}
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
				config := fmt.Sprintf(`{"providers":{%q:{"apiKey":"fixture","baseUrl":%q,"api":"openai-completions","models":[{"id":"m","name":"M"}]}}}`, provider, server.URL)
				if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{"+gate.setting+"}"), 0o600); err != nil {
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
				identity := getClientIdentity(services.SettingsManager())
				if (identity.Attribution != nil) != gate.on {
					t.Fatalf("the gate is %v, want %v for %s", identity.Attribution != nil, gate.on, gate.name)
				}
				options := ai.StreamOptions{SessionID: "session-1", Identity: identity}
				stream := services.ModelRuntime().Stream(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, options)
				if result := stream.Result(); result == nil || result.StopReason != ai.StopReasonStop {
					t.Fatalf("result = %#v", result)
				}
				headers := <-requests
				if got := headers.Get("x-opencode-session"); got != "session-1" {
					t.Errorf("x-opencode-session = %q, want session-1 whether telemetry is on or off", got)
				}
				got := headers.Get("x-opencode-client")
				switch {
				case gate.on && got != pigidentity.AttributionName:
					t.Errorf("x-opencode-client = %q, want %q with telemetry on", got, pigidentity.AttributionName)
				case !gate.on && got != "":
					t.Errorf("x-opencode-client = %q with telemetry off, want no attribution header", got)
				}
			})
		}
	}
}
