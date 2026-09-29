package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) initScopedModels() {
	m.scopedModelIDs = nil
	if len(m.opts.Settings.EnabledModels) == 0 {
		return
	}
	var models []RuntimeModel
	for _, item := range m.availableModelItems() {
		models = append(models, RuntimeModel{Provider: item.Provider, ID: item.ID, Name: item.Name})
	}
	for _, scoped := range ResolveModelScopeFromModels(m.opts.Settings.EnabledModels, models).ScopedModels {
		m.scopedModelIDs = append(m.scopedModelIDs, modelRef(scoped.Model))
	}
}

// scopedModelsSelection keeps selector IDs (including unavailable entries) separate from the available models used for cycling.
type scopedModelsSelection struct {
	available     []RuntimeModel
	configured    []string
	sessionScoped bool
	changed       bool
}

func newScopedModelsSelection(models []tui.ModelItem, configured, scoped []string) (*scopedModelsSelection, []string) {
	selection := &scopedModelsSelection{configured: slices.Clone(configured), sessionScoped: len(scoped) > 0}
	selection.updateAvailable(models)
	if selection.sessionScoped {
		return selection, slices.Clone(scoped)
	}
	return selection, selection.configuredIDs()
}

func (s *scopedModelsSelection) updateAvailable(models []tui.ModelItem) {
	s.available = make([]RuntimeModel, 0, len(models))
	for _, model := range models {
		s.available = append(s.available, RuntimeModel{Provider: model.Provider, ID: strings.TrimPrefix(model.FullID, model.Provider+"/"), Name: model.Name})
	}
}

func (s *scopedModelsSelection) configuredIDs() []string {
	if len(s.configured) == 0 {
		return nil
	}
	resolved := ResolveModelScopeFromModels(s.configured, s.available)
	ids := make([]string, 0, len(resolved.ScopedModels))
	for _, entry := range resolved.ScopedModels {
		ids = append(ids, modelRef(entry.Model))
	}
	for _, diagnostic := range resolved.Diagnostics {
		if diagnostic.Code == "no-match" && !slices.Contains(ids, diagnostic.Pattern) {
			ids = append(ids, diagnostic.Pattern)
		}
	}
	return ids
}

func (s *scopedModelsSelection) scopeIDs(enabled []string) []string {
	hasAvailable, allAvailable := false, true
	for _, model := range s.available {
		found := slices.Contains(enabled, modelRef(model))
		hasAvailable = hasAvailable || found
		allAvailable = allAvailable && found
	}
	if enabled == nil || !hasAvailable || allAvailable {
		return nil
	}
	resolved := ResolveModelScopeFromModels(enabled, s.available)
	ids := make([]string, 0, len(resolved.ScopedModels))
	for _, entry := range resolved.ScopedModels {
		ids = append(ids, modelRef(entry.Model))
	}
	return ids
}

func (s *scopedModelsSelection) apply(m *InteractiveMode, enabled []string) {
	m.scopedModelIDs = s.scopeIDs(enabled)
	providers := make(map[string]struct{})
	for _, model := range s.available {
		if len(m.scopedModelIDs) == 0 || slices.Contains(m.scopedModelIDs, modelRef(model)) {
			providers[model.Provider] = struct{}{}
		}
	}
	m.statusLine.SetProviderCount(len(providers))
}

func (s *scopedModelsSelection) persistedIDs(enabled []string) []string {
	if enabled == nil {
		return nil
	}
	allAvailable := len(enabled) == len(s.available) && !slices.ContainsFunc(enabled, func(id string) bool {
		return !slices.ContainsFunc(s.available, func(model RuntimeModel) bool { return id == modelRef(model) })
	})
	if allAvailable {
		return nil
	}
	return slices.Clone(enabled)
}
