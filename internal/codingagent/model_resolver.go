package codingagent

// Ports packages/coding-agent/src/core/model-resolver.ts.

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// ModelResolverRuntime supplies the catalog and configured-auth observations used by CLI resolution. Selection does not resolve credentials or refresh the catalog.
type ModelResolverRuntime interface {
	GetModels(providerID string) []RuntimeModel
	HasConfiguredAuth(providerID string) bool
}

// ResolveCliModelResult carries a model selection, optional thinking suffix, warning, or CLI diagnostic.
type ResolveCliModelResult struct {
	Model         *RuntimeModel
	ThinkingLevel string
	Warning       string
	Error         string
}

// buildFallbackModel copies a provider's default model, or its first model, under the requested custom ID.
func buildFallbackModel(provider, modelID string, availableModels []RuntimeModel) *RuntimeModel {
	var providerModels []RuntimeModel
	for _, model := range availableModels {
		if model.Provider == provider {
			providerModels = append(providerModels, model)
		}
	}
	if len(providerModels) == 0 {
		return nil
	}
	base := providerModels[0]
	if defaultID, ok := DefaultModelPerProvider()[provider]; ok {
		if index := slices.IndexFunc(providerModels, func(model RuntimeModel) bool { return model.ID == defaultID }); index >= 0 {
			base = providerModels[index]
		}
	}
	base.ID = modelID
	base.Name = modelID
	if base.NativeModel != nil {
		native := *base.NativeModel
		native.ID, native.DisplayName = modelID, modelID
		base.NativeModel = &native
	}
	return &base
}

// ResolveCliModel resolves explicit provider/model selections against all catalog models, not only available models. It parses a thinking suffix without applying it to a Session.
func ResolveCliModel(cliProvider, cliModel, cliThinking string, runtime ModelResolverRuntime) ResolveCliModelResult {
	if cliModel == "" {
		return ResolveCliModelResult{}
	}
	availableModels := runtime.GetModels("")
	if len(availableModels) == 0 {
		return ResolveCliModelResult{Error: "No models available. Check your installation or add models to models.json."}
	}
	providerMap := map[string]string{}
	for _, model := range availableModels {
		providerMap[strings.ToLower(model.Provider)] = model.Provider
	}
	provider := ""
	if cliProvider != "" {
		provider = providerMap[strings.ToLower(cliProvider)]
		if provider == "" {
			return ResolveCliModelResult{Error: fmt.Sprintf(`Unknown provider "%s". Use --list-models to see available providers/models.`, cliProvider)}
		}
	}

	pattern := cliModel
	inferredProvider := false
	if provider == "" {
		if maybeProvider, rest, ok := strings.Cut(cliModel, "/"); ok {
			if canonical := providerMap[strings.ToLower(maybeProvider)]; canonical != "" {
				provider = canonical
				pattern = rest
				inferredProvider = true
			}
		}
	}

	if provider == "" {
		if result, done := resolveBareExactModel(cliModel, availableModels, runtime); done {
			return result
		}
	}

	if cliProvider != "" && provider != "" {
		prefix := provider + "/"
		if strings.HasPrefix(strings.ToLower(cliModel), strings.ToLower(prefix)) {
			pattern = cliModel[len(prefix):]
		}
	}

	candidates := availableModels
	if provider != "" {
		candidates = nil
		for _, model := range availableModels {
			if model.Provider == provider {
				candidates = append(candidates, model)
			}
		}
	}
	parsed := ParseModelPattern(pattern, candidates, false)
	if parsed.Model != nil {
		if inferredProvider {
			if model := authenticatedRawExactMatch(cliModel, *parsed.Model, availableModels, runtime); model != nil {
				return ResolveCliModelResult{Model: model}
			}
		}
		return ResolveCliModelResult{Model: parsed.Model, ThinkingLevel: parsed.ThinkingLevel, Warning: parsed.Warning}
	}

	if inferredProvider {
		lower := strings.ToLower(cliModel)
		for index, model := range availableModels {
			if strings.ToLower(model.ID) == lower || strings.ToLower(modelRef(model)) == lower {
				return ResolveCliModelResult{Model: &availableModels[index]}
			}
		}
		if fallback := ParseModelPattern(cliModel, availableModels, false); fallback.Model != nil {
			return ResolveCliModelResult{Model: fallback.Model, ThinkingLevel: fallback.ThinkingLevel, Warning: fallback.Warning}
		}
	}

	if provider != "" {
		if result, ok := resolveFallbackModel(provider, pattern, cliThinking, parsed.Warning, availableModels); ok {
			return result
		}
	}

	display := cliModel
	if provider != "" {
		display = provider + "/" + pattern
	}
	return ResolveCliModelResult{Warning: parsed.Warning, Error: fmt.Sprintf(`Model "%s" not found. Use --list-models to see available models.`, display)}
}

