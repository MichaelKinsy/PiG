package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's defaultModelPerProvider (as ported in DefaultModelPerProviderOrder) names a model of the provider's own bundled catalog: a default the
// catalog does not hold is a broken default for every user of that provider. Pi e56d35ce5ece moved openai, azure and openai-codex to
// gpt-6.2-sol, which only a catalog that lists it satisfies.
func TestEveryProviderDefaultModelIsInTheBundledCatalog(t *testing.T) {
	for _, entry := range DefaultModelPerProviderOrder {
		if _, ok := ai.LookupModelExact(entry.Provider + "/" + entry.ModelID); !ok {
			t.Errorf("the default model of %s, %s, is not in the bundled catalog", entry.Provider, entry.ModelID)
		}
	}
}
