package codingagent

// Ports packages/coding-agent/src/core/model-resolver.ts (resolveModelReference and its helpers).

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// ThinkingSuffixMode is ModelReferenceRequest.thinkingSuffix.
type ThinkingSuffixMode int

const (
	// ThinkingSuffixNone keeps the suffix part of the id.
	ThinkingSuffixNone ThinkingSuffixMode = iota
	// ThinkingSuffixStrict parses a valid thinking level; any other suffix stays part of the id.
	ThinkingSuffixStrict
	// ThinkingSuffixLenient is strict, but an invalid suffix is dropped with a warning.
	ThinkingSuffixLenient
)

// ModelReferenceRequest mirrors upstream ModelReferenceRequest.
type ModelReferenceRequest struct {
	// Text is a bare model id or `provider/id`, optionally followed by `:<thinking>`.
	Text string
	// Provider restricts the lookup to one provider.
	Provider       string
	ThinkingSuffix ThinkingSuffixMode
	// Fuzzy matches id and name substrings when no exact match exists.
	Fuzzy bool
}

// ModelReferenceStatus is the ModelReferenceResult.status discriminant.
type ModelReferenceStatus int

const (
	ModelReferenceNotFound ModelReferenceStatus = iota
	ModelReferenceResolved
	ModelReferenceAmbiguous
)

// ModelReferenceTarget mirrors upstream ModelReferenceTarget.
type ModelReferenceTarget struct {
	Provider       string
	ProviderModels []RuntimeModel
	ModelText      string
	ModelID        string
	ThinkingLevel  string
}

// ModelReferenceResult mirrors upstream ModelReferenceResult.
type ModelReferenceResult struct {
	Status        ModelReferenceStatus
	Model         *RuntimeModel
	ThinkingLevel string
	Warning       string
	Candidates    []RuntimeModel
	UsableCount   int
	Target        *ModelReferenceTarget
}

var modelDateSuffix = lazyregexp.New(`-\d{8}$`)

// isAlias reports whether a model id has no date suffix.
func isAlias(id string) bool {
	if strings.HasSuffix(id, "-latest") {
		return true
	}
	return !modelDateSuffix.MatchString(id)
}

func modelRef(model RuntimeModel) string { return model.Provider + "/" + model.ID }

func modelsAreEqual(a, b RuntimeModel) bool {
	return a.Provider == b.Provider && a.ID == b.ID
}

// splitAtLastColon is text.substring(0, colonIndex) and text.substring(colonIndex + 1) for the last ":".
func splitAtLastColon(text string) (before, after string, ok bool) {
	colon := jsstring.LastIndexOf(text, ":", jsstring.Length(text))
	if colon == -1 {
		return "", "", false
	}
	return jsstring.Slice(text, 0, colon), jsstring.Slice(text, colon+1), true
}

func splitThinkingSuffix(text string) (base, level string, ok bool) {
	base, suffix, ok := splitAtLastColon(text)
	if !ok || !validModelThinkingLevel(suffix) {
		return "", "", false
	}
	return base, suffix, true
}

func splitProviderPrefix(text string, models []RuntimeModel) (provider, modelText string, ok bool) {
	before, after, ok := strings.Cut(text, "/")
	if !ok {
		return "", "", false
	}
	prefix := jsstring.ToLower(jsstring.Trim(before))
	modelText = jsstring.Trim(after)
	if prefix == "" || modelText == "" {
		return "", "", false
	}
	for _, model := range models {
		if jsstring.ToLower(model.Provider) == prefix {
			return model.Provider, modelText, true
		}
	}
	return "", "", false
}

