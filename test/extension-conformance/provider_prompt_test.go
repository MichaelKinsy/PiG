package extensionconformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// Pi sdk.ts:356-364 and agent-session.ts:1347-1394 share the same Session
// behavior across runtimes: awaited response notifications and active tool metadata.
func TestProviderResponseAndToolPromptsAcrossSDKs(t *testing.T) {
	t.Parallel()
	for _, test := range allHarnessCases() {
		t.Run(test.name, func(t *testing.T) {
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []struct {
						Role    string
						Content json.RawMessage
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				var system []string
				for _, message := range request.Messages {
					if message.Role == "system" || message.Role == "developer" {
						var text string
						if err := json.Unmarshal(message.Content, &text); err != nil {
							t.Error(err)
						}
						system = append(system, text)
					}
				}
				mu.Lock()
				requests = append(requests, strings.Join(system, "\n\n"))
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Add("X-Probe", "first")
				w.Header().Add("X-Probe", "second")
				_, _ = w.Write([]byte("data: {\"id\":\"test\",\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
			}))
			defer server.Close()
			provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "test", Model: "probe", APIKey: "key", BaseURL: server.URL})
			t.Cleanup(func() { _ = provider.Close() })
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			bridged, diagnostics := coding.BridgeNewRunnerTools(h.runner.Tools())
			if len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "probe", Provider: provider}, Runner: h.runner, Tools: bridged, SkipBuiltinTools: true, NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			done := make(chan struct{})
			go func() {
				defer close(done)
				for range session.Events() {
				}
			}()
			t.Cleanup(func() { _ = session.Close(); <-done })
			h.ui.ClearRecorded()
			loadouts := [][]string{{"guided_tool", "sourced_tool"}, {}, {"guided_tool"}}
			for _, names := range loadouts {
				session.SetActiveToolsByName(names)
				if _, err := session.Send(context.Background(), "probe"); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			gotRequests := append([]string(nil), requests...)
			mu.Unlock()
			if len(gotRequests) != len(loadouts) {
				t.Fatalf("requests = %d", len(gotRequests))
			}
			for i, prompt := range gotRequests {
				want := "<tools>\n- guided_tool: Guided tool summary\n\nIn addition to the tools above, you may have access to other custom tools depending on the project.\n</tools>"
				if i == 1 {
					want = strings.Replace(want, "- guided_tool: Guided tool summary", "(none)", 1)
				}
				if !strings.Contains(prompt, want) {
					t.Fatalf("request %d prompt lacks %s:\n%s", i, want, prompt)
				}
				if got := strings.Contains(prompt, "- Use guided_tool when the user asks for guided behavior."); got != (i != 1) {
					t.Fatalf("request %d guided guideline = %t", i, got)
				}
				if got := strings.Contains(prompt, "- Use sourced_tool to test per-tool source attribution."); got != (i == 0) {
					t.Fatalf("request %d inactive guideline = %t", i, got)
				}
			}
			var want []string
			for range loadouts {
				want = append(want, "provider-response=after_provider_response:200:first, second:info", "provider-response=second:info")
			}
			observations := func() []string {
				var got []string
				for _, notification := range h.ui.Recorded() {
					if strings.HasPrefix(notification, "provider-response=") {
						got = append(got, notification)
					}
				}
				return got
			}
			waitFor(t, func() bool { return len(observations()) >= len(want) })
			if got := observations(); !reflect.DeepEqual(got, want) {
				t.Fatalf("response observers = %v, want %v", got, want)
			}
		})
	}
}
