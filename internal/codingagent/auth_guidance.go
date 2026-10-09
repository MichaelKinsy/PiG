package codingagent

import (
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

const unknownProvider = "unknown"

// PigDocsFile is the path of name in the documentation bundle PiG materializes under its config root. ok is false when
// the process strips docs: the bundle is never written, so no text may point at the file. The literal "docs" is
// pigdocs.SubDir, so a Binary that strips docs does not link pigdocs.
// pig additive (D92): Stock PiG strips nothing, so ok is always true there.
func PigDocsFile(name string) (path string, ok bool) {
	return filepath.Join(ConfigRoot(), "docs", name), !pigstrip.Has(pigstrip.ListFeatures, pigstrip.Docs)
}

// ProviderLoginHelp mirrors upstream auth-guidance.ts.
// pig additive (D92): a stripped /login or docs bundle leaves its sentence out.
func ProviderLoginHelp() string {
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
