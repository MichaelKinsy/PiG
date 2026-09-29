package main

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The startup model decision and runtime construction share the same opened log; a second open could lose a selected cwd or re-read changing history.
func TestStartupResumeReusesOpenedSessionManager(t *testing.T) {
	services := testServices(t, t.TempDir())
	storage := codingagent.NewSessionManagerWithDir(services.CWD(), t.TempDir())
	history, err := storage.Create("startup-shared-log", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := history.AppendThinkingLevelChange("low"); err != nil {
		t.Fatal(err)
	}
	if _, err := history.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "saved"}}}}); err != nil {
		t.Fatal(err)
	}
	// Session files materialize on the first assistant turn, as in Pi.
	if _, err := history.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "openai", ModelID: "gpt-4o", API: ai.APIOpenAIResponses, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "saved answer"}}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	flags := CLIFlags{sessionCwdOverride: &cwd}
	selection := startupSessionSelection{runtimeCWD: services.CWD(), resumePath: history.Path()}
	if err := selection.loadSession(flags); err != nil {
		t.Fatal(err)
	}
	manager := selection.manager
	if manager == nil || manager.CWD() != cwd {
		t.Fatalf("selected manager=%v, want cwd %s", manager, cwd)
	}
	if err := os.Remove(history.Path()); err != nil {
		t.Fatal(err)
	}
	if err := selection.loadSession(flags); err != nil || selection.manager != manager {
		t.Fatalf("startup reopened the selected log: %v", err)
	}
	options := selection.startOptions(flags)
	if options.SessionManager != manager || options.CWDOverride == nil || *options.CWDOverride != cwd {
		t.Fatalf("runtime options lost selected manager/cwd: %+v", options)
	}
}

// Pi sdk.ts:198-224 selects explicit model, then an authenticated saved model, then the configured/default fallback.
func TestStartupResumeModelPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, explicit, saved             string
		savedAuth, message                bool
		wantProvider, wantID, wantMessage string
	}{
		{"explicit wins", "openai/gpt-4o", "claude-opus-4-8", true, true, "openai", "gpt-4o", ""},
		{"saved precedes settings", "", "claude-opus-4-8", true, true, "anthropic", "claude-opus-4-8", ""},
		{"missing saved model falls back", "", "missing-model", true, true, "openai", "gpt-4o", "Could not restore model anthropic/missing-model. Using openai/gpt-4o"},
		{"saved without auth falls back", "", "claude-opus-4-8", false, true, "openai", "gpt-4o", "Could not restore model anthropic/claude-opus-4-8. Using openai/gpt-4o"},
		{"metadata-only log falls back", "", "claude-opus-4-8", true, false, "openai", "gpt-4o", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
				t.Setenv(key, "")
			}
			services := testServices(t, t.TempDir())
			if err := services.Auth().Set("openai", ai.Credential{Type: ai.CredentialAPIKey, Key: "fallback-key"}); err != nil {
				t.Fatal(err)
			}
			if tc.savedAuth {
				if err := services.Auth().Set("anthropic", ai.Credential{Type: ai.CredentialAPIKey, Key: "saved-key"}); err != nil {
					t.Fatal(err)
				}
			}
			manager, err := coding.NewInMemorySessionManager(services.CWD())
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.AppendModelSwitch("anthropic", tc.saved, ""); err != nil {
				t.Fatal(err)
			}
			if tc.message {
				if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "saved message"}}}}); err != nil {
					t.Fatal(err)
				}
			}
			selected, err := selectStartupModel(t.Context(), startupModelOptions{CLIModel: tc.explicit, Continuing: true, SessionManager: manager}, codingagent.Settings{DefaultProvider: "openai", DefaultModel: "gpt-4o"}, services)
			if err != nil {
				t.Fatal(err)
			}
			if selected.Model == nil || selected.Model.ID != tc.wantID || selected.Model.ProviderMeta.ProviderID != tc.wantProvider {
				t.Fatalf("selected=%+v, want %s/%s", selected.Model, tc.wantProvider, tc.wantID)
			}
			if selected.ModelFallbackMessage != tc.wantMessage {
				t.Fatalf("fallback message=%q, want %q", selected.ModelFallbackMessage, tc.wantMessage)
			}
			if len(selected.Warnings) != 0 {
				t.Fatalf("interactive fallback message leaked into general diagnostics: %v", selected.Warnings)
			}
		})
	}
}
