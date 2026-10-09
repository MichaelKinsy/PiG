package codingagent_test

// pi: packages/coding-agent/src/core/auth-guidance.ts

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// agent-session.ts:1673-1691 rejects a prompt without a model or usable auth before _checkCompaction and before_agent_start, so no agent_start or agent_settled follows; interactive-mode.ts:1186-1191 shows the rejection through showError (4463-4466). A Session created without a model holds agent.ts:57-68 DEFAULT_MODEL, whose "unknown" provider auth-guidance.ts:22-25 names "the selected model".
func TestInteractivePromptValidationPrecedesBeforeAgentStart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model string
		want  string
	}{
		{"default model", "default", "Error: No API key found for the selected model."},
		{"no model", "none", "Error: No model selected."},
		{"model without auth", "faux", "Error: No API key found for faux."},
		{"invalid section", "bad-section", "Invalid system prompt section name: preamble"},
		{"null tools", "null-tools", "Cannot read properties of null (reading 'length')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			t.Setenv("FAUX_API_KEY", "")
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			cwd := t.TempDir()
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var events []string
			record := func(name string) extension.HandlerFn {
				return func(args ...any) (any, error) {
					mu.Lock()
					events = append(events, name)
					mu.Unlock()
					if name == "before_agent_start" && tc.model == "bad-section" {
						options := extension.BeforeAgentStartOptions(args[1].(context.Context))
						*options.Sections = append(*options.Sections, ai.PromptSection{Name: "preamble", Value: new("invalid")})
					}
					if name == "before_agent_start" && tc.model == "null-tools" {
						extension.SetBeforeAgentStartSelectedTools(args[1].(context.Context), json.RawMessage(`null`))
					}
					return nil, nil
				}
			}
			ext := extension.Extension{Name: "observe", Handlers: map[string][]extension.HandlerFn{
				"before_agent_start": {record("before_agent_start")},
				"agent_start":        {record("agent_start")},
				"agent_settled":      {record("agent_settled")},
			}}
			runner := inproc.NewRunner([]extension.Extension{ext}, cwd)
			provider := &loadoutProvider{}
			var model *ai.Model
			if tc.model == "faux" || tc.model == "bad-section" || tc.model == "null-tools" {
				model = &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
			}
			if tc.model == "bad-section" || tc.model == "null-tools" {
				if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "test-key"}); err != nil {
					t.Fatal(err)
				}
			}
			session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			if tc.model == "none" {
				session.Agent().SetModel(nil)
			}
			h := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session, SettingsManager: services.SettingsManager(), ExtensionRunner: runner}, nil)
			h.Do(func() { h.Enter("hello") })
			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(h.Chat(), "Error:") && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			h.WaitIdle(t, 10*time.Second)
			if chat := h.Chat(); !strings.Contains(chat, tc.want) || strings.Contains(chat, "hello") {
				t.Fatalf("chat = %q, want %q and no user message", chat, tc.want)
			}
			mu.Lock()
			defer mu.Unlock()
			var wantEvents []string
			if tc.model == "bad-section" || tc.model == "null-tools" {
				wantEvents = []string{"before_agent_start"}
			}
			if tools, _ := provider.requests(); !slices.Equal(events, wantEvents) || len(tools) != 0 {
				t.Fatalf("rejected prompt emitted %v and sent %d requests", events, len(tools))
			}
		})
	}
}
