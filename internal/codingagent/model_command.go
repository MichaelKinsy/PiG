package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.

import (
	"context"
	"errors"
	"strings"
	"time"
)

// findExactModelMatch reads the cached scope on the owner loop. Only an unscoped miss starts owned refresh work; its completion returns to the owner loop before accessing UI state.
func (m *InteractiveMode) findExactModelMatch(ctx context.Context, searchTerm string, complete func(string, bool)) {
	cached, ok := m.resolveAvailableModel(searchTerm)
	if ok || len(m.scopedModelIDs) > 0 {
		complete(cached, ok)
		return
	}
	m.showStatus("Refreshing model catalogs…")
	ownerCtx, cancelOwner := context.WithCancel(ctx)
	stop := func() bool { return false }
	backgroundCtx := m.backgroundCtx
	if backgroundCtx != nil {
		stop = context.AfterFunc(backgroundCtx, cancelOwner)
	}
	// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:findExactModelMatch
	refreshCtx, cancel := context.WithTimeout(ownerCtx, 15*time.Second)
	registry := m.opts.ModelRegistry
	m.backgroundTasks.Go(func() {
		defer cancelOwner()
		defer cancel()
		defer stop()
		result, err := RefreshModelCatalogs(refreshCtx, registry)
		warning := ""
		switch {
		case errors.Is(refreshCtx.Err(), context.DeadlineExceeded):
			warning = "Model refresh timed out; searching cached models."
		case err != nil:
			warning = "Could not refresh model catalogs: " + err.Error()
		case len(result.Errors) > 0:
			warning = "Could not refresh " + strings.Join(result.failedProviders(), ", ") + "; searching cached models."
		}
		if ownerCtx.Err() != nil {
			return
		}
		// Cancellation makes a rejected completion irrelevant; accepted work checks ownership again before mutation.
		_ = m.postToMain(ownerCtx, func() {
			if ctx.Err() != nil || (backgroundCtx != nil && backgroundCtx.Err() != nil) {
				return
			}
			if warning != "" {
				m.showWarning(warning)
			}
			match, found := resolveModelFromItems(searchTerm, m.availableModelItems())
			complete(match, found)
		})
	})
}