// ResolveModelReference mirrors upstream resolveModelReference. usable only breaks ties between providers.
func ResolveModelReference(request ModelReferenceRequest, models []RuntimeModel, usable func(provider string) bool) ModelReferenceResult {
	text := jsstring.Trim(request.Text)
	if text == "" {
		return ModelReferenceResult{}
	}
	constraint := jsstring.ToLower(request.Provider)
	pool := models
	if constraint != "" {
		pool = nil
		for _, model := range models {
			if jsstring.ToLower(model.Provider) == constraint {
				pool = append(pool, model)
			}
		}
	}
	result := resolveReferenceText(text, request, pool, usable)
	if result.Status != ModelReferenceNotFound {
		return result
	}

	splitProvider, splitText, hasSplit := splitProviderPrefix(text, pool)
	var anchor *RuntimeModel
	if constraint != "" {
		if len(pool) > 0 {
			anchor = &pool[0]
		}
	} else if hasSplit {
		for i := range pool {
			if pool[i].Provider == splitProvider {
				anchor = &pool[i]
				break
			}
		}
	}
	if anchor == nil {
		return result
	}
	modelText := text
	if hasSplit {
		modelText = splitText
	}
	modelID, level := modelText, ""
	if request.ThinkingSuffix != ThinkingSuffixNone {
		if base, suffixLevel, ok := splitThinkingSuffix(modelText); ok {
			modelID, level = base, suffixLevel
		}
	}
	providerModels := []RuntimeModel{*anchor}
	for _, model := range pool {
		if model.Provider == anchor.Provider && !modelsAreEqual(model, *anchor) {
			providerModels = append(providerModels, model)
		}
	}
	return ModelReferenceResult{Status: ModelReferenceNotFound, Target: &ModelReferenceTarget{
		Provider:       anchor.Provider,
		ProviderModels: providerModels,
		ModelText:      modelText,
		ModelID:        modelID,
		ThinkingLevel:  level,
	}}
}

// FindExactModelReferenceMatch mirrors upstream findExactModelReferenceMatch.
func FindExactModelReferenceMatch(modelReference string, models []RuntimeModel) *RuntimeModel {
	result := ResolveModelReference(ModelReferenceRequest{Text: modelReference, ThinkingSuffix: ThinkingSuffixNone}, models, func(string) bool { return true })
	if result.Status == ModelReferenceResolved {
		return result.Model
	}
	return nil
}

func resolveReferenceText(text string, request ModelReferenceRequest, pool []RuntimeModel, usable func(string) bool) ModelReferenceResult {
	if reference, ok := selectReference(text, pool, request.Fuzzy, usable); ok {
		return reference
	}
	if request.Fuzzy {
		if fuzzy, ok := selectFuzzy(fuzzyMatches(text, pool), usable); ok {
			return fuzzy
		}
	}

	if request.ThinkingSuffix == ThinkingSuffixNone {
		return ModelReferenceResult{}
	}
	if base, level, ok := splitThinkingSuffix(text); ok {
		inner := resolveReferenceText(base, request, pool, usable)
		if inner.Status == ModelReferenceNotFound {
			return inner
		}
		// Only use this thinking level if no warning from inner recursion
		if inner.Warning != "" {
			inner.ThinkingLevel = ""
		} else {
			inner.ThinkingLevel = level
		}
		return inner
	}
	// Keep an invalid suffix as part of the id and fail, rather than resolve a different model
	if request.ThinkingSuffix == ThinkingSuffixStrict {
		return ModelReferenceResult{}
	}
	prefix, suffix, ok := splitAtLastColon(text)
	if !ok {
		return ModelReferenceResult{}
	}
	inner := resolveReferenceText(prefix, request, pool, usable)
	if inner.Status == ModelReferenceNotFound {
		return inner
	}
	inner.ThinkingLevel = ""
	inner.Warning = fmt.Sprintf(`Invalid thinking level "%s" in pattern "%s". Using default instead.`, suffix, text)
	return inner
}

