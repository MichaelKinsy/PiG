package main

// Startup model selection. Mirrors upstream main.ts buildSessionOptions (CLI
// model, then the --models / enabledModels scope for a new session) and
// sdk.ts createAgentSession's findInitialModel fallback (saved default with
// auth, then a known provider default, then the first available model) from
// packages/coding-agent/src/core/model-resolver.ts.

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// startupModelRuntime is the ModelRuntime surface startup model selection
// reads: the composed model list and per-provider configured auth.
type startupModelRuntime struct {
	models  []codingagent.RuntimeModel
	hasAuth func(providerID string) bool
	auth    map[string]bool
}

func newStartupModelRuntime(models []codingagent.RuntimeModel, hasAuth func(providerID string) bool) *startupModelRuntime {
	return &startupModelRuntime{models: models, hasAuth: hasAuth, auth: map[string]bool{}}
}

// GetModels returns every composed model, or one provider's.
func (rt *startupModelRuntime) GetModels(providerID string) []codingagent.RuntimeModel {
	if providerID == "" {
		return slices.Clone(rt.models)
	}
	var models []codingagent.RuntimeModel
	for _, model := range rt.models {
		if model.Provider == providerID {
			models = append(models, model)
		}
	}
	return models
}

// HasConfiguredAuth reports the provider's configured auth, read once.
func (rt *startupModelRuntime) HasConfiguredAuth(providerID string) bool {
	configured, ok := rt.auth[providerID]
	if !ok {
		configured = rt.hasAuth(providerID)
		rt.auth[providerID] = configured
	}
	return configured
}

// getModel mirrors upstream ModelRuntime.getModel: an exact composed model.
func (rt *startupModelRuntime) getModel(providerID, modelID string) *codingagent.RuntimeModel {
	index := slices.IndexFunc(rt.models, func(model codingagent.RuntimeModel) bool {
		return model.Provider == providerID && model.ID == modelID
	})
	if index < 0 {
		return nil
	}
	return &rt.models[index]
}

// getAvailable mirrors upstream ModelRuntime.getAvailableSnapshot: the
// composed models whose provider has configured auth.
func (rt *startupModelRuntime) getAvailable() []codingagent.RuntimeModel {
	var available []codingagent.RuntimeModel
	for _, model := range rt.models {
		if rt.HasConfiguredAuth(model.Provider) {
			available = append(available, model)
		}
	}
	return available
}

// GetModel returns an exact composed model without reading authentication.
func (rt *startupModelRuntime) GetModel(providerID, modelID string) *codingagent.RuntimeModel {
	return rt.getModel(providerID, modelID)
}

// GetAvailableSnapshot returns the existing startup adapter's available-model observation.
func (rt *startupModelRuntime) GetAvailableSnapshot() []codingagent.RuntimeModel {
	return rt.getAvailable()
}

// ScopedModel is the shared scope entry used by startup and interactive selection.
type ScopedModel = codingagent.ScopedModel

func resolveModelScopeFromModels(patterns []string, availableModels []codingagent.RuntimeModel) ([]ScopedModel, []string) {
	result := codingagent.ResolveModelScopeFromModels(patterns, availableModels)
	var warnings []string
	for _, diagnostic := range result.Diagnostics {
		warnings = append(warnings, diagnostic.Message)
	}
	return result.ScopedModels, warnings
}

// findInitialModel delegates startup's saved-default and available-model selection to the shared resolver. Session construction still owns startup thinking selection.
func findInitialModel(rt *startupModelRuntime, defaultProvider, defaultModelID string) (*codingagent.RuntimeModel, error) {
	result, err := codingagent.FindInitialModel(codingagent.FindInitialModelOptions{
		ModelRuntime: rt, IsContinuing: true, DefaultProvider: defaultProvider, DefaultModelId: defaultModelID,
	})
	return result.Model, err
}

// startupModelOptions carries the CLI and session inputs startup model
// selection reads.
type startupModelOptions struct {
	CLIProvider string
	CLIModel    string
	CLIThinking string
	// ScopePatterns are --models, else the enabledModels setting.
	ScopePatterns []string
	// Continuing reports a resumed, continued or forked session, whose saved
	// model takes precedence over the scope.
	Continuing bool
	// SessionManager is the already-opened Session used for restoration before default-model fallback.
	SessionManager *coding.SessionManager
	// APIKey is --api-key: a non-persistent key for the CLI or scope model's
	// provider.
	APIKey string
}

// startupModel keeps immediate scope warnings separate from deferred model warnings and errors. Selection and fallback continue after an explicit-model diagnostic so metadata can still use the runtime.
type startupModel struct {
	Model                *ai.Model
	Thinking             string
	Warnings             []string
	ScopeWarnings        []string
	Errors               []error
	ModelFallbackMessage string
}

