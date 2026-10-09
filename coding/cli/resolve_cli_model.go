package cli

import "github.com/MichaelKinsy/PiG/internal/codingagent"

// ResolveCliModelResult is the shared model-selection result used by CLI and auth commands.
type ResolveCliModelResult = codingagent.ResolveCliModelResult

// ParsedModelResult is the shared model-pattern result used by startup and interactive selection.
type ParsedModelResult = codingagent.ParsedModelResult

func modelRef(model codingagent.RuntimeModel) string { return model.Provider + "/" + model.ID }

func modelsAreEqual(a, b codingagent.RuntimeModel) bool {
	return a.Provider == b.Provider && a.ID == b.ID
}

// ParseModelPattern resolves CLI patterns with the same parser as interactive scopes.
func ParseModelPattern(pattern string, availableModels []codingagent.RuntimeModel, allowInvalidThinkingLevelFallback bool) ParsedModelResult {
	return codingagent.ParseModelPattern(pattern, availableModels, allowInvalidThinkingLevelFallback)
}

// ResolveCliModel delegates CLI and auth model selection to the shared resolver.
func ResolveCliModel(cliProvider, cliModel, cliThinking string, runtime codingagent.ModelResolverRuntime) ResolveCliModelResult {
	return codingagent.ResolveCliModel(cliProvider, cliModel, cliThinking, runtime)
}
