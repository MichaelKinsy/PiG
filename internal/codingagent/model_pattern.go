package codingagent

// Ports packages/coding-agent/src/core/model-resolver.ts.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// ParsedModelResult mirrors upstream ParsedModelResult.
type ParsedModelResult struct {
	Model         *RuntimeModel
	ThinkingLevel string
	Warning       string
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

// FindExactModelReferenceMatch mirrors upstream findExactModelReferenceMatch.
func FindExactModelReferenceMatch(modelReference string, availableModels []RuntimeModel) *RuntimeModel {
	trimmed := jsstring.Trim(modelReference)
	if trimmed == "" {
		return nil
	}
	normalized := jsstring.ToLower(trimmed)
	var canonical []RuntimeModel
	for _, model := range availableModels {
		if jsstring.ToLower(modelRef(model)) == normalized {
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
		provider, modelID = jsstring.Trim(provider), jsstring.Trim(modelID)
		if provider != "" && modelID != "" {
			var matches []RuntimeModel
			for _, model := range availableModels {
				if jsstring.ToLower(model.Provider) == jsstring.ToLower(provider) && jsstring.ToLower(model.ID) == jsstring.ToLower(modelID) {
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
	var idMatches []RuntimeModel
	for _, model := range availableModels {
		if jsstring.ToLower(model.ID) == normalized {
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
func tryMatchModel(pattern string, availableModels []RuntimeModel) *RuntimeModel {
	if exact := FindExactModelReferenceMatch(pattern, availableModels); exact != nil {
		return exact
	}
	lower := jsstring.ToLower(pattern)
	var aliases, dated []RuntimeModel
	for _, model := range availableModels {
		if !strings.Contains(jsstring.ToLower(model.ID), lower) && !strings.Contains(jsstring.ToLower(model.Name), lower) {
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
	slices.SortStableFunc(candidates, func(a, b RuntimeModel) int {
		return collator.CompareString(b.ID, a.ID)
	})
	return &candidates[0]
}

// ParseModelPattern mirrors upstream parseModelPattern.
func ParseModelPattern(pattern string, availableModels []RuntimeModel, allowInvalidThinkingLevelFallback bool) ParsedModelResult {
	if exact := tryMatchModel(pattern, availableModels); exact != nil {
		return ParsedModelResult{Model: exact}
	}
	lastColon := strings.LastIndex(pattern, ":")
	if lastColon == -1 {
		return ParsedModelResult{}
	}
	prefix, suffix := pattern[:lastColon], pattern[lastColon+1:]
	if validModelThinkingLevel(suffix) {
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

func validModelThinkingLevel(level string) bool {
	switch level {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}
