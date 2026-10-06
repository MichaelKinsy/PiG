package codingagent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) scopedModelItems(items []tui.ModelSelectorItem) []tui.ModelSelectorItem {
	// Scoped rows retain session order; only the all-models list is sorted.
	itemsByID := make(map[string][]int, len(items)*2)
	for i, item := range items {
		itemsByID[item.FQ()] = append(itemsByID[item.FQ()], i)
		itemsByID[item.ID] = append(itemsByID[item.ID], i)
	}
	seen := make([]bool, len(items))
	var scoped []tui.ModelSelectorItem
	for _, id := range m.scopedModelIDs {
		for _, i := range itemsByID[id] {
			if !seen[i] {
				scoped = append(scoped, items[i])
				seen[i] = true
			}
		}
	}
	return scoped
}

type modelSelectorRefresh struct {
	models       []tui.ModelSelectorItem
	updateModels bool
	errorText    string
}

func (m *InteractiveMode) pickModel(ctx context.Context, initialQuery string) (spec string, accepted, persist bool) {
	items := m.availableModelItems()
	selector := tui.NewModelSelector("Select model", m.scopedModelItems(items), items, modelSpec(m.opts.Model))
	if sm := m.opts.SettingsManager; sm != nil && sm.GetDefaultProvider() != "" && sm.GetDefaultModel() != "" {
		selector.SetDefaultModel(sm.GetDefaultProvider() + "/" + sm.GetDefaultModel())
	}
	selector.SetFilter(initialQuery)
	selector.SetStatus("Refreshing model catalogs…")
	// upstream: packages/coding-agent/src/modes/interactive/components/model-selector.ts:refreshModels
	refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	refresh := make(chan modelSelectorRefresh, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, err := RefreshModelCatalogs(refreshCtx, m.opts.ModelRegistry)
		refreshed := modelSelectorRefresh{updateModels: err == nil, errorText: modelCatalogRefreshError(result, err)}
		if err == nil {
			refreshed.models = m.availableModelItems()
			if refreshed.errorText == "" && m.opts.ModelRegistry != nil {
				refreshed.errorText = m.opts.ModelRegistry.LoadError()
			}
		}
		refresh <- refreshed
	}()
	defer func() {
		cancel()
		<-done
	}()
	spec, accepted = m.runEditorSlotModelSelector(ctx, selector, refresh)
	return spec, accepted, accepted && selector.SelectedAsDefault()
}

func (result CatalogRefreshResult) failedProviders() []string {
	if len(result.errorOrder) == len(result.Errors) {
		return result.errorOrder
	}
	return slices.Sorted(maps.Keys(result.Errors))
}

func modelCatalogRefreshError(result CatalogRefreshResult, err error) string {
	providers := result.failedProviders()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Model refresh timed out; showing cached models."
	case err != nil:
		return "Could not refresh model catalogs: " + err.Error()
	case len(result.Errors) == 1:
		return "Could not refresh " + providers[0] + "; showing cached models."
	case len(result.Errors) > 1:
		return fmt.Sprintf("Could not refresh %d model catalogs (%s); showing cached models.", len(result.Errors), strings.Join(providers, ", "))
	default:
		return ""
	}
}

func (m *InteractiveMode) handleModelPicker() {
	ctx := m.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	sc := m.buildSlashContext(ctx)
	if sc.PickModel == nil || sc.SwitchModel == nil {
		m.appendToChat(tui.NewText("\033[33mmodel picker unavailable in this build\033[0m"))
		return
	}
	spec, ok := sc.PickModel("")
	if !ok {
		return
	}
	if err := sc.SwitchModel(spec); err != nil {
		m.appendToChat(tui.NewText("\033[31mmodel switch failed: " + err.Error() + "\033[0m"))
		return
	}
	showModelSelectionStatus(sc, spec)
}

