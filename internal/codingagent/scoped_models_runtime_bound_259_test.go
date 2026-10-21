package codingagent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Issue #259: the selector's scope update installed registry catalog descriptors (no backend), so a model cycled to afterwards failed prompt
// validation with "No model selected". Pi installs the Session ModelRuntime's available snapshot (interactive-mode.ts:5397,5421-5431).
func TestScopedSelectionKeepsRuntimeBoundModelsForCycling259(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set("local", ai.Credential{Type: ai.CredentialAPIKey, Key: "k"}); err != nil {
		t.Fatal(err)
	}
	err = services.Registry().RegisterExtensionProvider("local", extension.ProviderConfig{API: "openai-completions", APIKey: "k", BaseURL: "http://127.0.0.1:1/v1", Models: []extension.ProviderModelConfig{
		{ID: "one", Name: "One", ContextWindow: 8000, MaxTokens: 100, Input: []string{"text"}},
		{ID: "two", Name: "Two", ContextWindow: 8000, MaxTokens: 100, Input: []string{"text"}},
		{ID: "three", Name: "Three", ContextWindow: 8000, MaxTokens: 100, Input: []string{"text"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	first := services.ModelRuntime().GetModel("local", "one")
	session, err := coding.NewSession(services, coding.SessionOptions{Model: first, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	harness := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{
		CWD: t.TempDir(), AgentDir: agentDir, Model: first, SessionHandle: session, SettingsManager: services.SettingsManager(),
		ModelRegistry: services.Registry().ModelRegistry,
	}, nil)

	items := []tui.ModelItem{{FullID: "local/one", Provider: "local"}, {FullID: "local/two", Provider: "local"}, {FullID: "local/three", Provider: "local"}}
	harness.ApplyScopedSelection(items, []string{"local/one", "local/two"})

	for _, entry := range session.ScopedModels() {
		if entry.Model == nil || entry.Model.Provider == nil {
			t.Fatalf("scope entry %+v has no backend; the scope must hold the Session ModelRuntime's models", entry.Model)
		}
	}
	result, err := session.CycleModel("forward")
	if err != nil || result == nil {
		t.Fatalf("cycle = %v, %v", result, err)
	}
	if err := session.Prompt(context.Background(), "hello"); errors.Is(err, agent.ErrNoModelSelected) {
		t.Fatalf("prompt after scope update on %s: %v; the scope must hold runtime-bound models", result.Model.ID, err)
	}
}
