package codemode_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	codemode "github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
)

// mustDescription is CreateDescription for tests.
func mustDescription(t *testing.T, tools []extension.AgentTool, options codemode.DescriptionOptions) string {
	t.Helper()
	return codemode.CreateDescription(tools, options)
}

// Pi 6d3e342b2 (#11803): a $ref with a malformed percent escape in one tool's schema renders as `unknown` in that tool's declaration. Before, the
// description threw a URIError, and prepareLoadout with it, which failed every codemode run.
func TestMalformedRefInAToolSchemaRendersAsUnknownInTheDescription(t *testing.T) {
	bad := agentTool("bad", "Has a malformed reference.")
	bad.Parameters = []byte(`{"type":"object","properties":{"a":{"$ref":"#/$defs/%FF"}},"$defs":{}}`)
	if got := codemode.CreateDescription([]extension.AgentTool{bad}, codemode.DescriptionOptions{}); !strings.Contains(got, "bad(args: { a?: unknown; })") {
		t.Fatalf("description = %q", got)
	}
}
