package cli

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestStartupTestFauxUnknownIDResolvesLikeAnyProvider pins `--model test-faux/<id the faux provider does not list>`. Pi has no built-in faux
// provider: the parity harness registers it from an extension (test/parity/testdata/test-faux-provider.ts:701, one model faux-1), so the id
// is resolveCliModel's custom id: the warning, and a copy of faux-1 under the id (model-resolver.ts:302-313, 518-532). Pi 1.5.2 by run
// prints the warning and sends its requests under the id.
func TestStartupTestFauxUnknownIDResolvesLikeAnyProvider(t *testing.T) {
	dir := isolateProviderAuthEnv(t)
	t.Setenv("PIG_TEST_FAUX", "1")
	coding.EnableTestFauxProvider()
	services := testServices(t, dir)
	t.Cleanup(services.Close)

	for _, selection := range []struct{ name, model, provider string }{
		{name: "provider/id", model: "test-faux/nosuch"},
		{name: "--provider with a bare id", model: "nosuch", provider: "test-faux"},
	} {
		t.Run(selection.name, func(t *testing.T) {
			model, _, warning, err := resolveModel(selection.model, selection.provider, codingagent.Settings{}, services)
			if err != nil || model == nil {
				t.Fatalf("model %v, error %v", model, err)
			}
			if want := `Model "nosuch" not found for provider "test-faux". Using custom model id.`; warning != want {
				t.Errorf("warning = %q, want %q", warning, want)
			}
			if model.ID != "nosuch" || model.DisplayName != "nosuch" {
				t.Errorf("model is %q named %q, want nosuch named nosuch", model.ID, model.DisplayName)
			}
			if model.Capabilities.ContextWindow != ai.TestFauxContextWindow || model.Capabilities.MaxOutputTokens != ai.TestFauxMaxTokens {
				t.Errorf("window/limit = %d/%d, want faux-1's %d/%d", model.Capabilities.ContextWindow, model.Capabilities.MaxOutputTokens, ai.TestFauxContextWindow, ai.TestFauxMaxTokens)
			}
		})
	}

	known, thinking, warning, err := resolveModel("test-faux/faux-1:high", "", codingagent.Settings{}, services)
	if err != nil || known == nil || warning != "" || known.ID != "faux-1" || thinking != "high" {
		t.Fatalf("the listed model with a thinking suffix: model %v, thinking %q, warning %q, error %v", known, thinking, warning, err)
	}
}
