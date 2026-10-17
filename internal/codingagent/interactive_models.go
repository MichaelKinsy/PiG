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

// modelSelectorRuntime is the ModelRuntime the model selector reads (model-selector.ts modelRuntime): the registry's models, its load error and its
// catalog refresh. After a refresh the footer's provider information is updated on the owner loop.
type modelSelectorRuntime struct {
	m        *InteractiveMode
	registry *ModelRegistry
}

func (r modelSelectorRuntime) GetAvailableSnapshot() []*ai.Model {
	return r.registry.GetAvailableModelData()
}

func (r modelSelectorRuntime) GetModel(provider, id string) *ai.Model {
	for _, model := range r.registry.GetAllModelData() {
		if model.ProviderID() == provider && model.ModelID() == id {
			return model
		}
	}
	return nil
}

func (r modelSelectorRuntime) GetError() string { return r.registry.LoadError() }

// Refresh is ModelRuntime.refresh as the selector reaches it: the interactive mode's shared all-catalog refresh. A refresh that the context
// ended is an aborted result (model-catalog-refresh.ts raceWithAbortSignal), and a settled one republishes the provider info on the main loop.
func (r modelSelectorRuntime) Refresh(ctx context.Context, _ ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	result, err := RefreshModelCatalogs(ctx, r.registry)
	if err != nil {
		return ai.ModelsRefreshResult{Aborted: true}
	}
	r.m.runOnMain(ctx, r.m.updateProviderInfo)
	return ai.ModelsRefreshResult{Aborted: result.Aborted, Errors: result.Errors, ErrorOrder: result.failedProviders()}
}

// pickModel shows the model selector until the user decides. An accepted selection stays shown and focused until the caller calls done, as Pi's showModelSelector closes the selector only once setModel settles (interactive-mode.ts:5313-5320). Meanwhile its cancel closes it, and another selection is applied after the pending one, as Pi's selectModel starts another setModel.
func (m *InteractiveMode) pickModel(ctx context.Context, initialQuery string) (spec string, accepted, persist bool, done func()) {
	runtime := modelSelectorRuntime{m: m, registry: m.modelRegistryOrDefault()}
	var defaultModel *tui.DefaultModelReference
	if sm := m.opts.SettingsManager; sm != nil && sm.GetDefaultProvider() != "" && sm.GetDefaultModel() != "" {
		defaultModel = &tui.DefaultModelReference{Provider: sm.GetDefaultProvider(), ID: sm.GetDefaultModel()}
	}
	var scoped []tui.ScopedModelItem
	for _, item := range m.scopedModelItems(m.availableModelItems()) {
		if model := runtime.GetModel(item.Provider, item.ID); model != nil {
			scoped = append(scoped, tui.ScopedModelItem{Model: model})
		}
	}
	pending := false
	var selector *tui.ModelSelectorComponent
	selectAgain := func(persist bool) {
		if pending {
			m.reselectModel(ctx, selector.SelectedFQ(), persist, done)
		}
	}
	selector = tui.NewModelSelectorComponent(m.tuiInst, m.opts.Model, runtime, scoped,
		func(*ai.Model) { selectAgain(false) }, func() { done() }, initialQuery,
		func(*ai.Model) { selectAgain(true) }, defaultModel)
	done = m.showModelSelector(selector)
	spec, accepted = m.runModelSelectorInput(ctx, selector)
	if !accepted {
		done()
		return "", false, false, nil
	}
	pending = true
	return spec, true, selector.SelectedAsDefault(), done
}

// reselectModel applies a selection made while an earlier one awaits model_select: Pi's selectModel starts another setModel at once, then closes the selector and reports the outcome (interactive-mode.ts:5313-5325).
func (m *InteractiveMode) reselectModel(ctx context.Context, spec string, persist bool, done func()) {
	m.switchModel(ctx, spec, persist, func(err error) {
		done()
		if err != nil {
			m.showError(err.Error())
			return
		}
		showModelSelectionStatus(&SlashContext{ShowStatus: m.showStatus, modelSelectionPersist: persist}, spec)
	})
}

// switchModel applies the model spec names and calls finish with the result on the owner loop once the change settles; it does not wait for that. With a Session, Pi's setModel checks auth, applies the model synchronously and then awaits model_select (agent-session.ts:2430-2449): SetModelOnMain checks auth and awaits the handlers off the loop and dispatches the state change to the loop, where the mode shows the model at once unless the Session was replaced meanwhile.
// The Session emits model_select once, for a changed selection (agent-session.ts:2372-2384, 2411).
// With a Session, SetModel persists the default and extends a nonempty scope itself, before model_select (agent-session.ts:setModel; interactive-mode.ts:5314).
func (m *InteractiveMode) switchModel(ctx context.Context, spec string, persist bool, finish func(error)) {
	m.invalidatePostLoginSelection()
	if m.opts.ModelBuilder == nil {
		finish(fmt.Errorf("model switching is not configured (no ModelBuilder)"))
		return
	}
	newModel, err := m.opts.ModelBuilder(spec)
	if err != nil {
		finish(err)
		return
	}
	showModel := func() {
		m.opts.Model = newModel
		m.statusLine.SetModel(newModel)
		m.refreshThinkingLevel()
	}
	handle := m.opts.SessionHandle
	if handle == nil {
		if m.agent != nil {
			m.agent.SetModel(newModel)
		}
		showModel()
		if persist {
			if err := m.persistDefaultModel(newModel); err != nil {
				finish(err)
				return
			}
		}
		m.tuiInst.Render()
		finish(nil)
		return
	}
	dispatch := func(mutate func() error) error {
		return m.runOnMainAndWait(ctx, func() error {
			if err := mutate(); err != nil {
				return err
			}
			// Pi's footer reads the current Session's model.
			if m.opts.SessionHandle == handle {
				showModel()
			}
			return nil
		})
	}
	notify := m.modelNotificationContext(ctx)
	m.awaitModelChange(ctx, func() error {
		return handle.SetModelOnMain(notify, newModel, ModelMutationOptions{Persist: persist}, dispatch)
	}, func(err error) {
		if err == nil {
			// A handler may have changed the thinking level.
			m.refreshThinkingLevel()
			if persist {
				m.initScopedModels()
			}
			m.tuiInst.Render()
		}
		finish(err)
	})
}

