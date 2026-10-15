package codingagent

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui"
)

// availableModelItems uses the runtime's available catalog, including account-specific model filters.
func (m *InteractiveMode) availableModelItems() []tui.ModelSelectorItem {
	registry := m.modelRegistryOrDefault()
	var items []tui.ModelSelectorItem
	for _, model := range registry.GetAvailable() {
		items = append(items, tui.ModelSelectorItem{Provider: model.ProviderID, ID: model.ModelID, Name: model.DisplayName})
	}
	return items
}

// modelRegistryOrDefault is the mode's model registry, or a registry over the agent directory's auth when the mode has none.
func (m *InteractiveMode) modelRegistryOrDefault() *ModelRegistry {
	var registry *ModelRegistry
	agentDir := ""
	if m != nil {
		registry = m.opts.ModelRegistry
		agentDir = m.opts.AgentDir
	}
	if registry == nil {
		registry = NewModelRegistry(agentDir)
		if agentDir != "" {
			if auth, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json")); err == nil {
				registry.SetAuthStorage(auth)
			}
		}
	}
	return registry
}

// persistDefaultModel saves the explicit default and adds it to a nonempty model scope.
func (m *InteractiveMode) persistDefaultModel(model *ai.Model) error {
	spec := modelSpec(model)
	provider, _, _ := strings.Cut(spec, "/")
	if sm := m.opts.SettingsManager; sm != nil {
		if err := sm.SetDefaultModelAndProvider(provider, model.ID); err != nil {
			return err
		}
	}
	return m.addPersistedDefaultToNonEmptyScope(model)
}

func (m *InteractiveMode) addPersistedDefaultToNonEmptyScope(model *ai.Model) error {
	spec := modelSpec(model)
	if len(m.scopedModelIDs) == 0 || slices.Contains(m.scopedModelIDs, spec) || slices.Contains(m.scopedModelIDs, model.ID) {
		return nil
	}
	m.scopedModelIDs = append(m.scopedModelIDs, spec)
	if sm := m.opts.SettingsManager; sm != nil {
		enabled := sm.GetEnabledModels()
		if len(enabled) > 0 && !slices.ContainsFunc(enabled, func(pattern string) bool { return jsstring.ToLower(pattern) == jsstring.ToLower(spec) }) {
			enabled = append(enabled, spec)
			return sm.UpdateGlobal(func(settings *Settings) { settings.EnabledModels = enabled })
		}
	}
	return nil
}
