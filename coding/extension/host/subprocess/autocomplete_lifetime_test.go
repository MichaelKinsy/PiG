package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestAutocompleteCancellationPrecedesDispatchAndReleasesReferences(t *testing.T) {
	bridge := NewUIBridge(nil)
	owner, other := &Conn{}, &Conn{}
	provider := &extension.AutocompleteProvider{GetSuggestions: func(ctx context.Context, _ []string, _, _ int, _ bool) (*extension.AutocompleteSuggestions, error) {
		return nil, ctx.Err()
	}}
	descriptor := bridge.autocompleteReference("owner", owner, provider)
	args, _ := json.Marshal(autocompleteInvocation{ID: descriptor.ID, Operation: "getSuggestions", QueryID: "query", Lines: []string{"input"}})
	call := &CallPayload{Method: "ui.autocomplete.invoke", Args: args}
	bridge.reserveAutocompleteCall(owner, call)
	if _, err := bridge.handleAutocompleteCancel(owner, json.RawMessage(`{"queryId":"query"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.handleAutocompleteInvoke(t.Context(), owner, args); !errors.Is(err, context.Canceled) {
		t.Fatalf("query cancellation=%v", err)
	}
	bridge.releaseAutocompleteCall(owner, call)
	if len(bridge.autocompleteQueries) != 0 {
		t.Fatal("completed query retained cancellation state")
	}
	release, _ := json.Marshal(map[string]string{"id": descriptor.ID})
	bridge.releaseAutocompleteReference(other, release)
	if len(bridge.autocompleteReferences) != 1 {
		t.Fatal("another connection released the captured provider")
	}
	bridge.releaseAutocompleteReference(owner, release)
	if len(bridge.autocompleteReferences) != 0 {
		t.Fatal("provider reference was not released")
	}
	bridge.autocompleteReference("owner", owner, provider)
	bridge.autocompleteReference("other", other, provider)
	bridge.ClearExtensionConn("owner", owner)
	if len(bridge.autocompleteReferences) != 1 {
		t.Fatal("connection close did not isolate its references")
	}
	bridge.ClearExtensionConn("other", other)
	if len(bridge.autocompleteReferences) != 0 {
		t.Fatal("connection close retained providers")
	}
}
