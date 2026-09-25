package main

// Mirrors upstream packages/coding-agent/src/core/model-resolver.ts
// resolveCliModel and its helpers (findExactModelReferenceMatch,
// tryMatchModel, parseModelPattern, buildFallbackModel, isAlias) over the
// request-auth runtime's model list. The auth commands resolve --model with
// it.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// cliModelRuntime is the ModelRuntime surface resolveCliModel reads.
type cliModelRuntime interface {
	GetModels(providerID string) []codingagent.RuntimeModel
	HasConfiguredAuth(providerID string) bool
}

// ResolveCliModelResult mirrors upstream ResolveCliModelResult.
type ResolveCliModelResult struct {
	Model         *codingagent.RuntimeModel
	ThinkingLevel string
	Warning       string
	Error         string
}

// ParsedModelResult mirrors upstream ParsedModelResult.
type ParsedModelResult struct {
	Model         *codingagent.RuntimeModel
	ThinkingLevel string
	Warning       string
}

var modelDateSuffix = regexp.MustCompile(`-\d{8}$`)

// isAlias reports whether a model id has no date suffix.
func isAlias(id string) bool {
	if strings.HasSuffix(id, "-latest") {
		return true
	}
	return !modelDateSuffix.MatchString(id)
}

func modelRef(model codingagent.RuntimeModel) string { return model.Provider + "/" + model.ID }

func modelsAreEqual(a, b codingagent.RuntimeModel) bool {
	return a.Provider == b.Provider && a.ID == b.ID
}

// FindExactModelReferenceMatch mirrors upstream findExactModelReferenceMatch.
func FindExactModelReferenceMatch(modelReference string, availableModels []codingagent.RuntimeModel) *codingagent.RuntimeModel {
	trimmed := strings.TrimSpace(modelReference)
	if trimmed == "" {
		return nil
	}
	normalized := strings.ToLower(trimmed)
	var canonical []codingagent.RuntimeModel
	for _, model := range availableModels {
		if strings.ToLower(modelRef(model)) == normalized {
			canonical = append(canonical, model)
		}
	}
	if len(canonical) == 1 {
		return &canonical[0]
	}
	if len(canonical) > 1 {
		return nil
	}
	if provider, modelID, ok := strings.Cut(trimmed, "/"); ok {
		provider, modelID = strings.TrimSpace(provider), strings.TrimSpace(modelID)
		if provider != "" && modelID != "" {
			var matches []codingagent.RuntimeModel
			for _, model := range availableModels {
				if strings.EqualFold(model.Provider, provider) && strings.EqualFold(model.ID, modelID) {
					matches = append(matches, model)
				}
			}
			if len(matches) == 1 {
				return &matches[0]
			}
			if len(matches) > 1 {
				return nil
			}
		}
	}
	var idMatches []codingagent.RuntimeModel
	for _, model := range availableModels {
		if strings.ToLower(model.ID) == normalized {
			idMatches = append(idMatches, model)
		}
	}
	if len(idMatches) == 1 {
		return &idMatches[0]
	}
	return nil
}

// tryMatchModel mirrors upstream tryMatchModel: an exact reference, otherwise
// the highest-sorting alias (or dated version) whose id or name contains the
// pattern.
func tryMatchModel(pattern string, availableModels []codingagent.RuntimeModel) *codingagent.RuntimeModel {
	if exact := FindExactModelReferenceMatch(pattern, availableModels); exact != nil {
		return exact
	}
	lower := strings.ToLower(pattern)
	var aliases, dated []codingagent.RuntimeModel
	for _, model := range availableModels {
		if !strings.Contains(strings.ToLower(model.ID), lower) && !strings.Contains(strings.ToLower(model.Name), lower) {
			continue
		}
		if isAlias(model.ID) {
			aliases = append(aliases, model)
		} else {
			dated = append(dated, model)
		}
	}
	candidates := aliases
	if len(candidates) == 0 {
		candidates = dated
	}
	if len(candidates) == 0 {
		return nil
	}
	// Upstream sorts with String.prototype.localeCompare, descending.
	collator := collate.New(language.Und)
	slices.SortStableFunc(candidates, func(a, b codingagent.RuntimeModel) int {
		return collator.CompareString(b.ID, a.ID)
	})
	return &candidates[0]
}

