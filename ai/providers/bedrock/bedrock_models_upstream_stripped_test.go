//go:build pig_strip_bedrock_converse_stream

package bedrock

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A build that strips bedrock-converse-stream offers no Bedrock model and does not build the provider.
func TestBedrockUpstreamModelCatalog(t *testing.T) {
	if models := ai.ListModels("amazon-bedrock"); len(models) != 0 {
		t.Fatalf("%d Bedrock models are offered, the first %s", len(models), models[0].ID)
	}
	if !ai.ProviderStripped("amazon-bedrock") {
		t.Fatal("amazon-bedrock is not stripped")
	}
	for _, provider := range ai.BuiltinProviders() {
		if provider.ID == "amazon-bedrock" {
			t.Fatal("BuiltinProviders() built amazon-bedrock")
		}
	}
}
