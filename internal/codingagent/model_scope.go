package codingagent

// Ports packages/coding-agent/src/core/model-resolver.ts.

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

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

// ResolveModelScope is ResolveModelScopeWithDiagnostics that writes each diagnostic to stderr as a yellow "Warning: " line and returns only the scope (model-resolver.ts:372-382 resolveModelScope, console.warn(chalk.yellow(...))).
func ResolveModelScope(ctx context.Context, patterns []string, runtime ModelScopeRuntime) ([]ScopedModel, error) {
	return resolveModelScopeWarningTo(ctx, os.Stderr, term.IsTerminal(int(os.Stderr.Fd())), patterns, runtime)
}

func resolveModelScopeWarningTo(ctx context.Context, w io.Writer, color bool, patterns []string, runtime ModelScopeRuntime) ([]ScopedModel, error) {
	result, err := ResolveModelScopeWithDiagnostics(ctx, patterns, runtime)
	if err != nil {
		return nil, err
	}
	for _, diagnostic := range result.Diagnostics {
		_, _ = fmt.Fprintln(w, formatReportedDiagnostic(AgentSessionRuntimeDiagnostic{Type: "warning", Message: diagnostic.Message}, color))
	}
	return result.ScopedModels, nil
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

// modelGlobMatch is minimatch(name, pattern, { nocase: true }).
func modelGlobMatch(pattern, name string) bool {
	return minimatch.Match(name, pattern, minimatch.Options{NoCase: true})
}