// resolveBareExactModel prefers the sole authenticated match for an ambiguous bare model ID; otherwise the caller must name its provider.
func resolveBareExactModel(cliModel string, availableModels []RuntimeModel, runtime ModelResolverRuntime) (ResolveCliModelResult, bool) {
	lower := strings.ToLower(cliModel)
	var exact []RuntimeModel
	for _, model := range availableModels {
		if strings.ToLower(model.ID) == lower || strings.ToLower(modelRef(model)) == lower {
			exact = append(exact, model)
		}
	}
	if len(exact) == 0 {
		return ResolveCliModelResult{}, false
	}
	if len(exact) == 1 {
		return ResolveCliModelResult{Model: &exact[0]}, true
	}
	var authenticated []RuntimeModel
	for _, model := range exact {
		if runtime.HasConfiguredAuth(model.Provider) {
			authenticated = append(authenticated, model)
		}
	}
	if len(authenticated) == 1 {
		return ResolveCliModelResult{Model: &authenticated[0]}, true
	}
	refs := make([]string, 0, len(exact))
	for _, model := range exact {
		refs = append(refs, modelRef(model))
	}
	collator := collate.New(language.Und)
	slices.SortStableFunc(refs, collator.CompareString)
	hint := "More than one matching provider is authenticated."
	if len(authenticated) == 0 {
		hint = "No matching provider is authenticated."
	}
	return ResolveCliModelResult{Error: fmt.Sprintf(`Model "%s" is ambiguous across providers: %s. %s Use --provider or provider/model.`, cliModel, strings.Join(refs, ", "), hint)}, true
}

// authenticatedRawExactMatch prefers one authenticated raw model-ID match when provider inference selected an unauthenticated provider.
func authenticatedRawExactMatch(cliModel string, inferred RuntimeModel, availableModels []RuntimeModel, runtime ModelResolverRuntime) *RuntimeModel {
	lower := strings.ToLower(cliModel)
	var raw []RuntimeModel
	for _, model := range availableModels {
		if strings.ToLower(model.ID) == lower && !modelsAreEqual(model, inferred) {
			raw = append(raw, model)
		}
	}
	if len(raw) == 0 || runtime.HasConfiguredAuth(inferred.Provider) {
		return nil
	}
	var authenticated []RuntimeModel
	for _, model := range raw {
		if runtime.HasConfiguredAuth(model.Provider) {
			authenticated = append(authenticated, model)
		}
	}
	if len(authenticated) == 1 {
		return &authenticated[0]
	}
	return nil
}

// resolveFallbackModel retains an explicit CLI thinking selection; otherwise it extracts a valid suffix from the custom model ID.
func resolveFallbackModel(provider, pattern, cliThinking, warning string, availableModels []RuntimeModel) (ResolveCliModelResult, bool) {
	fallbackPattern := pattern
	fallbackThinking := ""
	if cliThinking == "" {
		if lastColon := strings.LastIndex(pattern, ":"); lastColon != -1 {
			if suffix := pattern[lastColon+1:]; validModelThinkingLevel(suffix) {
				fallbackPattern = pattern[:lastColon]
				fallbackThinking = suffix
			}
		}
	}
	model := buildFallbackModel(provider, fallbackPattern, availableModels)
	if model == nil {
		return ResolveCliModelResult{}, false
	}
	requestedThinking := cliThinking
	if requestedThinking == "" {
		requestedThinking = fallbackThinking
	}
	if requestedThinking != "" && requestedThinking != "off" {
		model.Reasoning = true
		if model.NativeModel != nil {
			model.NativeModel.ProviderMeta.Reasoning = true
		}
	}
	message := fmt.Sprintf(`Model "%s" not found for provider "%s". Using custom model id.`, fallbackPattern, provider)
	if warning != "" {
		message = warning + " " + message
	}
	return ResolveCliModelResult{Model: model, ThinkingLevel: fallbackThinking, Warning: message}, true
}
