package codingagent

import (
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

const unknownProvider = "unknown"

// PigDocsFile is the path of name in this binary's documentation bundle. ok is false when the process strips docs or the bundle is not on disk
// (the binary embeds none, or startup could not write it): no text may point at a file that is not there.
// pig additive (D92): Stock PiG strips nothing.
// pig additive (D22): a bundle that is not on disk is not named.
func PigDocsFile(name string) (path string, ok bool) {
	docs := GetDocsPath()
	if docs == "" || pigstrip.Has(pigstrip.ListFeatures, pigstrip.Docs) {
		return "", false
	}
	return filepath.Join(docs, name), true
}

// ProviderLoginHelp mirrors upstream auth-guidance.ts.
// pig additive (D92): a stripped /login or docs bundle leaves its sentence out.
func ProviderLoginHelp() string {
	// The help names pages to a user, who may open them at once: write the bundle first when startup deferred it.
	configroot.WriteDocs()
	var help []string
	if !pigstrip.Has(pigstrip.ListCommands, "/login") {
		help = append(help, "Use /login to log into a provider via OAuth or API key.")
	}
	providers, ok := PigDocsFile("providers.md")
	if ok {
		models, _ := PigDocsFile("models.md")
		help = append(help, "See:\n  "+providers+"\n  "+models)
	}
	return strings.Join(help, " ")
}

func FormatNoModelsAvailableMessage() string {
	return strings.TrimSuffix("No models available. "+ProviderLoginHelp(), " ")
}

func FormatNoModelSelectedMessage() string {
	parts := []string{"No model selected."}
	if help := ProviderLoginHelp(); help != "" {
		parts = append(parts, help)
	}
	// pig additive (D92): a stripped /model is not suggested.
	if !pigstrip.Has(pigstrip.ListCommands, "/model") {
		parts = append(parts, "Then use /model to select a model.")
	}
	return strings.Join(parts, "\n\n")
}

func FormatNoAPIKeyFoundMessage(provider string) string {
	providerDisplay := provider
	if providerDisplay == "" || providerDisplay == unknownProvider {
		providerDisplay = "the selected model"
	}
	return strings.TrimSuffix("No API key found for "+providerDisplay+".\n\n"+ProviderLoginHelp(), "\n\n")
}