// cycleModel delegates ordering, scope, and thinking-level changes to the Session.
// upstream: interactive-mode.ts:cycleModel
func (m *InteractiveMode) cycleModel(forward bool) {
	m.invalidatePostLoginSelection()
	if m.opts.SessionHandle == nil {
		m.showError("Model switching not configured")
		return
	}
	direction := "backward"
	if forward {
		direction = "forward"
	}
	result, err := m.opts.SessionHandle.CycleModel(direction)
	if err != nil {
		m.showError(err.Error())
		return
	}
	if result == nil {
		message := "Only one model available"
		if len(m.opts.SessionHandle.ScopedModels()) > 0 {
			message = "Only one model in scope"
		}
		m.statusLine.Flash(message, 3*time.Second)
		return
	}
	m.opts.Model = result.Model
	m.statusLine.SetModel(result.Model)
	m.refreshThinkingLevel()

	displayName := result.Model.DisplayName
	if displayName == "" {
		displayName = result.Model.ID
	}
	thinking := ""
	if (result.Model.ProviderMeta.Reasoning || result.Model.Capabilities.MaxThinking != "") && result.ThinkingLevel != ai.ThinkingOff {
		thinking = " (thinking: " + string(result.ThinkingLevel) + ")"
	}
	m.statusLine.Flash("Switched to "+displayName+thinking, 3*time.Second)
}

func modelSpec(model *ai.Model) string {
	if model == nil {
		return ""
	}
	providerID := model.ProviderMeta.ProviderID
	if providerID == "" && model.Provider != nil {
		providerID = model.Provider.ID()
	}
	if providerID == "" || strings.HasPrefix(model.ID, providerID+"/") {
		return model.ID
	}
	return providerID + "/" + model.ID
}

// showScopedModels opens the selector immediately, including configured IDs absent from the available catalog.
func (m *InteractiveMode) showScopedModels() {
	availableModels := func() []tui.ModelItem {
		items := m.availableModelItems()
		models := make([]tui.ModelItem, 0, len(items))
		for _, item := range items {
			models = append(models, tui.ModelItem{FullID: item.FQ(), Name: item.Name, Provider: item.Provider})
		}
		return models
	}

	allModels := availableModels()
	configured := m.opts.Settings.EnabledModels
	if m.opts.SettingsManager != nil {
		configured = m.opts.SettingsManager.GetEnabledModels()
	}
	selection, enabledIDs := newScopedModelsSelection(allModels, configured, m.scopedModelIDs)

	sl := tui.NewScopedModelsList(tui.ScopedModelsConfig{
		AllModels:       allModels,
		EnabledModelIDs: enabledIDs,
		RefreshStatus:   "Refreshing model catalogs…",
	})
	// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:showModelsSelector
	refreshCtx, cancelRefresh := context.WithTimeout(m.runCtx, 15*time.Second)
	refresh := make(chan scopedModelsRefresh, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		status := "Model catalogs refreshed."
		kind := tui.RefreshStatusSuccess
		result, err := RefreshModelCatalogs(refreshCtx, m.opts.ModelRegistry)
		if errors.Is(err, context.Canceled) {
			return
		}
		if warning := modelCatalogRefreshError(result, err); warning != "" {
			if err == nil && len(result.Errors) > 0 {
				warning = "Could not refresh " + strings.Join(result.failedProviders(), ", ") + "; showing cached models."
			}
			status, kind = warning, tui.RefreshStatusWarning
		} else if m.opts.ModelRegistry != nil && m.opts.ModelRegistry.LoadError() != "" {
			status = "Could not refresh model catalogs: " + m.opts.ModelRegistry.LoadError()
			kind = tui.RefreshStatusWarning
		}
		refresh <- scopedModelsRefresh{models: availableModels(), status: status, kind: kind}
	}()
	defer func() {
		cancelRefresh()
		<-done
	}()

	m.runModalScopedModels(sl, refresh, selection)
}

func (m *InteractiveMode) persistScopedModelIDs(enabledIDs []string) {
	if m.opts.SettingsManager == nil {
		return
	}
	if err := m.opts.SettingsManager.UpdateGlobal(func(gs *Settings) {
		gs.EnabledModels = enabledIDs
	}); err != nil {
		m.statusLine.Flash("Failed to save: "+err.Error(), 3*time.Second)
		return
	}
	m.statusLine.Flash("Model selection saved to settings", 2*time.Second)
}

// deliverUserMessage injects a user-authored prompt from an extension host
// action. Upstream sendUserMessage routes through prompt(): idle injections
// start a turn immediately; busy injections queue as steer/followUp. Keep this
// as the single implementation for subprocess and in-process extension calls.
