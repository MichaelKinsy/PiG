package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-status.test.ts:455
func TestInteractiveLoginArgumentCompletionUpstream(t *testing.T) {
	want := []tui.AutocompleteItem{{Value: "anthropic", Label: "anthropic", Description: "Anthropic · subscription/API key"}}
	runtime := &RequestAuthRuntime{providers: []*RuntimeProvider{
		{ID: "anthropic", Auth: ai.ProviderAuth{OAuth: &ai.OAuthAuth{}, APIKey: &ai.APIKeyAuth{}}},
		{ID: "openai", Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{}}},
	}}
	mode := &InteractiveMode{opts: InteractiveOptions{AgentDir: t.TempDir(), RequestAuthRuntime: runtime}}
	line := "/login subscription anthrop"
	suggestions := mode.buildAutocompleteProvider().GetSuggestions([]string{line}, 0, len(line))
	if suggestions == nil || !reflect.DeepEqual(suggestions.Items, want) {
		t.Fatalf("production command suggestions=%+v", suggestions)
	}
}
