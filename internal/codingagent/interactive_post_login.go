package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// isUnknownModel includes the nil model used by PiG for Pi's initial unknown/unknown/unknown sentinel.
func isUnknownModel(model *ai.Model) bool {
	return model == nil || (model.ID == "unknown" && model.ProviderMeta.ProviderID == "unknown" && model.ProviderMeta.API == "unknown")
}

// completeProviderAuthentication completes local selection before starting the bounded catalog refresh. Deferred selection never replaces a model or session chosen during that refresh.
func (m *InteractiveMode) completeProviderAuthentication(providerID, providerName string, authType ai.CredentialType, previousModel *ai.Model) {
	actionLabel := "Logged in to " + providerName
	if authType == ai.CredentialAPIKey {
		actionLabel = "Saved API key for " + providerName
	}
	defaultID, hasDefault := DefaultModelPerProvider()[providerID]
	deferSelection := isUnknownModel(previousModel) && hasDefault && !slices.ContainsFunc(m.availableModelItems(), func(model tui.ModelSelectorItem) bool {
		return model.Provider == providerID && model.ID == defaultID
	})
	session, handle := m.currentSession(), m.opts.SessionHandle
	refresh := func() {
		ctx := m.backgroundCtx
		if ctx == nil {
			ctx = m.runCtx
		}
		ownerCtx := ctx
		if ctx == nil {
			ctx = context.Background()
		}
		registry, llamaHost := m.opts.ModelRegistry, m.opts.Llama
		m.backgroundTasks.Go(func() {
			// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:completeProviderAuthentication
			refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			result := CatalogRefreshResult{}
			if registry != nil {
				result = registry.RefreshCatalogs(refreshCtx, CatalogRefreshOptions{AllowNetwork: ModelNetworkEnabled(), Providers: []string{providerID}})
			}
			if llamaHost != nil && llamaHost.Provider().ID == providerID {
				llamaResult := llamaHost.Refresh(refreshCtx, true)
				result.Aborted = result.Aborted || llamaResult.Aborted
				if llamaResult.Err != nil {
					result.Errors = map[string]error{providerID: llamaResult.Err}
				}
			}
			m.runOnMain(ownerCtx, func() {
				if ctx.Err() != nil {
					return
				}
				if warning := catalogRefreshWarning(actionLabel, result); warning != "" {
					m.showWarning(warning)
				}
				if deferSelection && m.currentSession() == session && m.opts.SessionHandle == handle && m.opts.Model == previousModel {
					m.finishProviderAuthentication(providerID, actionLabel, previousModel, nil)
				}
				m.updateProviderInfo()
				if m.tuiInst != nil {
					m.tuiInst.RequestRender()
				}
			})
		})
	}
	if deferSelection {
		m.showStatus(fmt.Sprintf("%s. Credentials saved to %s. Refreshing model catalog…", actionLabel, filepath.Join(m.opts.AgentDir, "auth.json")))
		refresh()
	} else {
		m.finishProviderAuthentication(providerID, actionLabel, previousModel, refresh)
	}
}

// postLoginModel selects only within the authenticated provider, preserving catalog order for Radius accounts without balanced.
func postLoginModel(providerID, actionLabel string, models []tui.ModelSelectorItem) (string, string) {
	providerModels := slices.DeleteFunc(slices.Clone(models), func(model tui.ModelSelectorItem) bool { return model.Provider != providerID })
	defaultID, hasDefault := DefaultModelPerProvider()[providerID]
	switch {
	case providerID == "llama.cpp":
		return "", llamaCppPostLoginGuidance(actionLabel, len(providerModels))
	case !hasDefault:
		return "", fmt.Sprintf(`%s, but no default model is configured for provider "%s". Use /model to select a model.`, actionLabel, providerID)
	case len(providerModels) == 0:
		return "", actionLabel + ", but no models are available for that provider. Use /model to select a model."
	}
	for _, model := range providerModels {
		if model.ID == defaultID {
			return model.ID, ""
		}
	}
	if providerID == "radius" {
		return providerModels[0].ID, ""
	}
	return "", fmt.Sprintf(`%s, but its default model "%s" is not available. Use /model to select a model.`, actionLabel, defaultID)
}

func (m *InteractiveMode) finishProviderAuthentication(providerID, actionLabel string, previousModel *ai.Model, after func()) {
	var modelID, selectionError string
	if isUnknownModel(previousModel) {
		modelID, selectionError = postLoginModel(providerID, actionLabel, m.availableModelItems())
	}
	finish := func(selected *ai.Model, err error) {
		if err != nil {
			selectionError = fmt.Sprintf("%s, but selecting its default model failed: %s. Use /model to select a model.", actionLabel, err)
			selected = nil
		}
		m.updateProviderInfo()
		status := actionLabel + "."
		if selected != nil {
			status += " Selected " + selected.ID + "."
		}
		m.showStatus(status + " Credentials saved to " + filepath.Join(m.opts.AgentDir, "auth.json"))
		if selectionError != "" {
			m.showError(selectionError)
		} else {
			m.maybeWarnAboutAnthropicSubscriptionAuthAsync()
		}
		if after != nil {
			after()
		}
	}
	if modelID == "" {
		finish(nil, nil)
		return
	}

	// Building a model can resolve credentials, and SetModel awaits extension handlers. Both belong off the input/render loop; UI mutation returns to that loop in order.
	ctx := m.backgroundCtx
	if ctx == nil {
		ctx = m.runCtx
	}
	builder, handle, agent := m.opts.ModelBuilder, m.opts.SessionHandle, m.agent
	m.backgroundTasks.Go(func() {
		var model *ai.Model
		var err error
		if builder == nil {
			err = fmt.Errorf("model switching is not configured (no ModelBuilder)")
		} else {
			model, err = builder(providerID + "/" + modelID)
		}
		if err == nil {
			if handle != nil {
				err = handle.SetModel(model, ModelMutationOptions{Persist: true})
			} else if agent != nil {
				agent.SetModel(model)
			}
		}
		m.runOnMain(ctx, func() {
			if ctx != nil && ctx.Err() != nil {
				return
			}
			if err == nil {
				m.opts.Model = model
				if m.statusLine != nil {
					m.statusLine.SetModel(model)
				}
				m.refreshThinkingLevel()
				if handle != nil {
					err = m.addPersistedDefaultToNonEmptyScope(model)
				} else {
					err = m.persistDefaultModel(model)
				}
			}
			finish(model, err)
		})
	})
}
