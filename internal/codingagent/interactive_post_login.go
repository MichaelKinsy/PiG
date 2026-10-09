package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts

import (
	"context"
	"errors"
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

// invalidatePostLoginSelection invalidates pending authentication selection when an owner-loop model or Session command starts, including commands that retain the same model pointer.
func (m *InteractiveMode) invalidatePostLoginSelection() { m.modelSelectionGeneration++ }

// synchronizeLoginCredential recomposes the provider after a login stored its credential, as upstream ModelRuntime.login does before the login returns. A failure is shown here as upstream's login catch blocks show it, and the caller skips completing the authentication.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:showApiKeyLoginDialog, showLoginDialog
func (m *InteractiveMode) synchronizeLoginCredential(ctx context.Context, providerID, providerName string, authType ai.CredentialType, redact func(string) string) (failed bool) {
	runtime := m.opts.RequestAuthRuntime
	if runtime == nil {
		return false
	}
	err := runtime.SynchronizeCredentialState(ctx, providerID, CredentialSynchronizationLogin, nil)
	if err == nil {
		return false
	}
	if redact == nil {
		redact = func(text string) string { return text }
	}
	actionLabel := "Logged in to " + providerName
	if authType == ai.CredentialAPIKey {
		actionLabel = "Saved API key for " + providerName
	}
	m.showError(redact(actionLabel + ", but local model state could not be synchronized: " + err.Error()))
	return true
}

// completeProviderAuthentication completes local selection before starting the bounded catalog refresh. Deferred selection never replaces a model or session chosen during that refresh. authPath is the credential store's reported location; an empty path uses the ordinary auth.json store.
func (m *InteractiveMode) completeProviderAuthentication(providerID, providerName string, authType ai.CredentialType, previousModel *ai.Model, authPath string, redact func(string) string) {
	if redact == nil {
		redact = func(text string) string { return text }
	}
	m.invalidatePostLoginSelection()
	generation := m.modelSelectionGeneration
	if authPath == "" {
		authPath = filepath.Join(m.opts.AgentDir, "auth.json")
	}
	actionLabel := "Logged in to " + providerName
	if authType == ai.CredentialAPIKey {
		actionLabel = "Saved API key for " + providerName
	}
	if m.opts.DefaultModelPerProvider == nil {
		m.opts.DefaultModelPerProvider = DefaultModelPerProvider()
	}
	defaultID, hasDefault := m.opts.DefaultModelPerProvider[providerID]
	deferSelection := isUnknownModel(previousModel) && hasDefault && !slices.ContainsFunc(m.availableModelItems(), func(model tui.ModelSelectorItem) bool {
		return model.Provider == providerID && model.ID == defaultID
	})
	session, handle := m.currentSession(), m.opts.SessionHandle
	registry := m.opts.ModelRegistry
	refresh := func() {
		ctx := m.backgroundCtx
		if ctx == nil {
			ctx = m.runCtx
		}
		ownerCtx := ctx
		if ctx == nil {
			ctx = context.Background()
		}
		m.backgroundTasks.Go(func() {
			// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:completeProviderAuthentication
			refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			result := CatalogRefreshResult{}
			if registry != nil {
				result = registry.RefreshCatalogs(refreshCtx, CatalogRefreshOptions{AllowNetwork: ModelNetworkEnabled(), Providers: []string{providerID}})
			}
			m.runOnMain(ownerCtx, func() {
				if ctx.Err() != nil {
					return
				}
				if warning := catalogRefreshWarning(actionLabel, result); warning != "" {
					m.showWarning(redact(warning))
				}
				if deferSelection && m.modelSelectionGeneration == generation && m.currentSession() == session && m.opts.SessionHandle == handle && m.opts.Model == previousModel {
					m.finishProviderAuthentication(providerID, actionLabel, previousModel, authPath, redact, nil)
				}
				m.updateProviderInfo()
				if m.tuiInst != nil {
					m.tuiInst.RequestRender()
				}
			})
		})
	}
	if deferSelection {
		m.showStatus(redact(fmt.Sprintf("%s. Credentials saved to %s. Refreshing model catalog…", actionLabel, authPath)))
		refresh()
	} else {
		m.finishProviderAuthentication(providerID, actionLabel, previousModel, authPath, redact, refresh)
	}
}

// postLoginModel selects only within the authenticated provider, preserving catalog order for Radius accounts without balanced.
func postLoginModel(providerID, actionLabel, defaultID string, models []tui.ModelSelectorItem) (string, string) {
	providerModels := slices.DeleteFunc(slices.Clone(models), func(model tui.ModelSelectorItem) bool { return model.Provider != providerID })
	hasDefault := defaultID != ""
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

var errPostLoginSelectionSuperseded = errors.New("post-login model selection superseded")

func (m *InteractiveMode) finishProviderAuthentication(providerID, actionLabel string, previousModel *ai.Model, authPath string, redact func(string) string, after func()) {
	var modelID, selectionError string
	if isUnknownModel(previousModel) {
		modelID, selectionError = postLoginModel(providerID, actionLabel, m.opts.DefaultModelPerProvider[providerID], m.availableModelItems())
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
		m.showStatus(redact(status + " Credentials saved to " + authPath))
		if selectionError != "" {
			m.showError(redact(selectionError))
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

	ctx := m.backgroundCtx
	if ctx == nil {
		ctx = m.runCtx
	}
	generation, session := m.modelSelectionGeneration, m.currentSession()
	builder, handle, agent := m.opts.ModelBuilder, m.opts.SessionHandle, m.agent
	expected := previousModel
	// Only the owner loop reads these identities. Model construction and extension notifications may wait, but the checked state commit cannot interleave with another owner-loop command.
	isCurrent := func() bool {
		return m.modelSelectionGeneration == generation && m.currentSession() == session && m.opts.SessionHandle == handle && m.opts.Model == expected
	}
	m.backgroundTasks.Go(func() {
		var model *ai.Model
		var err error
		if builder == nil {
			err = fmt.Errorf("model switching is not configured (no ModelBuilder)")
		} else {
			model, err = builder(providerID + "/" + modelID)
		}
		if err == nil {
			apply := func(mutate func() error) error {
				result := make(chan error, 1)
				if postErr := m.postToMain(ctx, func() {
					if ctx != nil && ctx.Err() != nil {
						result <- ctx.Err()
						return
					}
					if !isCurrent() {
						result <- errPostLoginSelectionSuperseded
						return
					}
					if mutationErr := mutate(); mutationErr != nil {
						result <- mutationErr
						return
					}
					if !isCurrent() {
						result <- errPostLoginSelectionSuperseded
						return
					}
					m.opts.Model = model
					expected = model
					if m.statusLine != nil {
						m.statusLine.SetModel(model)
					}
					m.refreshThinkingLevel()
					if handle != nil {
						m.initScopedModels()
						result <- nil
					} else {
						result <- m.persistDefaultModel(model)
					}
				}); postErr != nil {
					return postErr
				}
				if ctx == nil {
					return <-result
				}
				select {
				case applyErr := <-result:
					return applyErr
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if handle != nil {
				err = handle.SetModelOnMain(model, ModelMutationOptions{Persist: true}, apply)
			} else {
				err = apply(func() error {
					if agent != nil {
						agent.SetModel(model)
					}
					return nil
				})
			}
		}
		m.runOnMain(ctx, func() {
			if ctx != nil && ctx.Err() != nil {
				return
			}
			if errors.Is(err, errPostLoginSelectionSuperseded) || !isCurrent() {
				if after != nil {
					after()
				}
				return
			}
			finish(model, err)
		})
	})
}

// llamaCppPostLoginGuidance mirrors interactive-mode.ts llamaCppPostLoginGuidance.
func llamaCppPostLoginGuidance(actionLabel string, loadedModelCount int) string {
	if loadedModelCount == 0 {
		return actionLabel + ". No llama.cpp models are loaded. Use /llama to load a model, then /model to select it."
	}
	return actionLabel + ". Use /model to select a loaded llama.cpp model, or /llama to manage models."
}