// selectReference mirrors upstream selectReference: `provider/rest` is only a hint, the whole text may also be a raw model id on another provider.
func selectReference(text string, pool []RuntimeModel, fuzzy bool, usable func(string) bool) (ModelReferenceResult, bool) {
	lowerText := jsstring.ToLower(text)
	var raw []RuntimeModel
	for _, model := range pool {
		if jsstring.ToLower(model.ID) == lowerText {
			raw = append(raw, model)
		}
	}
	var named *RuntimeModel
	if provider, modelText, ok := splitProviderPrefix(text, pool); ok {
		var providerModels []RuntimeModel
		for _, model := range pool {
			if model.Provider == provider {
				providerModels = append(providerModels, model)
			}
		}
		lowerModelText := jsstring.ToLower(modelText)
		// The provider's own literal id (OpenRouter's `openrouter/free`) beats its fuzzy matches
		if i := slices.IndexFunc(providerModels, func(model RuntimeModel) bool { return jsstring.ToLower(model.ID) == lowerModelText }); i >= 0 {
			named = &providerModels[i]
		} else if i := slices.IndexFunc(raw, func(model RuntimeModel) bool { return model.Provider == provider }); i >= 0 {
			named = &raw[i]
		} else if fuzzy {
			named = bestFuzzyMatch(fuzzyMatches(modelText, providerModels))
		}
	}

	var others []RuntimeModel
	for _, model := range raw {
		if named == nil || !modelsAreEqual(model, *named) {
			others = append(others, model)
		}
	}
	candidates := others
	if named != nil {
		candidates = append([]RuntimeModel{*named}, others...)
	}
	if len(candidates) == 0 {
		return ModelReferenceResult{}, false
	}
	if distinctProviders(candidates) == 1 {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: &candidates[0]}, true
	}
	if named != nil && usable(named.Provider) {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: named}, true
	}
	var usableOthers []RuntimeModel
	for _, model := range others {
		if usable(model.Provider) {
			usableOthers = append(usableOthers, model)
		}
	}
	if len(usableOthers) == 1 {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: &usableOthers[0]}, true
	}
	if len(usableOthers) == 0 && named != nil {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: named}, true
	}
	return ModelReferenceResult{Status: ModelReferenceAmbiguous, Candidates: candidates, UsableCount: len(usableOthers)}, true
}

func distinctProviders(models []RuntimeModel) int {
	seen := map[string]struct{}{}
	for _, model := range models {
		seen[model.Provider] = struct{}{}
	}
	return len(seen)
}

func fuzzyMatches(text string, models []RuntimeModel) []RuntimeModel {
	lower := jsstring.ToLower(text)
	var matches []RuntimeModel
	for _, model := range models {
		if strings.Contains(jsstring.ToLower(model.ID), lower) || strings.Contains(jsstring.ToLower(model.Name), lower) {
			matches = append(matches, model)
		}
	}
	return matches
}

func selectFuzzy(matches []RuntimeModel, usable func(string) bool) (ModelReferenceResult, bool) {
	if len(matches) == 0 {
		return ModelReferenceResult{}, false
	}
	var usableMatches []RuntimeModel
	if distinctProviders(matches) > 1 {
		for _, model := range matches {
			if usable(model.Provider) {
				usableMatches = append(usableMatches, model)
			}
		}
	}
	pool := matches
	if len(usableMatches) > 0 {
		pool = usableMatches
	}
	best := bestFuzzyMatch(pool)
	if best == nil {
		return ModelReferenceResult{}, false
	}
	// The same id on several providers would otherwise be decided by catalog order
	var tied []RuntimeModel
	for _, model := range pool {
		if model.ID == best.ID {
			tied = append(tied, model)
		}
	}
	if distinctProviders(tied) == 1 {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: best}, true
	}
	usableCount := 0
	if len(usableMatches) > 0 {
		usableCount = len(tied)
	}
	return ModelReferenceResult{Status: ModelReferenceAmbiguous, Candidates: tied, UsableCount: usableCount}, true
}

// bestFuzzyMatch prefers aliases over dated versions, then the latest; upstream sorts with String.prototype.localeCompare, descending.
func bestFuzzyMatch(matches []RuntimeModel) *RuntimeModel {
	var aliases []RuntimeModel
	for _, model := range matches {
		if isAlias(model.ID) {
			aliases = append(aliases, model)
		}
	}
	candidates := slices.Clone(matches)
	if len(aliases) > 0 {
		candidates = aliases
	}
	if len(candidates) == 0 {
		return nil
	}
	collator := jsstring.NewCollator()
	slices.SortStableFunc(candidates, func(a, b RuntimeModel) int {
		return collator.CompareString(b.ID, a.ID)
	})
	return &candidates[0]
}

func validModelThinkingLevel(level string) bool {
	switch level {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
