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
	"github.com/MichaelKinsy/PiG/coding/extension"
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

func modelCatalogRefreshError(result CatalogRefreshResult, err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "Model refresh timed out; showing cached models."
	case err != nil:
		return "Could not refresh model catalogs: " + err.Error()
	case len(result.Errors) == 1:
		return "Could not refresh " + slices.Sorted(maps.Keys(result.Errors))[0] + "; showing cached models."
	case len(result.Errors) > 1:
		return fmt.Sprintf("Could not refresh %d model catalogs (%s); showing cached models.", len(result.Errors), strings.Join(slices.Sorted(maps.Keys(result.Errors)), ", "))
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

// cycleModel cycles through available models in the given direction.
// forward=true goes to the next model; forward=false goes to the previous.
// Mirrors upstream interactive-mode.ts:3303-3320 (cycleModel).
//
// upstream: keybindings.ts:76-82 (app.model.cycleForward / cycleBackward)
func (m *InteractiveMode) cycleModel(forward bool) {
	// Build the same filtered model list that PickModel uses.
	catalog := ai.ListModels("")
	if len(catalog) == 0 {
		m.statusLine.Flash("No models available", 3*time.Second)
		return
	}

	// Filter to reachable + authenticated providers.
	reachable := ReachableProviders()
	authed := AuthenticatedProviders(m.opts.AgentDir)

	var items []ai.GeneratedModel
	for _, mm := range catalog {
		if !reachable[mm.Provider] {
			continue
		}
		if !authed[mm.Provider] {
			continue
		}
		items = append(items, mm)
	}

	// Merge registered dynamic providers (example-provider, radius) before scoping so
	// the scope filter applies to them uniformly. Already auth-filtered.
	for _, e := range m.dynamicProviderModels() {
		items = append(items, ai.GeneratedModel{Provider: e.ProviderID, ID: e.ModelID, DisplayName: e.DisplayName})
	}

	// If scoped model IDs are set, filter to only those.
	if m.scopedModelIDs != nil {
		scoped := make(map[string]bool, len(m.scopedModelIDs))
		for _, id := range m.scopedModelIDs {
			scoped[id] = true
		}
		items = slices.DeleteFunc(items, func(mm ai.GeneratedModel) bool {
			_, scopedQualified := scoped[generatedModelSpec(mm)]
			_, scopedBare := scoped[mm.ID]
			return !scopedQualified && !scopedBare
		})
	}

	if len(items) <= 1 {
		msg := "Only one model available"
		if len(items) == 0 {
			msg = "No authenticated models"
		}
		m.statusLine.Flash(msg, 3*time.Second)
		return
	}

	// Find current model in the list.
	currentID := modelSpec(m.opts.Model)
	currentBareID := ""
	if m.opts.Model != nil {
		currentBareID = m.opts.Model.ID
	}
	currentIdx := -1
	for i, mm := range items {
		if generatedModelSpec(mm) == currentID || mm.ID == currentBareID {
			currentIdx = i
			break
		}
	}
	if currentIdx == -1 {
		currentIdx = 0
	}

	// Cycle.
	n := len(items)
	var nextIdx int
	if forward {
		nextIdx = (currentIdx + 1) % n
	} else {
		nextIdx = (currentIdx - 1 + n) % n
	}
	next := items[nextIdx]
	spec := generatedModelSpec(next)

	// Switch via the same path as /model.
	if m.opts.ModelBuilder == nil {
		m.statusLine.Flash("Model switching not configured", 3*time.Second)
		return
	}
	newModel, err := m.opts.ModelBuilder(spec)
	if err != nil {
		m.statusLine.Flash("Model switch failed: "+err.Error(), 5*time.Second)
		return
	}
	if m.opts.SessionHandle != nil {
		if err := m.opts.SessionHandle.SetModel(newModel); err != nil {
			m.statusLine.Flash("Model switch failed: "+err.Error(), 5*time.Second)
			return
		}
	} else if m.agent != nil {
		m.agent.SetModel(newModel)
	}
	prevModel := m.opts.Model // snapshot before overwrite
	m.opts.Model = newModel
	m.statusLine.SetModel(newModel)
	m.refreshThinkingLevel()

	// Emit model_select for extensions. Upstream cycleModel is async and its key
	// handler fires it without awaiting, so the render loop keeps ticking while
	// extensions handle the event. Here cycleModel runs on the Bubbletea update
	// goroutine, so emit off-thread: a synchronous emit blocks this goroutine on
	// the extension round-trip and freezes the working spinner: badly during
	// active streaming, when the bridge is already busy. modelToExtModel is
	// evaluated now (before the go statement) so the goroutine captures values.
	go emitModelSelect(m.newRunner,
		modelToExtModel(newModel),
		modelToExtModel(prevModel),
		extension.ModelSelectSourceCycle)

	displayName := next.DisplayName
	if displayName == "" {
		displayName = next.ID
	}
	m.statusLine.Flash("Switched to "+displayName, 3*time.Second)
}

// dynamicProviderModels returns models from registered dynamic providers
// (e.g. example-provider, radius) that have configured auth. Upstream's interactive
// model surfaces build from modelRuntime.getAvailable(), which merges these
// registered providers with the static catalog; pig's static ai.ListModels
// omits them, so the model-list callers append these to match. Nil-safe.
func (m *InteractiveMode) dynamicProviderModels() []ModelEntry {
	if m == nil || m.opts.ModelRegistry == nil {
		return nil
	}
	return m.opts.ModelRegistry.GetAvailable()
}

func generatedModelSpec(mm ai.GeneratedModel) string {
	if mm.Provider == "" || strings.HasPrefix(mm.ID, mm.Provider+"/") {
		return mm.ID
	}
	return mm.Provider + "/" + mm.ID
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

// showScopedModels opens the /scoped-models multi-select list.
// Mirrors upstream showModelsSelector (interactive-mode.ts:3925).
func (m *InteractiveMode) showScopedModels() {
	availableModels := func() []tui.ModelItem {
		catalog := ai.ListModels("")
		reachable := ReachableProviders()
		authed := AuthenticatedProviders(m.opts.AgentDir)
		models := make([]tui.ModelItem, 0, len(catalog))
		for _, mm := range catalog {
			if !reachable[mm.Provider] || !authed[mm.Provider] {
				continue
			}
			models = append(models, tui.ModelItem{
				FullID:   mm.ID,
				Name:     mm.DisplayName,
				Provider: mm.Provider,
			})
		}
		for _, entry := range m.dynamicProviderModels() {
			models = append(models, tui.ModelItem{
				FullID:   entry.ModelID,
				Name:     entry.DisplayName,
				Provider: entry.ProviderID,
			})
		}
		return models
	}

	allModels := availableModels()
	if len(allModels) == 0 {
		m.statusLine.Flash("No authenticated models", 3*time.Second)
		return
	}

	sl := tui.NewScopedModelsList(tui.ScopedModelsConfig{
		AllModels:       allModels,
		EnabledModelIDs: m.scopedModelIDs,
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

	result := m.runModalScopedModels(sl, refresh)
	if result.Cancelled {
		return
	}
	m.scopedModelIDs = result.EnabledIDs
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
