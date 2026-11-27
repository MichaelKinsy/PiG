package mcpext

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/mcp"
)

func TestDeprecatedMcpResultAliasesDelegateToMCP(t *testing.T) {
	schema := CreateMcpResultSchema(json.RawMessage(`{"type":"object"}`))
	wantSchema := mcp.CreateMcpResultSchema(json.RawMessage(`{"type":"object"}`))
	if string(schema) != string(wantSchema) {
		t.Fatalf("CreateMcpResultSchema = %s, want %s", schema, wantSchema)
	}
	got := ToModelContent("docs", []mcp.ContentBlock{{Type: mcp.ContentText, Text: "hello"}}, ConvertMcpResultOptions{})
	if len(got) != 1 {
		t.Fatalf("ToModelContent len = %d, want 1", len(got))
	}
	tc, ok := got[0].(ai.TextContent)
	if !ok || tc.Text != "hello" {
		t.Fatalf("ToModelContent[0] = %#v, want TextContent hello", got[0])
	}
}
