package codemode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 6d3e342b2 (#11803): a `$ref` segment with malformed percent-encoding renders as `unknown`, so a codemode call with such a tool in reach runs
// instead of failing with `URI malformed` (resolveRef threw out of renderToolSample, execute.ts).
func TestExecuteRunsWithAToolSchemaWithMalformedRefEncoding(t *testing.T) {
	broken := extension.AgentTool{Name: "broken", Description: "d", Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"$ref":"#/$defs/%FF"}},"$defs":{}}`)}
	params, _ := json.Marshal(map[string]string{"code": "return 1"})
	base := extension.NewContext("", nil, func() error { return nil }, extension.ContextActions{})
	tc := extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{GetCallableTools: func() []extension.AgentTool { return []extension.AgentTool{broken} }})
	if _, err := Execute(extension.WithToolContext(context.Background(), tc), "call", params, nil, Options{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}
