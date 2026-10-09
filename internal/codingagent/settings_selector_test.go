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

// Pi: packages/coding-agent/src/modes/interactive/components/settings-selector.ts:62 (SettingsConfig.currentModel).
func TestModelThinkingSubmenuOrderingSearchBackAndEmpty(t *testing.T) {
	models := []*ai.Model{}
	for _, spec := range []string{"z/last", "a/first", "z/default", "z/current"} {
		provider, id, _ := strings.Cut(spec, "/")
		models = append(models, &ai.Model{ID: id, ProviderMeta: ai.ProviderMetadata{ProviderID: provider}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelHigh}})
	}
	config := SettingsConfig{DefaultModel: "z/default", CurrentModel: models[3], AvailableDefaultModels: models}
	var changes int
	done := false
	menu := newModelThinkingSubmenu(config, map[string]string{"a/first": "low"}, func(*ai.Model, string) { changes++ }, func() { done = true })
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
	menu = newModelThinkingSubmenu(SettingsConfig{}, map[string]string{}, func(*ai.Model, string) { changes++ }, func() { done = true })
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

// Pi: packages/coding-agent/src/core/settings-manager.ts:877 (SettingsManager.getModelThinkingLevel).
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
	model := &ai.Model{ID: "reasoner", ProviderMeta: ai.ProviderMetadata{ProviderID: "fixture"}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevel(ai.ThinkingHigh)}}
	thinking := mustNewAgent(agent.AgentOptions{Model: model, ThinkingLevel: ai.ThinkingLow})
	mode := &InteractiveMode{opts: InteractiveModeOptions{Model: model, ModelRegistry: registry, SettingsManager: sm, SessionHandle: &recordingCompactHandle{agent: thinking, thinkingSettings: sm}}, agent: thinking, editor: tui.NewEditor(), thinkingLevel: "low", uiTaskCh: make(chan func(), 64)}
	settle := startOwnerLoop(t, mode)
	mode.statusLine = NewFooterComponent(model, "", nil)
	mode.statusLine.SetStatusHook(func(message string) { t.Errorf("per-model settings appended a session selection notice: %s", message) })
	before, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	// openSubmenu runs keys against the per-model thinking row opened in a fresh selector, as the user does, and returns the summary the row shows.
	openSubmenu := func(keys ...string) string {
		sc := mode.buildSlashContext(t.Context())
		var summary string
		shown := useSettingsSelector(sc, func(selector *SettingsSelectorComponent) {
			list := selector.GetSettingsList()
			list.SelectItem("model-thinking")
			list.HandleInput("\r")
			for _, key := range keys {
				list.HandleInput(key)
			}
			summary = settingsRowValue(t, list, "model-thinking")
		})
		if err := settingsHandler(sc); err != nil {
			t.Fatal(err)
		}
		if !*shown {
			t.Fatal("settings selector not shown")
		}
		return summary
	}
	if summary := openSubmenu("\r", "\x1b", "\x1b"); summary != "none" {
		t.Fatalf("cancel summary = %q", summary)
	}
	after, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("browsing/cancellation rewrote settings")
	}

	// off wraps to high, the last supported level.
	if summary := openSubmenu("\r", "\x1b[A", "\r", "\x1b"); summary != "1 configured" {
		t.Fatalf("set summary = %q", summary)
	}
	settle()
	if mode.agent.ThinkingLevel() != ai.ThinkingHigh || sm.GetModelThinkingLevel("fixture", "reasoner") != "high" {
		t.Fatal("current-model override did not apply to both Session and settings")
	}
	if sm.GetDefaultThinkingLevel() != "low" {
		t.Fatal("override rewrote global thinking")
	}
	// The checked high level, then the clear-override row after it.
	if summary := openSubmenu("\r", "\x1b[B", "\r", "\x1b"); summary != "none" {
		t.Fatalf("clear summary = %q", summary)
	}
	settle()
	if mode.agent.ThinkingLevel() != ai.ThinkingLow || sm.GetModelThinkingLevel("fixture", "reasoner") != "" {
		t.Fatal("clear did not restore global default in Session")
	}
}

func BenchmarkModelThinkingSubmenu(b *testing.B) {
	models := make([]*ai.Model, 200)
	for i := range models {
		models[i] = &ai.Model{ID: strings.Repeat("model", i%8+1), ProviderMeta: ai.ProviderMetadata{ProviderID: "fixture"}}
	}
	for b.Loop() {
		menu := newModelThinkingSubmenu(SettingsConfig{AvailableDefaultModels: models}, map[string]string{}, func(*ai.Model, string) {}, func() {})
		menu.HandleInput("model")
		menu.Render(100)
	}
}
