package tui

// Ports packages/coding-agent/src/modes/interactive/components/model-selector.ts constructor, loadModelsFromSnapshot, refreshModels and dispose.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// modelSelectorRefreshTimeout is model-selector.ts:185 `timeoutMs`: the catalog refresh a selector starts is aborted after it.
// upstream: packages/coding-agent/src/modes/interactive/components/model-selector.ts:timeoutMs
var modelSelectorRefreshTimeout = 15 * time.Second

// ModelSelectorRuntime is the consumer-owned view of Pi's ModelRuntime that the selector reads (model-selector.ts:162, 169, 202 and refreshModelCatalogs'
// `modelRuntime.refresh({ signal })`): the available snapshot, one model by identity (nil when unknown), the runtime's load error, and the catalog
// refresh. Package tui cannot import coding, which owns ModelRuntime, so the interface lists exactly those four members; coding.ModelRuntime
// implements it (compile-time assertion in coding/model_runtime_selector.go).
type ModelSelectorRuntime interface {
	GetAvailableSnapshot() []*ai.Model
	GetModel(provider, id string) *ai.Model
	GetError() string
	Refresh(ctx context.Context, options ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult
}

// ScopedModelItem is one scoped model of a session (model-selector.ts ScopedModelItem).
type ScopedModelItem struct {
	Model         *ai.Model
	ThinkingLevel string
}

// DefaultModelReference names the default model (model-selector.ts DefaultModelReference).
type DefaultModelReference struct {
	Provider string
	ID       string
}

// modelSelectorCallbacks are the Pi constructor's callbacks and the models they take, by provider-qualified spec.
type modelSelectorCallbacks struct {
	models            map[string]*ai.Model
	onSelect          func(*ai.Model)
	onSelectAsDefault func(*ai.Model)
	onCancel          func()
}

func (c *modelSelectorCallbacks) selected(fq string, asDefault bool) {
	if c == nil {
		return
	}
	model := c.models[fq]
	switch {
	case asDefault && c.onSelectAsDefault != nil:
		c.onSelectAsDefault(model)
	case !asDefault && c.onSelect != nil:
		c.onSelect(model)
	}
}

func (c *modelSelectorCallbacks) cancel() {
	if c != nil && c.onCancel != nil {
		c.onCancel()
	}
}

func modelSelectorItemOf(model *ai.Model) ModelSelectorItem {
	return ModelSelectorItem{Provider: model.ProviderID(), ID: model.ModelID(), Name: model.DisplayName}
}

// NewModelSelectorComponent is model-selector.ts's constructor: it builds the picker, loads the runtime's available models and the scoped
// models' current metadata, asks the TUI to render, and starts the catalog refresh in the background (15 seconds at most). The refresh's outcome
// reaches the picker on the TUI's owner loop (PostToOwner); Dispose stops it. onSelect, onCancel and onSelectAsDefault run on the owner loop when
// the user decides; without onSelectAsDefault the picker has no "set as default" action. initialSearch starts the search input filled.
func NewModelSelectorComponent(ui TUI, current *ai.Model, runtime ModelSelectorRuntime, scoped []ScopedModelItem, onSelect func(*ai.Model), onCancel func(), initialSearch string, onSelectAsDefault func(*ai.Model), defaultModel *DefaultModelReference) *ModelSelectorComponent {
	callbacks := &modelSelectorCallbacks{models: map[string]*ai.Model{}, onSelect: onSelect, onSelectAsDefault: onSelectAsDefault, onCancel: onCancel}
	items := func() []ModelSelectorItem {
		var items []ModelSelectorItem
		for _, model := range runtime.GetAvailableSnapshot() {
			item := modelSelectorItemOf(model)
			callbacks.models[item.FQ()] = model
			items = append(items, item)
		}
		return items
	}
	var scopedItems []ModelSelectorItem
	for _, entry := range scoped {
		model := entry.Model
		if refreshed := runtime.GetModel(model.ProviderID(), model.ModelID()); refreshed != nil {
			model = refreshed
		}
		item := modelSelectorItemOf(model)
		callbacks.models[item.FQ()] = model
		scopedItems = append(scopedItems, item)
	}
	currentSpec := ""
	if current != nil {
		currentSpec = modelSelectorItemOf(current).FQ()
	}
	snapshot := items()
	ms := NewStaticModelSelectorComponent("Select model", scopedItems, snapshot, currentSpec)
	if onSelectAsDefault == nil {
		// Without onSelectAsDefault the picker has no "set as default" action and no hint for it (model-selector.ts:139, 413).
		ms.noSaveDefault = true
		ms.Remove(ms.saveHint)
	}
	ms.callbacks = callbacks
	if defaultModel != nil {
		ms.SetDefaultModel(defaultModel.Provider + "/" + defaultModel.ID)
	}
	ms.SetFilter(initialSearch)
	ms.SetStatus("Refreshing model catalogs…")
	ui.RequestRender()

	// life ends when the selector is disposed; the refresh also ends at its timeout, and the outcome of a timed-out refresh is still
	// delivered, so the post waits on life, not on the refresh's own context (model-selector.ts refreshModels: AbortController + timeout).
	life, cancel := context.WithCancel(context.Background())
	ctx, cancelTimeout := context.WithTimeout(life, modelSelectorRefreshTimeout)
	ms.SetRefreshCancel(cancel)
	go func() {
		defer cancel()
		defer cancelTimeout()
		// refreshModelCatalogs races the refresh with the selector's signal (model-catalog-refresh.ts raceWithAbortSignal): once the timeout or
		// Dispose aborts the signal, the call rejects, whatever the refresh settles with later. A refresh that stopped because the signal
		// aborted is the same rejection.
		settled := make(chan ai.ModelsRefreshResult, 1)
		go func() { settled <- runtime.Refresh(ctx) }()
		var result ai.ModelsRefreshResult
		rejected := false
		select {
		case result = <-settled:
			rejected = result.Aborted && ctx.Err() != nil
		case <-ctx.Done():
			rejected = true
		}
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
		_ = ui.PostToOwner(life, func() {
			if ms.closed {
				return
			}
			if rejected {
				ms.rejectCatalogRefresh(timedOut)
			} else {
				ms.applyCatalogRefresh(runtime, items, result, timedOut)
			}
			ui.RequestRender()
		})
	}()
	return ms
}

// rejectCatalogRefresh is the catch of model-selector.ts refreshModels (:207-215) for the timeout: the refresh status is cleared, the timeout
// message shown and the list redrawn (updateList) without reloading the snapshot, so models the aborted refresh added stay hidden. The only other
// rejection is the selector's own disposal, which never reaches here.
func (m *ModelSelectorComponent) rejectCatalogRefresh(timedOut bool) {
	if timedOut {
		m.SetError("Model refresh timed out; showing cached models.")
	}
}

// applyCatalogRefresh is the end of model-selector.ts refreshModels (:184-206) for a refresh that settled before the signal aborted: the refresh
// status or error, then the snapshot reloaded with the query kept. timedOut is Pi's flag set by the 15 second timer; Pi's `result.aborted && timedOut`
// branch stays as written, although a timed-out refresh rejects (rejectCatalogRefresh).
func (m *ModelSelectorComponent) applyCatalogRefresh(runtime ModelSelectorRuntime, items func() []ModelSelectorItem, result ai.ModelsRefreshResult, timedOut bool) {
	failed := result.ErrorOrder
	if len(failed) != len(result.Errors) {
		failed = slices.Sorted(maps.Keys(result.Errors))
	}
	switch {
	case result.Aborted && timedOut:
		m.SetError("Model refresh timed out; showing cached models.")
	case len(failed) == 1:
		m.SetError("Could not refresh " + failed[0] + "; showing cached models.")
	case len(failed) > 1:
		m.SetError(fmt.Sprintf("Could not refresh %d model catalogs (%s); showing cached models.", len(failed), strings.Join(failed, ", ")))
	default:
		if message := runtime.GetError(); message != "" {
			m.SetError(message)
		} else {
			m.SetRefreshSuccess("Model catalogs refreshed.")
		}
	}
	m.UpdateModels(items())
}
