package cli

import "github.com/MichaelKinsy/PiG/internal/codingagent"

// ResolveCliModelResult is the shared model-selection result used by CLI and auth commands.
type ResolveCliModelResult = codingagent.ResolveCliModelResult

func modelRef(model codingagent.RuntimeModel) string { return model.Provider + "/" + model.ID }

func modelsAreEqual(a, b codingagent.RuntimeModel) bool {
	return a.Provider == b.Provider && a.ID == b.ID
}

// ResolveCliModel delegates CLI and auth model selection to the shared resolver.
func ResolveCliModel(cliProvider, cliModel, cliThinking string, inlineAPIKey bool, runtime codingagent.ModelResolverRuntime) ResolveCliModelResult {
	return codingagent.ResolveCliModel(cliProvider, cliModel, cliThinking, inlineAPIKey, runtime)
}
