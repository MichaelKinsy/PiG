package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The mode binding, not a test-only refresh hook, must publish session_start and command tools to the next real provider request.
func TestDynamicToolsModeBindingReachesProvider(t *testing.T) {
	type observation struct {
		names  []string
		prompt string
	}
	observed := make(chan observation, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools    []struct{ Function struct{ Name string } }
			Messages []struct {
				Role    string
				Content json.RawMessage
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var declared []string
		var prompt string
		for _, tool := range request.Tools {
			declared = append(declared, tool.Function.Name)
		}
		for _, message := range request.Messages {
			if message.Role == "system" || message.Role == "developer" {
				var text string
				if err := json.Unmarshal(message.Content, &text); err != nil {
					t.Error(err)
				}
				prompt += text
			}
		}
		observed <- observation{declared, prompt}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"dynamic\",\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "dynamic-test", Model: "dynamic", APIKey: "test", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	defer h.Shutdown("complete")
	bridge := subprocess.NewUIBridge(func() {})
	h.SetUIBridge(bridge)
	source, err := filepath.Abs("../../.upstream/current/packages/coding-agent/examples/extensions/dynamic-tools.ts")
	if err != nil {
		t.Fatal(err)
	}
	loaded, failures := h.LoadAll(t.Context(), []subprocess.ExtConfig{{Name: "dynamic-tools", Source: source, Enabled: true}})
	if len(failures) != 0 {
		t.Fatal(failures)
	}
	runner := inproc.NewRunner(loaded, t.TempDir())
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Runner: runner, Model: &ai.Model{ID: "dynamic", Provider: provider}, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range session.Events() {
		}
	}()
	defer func() { _ = session.Close(); <-done }()
	bindSessionExtensionActions(runner, bridge, func() *coding.Session { return session }, extension.ContextActions{})
	session.EmitSessionStart("startup")
	if !runner.ExecuteCommand(t.Context(), "add-echo-tool", "shout") {
		t.Fatal("missing command")
	}
	if _, err := session.Send(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	got := <-observed
	declared, prompt := got.names, got.prompt
	if !slices.Equal(declared, []string{"echo_session", "shout"}) {
		t.Fatalf("provider tools=%v", declared)
	}
	for _, text := range []string{"echo_session: Echo back user-provided text with [session] prefix", "shout: Echo back user-provided text with [shout] prefix", "Use echo_session when the user asks for exact echo output."} {
		if !strings.Contains(prompt, text) {
			t.Fatalf("provider prompt lacks %q: %s", text, prompt)
		}
	}
}
