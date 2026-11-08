package models

import (
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
)

// Ports apps/p2/plugins/models/scope.ts: the model scope for cycling (pi's `enabledModels`, coding-agent
// core/model-resolver.ts `resolveModelScopeFromModels`), pure. Patterns are `provider/modelId` or `modelId`, a
// case-insensitive `*`/`?` glob over either, each with an optional `:thinkingLevel` suffix. Exact references win over
// globs; duplicates keep their first position.

var scopeLevels = map[string]bool{"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// ScopedModel is a model of the scope, with the level its pattern gave.
type ScopedModel struct {
	Provider      string
	ModelID       string
	ThinkingLevel string // empty: none
}

// ScopeResult is the models a scope resolves to, in pattern order, and the patterns that matched nothing.
type ScopeResult struct {
	Models    []ScopedModel
	Unmatched []string
}

// globSpecial are the characters JavaScript's `/[.+^${}()|[\]\\]/g` escapes.
const globSpecial = ".+^${}()|[]\\"

func glob(pattern string) *regexp.Regexp {
	var source strings.Builder
	source.WriteString("(?i)^")
	for _, char := range pattern {
		switch {
		case char == '*':
			source.WriteString(".*")
		case char == '?':
			source.WriteString(".")
		case strings.ContainsRune(globSpecial, char):
			source.WriteString("\\" + string(char))
		default:
			source.WriteRune(char)
		}
	}
	source.WriteString("$")
	return regexp.MustCompile(source.String())
}

func exact(reference string, models []contract.ModelInfo) *contract.ModelInfo {
	lower := strings.ToLower(reference)
	for i := range models {
		if strings.ToLower(models[i].Provider+"/"+models[i].ModelID) == lower {
			return &models[i]
		}
	}
	for i := range models {
		if strings.ToLower(models[i].ModelID) == lower {
			return &models[i]
		}
	}
	return nil
}

// ResolveModelScope resolves patterns against models.
func ResolveModelScope(patterns []string, models []contract.ModelInfo) ScopeResult {
	result := ScopeResult{Models: []ScopedModel{}, Unmatched: []string{}}
	add := func(model contract.ModelInfo, thinkingLevel string) {
		for _, item := range result.Models {
			if item.Provider == model.Provider && item.ModelID == model.ModelID {
				return
			}
		}
		result.Models = append(result.Models, ScopedModel{Provider: model.Provider, ModelID: model.ModelID, ThinkingLevel: thinkingLevel})
	}
	for _, pattern := range patterns {
		reference := strings.TrimSpace(pattern)
		level := ""
		// A model ID may contain colons (OpenRouter's `:exacto`): the whole pattern first, then a level suffix.
		if colon := strings.LastIndex(reference, ":"); exact(reference, models) == nil && colon != -1 && scopeLevels[reference[colon+1:]] {
			level = reference[colon+1:]
			reference = reference[:colon]
		}
		if single := exact(reference, models); single != nil {
			add(*single, level)
			continue
		}
		if !strings.ContainsAny(reference, "*?") {
			result.Unmatched = append(result.Unmatched, pattern)
			continue
		}
		matcher := glob(reference)
		matched := false
		for _, model := range models {
			if matcher.MatchString(model.Provider+"/"+model.ModelID) || matcher.MatchString(model.ModelID) {
				matched = true
				add(model, level)
			}
		}
		if !matched {
			result.Unmatched = append(result.Unmatched, pattern)
		}
	}
	return result
}
