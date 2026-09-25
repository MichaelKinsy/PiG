package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type cycleTestProvider struct{ id string }

func (p cycleTestProvider) ID() string   { return p.id }
func (p cycleTestProvider) Close() error { return nil }
func (p cycleTestProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return completedTestStream(p.id), nil
}

func TestCycleModelUsesProviderQualifiedSpec(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{
  "github-copilot": {"type":"oauth", "refresh":"refresh-token", "access":"access-token", "expires": 4102444800000}
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	sm := NewSettingsManager(t.TempDir(), agentDir)
	if err := sm.SetDefaultModelAndProvider("saved", "original"); err != nil {
		t.Fatal(err)
	}
	var gotSpec string
	m := &InteractiveMode{
		opts: InteractiveOptions{
			AgentDir:        agentDir,
			SettingsManager: sm,
			Model: &ai.Model{
				ID:       "gpt-5.4",
				Provider: cycleTestProvider{id: "github-copilot"},
			},
			ModelBuilder: func(spec string) (*ai.Model, error) {
				gotSpec = spec
				provider, modelID, ok := strings.Cut(spec, "/")
				if !ok {
					provider, modelID = "", spec
				}
				return &ai.Model{ID: modelID, Provider: cycleTestProvider{id: provider}}, nil
			},
		},
	}
	m.statusLine = NewStatusLine(m.opts.Model, "test", nil)

	m.cycleModel(true)
	if got := sm.Get(); got.DefaultProvider != "saved" || got.DefaultModel != "original" {
		t.Errorf("model cycle rewrote defaults: %s/%s", got.DefaultProvider, got.DefaultModel)
	}

	if gotSpec == "" {
		t.Fatal("ModelBuilder was not called")
	}
	if !strings.HasPrefix(gotSpec, "github-copilot/") {
		t.Fatalf("cycleModel passed %q to ModelBuilder; want provider-qualified github-copilot/<id>", gotSpec)
	}
}

func TestGeneratedModelSpecPreservesSlashInModelID(t *testing.T) {
	got := generatedModelSpec(ai.GeneratedModel{Provider: "openrouter", ID: "openai/gpt-5.5"})
	want := "openrouter/openai/gpt-5.5"
	if got != want {
		t.Fatalf("generatedModelSpec = %q, want %q", got, want)
	}
}
