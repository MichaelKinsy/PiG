package codingagent

// Ports packages/coding-agent/src/core/model-resolver.ts.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
	"github.com/MichaelKinsy/PiG/internal/minimatch"
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

// ModelScopeRuntime supplies the models a scope resolves against (upstream ModelRuntime.getAvailable).
type ModelScopeRuntime interface {
	GetAvailable(ctx context.Context) ([]RuntimeModel, error)
}

// ResolveModelScopeWithDiagnostics resolves patterns against the runtime's available models and returns the diagnostics with the scope.
// A GetAvailable failure is returned unchanged, as upstream's awaited getAvailable rejects (model-resolver.ts resolveModelScopeWithDiagnostics).
func ResolveModelScopeWithDiagnostics(ctx context.Context, patterns []string, runtime ModelScopeRuntime) (ResolveModelScopeResult, error) {
	models, err := runtime.GetAvailable(ctx)
	if err != nil {
		return ResolveModelScopeResult{}, err
	}
	return ResolveModelScopeFromModels(patterns, models), nil
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
	noMatch := func(pattern string) {
		diagnostic("no-match", `No models match pattern "`+pattern+`"`, pattern)
	}
	ambiguous := func(pattern string, candidates []RuntimeModel) {
		var providers []string
		for _, model := range candidates {
			if !slices.Contains(providers, model.Provider) {
				providers = append(providers, model.Provider)
			}
		}
		collator := localecompare.NewCollator()
		slices.SortStableFunc(providers, collator.CompareString)
		diagnostic("ambiguous", fmt.Sprintf(`Pattern "%s" matches several providers: %s. Use provider/id to choose one.`, pattern, strings.Join(providers, ", ")), pattern)
	}
	everyUsable := func(string) bool { return true }
	for _, pattern := range patterns {
		// Check if pattern contains glob characters
		if strings.ContainsAny(pattern, "*?[") {
			// Extract optional thinking level suffix (e.g., "provider/*:high")
			globPattern, level, ok := splitThinkingSuffix(pattern)
			if !ok {
				globPattern, level = pattern, ""
			}

			// Ids such as `bracketed-model[1m]` are not globs
			exact := ResolveModelReference(ModelReferenceRequest{Text: globPattern, ThinkingSuffix: ThinkingSuffixNone}, models, everyUsable)
			var matches []RuntimeModel
			switch exact.Status {
			case ModelReferenceResolved:
				matches = []RuntimeModel{*exact.Model}
			case ModelReferenceAmbiguous:
				matches = exact.Candidates
				ambiguous(pattern, matches)
			default:
				// Match against "provider/modelId" format OR just model ID
				// This allows "*sonnet*" to match without requiring "anthropic/*sonnet*"
				for _, model := range models {
					if modelGlobMatch(globPattern, modelRef(model)) || modelGlobMatch(globPattern, model.ID) {
						matches = append(matches, model)
					}
				}
			}
			if len(matches) == 0 {
				noMatch(pattern)
			}
			for _, model := range matches {
				add(model, level)
			}
			continue
		}

		resolved := ResolveModelReference(ModelReferenceRequest{Text: pattern, ThinkingSuffix: ThinkingSuffixLenient, Fuzzy: true}, models, everyUsable)
		if resolved.Status == ModelReferenceNotFound {
			noMatch(pattern)
			continue
		}
		if resolved.Warning != "" {
			diagnostic("invalid-thinking-level", resolved.Warning, pattern)
		}
		matches := resolved.Candidates
		if resolved.Status == ModelReferenceAmbiguous {
			ambiguous(pattern, resolved.Candidates)
		} else {
			matches = []RuntimeModel{*resolved.Model}
		}
		for _, model := range matches {
			add(model, resolved.ThinkingLevel)
		}
	}
	return result
}

// modelGlobMatch is minimatch(name, pattern, { nocase: true }).
func modelGlobMatch(pattern, name string) bool {
	return minimatch.Match(name, pattern, minimatch.Options{NoCase: true})
}
