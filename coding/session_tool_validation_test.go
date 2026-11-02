package coding

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's wrapRegisteredTool retains the original parameter schema while replacing execute. Session context binding must likewise keep the tool's argument schema, which is its plain parameters.
func TestSessionToolBindingPreservesArgumentSchema(t *testing.T) {
	parameters := json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"]}`)
	session, err := NewSession(newTestServices(t), SessionOptions{
		Model: fakeModel(), SkipBuiltinTools: true,
		CustomTools: []extension.ToolDefinition{{Name: "native", Parameters: parameters}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	session.SetActiveToolsByName([]string{"native"})
	bound := session.Agent().Tools()[0]
	schema, ok := bound.(interface{ ArgumentSchema() json.RawMessage })
	if !ok {
		t.Fatal("Session context wrapper discarded the tool's argument schema")
	}
	if got := schema.ArgumentSchema(); !bytes.Equal(got, parameters) {
		t.Fatalf("argument schema = %s, want %s", got, parameters)
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
