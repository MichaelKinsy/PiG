package coding

// Ports packages/coding-agent/src/core/model-resolver.ts (resolveCliModel, resolveModelScopeWithDiagnostics, resolveModelScope).

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ModelScopeDiagnostic identifies a scope pattern that did not resolve cleanly (model-resolver.ts ResolveModelScopeResult.diagnostics).
type ModelScopeDiagnostic = icodingagent.ModelScopeDiagnostic

// ResolveModelScopeResult is the resolved scope in pattern order and its diagnostics (model-resolver.ts ResolveModelScopeResult).
type ResolveModelScopeResult struct {
	ScopedModels []ScopedModel
	Diagnostics  []ModelScopeDiagnostic
}

// ResolveCliModelOptions are the options of [ResolveCliModel] (model-resolver.ts resolveCliModel options).
type ResolveCliModelOptions struct {
	CliProvider  string
	CliModel     string
	CliThinking  ai.ThinkingLevel
	ModelRuntime *ModelRuntime
}

// ResolveCliModelResult is a CLI model selection (model-resolver.ts ResolveCliModelResult). When Error is set, Model is nil.
type ResolveCliModelResult struct {
	Model         *ai.Model
	ThinkingLevel ai.ThinkingLevel
	Warning       string
	// Error is a message suitable for CLI display.
	Error string
}

// resolverRuntime projects a ModelRuntime onto the resolver's catalog and auth observations, keeping each native model so a resolved selection maps back without a lookup.
type resolverRuntime struct{ runtime *ModelRuntime }

func runtimeModelOf(model *ai.Model) icodingagent.RuntimeModel {
	return icodingagent.RuntimeModel{NativeModel: model, Provider: model.ProviderMeta.ProviderID, ID: model.ID, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning || model.Capabilities.MaxThinking != "", Headers: ai.ProviderHeadersFromStrings(model.ProviderMeta.Headers)}
}

func runtimeModelsOf(models []*ai.Model) []icodingagent.RuntimeModel {
	result := make([]icodingagent.RuntimeModel, 0, len(models))
	for _, model := range models {
		result = append(result, runtimeModelOf(model))
	}
	return result
}

func (r resolverRuntime) GetModels(providerID string) []icodingagent.RuntimeModel {
	if providerID == "" {
		return runtimeModelsOf(r.runtime.GetModels())
	}
	return runtimeModelsOf(r.runtime.GetModels(providerID))
}

func (r resolverRuntime) HasConfiguredAuth(providerID string) bool {
	return r.runtime.HasConfiguredAuth(providerID)
}

func (r resolverRuntime) GetAvailable(ctx context.Context) ([]icodingagent.RuntimeModel, error) {
	models, err := r.runtime.GetAvailable(ctx)
	if err != nil {
		return nil, err
	}
	return runtimeModelsOf(models), nil
}

func scopedModelsOf(scoped []icodingagent.ScopedModel) []ScopedModel {
	result := make([]ScopedModel, 0, len(scoped))
	for _, entry := range scoped {
		result = append(result, ScopedModel{Model: entry.Model.NativeModel, ThinkingLevel: ai.ModelThinkingLevel(entry.ThinkingLevel)})
	}
	return result
}

// ResolveCliModel resolves one model from CLI flags against every catalog model, not only those with configured auth, so `--api-key` works for first-time setup. It parses a "<pattern>:<thinking>" suffix and returns the level without applying it.
// upstream: model-resolver.ts:406 (resolveCliModel)
func ResolveCliModel(options ResolveCliModelOptions) ResolveCliModelResult {
	resolved := icodingagent.ResolveCliModel(options.CliProvider, options.CliModel, string(options.CliThinking), resolverRuntime{options.ModelRuntime})
	result := ResolveCliModelResult{ThinkingLevel: resolved.ThinkingLevel, Warning: resolved.Warning, Error: resolved.Error}
	if resolved.Model != nil {
		result.Model = resolved.Model.NativeModel
	}
	return result
}

// ResolveModelScopeWithDiagnostics resolves scope patterns against the runtime's available models and returns the diagnostics with the scope. A GetAvailable failure is returned unchanged.
// upstream: model-resolver.ts:364 (resolveModelScopeWithDiagnostics)
func ResolveModelScopeWithDiagnostics(ctx context.Context, patterns []string, runtime *ModelRuntime) (ResolveModelScopeResult, error) {
	resolved, err := icodingagent.ResolveModelScopeWithDiagnostics(ctx, patterns, resolverRuntime{runtime})
	if err != nil {
		return ResolveModelScopeResult{}, err
	}
	return ResolveModelScopeResult{ScopedModels: scopedModelsOf(resolved.ScopedModels), Diagnostics: resolved.Diagnostics}, nil
}

// ResolveModelScope is [ResolveModelScopeWithDiagnostics] that writes each diagnostic to stderr as a yellow "Warning: " line and returns only the scope.
// upstream: model-resolver.ts:372-382 (resolveModelScope)
func ResolveModelScope(ctx context.Context, patterns []string, runtime *ModelRuntime) ([]ScopedModel, error) {
	scoped, err := icodingagent.ResolveModelScope(ctx, patterns, resolverRuntime{runtime})
	if err != nil {
		return nil, err
	}
	return scopedModelsOf(scoped), nil
}
