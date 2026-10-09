package coding

import (
	"bytes"
	"testing"

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
