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
	"path"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
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

// ScopedModel mirrors upstream ScopedModel: a model and the thinking level its
// pattern named, if any.
type ScopedModel struct {
	Model         codingagent.RuntimeModel
	ThinkingLevel string
}

// resolveModelScopeFromModels mirrors upstream resolveModelScopeFromModels. It
// returns the scoped models and the warnings upstream prints for patterns
// that match nothing or carry an invalid thinking level.
func resolveModelScopeFromModels(patterns []string, availableModels []codingagent.RuntimeModel) ([]ScopedModel, []string) {
	var scoped []ScopedModel
	var warnings []string
	add := func(model codingagent.RuntimeModel, thinkingLevel string) {
		if !slices.ContainsFunc(scoped, func(existing ScopedModel) bool { return modelsAreEqual(existing.Model, model) }) {
			scoped = append(scoped, ScopedModel{Model: model, ThinkingLevel: thinkingLevel})
		}
	}
	for _, pattern := range patterns {
		if strings.ContainsAny(pattern, "*?[") {
			globPattern, thinkingLevel := pattern, ""
			if colon := strings.LastIndex(pattern, ":"); colon != -1 && validThinkingLevels[pattern[colon+1:]] {
				globPattern, thinkingLevel = pattern[:colon], pattern[colon+1:]
			}
			if exact := FindExactModelReferenceMatch(globPattern, availableModels); exact != nil {
				add(*exact, thinkingLevel)
				continue
			}
			matched := false
			for _, model := range availableModels {
				if globMatchFold(globPattern, modelRef(model)) || globMatchFold(globPattern, model.ID) {
					add(model, thinkingLevel)
					matched = true
				}
			}
			if !matched {
				warnings = append(warnings, `No models match pattern "`+pattern+`"`)
			}
			continue
		}
		parsed := ParseModelPattern(pattern, availableModels, true)
		if parsed.Warning != "" {
			warnings = append(warnings, parsed.Warning)
		}
		if parsed.Model == nil {
			warnings = append(warnings, `No models match pattern "`+pattern+`"`)
			continue
		}
		add(*parsed.Model, parsed.ThinkingLevel)
	}
	return scoped, warnings
}

// globMatchFold matches a minimatch-style glob case-insensitively.
func globMatchFold(pattern, name string) bool {
	matched, err := path.Match(strings.ToLower(pattern), strings.ToLower(name))
	return err == nil && matched
}

// findInitialModel mirrors upstream findInitialModel's saved-default and
// available-model steps: the saved default when its provider has auth, then
// the first available model that is a known provider's default, then the
// first available model.
func findInitialModel(rt *startupModelRuntime, defaultProvider, defaultModelID string) *codingagent.RuntimeModel {
	if defaultProvider != "" && defaultModelID != "" {
		if found := rt.getModel(defaultProvider, defaultModelID); found != nil && rt.HasConfiguredAuth(found.Provider) {
			return found
		}
	}
	available := rt.getAvailable()
	if len(available) == 0 {
		return nil
	}
	for _, entry := range defaultModelPerProviderOrder {
		index := slices.IndexFunc(available, func(model codingagent.RuntimeModel) bool {
			return model.Provider == entry.provider && model.ID == entry.modelID
		})
		if index >= 0 {
			return &available[index]
		}
	}
	return &available[0]
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
	// APIKey is --api-key: a non-persistent key for the CLI or scope model's
	// provider.
	APIKey string
}

// startupModel is the selected model, the thinking level its CLI pattern or
// scope entry named, and the warnings to report.
type startupModel struct {
	Model    *ai.Model
	Thinking string
	Warnings []string
}

// selectStartupModel picks the session's starting model the way upstream
// main.ts and createAgentSession do. A nil Model with a nil error means no
// model is available.
func selectStartupModel(ctx context.Context, options startupModelOptions, settings codingagent.Settings, registry *codingagent.ModelRegistry) (startupModel, error) {
	rt := newStartupModelRuntime(registry.RuntimeModels(), registry.HasConfiguredAuth)
	var result startupModel
	selected, err := selectSessionOptionModel(rt, options, settings, &result)
	if err != nil {
		return result, err
	}
	if options.APIKey != "" {
		if selected == nil {
			return result, errors.New("--api-key requires a model to be specified via --model, --provider/--model, or --models")
		}
		registry.SetRuntimeAPIKey(selected.Provider, options.APIKey)
	}
	if selected == nil {
		if selected = findInitialModel(rt, settings.DefaultProvider, settings.DefaultModel); selected == nil {
			return result, nil
		}
	}
	result.Model, _, _, err = buildModelFromRef(ctx, selected.Provider, selected.ID, registry)
	return result, err
}

// selectSessionOptionModel mirrors upstream main.ts buildSessionOptions: the
// --model resolution, else the scope's saved default or first entry for a new
// session. It records the thinking level and warnings in result.
func selectSessionOptionModel(rt *startupModelRuntime, options startupModelOptions, settings codingagent.Settings, result *startupModel) (*codingagent.RuntimeModel, error) {
	if model, thinking, ok := testFauxCLIModel(options); ok {
		result.Thinking = thinking
		return model, nil
	}
	if options.CLIModel != "" {
		resolved := ResolveCliModel(options.CLIProvider, options.CLIModel, options.CLIThinking, rt)
		if resolved.Warning != "" {
			result.Warnings = append(result.Warnings, resolved.Warning)
		}
		if resolved.Error != "" {
			return nil, errors.New(resolved.Error)
		}
		if resolved.Model != nil {
			result.Thinking = resolved.ThinkingLevel
			return resolved.Model, nil
		}
	}
	if len(options.ScopePatterns) == 0 || options.Continuing {
		return nil, nil
	}
	scoped, warnings := resolveModelScopeFromModels(options.ScopePatterns, rt.getAvailable())
	result.Warnings = append(result.Warnings, warnings...)
	if len(scoped) == 0 {
		return nil, nil
	}
	chosen := scoped[0]
	if saved := rt.getModel(settings.DefaultProvider, settings.DefaultModel); saved != nil {
		if index := slices.IndexFunc(scoped, func(entry ScopedModel) bool { return modelsAreEqual(entry.Model, *saved) }); index >= 0 {
			chosen = scoped[index]
		}
	}
	result.Thinking = chosen.ThinkingLevel
	return &chosen.Model, nil
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
