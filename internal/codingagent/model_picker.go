package codingagent

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// availableModelItems uses the composed runtime catalog and provider auth rather than a second interactive provider allowlist.
func (m *InteractiveMode) availableModelItems() []tui.ModelSelectorItem {
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
	auth := make(map[string]bool)
	var items []tui.ModelSelectorItem
	for _, model := range registry.RuntimeModels() {
		ready, checked := auth[model.Provider]
		if !checked {
			ready = registry.HasConfiguredAuth(model.Provider)
			auth[model.Provider] = ready
		}
		if ready {
			items = append(items, tui.ModelSelectorItem{Provider: model.Provider, ID: model.ID, Name: model.Name})
		}
	}
	return items
}

// persistDefaultModel adds an explicitly saved default to a nonempty model scope, as AgentSession._addPersistedDefaultToNonEmptyScope does.
func (m *InteractiveMode) persistDefaultModel(model *ai.Model) error {
	spec := modelSpec(model)
	provider, _, _ := strings.Cut(spec, "/")
	if sm := m.opts.SettingsManager; sm != nil {
		if err := sm.SetDefaultModelAndProvider(provider, model.ID); err != nil {
			return err
		}
	}
	if len(m.scopedModelIDs) == 0 || slices.Contains(m.scopedModelIDs, spec) || slices.Contains(m.scopedModelIDs, model.ID) {
		return nil
	}
	m.scopedModelIDs = append(m.scopedModelIDs, spec)
	if sm := m.opts.SettingsManager; sm != nil {
		enabled := sm.GetEnabledModels()
		if len(enabled) > 0 && !slices.ContainsFunc(enabled, func(pattern string) bool { return strings.EqualFold(pattern, spec) }) {
			enabled = append(enabled, spec)
			return sm.UpdateGlobal(func(settings *Settings) { settings.EnabledModels = enabled })
		}
	}
	return nil
}
