package coding

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's wrapRegisteredTool retains the original parameter schema while replacing execute. Session context binding must likewise retain host-only TypeBox conversion metadata without exposing it to the provider.
func TestSessionToolBindingPreservesValidationSchema(t *testing.T) {
	parameters := json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`)
	validation := json.RawMessage(`{"~kind":"Object","type":"object","properties":{"count":{"~kind":"Integer","type":"integer"}},"required":["count"]}`)
	session, err := NewSession(newTestServices(t), SessionOptions{
		Model: fakeModel(), SkipBuiltinTools: true,
		CustomTools: []extension.ToolDefinition{{Name: "native", Parameters: parameters, ValidationParameters: validation}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	session.SetActiveToolsByName([]string{"native"})
	bound := session.Agent().Tools()[0]
	schema, ok := bound.(interface{ ArgumentSchema() json.RawMessage })
	if !ok {
		t.Fatal("Session context wrapper discarded the tool's validation schema")
	}
	if got := schema.ArgumentSchema(); !bytes.Equal(got, validation) {
		t.Fatalf("validation schema = %s, want %s", got, validation)
	}
	providerSchema, err := json.Marshal(bound.Schema().Parameters)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(providerSchema, []byte("~kind")) {
		t.Fatalf("host-only metadata leaked into provider schema: %s", providerSchema)
	}
}

func TestSessionToolBindingKeepsPlainSchemaCapabilityAbsent(t *testing.T) {
	session, err := NewSession(newTestServices(t), SessionOptions{
		Model: fakeModel(), SkipBuiltinTools: true,
		Tools: []agent.AgentTool{&fakeTool{name: "plain"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	session.SetActiveToolsByName([]string{"plain"})
	if _, ok := session.Agent().Tools()[0].(interface{ ArgumentSchema() json.RawMessage }); ok {
		t.Fatal("Session context wrapper manufactured an alternate schema capability")
	}
}
