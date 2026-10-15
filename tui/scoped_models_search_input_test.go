package tui

import (
	"strings"
	"testing"
)

// scoped-models-selector.ts builds its search with `new Input()`: a key that reaches the input as submit (LF under the Kitty protocol, where it is ctrl+j and
// not select.confirm) calls an absent onSubmit and leaves the input editable, so later typing still lands in the query.
func TestScopedModelsSearchStaysEditableAfterASubmitKey(t *testing.T) {
	previous := IsKittyProtocolActive()
	SetKittyProtocolActive(true)
	t.Cleanup(func() { SetKittyProtocolActive(previous) })
	restoreKeybindingsAfterTest(t)
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), nil))
	list := NewScopedModelsList(ScopedModelsConfig{AllModels: []ModelItem{{FullID: "openai/gpt-5", Name: "gpt-5", Provider: "openai"}}})
	for _, key := range []string{"g", "\n", "p", "t"} {
		list.HandleInput(key)
	}
	var query string
	for _, line := range list.Render(60) {
		if strings.HasPrefix(line, "> ") {
			query = line
		}
	}
	if !strings.Contains(query, "gpt") {
		t.Fatalf("the search lost the typing after LF: %q", query)
	}
}
