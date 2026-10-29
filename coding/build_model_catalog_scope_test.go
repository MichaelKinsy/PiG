package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A model id another provider catalogs must not lend that provider's API to an extension provider's model.
func TestBuildModelIgnoresOtherProvidersCatalog(t *testing.T) {
	if _, ok := ai.LookupModelExact("azure/gpt-6-astra"); !ok {
		t.Fatal("the generated catalog no longer has azure/gpt-6-astra: pick another model id that one provider catalogs and the test's extension provider does not")
	}
	services := newTestServices(t)
	if err := services.Registry().RegisterExtensionProvider("proxy", extension.ProviderConfig{BaseURL: "http://127.0.0.1:1/v1", API: ai.APIOpenAIResponses, APIKey: "test-key"}); err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("proxy/gpt-6-astra", services)
	if err != nil {
		t.Fatal(err)
	}
	if model.ProviderMeta.API != ai.APIOpenAIResponses || model.ProviderMeta.BaseURL != "http://127.0.0.1:1/v1" {
		t.Fatalf("api = %q, baseURL = %q", model.ProviderMeta.API, model.ProviderMeta.BaseURL)
	}
}
