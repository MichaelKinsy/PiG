//go:build !pig_strip_bedrock_converse_stream

package coding

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/ai/providers/bedrock"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/mcp"
)

// A tool's schema reaches the provider in the order its author wrote it (Pi sends tool.parameters as it was given; the bytes are part of the prompt
// and of the provider's prompt-cache key), whichever way the tool arrives: an extension's declaration over the wire, an MCP server's input schema, a
// built-in's source literal. The schemas are not alphabetical at the top level or inside properties.
func TestToolSchemaKeepsAuthoredOrderOnTheBedrockWireFromEveryArrival(t *testing.T) {
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	const extensionSchema = `{"type":"object","properties":{"zeta":{"type":"string","description":"Z"},"alpha":{"type":"number"}},"required":["zeta"]}`
	bridge, err := newBridgeTool(fixtureTool("lookup", "Look up", extensionSchema, nil))
	if err != nil {
		t.Fatal(err)
	}
	const mcpSchema = `{"properties":{"zeta":{"type":"string","description":"Z"},"alpha":{"type":"number"}},"required":["zeta"],"additionalProperties":false}`
	mcpParameters := mcp.NormalizeMcpInputSchema(json.RawMessage(mcpSchema))
	mcpBridge, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "remote", Description: "Remote", Parameters: mcpParameters}})
	if err != nil {
		t.Fatal(err)
	}
	read := (&tools.ReadTool{CWD: t.TempDir()}).Schema()
	for _, tc := range []struct {
		name string
		tool ai.ToolSchema
		want string
	}{
		{"extension declaration", bridge.Schema(), extensionSchema},
		{"MCP input schema", mcpBridge.Schema(), string(mcpParameters)},
		// read declares json_schema constrainedSampling "prefer", so Bedrock gets the strict rebuild of its declaration (makeStrictJsonSchema: type, required, properties, additionalProperties).
		{"built-in read", read, `{"type":"object","required":["path","offset","limit"],"properties":{"path":{"type":"string","description":"Path to the file to read (relative or absolute)"},"offset":{"anyOf":[{"type":"number","description":"Line number to start reading from (1-indexed)"},{"type":"null"}]},"limit":{"anyOf":[{"type":"number","description":"Maximum number of lines to read"},{"type":"null"}]}},"additionalProperties":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"stop"}`))
			}))
			defer server.Close()
			provider := bedrock.NewBedrockProvider("us.anthropic.claude-sonnet-4-5-20250929-v1:0", server.URL)
			defer func() { _ = provider.Close() }()
			stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}, Tools: []ai.ToolSchema{tc.tool}}), ai.StreamOptions{})
			if err == nil {
				stream.Result()
			}
			if want := `"inputSchema":{"json":` + tc.want + `}`; !strings.Contains(string(body), want) {
				t.Fatalf("request body\n got: %s\nwant it to contain: %s", body, want)
			}
		})
	}
}