// selectStartupModel resolves explicit selection, then the loaded Session's model, then configured/provider defaults. It retains ordered errors in the result and returns their aggregate. A nil Model with nil error means no authenticated model is available.
func selectStartupModel(ctx context.Context, options startupModelOptions, settings codingagent.Settings, services *coding.Services) (startupModel, error) {
	registry := services.Registry().ModelRegistry
	// upstream: main.ts:803-819 resolves against modelRuntime, whose catalog and auth include the virtual models the services registered (agent-session-services.ts:182-193).
	modelRuntime := services.ModelRuntime()
	rt := newStartupModelRuntime(modelRuntime.RuntimeModels(), modelRuntime.HasConfiguredAuth)
	var result startupModel
	selected, err := selectSessionOptionModel(rt, options, settings, &result)
	if err != nil {
		result.Errors = append(result.Errors, err)
	}
	if options.APIKey != "" {
		if selected == nil {
			result.Errors = append(result.Errors, errors.New("--api-key requires a model to be specified via --model, --provider/--model, or --models"))
		} else {
			registry.SetRuntimeAPIKey(selected.Provider, options.APIKey)
		}
	}
	if selected == nil && options.SessionManager != nil {
		// sdk.ts:198-224 restores an authenticated saved model before consulting defaults.
		existing := codingagent.BuildSessionContext(options.SessionManager.GetBranch())
		if len(existing.Messages) > 0 && existing.Model != nil {
			if rt.HasConfiguredAuth(existing.Model.Provider) {
				selected = rt.getModel(existing.Model.Provider, existing.Model.ModelID)
			}
			if selected == nil {
				result.ModelFallbackMessage = "Could not restore model " + existing.Model.Provider + "/" + existing.Model.ModelID
			}
		}
	}
	if selected == nil {
		selected, err = findInitialModel(rt, settings.DefaultProvider, settings.DefaultModel)
		if err != nil {
			return result, err
		}
		if selected == nil {
			result.ModelFallbackMessage = codingagent.FormatNoModelsAvailableMessage()
			return result, errors.Join(result.Errors...)
		}
		if result.ModelFallbackMessage != "" {
			result.ModelFallbackMessage += ". Using " + selected.Provider + "/" + selected.ID
		}
	}
	result.Model, err = buildModelFromRef(ctx, selected.Provider, selected.ID, services)
	// A virtual model is the runtime's shared catalog entry and carries its reasoning from its definition (virtual-models.ts:159-176 createVirtualModel), so startup does not rewrite it.
	if err == nil && selected.Reasoning && result.Model != nil && !coding.IsVirtualModel(result.Model) {
		result.Model.ProviderMeta.Reasoning = true
		if result.Model.Capabilities.MaxThinking == "" {
			result.Model.Capabilities.MaxThinking = ai.ThinkingHigh
		}
	}
	if err != nil {
		result.Errors = append(result.Errors, err)
	}
	return result, errors.Join(result.Errors...)
}

// selectSessionOptionModel mirrors upstream main.ts buildSessionOptions: the
// --model resolution, else the scope's saved default or first entry for a new
// session. It records the thinking level and warnings in result.
func selectSessionOptionModel(rt *startupModelRuntime, options startupModelOptions, settings codingagent.Settings, result *startupModel) (*codingagent.RuntimeModel, error) {
	// Upstream resolves the scope before explicit model selection, including metadata and continuing sessions.
	var scoped []ScopedModel
	if len(options.ScopePatterns) > 0 {
		scoped, result.ScopeWarnings = resolveModelScopeFromModels(options.ScopePatterns, rt.getAvailable())
	}
	if model, thinking, ok := testFauxCLIModel(options); ok {
		result.Thinking = thinking
		return model, nil
	}
	var cliErr error
	if options.CLIModel != "" {
		resolved := ResolveCliModel(options.CLIProvider, options.CLIModel, options.CLIThinking, rt)
		if resolved.Warning != "" {
			result.Warnings = append(result.Warnings, resolved.Warning)
		}
		if resolved.Error != "" {
			cliErr = errors.New(resolved.Error)
		}
		if resolved.Model != nil {
			result.Thinking = resolved.ThinkingLevel
			return resolved.Model, nil
		}
	}
	if len(scoped) == 0 || options.Continuing {
		return nil, cliErr
	}
	chosen := scoped[0]
	if saved := rt.getModel(settings.DefaultProvider, settings.DefaultModel); saved != nil {
		if index := slices.IndexFunc(scoped, func(entry ScopedModel) bool { return modelsAreEqual(entry.Model, *saved) }); index >= 0 {
			chosen = scoped[index]
		}
	}
	result.Thinking = chosen.ThinkingLevel
	return &chosen.Model, cliErr
}

// testFauxCLIModel selects the test-only test-faux provider, which has no
// catalog entry, for a --model naming it. It exists only when PIG_TEST_FAUX=1,
// so normal runs resolve such a --model like any unknown model.
func testFauxCLIModel(options startupModelOptions) (*codingagent.RuntimeModel, string, bool) {
	if os.Getenv("PIG_TEST_FAUX") != "1" {
		return nil, "", false
	}
	spec := options.CLIModel
	if options.CLIProvider == "test-faux" && !strings.HasPrefix(spec, "test-faux/") {
		spec = "test-faux/" + spec
	}
	modelID, ok := strings.CutPrefix(spec, "test-faux/")
	if !ok || modelID == "" {
		return nil, "", false
	}
	thinking := ""
	if colon := strings.LastIndex(modelID, ":"); colon != -1 && validThinkingLevels[modelID[colon+1:]] {
		modelID, thinking = modelID[:colon], modelID[colon+1:]
	}
	return &codingagent.RuntimeModel{Provider: "test-faux", ID: modelID, Name: "Test Faux"}, thinking, true
}
