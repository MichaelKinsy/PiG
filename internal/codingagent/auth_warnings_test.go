package codingagent

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func newAnthropicWarningMode(t *testing.T, model *ai.Model) (*InteractiveMode, *ai.AuthStorage) {
	t.Helper()
	agentDir := t.TempDir()
	store, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRequestAuthRuntime(t.Context(), RequestAuthRuntimeOptions{Credentials: store, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	m := &InteractiveMode{
		opts: InteractiveOptions{
			AgentDir:           agentDir,
			Model:              model,
			SettingsManager:    NewSettingsManager(t.TempDir(), agentDir),
			RequestAuthRuntime: runtime,
		},
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 120, 40),
	}
	return m, store
}

func anthropicWarningTestModel() *ai.Model {
	return &ai.Model{
		ID:       "claude-test",
		Provider: ai.NewAnthropicProvider(ai.AnthropicConfig{ProviderID: "anthropic", Model: "claude-test"}),
	}
}

func TestMaybeWarnAboutAnthropicSubscriptionAuthWarnsOnce(t *testing.T) {
	const wantWarning = "Anthropic subscription auth is active. Third-party harness usage draws from extra usage and is billed per token, not your Claude plan limits. Manage extra usage at https://claude.ai/settings/usage. Disable this warning in /settings."
	m, store := newAnthropicWarningMode(t, anthropicWarningTestModel())
	if err := store.Set("anthropic", ai.Credential{Type: ai.CredentialAPIKey, Key: "sk-ant-oat01-test"}); err != nil {
		t.Fatal(err)
	}

	m.maybeWarnAboutAnthropicSubscriptionAuth(t.Context())
	m.maybeWarnAboutAnthropicSubscriptionAuth(t.Context())

	if !m.anthropicSubWarningShown {
		t.Fatal("subscription warning was not marked shown")
	}
	if got := m.chatContainer.ChildCount(); got != 2 {
		t.Fatalf("chat children = %d, want one warning block", got)
	}
	if anthropicSubscriptionAuthWarning != wantWarning {
		t.Fatalf("warning = %q, want upstream text %q", anthropicSubscriptionAuthWarning, wantWarning)
	}
	if rendered := strings.Join(m.chatContainer.Render(500), "\n"); !strings.Contains(rendered, wantWarning) {
		t.Fatalf("warning output = %q, want %q", rendered, wantWarning)
	}
}

func TestMaybeWarnAboutAnthropicSubscriptionAuthUsesResolvedEnvironmentAuth(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-oat01-environment")
	m, _ := newAnthropicWarningMode(t, anthropicWarningTestModel())

	m.maybeWarnAboutAnthropicSubscriptionAuth(t.Context())

	if !m.anthropicSubWarningShown {
		t.Fatal("subscription key resolved from ANTHROPIC_API_KEY did not show the upstream warning")
	}
}

func TestMaybeWarnAboutAnthropicSubscriptionAuthWarnsForStoredOAuth(t *testing.T) {
	m, store := newAnthropicWarningMode(t, anthropicWarningTestModel())
	if err := store.Set("anthropic", ai.Credential{Type: ai.CredentialOAuth, Refresh: "refresh", Access: "expired"}); err != nil {
		t.Fatal(err)
	}

	m.maybeWarnAboutAnthropicSubscriptionAuth(t.Context())

	if !m.anthropicSubWarningShown || m.chatContainer.ChildCount() != 2 {
		t.Fatalf("shown = %t children = %d, want one OAuth warning", m.anthropicSubWarningShown, m.chatContainer.ChildCount())
	}
}

func TestMaybeWarnAboutAnthropicSubscriptionAuthIgnoresOtherModels(t *testing.T) {
	model := &ai.Model{
		ID:       "openai-test",
		Provider: ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "openai", Model: "openai-test"}),
	}
	m, store := newAnthropicWarningMode(t, model)
	if err := store.Set("anthropic", ai.Credential{Type: ai.CredentialOAuth, Refresh: "refresh"}); err != nil {
		t.Fatal(err)
	}

	m.maybeWarnAboutAnthropicSubscriptionAuth(t.Context())

	if m.anthropicSubWarningShown || m.chatContainer.ChildCount() != 0 {
		t.Fatalf("shown = %t children = %d, want no warning", m.anthropicSubWarningShown, m.chatContainer.ChildCount())
	}
}

func TestMaybeWarnAboutAnthropicSubscriptionAuthHonorsDisabledSetting(t *testing.T) {
	m, store := newAnthropicWarningMode(t, anthropicWarningTestModel())
	if err := store.Set("anthropic", ai.Credential{Type: ai.CredentialOAuth, Refresh: "refresh"}); err != nil {
		t.Fatal(err)
	}
	if err := m.opts.SettingsManager.SetWarnings(WarningSettings{AnthropicExtraUsage: false}); err != nil {
		t.Fatal(err)
	}

	m.maybeWarnAboutAnthropicSubscriptionAuth(t.Context())

	if m.anthropicSubWarningShown || m.chatContainer.ChildCount() != 0 {
		t.Fatalf("shown = %t children = %d, want no warning", m.anthropicSubWarningShown, m.chatContainer.ChildCount())
	}
}
