package codingagent

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui"
)

func suggestionValues(mode *InteractiveMode, prefix string) []string {
	suggestions := mode.buildAutocompleteProvider().GetSuggestions(context.Background(), []string{prefix}, 0, len(prefix), tui.AutocompleteSuggestionOptions{})
	if suggestions == nil {
		return nil
	}
	values := make([]string, 0, len(suggestions.Items))
	for _, item := range suggestions.Items {
		values = append(values, item.Value)
	}
	return values
}

// TestPigletStrippedCommandAbsentFromAutocomplete pins that a command the
// process strips leaves autocomplete while its siblings stay, and that
// without a strip the command is offered as in Pi.
func TestPigletStrippedCommandAbsentFromAutocomplete(t *testing.T) {
	mode := &InteractiveMode{opts: InteractiveModeOptions{AgentDir: t.TempDir()}}
	if got := suggestionValues(mode, "/sha"); !slices.Contains(got, "share") {
		t.Fatalf("stock autocomplete for /sha = %v, want share", got)
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListCommands, "/share"))
	if got := suggestionValues(mode, "/sha"); slices.Contains(got, "share") {
		t.Fatalf("stripped /share still autocompletes: %v", got)
	}
	if got := suggestionValues(mode, "/expo"); !slices.Contains(got, "export") {
		t.Fatalf("unstripped /export left autocomplete: %v", got)
	}
}

// TestPigletStrippedCommandAbsentFromRegistry pins that a stripped built-in
// no longer resolves or dispatches, so its text falls through to the model
// like any unknown slash command.
func TestPigletStrippedCommandAbsentFromRegistry(t *testing.T) {
	t.Cleanup(pigstrip.Strip(pigstrip.ListCommands, "/share"))
	registry := NewSlashRegistry()
	if _, ok := registry.Resolve("share"); ok {
		t.Fatal("stripped /share still resolves")
	}
	if registry.IsBuiltin("share") {
		t.Fatal("stripped /share is still a built-in")
	}
	if _, ok := registry.Resolve("export"); !ok {
		t.Fatal("unstripped /export no longer resolves")
	}
	for _, info := range registry.All() {
		if info.Name == "share" {
			t.Fatal("stripped /share is still listed")
		}
	}
	if err := registry.Dispatch(&SlashContext{}, "/share", nil); !errors.Is(err, ErrUnknownSlashCommand) {
		t.Fatalf("Dispatch(/share) = %v, want ErrUnknownSlashCommand", err)
	}
}