// ParseModelPattern mirrors upstream parseModelPattern.
func ParseModelPattern(pattern string, availableModels []codingagent.RuntimeModel, allowInvalidThinkingLevelFallback bool) ParsedModelResult {
	if exact := tryMatchModel(pattern, availableModels); exact != nil {
		return ParsedModelResult{Model: exact}
	}
	lastColon := strings.LastIndex(pattern, ":")
	if lastColon == -1 {
		return ParsedModelResult{}
	}
	prefix, suffix := pattern[:lastColon], pattern[lastColon+1:]
	if validThinkingLevels[suffix] {
		result := ParseModelPattern(prefix, availableModels, allowInvalidThinkingLevelFallback)
		if result.Model != nil {
			if result.Warning == "" {
				result.ThinkingLevel = suffix
			} else {
				result.ThinkingLevel = ""
			}
		}
		return result
	}
	if !allowInvalidThinkingLevelFallback {
		return ParsedModelResult{}
	}
	result := ParseModelPattern(prefix, availableModels, allowInvalidThinkingLevelFallback)
	if result.Model != nil {
		return ParsedModelResult{
			Model:   result.Model,
			Warning: fmt.Sprintf(`Invalid thinking level "%s" in pattern "%s". Using default instead.`, suffix, pattern),
		}
	}
	return result
}

// buildFallbackModel mirrors upstream buildFallbackModel: the provider's
// default model (or first model) under a custom id.
func buildFallbackModel(provider, modelID string, availableModels []codingagent.RuntimeModel) *codingagent.RuntimeModel {
	var providerModels []codingagent.RuntimeModel
	for _, model := range availableModels {
		if model.Provider == provider {
			providerModels = append(providerModels, model)
		}
	}
	if len(providerModels) == 0 {
		return nil
	}
	base := providerModels[0]
	if defaultID, ok := defaultModelPerProvider()[provider]; ok {
		if index := slices.IndexFunc(providerModels, func(model codingagent.RuntimeModel) bool { return model.ID == defaultID }); index >= 0 {
			base = providerModels[index]
		}
	}
	base.ID = modelID
	base.Name = modelID
	return &base
}

// ResolveCliModel mirrors upstream resolveCliModel.
func ResolveCliModel(cliProvider, cliModel, cliThinking string, runtime cliModelRuntime) ResolveCliModelResult {
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

// resolveBareExactModel resolves an exact bare or provider/id reference
// without provider inference. Bare exact ids can exist in several providers,
// so it prefers the sole authenticated provider and otherwise asks for an
// explicit provider.
func resolveBareExactModel(cliModel string, availableModels []codingagent.RuntimeModel, runtime cliModelRuntime) (ResolveCliModelResult, bool) {
	lower := strings.ToLower(cliModel)
	var exact []codingagent.RuntimeModel
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
	var authenticated []codingagent.RuntimeModel
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

// authenticatedRawExactMatch prefers one authenticated raw model-id match
// when provider inference picked an unauthenticated provider.
func authenticatedRawExactMatch(cliModel string, inferred codingagent.RuntimeModel, availableModels []codingagent.RuntimeModel, runtime cliModelRuntime) *codingagent.RuntimeModel {
	lower := strings.ToLower(cliModel)
	var raw []codingagent.RuntimeModel
	for _, model := range availableModels {
		if strings.ToLower(model.ID) == lower && !modelsAreEqual(model, inferred) {
			raw = append(raw, model)
		}
	}
	if len(raw) == 0 || runtime.HasConfiguredAuth(inferred.Provider) {
		return nil
	}
	var authenticated []codingagent.RuntimeModel
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

// resolveFallbackModel builds a custom-id model for a known provider, taking
// a thinking-level suffix from the pattern unless --thinking was given.
func resolveFallbackModel(provider, pattern, cliThinking, warning string, availableModels []codingagent.RuntimeModel) (ResolveCliModelResult, bool) {
	fallbackPattern := pattern
	fallbackThinking := ""
	if cliThinking == "" {
		if lastColon := strings.LastIndex(pattern, ":"); lastColon != -1 {
			if suffix := pattern[lastColon+1:]; validThinkingLevels[suffix] {
				fallbackPattern = pattern[:lastColon]
				fallbackThinking = suffix
			}
		}
	}
	model := buildFallbackModel(provider, fallbackPattern, availableModels)
	if model == nil {
		return ResolveCliModelResult{}, false
	}
	message := fmt.Sprintf(`Model "%s" not found for provider "%s". Using custom model id.`, fallbackPattern, provider)
	if warning != "" {
		message = warning + " " + message
	}
	return ResolveCliModelResult{Model: model, ThinkingLevel: fallbackThinking, Warning: message}, true
}
