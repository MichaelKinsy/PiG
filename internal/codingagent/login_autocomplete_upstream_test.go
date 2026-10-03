package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// .upstream/v1.0.0/packages/coding-agent/test/interactive-mode-status.test.ts:456
func TestInteractiveLoginArgumentCompletionUpstream(t *testing.T) {
	runtime := &RequestAuthRuntime{providers: []*RuntimeProvider{
		{ID: "anthropic", Auth: ai.ProviderAuth{OAuth: &ai.OAuthAuth{IsSubscription: true}, APIKey: &ai.APIKeyAuth{}}},
		{ID: "openai", Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{}}},
		{ID: "radius", Name: "Radius", Auth: ai.ProviderAuth{OAuth: &ai.OAuthAuth{IsSubscription: false}}},
	}}
	mode := &InteractiveMode{opts: InteractiveOptions{AgentDir: t.TempDir(), RequestAuthRuntime: runtime}}
	provider := mode.buildAutocompleteProvider()

	line := "/login subscription anthrop"
	want := []tui.AutocompleteItem{{Value: "anthropic", Label: "anthropic", Description: "Anthropic · subscription/API key"}}
	suggestions := provider.GetSuggestions([]string{line}, 0, len(line))
	if suggestions == nil || !reflect.DeepEqual(suggestions.Items, want) {
		t.Fatalf("production command suggestions=%+v", suggestions)
	}

	// interactive-mode-status.test.ts:511: OAuth sign-in without a subscription, such as Radius, is an account.
	radiusLine := "/login radius"
	radiusWant := []tui.AutocompleteItem{{Value: "radius", Label: "radius", Description: "Radius · account"}}
	radiusSuggestions := provider.GetSuggestions([]string{radiusLine}, 0, len(radiusLine))
	if radiusSuggestions == nil || !reflect.DeepEqual(radiusSuggestions.Items, radiusWant) {
		t.Fatalf("radius suggestions=%+v, want %+v", radiusSuggestions, radiusWant)
	}
}
