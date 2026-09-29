package codingagent

// Ports packages/coding-agent/src/core/model-resolver.ts.

import (
	"path"
	"slices"
	"strings"
)

// ScopedModel pairs a model with an explicitly selected thinking level, if any.
type ScopedModel struct {
	Model         RuntimeModel
	ThinkingLevel string
}

// ModelScopeDiagnostic identifies a pattern that did not resolve cleanly.
type ModelScopeDiagnostic struct {
	Type    string
	Code    string
	Message string
	Pattern string
}

// ResolveModelScopeResult preserves pattern order, first-occurrence model order, and diagnostics.
type ResolveModelScopeResult struct {
	ScopedModels []ScopedModel
	Diagnostics  []ModelScopeDiagnostic
}

// ResolveModelScopeFromModels resolves a scope without refreshing or reading authentication.
func ResolveModelScopeFromModels(patterns []string, models []RuntimeModel) ResolveModelScopeResult {
	result := ResolveModelScopeResult{}
	add := func(model RuntimeModel, level string) {
		if !slices.ContainsFunc(result.ScopedModels, func(existing ScopedModel) bool { return modelsAreEqual(existing.Model, model) }) {
			result.ScopedModels = append(result.ScopedModels, ScopedModel{Model: model, ThinkingLevel: level})
		}
	}
	diagnostic := func(code, message, pattern string) {
		result.Diagnostics = append(result.Diagnostics, ModelScopeDiagnostic{Type: "warning", Code: code, Message: message, Pattern: pattern})
	}
	for _, pattern := range patterns {
		if strings.ContainsAny(pattern, "*?[") {
			globPattern, level := pattern, ""
			if colon := strings.LastIndex(pattern, ":"); colon != -1 && validModelThinkingLevel(pattern[colon+1:]) {
				globPattern, level = pattern[:colon], pattern[colon+1:]
			}
			if exact := FindExactModelReferenceMatch(globPattern, models); exact != nil {
				add(*exact, level)
				continue
			}
			matched := false
			for _, model := range models {
				if modelGlobMatch(globPattern, modelRef(model)) || modelGlobMatch(globPattern, model.ID) {
					add(model, level)
					matched = true
				}
			}
			if !matched {
				diagnostic("no-match", `No models match pattern "`+pattern+`"`, pattern)
			}
			continue
		}
		parsed := ParseModelPattern(pattern, models, true)
		if parsed.Warning != "" {
			diagnostic("invalid-thinking-level", parsed.Warning, pattern)
		}
		if parsed.Model == nil {
			diagnostic("no-match", `No models match pattern "`+pattern+`"`, pattern)
			continue
		}
		add(*parsed.Model, parsed.ThinkingLevel)
	}
	return result
}

func modelGlobMatch(pattern, name string) bool {
	matched, err := path.Match(strings.ToLower(pattern), strings.ToLower(name))
	return err == nil && matched
}
