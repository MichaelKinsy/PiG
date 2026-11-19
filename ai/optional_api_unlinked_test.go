//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// D92: a model of an optional built-in API that no package registered fails with the package to import, and a runtime strip (strip.apis) takes precedence over it: the same model then reports the strip, not the missing link. A model without a provider id names the API's catalog provider.
func TestNewOptionalAPIProviderNamesThePackageToImportAndAStripTakesPrecedence(t *testing.T) {
	optional := optionalAPIs[APIBedrockConverseStream]
	registered := optional.registration
	optional.registration = nil
	t.Cleanup(func() { optional.registration = registered })

	const api = string(APIBedrockConverseStream)
	var model Model
	model.ProviderMeta.ProviderID = "my-bedrock"
	for _, tc := range []struct {
		name     string
		model    Model
		provider string
	}{{"model's provider", model, "my-bedrock"}, {"catalog provider", Model{}, "amazon-bedrock"}} {
		want := "Provider " + tc.provider + ": API " + api + " is not linked into this program; import github.com/MichaelKinsy/PiG/ai/providers/bedrock"
		if _, err := NewOptionalAPIProvider(APIBedrockConverseStream, tc.model); err == nil || err.Error() != want {
			t.Fatalf("%s: unlinked error = %v, want %q", tc.name, err, want)
		}
	}

	t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, api))
	want := "Provider my-bedrock: API " + api + " is stripped from this Piglet (strip.apis: " + api + ")"
	if _, err := NewOptionalAPIProvider(APIBedrockConverseStream, model); err == nil || err.Error() != want {
		t.Fatalf("stripped and unlinked error = %v, want %q", err, want)
	}
}