// modelNotificationContext bounds a model change's auth check and extension notifications by the mode's lifetime. Stop cancels it
// before it joins the mode's tasks, as Pi's shutdown exits without awaiting a pending model_select (interactive-mode.ts:4251-4291).
// It is read on the owner loop when the change starts.
func (m *InteractiveMode) modelNotificationContext(ctx context.Context) context.Context {
	if m.backgroundCtx != nil {
		return m.backgroundCtx
	}
	return ctx
}

// awaitModelChange runs work, which awaits the extensions' model_select handlers, on a task the mode owns, and then finish with its
// result on the owner loop. Pi does not await cycleModel or selectModel in the handlers that start them (interactive-mode.ts:3083-3084,
// 3180, 3195-3199, 5334-5340), and their awaits do not block its event loop (4498-4500, 5313-5320). So the owner loop keeps running
// while work runs: keys reach the focused component, a submitted prompt starts, and a change requested meanwhile starts at once and
// overlaps this one. Each finish runs when its own work returns.
// As in awaitReloadStep, a cancelled change reports the cancellation's cause once its work returned. A mode that has already stopped
// (ctx ended) runs no work, so a cycle's notification completion is never invoked, and a finish the stopped loop no longer accepts is
// dropped with it.
func (m *InteractiveMode) awaitModelChange(ctx context.Context, work func() error, finish func(error)) {
	if ctx.Err() != nil {
		finish(context.Cause(ctx))
		return
	}
	m.backgroundTasks.Go(func() {
		err := work()
		m.runOnMain(ctx, func() {
			if ctx.Err() != nil {
				err = context.Cause(ctx)
			}
			finish(err)
		})
	})
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
	switchModelThen(sc, spec)
}

// cycleModel delegates ordering, scope, and thinking-level changes to the Session.
// upstream: interactive-mode.ts:cycleModel
func (m *InteractiveMode) cycleModel(forward bool) {
	m.invalidatePostLoginSelection()
	handle := m.opts.SessionHandle
	if handle == nil {
		m.showError("Model switching not configured")
		return
	}
	direction := "backward"
	if forward {
		direction = "forward"
	}
	ctx := m.runCtx
	if ctx == nil {
		ctx = context.Background() //portlint:allow ctxbackground mode not running; Pi's cycleModel takes no signal (agent-session.ts:2482-2550)
	}
	// Pi's cycleModel applies the next model and thinking level before its first await (agent-session.ts:2482-2518, 2523-2550), so a later key or prompt already sees it.
	result, complete, err := handle.BeginModelCycleOnMain(m.modelNotificationContext(ctx), direction, ModelMutationOptions{})
	if err != nil {
		m.showError(err.Error())
		return
	}
	if result == nil {
		message := "Only one model available"
		if len(handle.ScopedModels()) > 0 {
			message = "Only one model in scope"
		}
		m.showStatus(message)
		return
	}
	m.opts.Model = result.Model
	m.statusLine.SetModel(result.Model)
	m.refreshThinkingLevel()
	m.awaitModelChange(ctx, func() error {
		if complete != nil {
			complete()
		}
		return nil
	}, func(err error) {
		if err != nil {
			m.showError(err.Error())
			return
		}
		m.finishCycleModel(result)
	})
}

// finishCycleModel is the rest of Pi's cycleModel once model_select settled.
func (m *InteractiveMode) finishCycleModel(result *ModelCycleResult) {
	// A handler may have changed the thinking level.
	m.refreshThinkingLevel()
	displayName := result.Model.DisplayName
	if displayName == "" {
		displayName = result.Model.ID
	}
	thinking := ""
	if (result.Model.ProviderMeta.Reasoning || result.Model.Capabilities.MaxThinking != "") && result.ThinkingLevel != ai.ThinkingOff {
		thinking = " (thinking: " + string(result.ThinkingLevel) + ")"
	}
	m.showStatus("Switched to " + displayName + thinking)
	// Pi warns after a successful cycle too (interactive-mode.ts:4510).
	m.maybeWarnAboutAnthropicSubscriptionAuthAsync()
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
	// interactive-mode.ts:5444-5445: setEnabledModels queues the write and records a failure for the settings diagnostics, and the status
	// always reports the save.
	// upstream: packages/coding-agent/src/core/settings-manager.ts:enqueueWrite
	_ = m.opts.SettingsManager.SetEnabledModels(enabledIDs)
	m.showStatus("Model selection saved to settings")
}

// deliverUserMessage injects a user-authored prompt from an extension host
// action. Upstream sendUserMessage routes through prompt(): idle injections
// start a turn immediately; busy injections queue as steer/followUp. Keep this
// as the single implementation for subprocess and in-process extension calls.
