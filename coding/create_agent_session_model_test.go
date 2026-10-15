package coding

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// sdk.ts:190-232 createAgentSession's model selection: an explicit model wins; an existing session restores its saved model when that model still
// exists and its provider has auth, else the settings default is used and the failed restore is reported; a session without messages ignores the
// saved model; and with no model at all the no-models message is returned.
func TestCreateAgentSessionFromServicesRestoresTheSavedModelOrFallsBackToTheDefault(t *testing.T) {
	for _, tc := range []struct {
		name         string
		savedModel   string
		savedAuth    bool
		messages     bool
		wantID       string
		wantProvider string
		wantMessage  string
	}{
		{"saved with auth is restored", "claude-opus-4-8", true, true, "claude-opus-4-8", "anthropic", ""},
		{"saved without auth falls back", "claude-opus-4-8", false, true, "gpt-4o", "openai", "Could not restore model anthropic/claude-opus-4-8. Using openai/gpt-4o"},
		{"saved missing from the catalog falls back", "missing-model", true, true, "gpt-4o", "openai", "Could not restore model anthropic/missing-model. Using openai/gpt-4o"},
		{"a log without messages is not restored", "claude-opus-4-8", true, false, "gpt-4o", "openai", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", ai.AnthropicFederationRuleIDEnv} {
				t.Setenv(key, "")
			}
			services := servicesWithDefaultTools(t, nil)
			if err := services.SettingsManager().SetDefaultModelAndProvider("openai", "gpt-4o"); err != nil {
				t.Fatal(err)
			}
			if err := services.ModelRuntime().SetRuntimeApiKey(t.Context(), "openai", "fallback-key"); err != nil {
				t.Fatal(err)
			}
			if tc.savedAuth {
				if err := services.ModelRuntime().SetRuntimeApiKey(t.Context(), "anthropic", "saved-key"); err != nil {
					t.Fatal(err)
				}
			}
			manager, err := NewInMemorySessionManager(services.CWD())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.AppendModelChange("anthropic", tc.savedModel); err != nil {
				t.Fatal(err)
			}
			if tc.messages {
				if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "saved message"}}}}); err != nil {
					t.Fatal(err)
				}
			}
			result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, SessionManager: manager})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = result.Session.Close() })
			model := result.Session.Model()
			if model == nil || model.ID != tc.wantID || model.ProviderMeta.ProviderID != tc.wantProvider {
				t.Fatalf("selected %+v, want %s/%s", model, tc.wantProvider, tc.wantID)
			}
			if result.ModelFallbackMessage != tc.wantMessage {
				t.Fatalf("fallback message = %q, want %q", result.ModelFallbackMessage, tc.wantMessage)
			}
		})
	}
}

func TestCreateAgentSessionFromServicesNeedsServices(t *testing.T) {
	if _, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{}); !errors.Is(err, ErrNoServices) {
		t.Fatalf("without services = %v, want ErrNoServices", err)
	}
}

// sdk.ts:259-295: the tools allowlist restricts the registered tools, not only the active ones, and the +name/-name entries of the tools option stay
// applied when the defaultTools setting reloads, so a tool the option removed is not re-added by the setting.
func TestCreateAgentSessionFromServicesCarriesTheToolSelectionIntoTheSession(t *testing.T) {
	other := registryTool("other_tool", "Other Tool", "An SDK tool outside the allowlist", "")
	allowlisted, err := sessionFromServices(t, nil, CreateAgentSessionFromServicesOptions{Tools: []string{"read"}, CustomTools: []extension.ToolDefinition{other}})
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedToolNames(allowlisted); !slices.Equal(got, []string{"read"}) {
		t.Errorf("allowlist registers %v, want [read]", got)
	}

	session, err := sessionFromServices(t, []string{"grep"}, CreateAgentSessionFromServicesOptions{Tools: []string{"-read"}})
	if err != nil {
		t.Fatal(err)
	}
	bindRegistryPort(t, session)
	writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"grep", "read"}})
	reloadRegistryPort(t, session)
	if got := sortedActiveNames(session); !slices.Equal(got, []string{"grep"}) {
		t.Errorf("after the setting adds read, active = %v, want [grep]: -read stays applied", got)
	}
}

// sdk.ts:201-220: the selection a log's latest model_change names is restored whatever its member types; one that names no catalog model (here a number)
// is reported rather than replaced by an earlier model.
func TestCreateAgentSessionFromServicesReportsASavedModelChangeThatNamesNoCatalogModel(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", ai.AnthropicFederationRuleIDEnv} {
		t.Setenv(key, "")
	}
	services := servicesWithDefaultTools(t, nil)
	if err := services.SettingsManager().SetDefaultModelAndProvider("openai", "gpt-4o"); err != nil {
		t.Fatal(err)
	}
	if err := services.ModelRuntime().SetRuntimeApiKey(t.Context(), "openai", "fallback-key"); err != nil {
		t.Fatal(err)
	}
	if err := services.ModelRuntime().SetRuntimeApiKey(t.Context(), "anthropic", "saved-key"); err != nil {
		t.Fatal(err)
	}
	ts := `"timestamp":"2026-01-01T00:00:00.000Z"`
	cwd, err := json.Marshal(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + string(cwd) + `}`,
		`{"type":"model_change","id":"a1","parentId":null,` + ts + `,"provider":"anthropic","modelId":"claude-opus-4-8"}`,
		`{"type":"message","id":"a2","parentId":"a1",` + ts + `,"message":{"role":"user","content":"saved","timestamp":1}}`,
		`{"type":"model_change","id":"a3","parentId":"a2",` + ts + `,"provider":1,"modelId":null}`,
	}
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := icodingagent.NewSessionManagerWithDir(services.CWD(), filepath.Dir(path)).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Session.Close() })
	if model := result.Session.Model(); model == nil || model.ID != "gpt-4o" {
		t.Fatalf("selected %+v, want the settings default gpt-4o", model)
	}
	if want := "Could not restore model 1/null. Using openai/gpt-4o"; result.ModelFallbackMessage != want {
		t.Fatalf("fallback message = %q, want %q", result.ModelFallbackMessage, want)
	}
}
