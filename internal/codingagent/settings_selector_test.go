package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestModelThinkingSubmenuOrderingSearchBackAndEmpty(t *testing.T) {
	models := []*ai.Model{}
	for _, spec := range []string{"z/last", "a/first", "z/default", "z/current"} {
		provider, id, _ := strings.Cut(spec, "/")
		models = append(models, &ai.Model{ID: id, ProviderMeta: ai.ProviderMetadata{ProviderID: provider}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh}})
	}
	settings := Settings{DefaultProvider: "z", DefaultModel: "default", ModelThinkingLevels: map[string]string{"a/first": "low"}}
	var changes int
	done := false
	menu := newModelThinkingSubmenu(settings, models, "z/current", func(*ai.Model, string) { changes++ }, func() { done = true })
	items := menu.steps[0].Options(nil)
	var got []string
	for _, item := range items {
		got = append(got, item.Value)
	}
	if want := []string{"z/current", "z/default", "a/first", "z/last"}; !slices.Equal(got, want) {
		t.Fatalf("model order = %v, want %v", got, want)
	}
	menu.HandleInput("low") // descriptions participate in fuzzy search
	menu.HandleInput("\r")
	if menu.context["model"] != "a/first" {
		t.Fatalf("search selected %v", menu.context)
	}
	menu.HandleInput("\x1b")
	if menu.stepIndex != 0 || done || changes != 0 {
		t.Fatalf("back = step %d done %v changes %d", menu.stepIndex, done, changes)
	}
	if got := menu.activeComponent.CurrentValue(); got != "z/current" {
		t.Fatalf("back did not rebuild preselection: %s", got)
	}
	menu.HandleInput("\x1b")
	if !done || changes != 0 {
		t.Fatal("cancel applied a value")
	}

	done = false
	menu = newModelThinkingSubmenu(Settings{}, nil, "", func(*ai.Model, string) { changes++ }, func() { done = true })
	if got := menu.activeComponent.CurrentValue(); got != "__none__" {
		t.Fatalf("empty model list = %s", got)
	}
	menu.HandleInput("\r")
	menu.HandleInput("\r")
	if changes != 0 || menu.stepIndex != 1 {
		t.Fatal("empty level list completed")
	}
	menu.HandleInput("\x1b")
	menu.HandleInput("\x1b")
	if !done {
		t.Fatal("empty submenu did not cancel")
	}
}

func TestSettingsModelThinkingAppliesAndClearsCurrentOverrideWithoutChangingDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"fixture":{"api":"openai-completions","baseUrl":"http://127.0.0.1:1/v1","apiKey":"local-fixture","models":[{"id":"reasoner","reasoning":true}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(t.TempDir(), dir)
	if err := sm.SetDefaultThinkingLevel("low"); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	model := &ai.Model{ID: "reasoner", ProviderMeta: ai.ProviderMetadata{ProviderID: "fixture"}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh}}
	mode := &InteractiveMode{opts: InteractiveOptions{Model: model, ModelRegistry: registry, SettingsManager: sm}, agent: agent.NewAgent(agent.AgentOptions{Model: model, ThinkingLevel: ai.ThinkingLow}), editor: tui.NewEditor(), thinkingLevel: "low"}
	mode.statusLine = NewStatusLine(model, "", nil)
	mode.statusLine.SetStatusHook(func(message string) { t.Errorf("per-model settings appended a session selection notice: %s", message) })
	before, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary *string
	component := mode.buildSlashContext(t.Context()).ModelThinkingSubmenu("none", func(value *string) { summary = value })
	menu := component.(*SteppedSubmenu)
	menu.HandleInput("\r")
	menu.HandleInput("\x1b")
	menu.HandleInput("\x1b")
	after, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("browsing/cancellation rewrote settings")
	}
	if summary == nil || *summary != "none" {
		t.Fatalf("cancel summary = %v", summary)
	}

	menu = mode.modelThinkingSettingsSubmenu("none", func(value *string) { summary = value }).(*SteppedSubmenu)
	menu.HandleInput("\r")
	menu.HandleInput("\x1b[A") // off wraps to high, the last supported level
	menu.HandleInput("\r")
	if mode.agent.ThinkingLevel() != ai.ThinkingHigh || sm.GetModelThinkingLevel("fixture", "reasoner") != "high" {
		t.Fatal("current-model override did not apply to both Session and settings")
	}
	if sm.GetDefaultThinkingLevel() != "low" {
		t.Fatal("override rewrote global thinking")
	}
	menu.HandleInput("\r")
	menu.HandleInput("\x1b[B") // checked high -> clear override
	menu.HandleInput("\r")
	if mode.agent.ThinkingLevel() != ai.ThinkingLow || sm.GetModelThinkingLevel("fixture", "reasoner") != "" {
		t.Fatal("clear did not restore global default in Session")
	}
	menu.HandleInput("\x1b")
	if summary == nil || *summary != "none" {
		t.Fatalf("clear summary = %v", summary)
	}
}

func BenchmarkModelThinkingSubmenu(b *testing.B) {
	models := make([]*ai.Model, 200)
	for i := range models {
		models[i] = &ai.Model{ID: strings.Repeat("model", i%8+1), ProviderMeta: ai.ProviderMetadata{ProviderID: "fixture"}}
	}
	for b.Loop() {
		menu := newModelThinkingSubmenu(Settings{}, models, "", func(*ai.Model, string) {}, func() {})
		menu.HandleInput("model")
		menu.Render(100)
	}
}
