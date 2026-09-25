package codingagent

import "path/filepath"

const unknownProvider = "unknown"

// ProviderLoginHelp mirrors upstream auth-guidance.ts.
func ProviderLoginHelp() string {
	return "Use /login to log into a provider via OAuth or API key. See:\n" +
		"  " + filepath.Join(ConfigRoot(), "docs", "providers.md") + "\n" +
		"  " + filepath.Join(ConfigRoot(), "docs", "models.md")
}

func FormatNoModelsAvailableMessage() string {
	return "No models available. " + ProviderLoginHelp()
}

func FormatNoModelSelectedMessage() string {
	return "No model selected.\n\n" + ProviderLoginHelp() + "\n\nThen use /model to select a model."
}

func FormatNoAPIKeyFoundMessage(provider string) string {
	providerDisplay := provider
	if providerDisplay == "" || providerDisplay == unknownProvider {
		providerDisplay = "the selected model"
	}
	return "No API key found for " + providerDisplay + ".\n\n" + ProviderLoginHelp()
}
