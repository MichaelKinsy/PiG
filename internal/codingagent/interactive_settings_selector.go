package codingagent

import (
	"github.com/MichaelKinsy/PiG/ai"
)

// runModalSettingsSelector shows the settings selector in the editor slot and blocks until its onCancel callback calls done. Each change runs its callback while the list stays open with its selection and search, as upstream SettingsList's onChange does (settings-list.ts:264-291). Mirrors upstream showSelector (interactive-mode.ts:3655-3666).
func (m *InteractiveMode) runModalSettingsSelector(build func(done func()) *SettingsSelectorComponent) {
	finished := false
	selector := build(func() { finished = true })
	list := selector.GetSettingsList()
	m.runEditorSlotComponent(selector, list.HandleInput, func() bool { return finished })
}

// settingsModels is the model list the per-model thinking submenu offers and the session's model (interactive-mode.ts:4877-4878).
func (m *InteractiveMode) settingsModels() ([]*ai.Model, *ai.Model) {
	var models []*ai.Model
	if m.opts.ModelRegistry != nil {
		for _, entry := range m.opts.ModelRegistry.GetAvailable() {
			generated := ai.GeneratedModel{Provider: entry.ProviderID, ID: entry.ModelID, Reasoning: entry.Reasoning, ThinkingLevelMap: entry.ThinkingLevelMap}
			models = append(models, generated.ToModel())
		}
	}
	return models, m.opts.Model
}

// applyModelThinkingLevel applies a changed per-model override to the session when it is for the current model; an empty level reverts to the global default (interactive-mode.ts:4962-4984).
func (m *InteractiveMode) applyModelThinkingLevel(provider, modelID, level string) {
	current := m.opts.Model
	if current == nil || current.ProviderMeta.ProviderID != provider || current.ID != modelID {
		return
	}
	if level == "" {
		level = string(m.opts.SettingsManager.GetDefaultThinkingLevel())
		if level == "" {
			level = DefaultThinkingLevel
		}
	}
	m.applyThinkingLevel(string(ai.ClampThinkingLevel(current, ai.ModelThinkingLevel(level))), false, func(err error) {
		if err != nil {
			m.showError(err.Error())
		}
	})
}
