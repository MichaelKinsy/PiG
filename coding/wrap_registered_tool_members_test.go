package coding

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// tool-definition-wrapper.ts:12-29 wrapToolDefinition copies the definition's outputSchema, constrainedSampling and prepareArguments onto the AgentTool
// it returns, which is what wrapper.ts:17-21 wrapRegisteredTool returns for an extension tool. The agent loop reads them off that AgentTool: it validates a
// successful result against outputSchema, sends constrainedSampling to the provider, and runs prepareArguments on the model's arguments first.
func TestWrapRegisteredToolCarriesTheDefinitionsMembers(t *testing.T) {
	registered := fixtureTool("probe", "d", `{"type":"object"}`, nil)
	registered.Definition.OutputSchema = json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"}}}`)
	registered.Definition.ConstrainedSampling = json.RawMessage(`{"type":"grammar","syntax":"lark","definition":"start: \"x\""}`)
	prepared := 0
	registered.Definition.PrepareArguments = func(params json.RawMessage) (json.RawMessage, error) {
		prepared++
		return json.RawMessage(`{"fixed":true}`), nil
	}
	tool, err := WrapRegisteredTool(registered, inproc.NewRunner(nil, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}

	structured, ok := tool.(agent.OutputSchemaProvider)
	if !ok || string(structured.OutputSchema()) != `{"type":"object","properties":{"n":{"type":"number"}}}` {
		t.Errorf("OutputSchema not carried: %v %v", ok, tool)
	}
	if sampling := tool.Schema().ConstrainedSampling; sampling == nil {
		t.Error("constrainedSampling not carried onto the tool's schema")
	}
	preparer, ok := tool.(interface {
		PrepareArguments(json.RawMessage) (json.RawMessage, error)
	})
	if !ok {
		t.Fatal("the wrapped tool has no PrepareArguments")
	}
	got, err := preparer.PrepareArguments(json.RawMessage(`{"legacy":1}`))
	if err != nil || string(got) != `{"fixed":true}` || prepared != 1 {
		t.Errorf("PrepareArguments = %s, %v after %d calls; want the definition's result from one call", got, err, prepared)
	}
}
