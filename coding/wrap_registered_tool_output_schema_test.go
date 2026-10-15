package coding

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi tool-definition-wrapper.ts:17 (`outputSchema: definition.outputSchema`) carries a registered tool's outputSchema onto the AgentTool that
// wrapRegisteredTool returns (wrapper.ts:17-21), so agent.ts AgentTool.outputSchema (types.ts:478) reads the extension's declared schema, and a
// definition without one gives an AgentTool without one.
func TestWrapRegisteredToolCarriesTheDefinitionsOutputSchemaToTheAgentTool(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"count":{"type":"number"}},"required":["count"]}`)
	declared := fixtureTool("counts", "Counts", `{"type":"object"}`, nil)
	declared.Definition.OutputSchema = schema
	tool, err := WrapRegisteredTool(declared, inproc.NewRunner(nil, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := tool.(agent.OutputSchemaProvider)
	if !ok {
		t.Fatal("the wrapped tool does not implement agent.OutputSchemaProvider")
	}
	if got := provider.OutputSchema(); !bytes.Equal(got, schema) {
		t.Errorf("OutputSchema() = %s, want the definition's %s", got, schema)
	}

	plain, err := WrapRegisteredTool(fixtureTool("plain", "Plain", `{"type":"object"}`, nil), inproc.NewRunner(nil, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.(agent.OutputSchemaProvider).OutputSchema(); len(got) != 0 {
		t.Errorf("a definition without an outputSchema gave the AgentTool %s", got)
	}
}
