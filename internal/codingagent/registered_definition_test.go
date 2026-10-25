package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A registered definition encodes its catalog record once for the provider members it is asked for, and again when they change.
func TestRegisteredDefinitionCatalogRecordIsEncodedOncePerMembers(t *testing.T) {
	definition := &registeredDefinition{raw: json.RawMessage(`{"id":"m","name":"M","extra":{"kept":true}}`)}
	first := definition.catalogRecord(ai.API("openai-completions"), "p", "http://a")
	want := ai.ExtensionModelRecord(definition.raw, ai.API("openai-completions"), "p", "http://a")
	if first == "" || first != want {
		t.Fatalf("record = %q, want %q", first, want)
	}
	if again := definition.catalogRecord(ai.API("openai-completions"), "p", "http://a"); again != first {
		t.Fatalf("second record = %q, want %q", again, first)
	}
	if moved, want := definition.catalogRecord(ai.API("openai-completions"), "p", "http://b"), ai.ExtensionModelRecord(definition.raw, ai.API("openai-completions"), "p", "http://b"); moved == first || moved != want {
		t.Fatalf("record after the base URL changed = %q, want %q and not %q", moved, want, first)
	}
}
