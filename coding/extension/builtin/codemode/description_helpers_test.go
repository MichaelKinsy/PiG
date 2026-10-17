package codemode_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	codemode "github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
)

// mustDescription is CreateDescription for tests whose schemas hold only decodable references.
func mustDescription(t *testing.T, tools []extension.AgentTool, options codemode.DescriptionOptions) string {
	t.Helper()
	description, err := codemode.CreateDescription(tools, options)
	if err != nil {
		t.Fatalf("CreateDescription: %v", err)
	}
	return description
}

// Pi's renderToolSample throws a URIError for a $ref segment with malformed percent-encoding; the description carries it out
// as an error, and the tool's prepareLoadout hook throws it (a panic the session reports as a prepare_loadout error).
func TestMalformedRefInAToolSchemaSurfacesFromTheDescription(t *testing.T) {
	bad := agentTool("bad", "Has a malformed reference.")
	bad.Parameters = []byte(`{"type":"object","properties":{"a":{"$ref":"#/$defs/%FF"}},"$defs":{}}`)
	if _, err := codemode.CreateDescription([]extension.AgentTool{bad}, codemode.DescriptionOptions{}); err == nil {
		t.Fatal("CreateDescription: want the URIError equivalent")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("prepareLoadout: want a thrown error")
		}
	}()
	tool := codemode.Definition(codemode.Options{})
	tool.PrepareLoadout(extension.ToolLoadout{Callable: []extension.AgentTool{bad}, Declared: []extension.AgentTool{bad}})
}
