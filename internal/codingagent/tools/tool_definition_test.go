package tools

import "testing"

// createToolDefinitionFromAgentTool keeps the tool's TypeBox parameters, which JSON.stringify writes in declaration order (read.ts readSchema).
func TestToolDefinitionFromAgentToolKeepsTheParameterMemberOrder(t *testing.T) {
	definition, err := ToolDefinitionFromAgentTool(CreateReadTool(t.TempDir(), nil))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","required":["path"],"properties":{"path":{"type":"string","description":"Path to the file to read (relative or absolute)"},"offset":{"type":"number","description":"Line number to start reading from (1-indexed)"},"limit":{"type":"number","description":"Maximum number of lines to read"}}}`
	if string(definition.Parameters) != want {
		t.Fatalf("read parameters = %s\nwant %s", definition.Parameters, want)
	}
}
