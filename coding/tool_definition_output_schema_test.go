package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Pi's tool-definition-wrapper carries a tool's outputSchema into and out of its definition
// (.upstream/v0.99.1/packages/coding-agent/src/core/tools/tool-definition-wrapper.ts:17,52), and bash declares one
// (.upstream/v0.99.1/packages/coding-agent/src/core/tools/bash.ts:262). Codemode resolves a nested call to
// `structuredContent` only for a tool whose definition has the schema
// (.upstream/v0.99.1/packages/coding-agent/src/extensions/codemode/execute.ts:207).
func TestToolDefinitionCarriesOutputSchemaOfBuiltinTools(t *testing.T) {
	bash := &tools.BashTool{CWD: t.TempDir()}
	definition, err := toolDefinition(bash)
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.OutputSchema) == 0 {
		t.Fatal("the bash definition has no outputSchema")
	}
	if !bytes.Equal(definition.OutputSchema, bash.OutputSchema()) {
		t.Errorf("outputSchema = %s, want %s", definition.OutputSchema, bash.OutputSchema())
	}
	if view := toolView(definition); !bytes.Equal(view.OutputSchema, bash.OutputSchema()) {
		t.Errorf("ctx.tools view outputSchema = %s", view.OutputSchema)
	}

	// Pi 1.0.4's read tool declares an output schema: the text, or an image block (read.ts readOutputSchema).
	readTool := &tools.ReadTool{CWD: t.TempDir()}
	read, err := toolDefinition(readTool)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.OutputSchema) == 0 || !bytes.Equal(read.OutputSchema, readTool.OutputSchema()) {
		t.Errorf("the read definition has outputSchema %s, want %s", read.OutputSchema, readTool.OutputSchema())
	}
}

// plainTool is a custom AgentTool that declares no output schema; schemaTool is upstream's runToolCall "echo" tool, which declares
// `outputSchema: Type.Object({ value: Type.String() })` (packages/agent/test/agent-loop.test.ts:2140, AgentTool.outputSchema, types.ts:478).
type plainTool struct{}

func (plainTool) Name() string  { return "plain" }
func (plainTool) Label() string { return "Plain" }
func (plainTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "plain", Description: "Plain tool", Parameters: map[string]any{"type": "object"}}
}
func (plainTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{}, nil
}
func (plainTool) ExecutionMode() agent.ToolExecutionMode { return "" }

type schemaTool struct{ plainTool }

func (schemaTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`)
}

// tool-definition-wrapper.ts:52 copies outputSchema of any AgentTool into its definition, and an AgentTool without one has none: only a tool that
// implements OutputSchemaProvider carries a schema (codemode renders `Promise<unknown>` for the others, declarations.ts:195).
func TestToolDefinitionCarriesTheOutputSchemaOfAnyAgentToolThatDeclaresOne(t *testing.T) {
	with, err := toolDefinition(schemaTool{})
	if err != nil {
		t.Fatal(err)
	}
	if want := (schemaTool{}).OutputSchema(); !bytes.Equal(with.OutputSchema, want) {
		t.Fatalf("outputSchema = %s, want %s", with.OutputSchema, want)
	}
	if view := toolView(with); !bytes.Equal(view.OutputSchema, with.OutputSchema) {
		t.Fatalf("ctx.tools view outputSchema = %s, want %s", view.OutputSchema, with.OutputSchema)
	}
	without, err := toolDefinition(plainTool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(without.OutputSchema) != 0 {
		t.Fatalf("a tool without an output schema got %s", without.OutputSchema)
	}
}
